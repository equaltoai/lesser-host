package microvmversions

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/aws/smithy-go"
)

var (
	// ErrImageNotFound reports that no image with the requested name exists
	// (first deploy — nothing to prune).
	ErrImageNotFound = errors.New("microvm image not found")
	// ErrConflict reports a delete that raced the image's UPDATING state
	// machine; the caller should settle-wait and retry (issue #1052).
	ErrConflict = errors.New("microvm image version delete conflict")
)

// SDKClient adapts the aws-sdk-go-v2 lambdamicrovms service to the Client
// interface. The SDK ships the lambda-microvms service model (v1.0.0), so no
// hand-rolled signed client is needed; the aws CLI lacks the namespace but the
// SDK's botocore-derived model exposes it.
type SDKClient struct {
	sdk *lambdamicrovms.Client
}

// NewSDKClient wraps a configured lambdamicrovms client.
func NewSDKClient(sdk *lambdamicrovms.Client) *SDKClient {
	return &SDKClient{sdk: sdk}
}

// ResolveImage finds the image with the exact requested name and returns its
// ARN plus the version the active controller references. The service's
// ListMicrovmImages NameFilter is a contains-match, so results are narrowed to
// exact Name equality here.
func (c *SDKClient) ResolveImage(ctx context.Context, name string) (Image, error) {
	var matches []types.MicrovmImageSummary
	var nextToken *string
	for {
		out, err := c.sdk.ListMicrovmImages(ctx, &lambdamicrovms.ListMicrovmImagesInput{
			NameFilter: aws.String(name),
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		})
		if err != nil {
			return Image{}, fmt.Errorf("microvmversions: list images: %w", err)
		}
		for _, summary := range out.Items {
			if summary.Name != nil && *summary.Name == name {
				matches = append(matches, summary)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	if len(matches) == 0 {
		return Image{}, ErrImageNotFound
	}
	if len(matches) > 1 {
		return Image{}, fmt.Errorf("microvmversions: ambiguous resolution: %d images named %q", len(matches), name)
	}

	img := matches[0]
	image := Image{Name: name}
	if img.ImageArn != nil {
		image.ARN = *img.ImageArn
	}
	if img.LatestActiveImageVersion != nil {
		image.LatestActiveImageVersion = *img.LatestActiveImageVersion
	}
	return image, nil
}

// ListVersions lists every version of the image, paging through the service's
// 50-item page size.
func (c *SDKClient) ListVersions(ctx context.Context, imageARN string) ([]Version, error) {
	var versions []Version
	var nextToken *string
	for {
		out, err := c.sdk.ListMicrovmImageVersions(ctx, &lambdamicrovms.ListMicrovmImageVersionsInput{
			ImageIdentifier: aws.String(imageARN),
			MaxResults:      aws.Int32(50),
			NextToken:       nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("microvmversions: list image versions: %w", err)
		}
		for _, item := range out.Items {
			versions = append(versions, toVersion(item))
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return versions, nil
}

// DeleteVersion deletes one image version, classifying settle-able conflicts
// as ErrConflict. The service documents the operation as idempotent.
func (c *SDKClient) DeleteVersion(ctx context.Context, imageARN, imageVersion string) error {
	_, err := c.sdk.DeleteMicrovmImageVersion(ctx, &lambdamicrovms.DeleteMicrovmImageVersionInput{
		ImageIdentifier: aws.String(imageARN),
		ImageVersion:    aws.String(imageVersion),
	})
	return classifyDeleteError(err)
}

// classifyDeleteError maps a ConflictException from the lambdamicrovms service
// to ErrConflict; every other error passes through unchanged.
func classifyDeleteError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ConflictException" {
		return fmt.Errorf("%w: %s", ErrConflict, apiErr.ErrorMessage())
	}
	return err
}

func toVersion(item types.MicrovmImageVersionSummary) Version {
	v := Version{
		State:  string(item.State),
		Status: string(item.Status),
	}
	if item.ImageVersion != nil {
		v.ImageVersion = *item.ImageVersion
	}
	if item.CreatedAt != nil {
		v.CreatedAt = *item.CreatedAt
	}
	return v
}
