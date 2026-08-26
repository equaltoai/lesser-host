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

// microvmListPageSize is the page size sent for every paged list call to the
// lambdamicrovms service. The service caps maxResults at 50 and rejects larger
// values with a ValidationException (issue #1058: ListMicrovmImages with
// maxResults=100 aborted a lab deploy fail-closed; factory manual verification
// hit the same bound on ListMicrovmImageVersions — 100 rejected, <=50
// accepted). The SDK performs no client-side bound check (ListMicrovmImages
// has no input-validation middleware at all; ListMicrovmImageVersions
// validates only that ImageIdentifier is present), so the cap is enforced only
// on the wire. We send exactly 50: the cap is a fixed service constant — the
// image also hard-caps at 50 versions (issue #1052) and the cap is not an
// adjustable quota — so there is no drift risk, and using the full page size
// minimizes round trips during pagination while staying wire-valid. Every list
// call in this package emits microvmListPageSize.
const microvmListPageSize = 50

// sdkSurface is the subset of *lambdamicrovms.Client that SDKClient calls.
// Declared as an interface so tests can drive the exact construction path the
// deploy uses — NewSDKClient over a stub that enforces the service's
// validation bounds — without any AWS calls. *lambdamicrovms.Client satisfies
// it structurally.
type sdkSurface interface {
	lambdamicrovms.ListMicrovmImagesAPIClient
	lambdamicrovms.ListMicrovmImageVersionsAPIClient
	DeleteMicrovmImageVersion(ctx context.Context, params *lambdamicrovms.DeleteMicrovmImageVersionInput, optFns ...func(*lambdamicrovms.Options)) (*lambdamicrovms.DeleteMicrovmImageVersionOutput, error)
}

// SDKClient adapts the aws-sdk-go-v2 lambdamicrovms service to the Client
// interface. The SDK ships the lambda-microvms service model (v1.0.0), so no
// hand-rolled signed client is needed; the aws CLI lacks the namespace but the
// SDK's botocore-derived model exposes it.
type SDKClient struct {
	sdk sdkSurface
}

// NewSDKClient wraps a configured lambdamicrovms client.
func NewSDKClient(sdk sdkSurface) *SDKClient {
	return &SDKClient{sdk: sdk}
}

// validateListMaxResults enforces the lambdamicrovms service's documented
// maxResults bound — a value above microvmListPageSize is rejected with a
// ValidationException, the exact error shape the service returns on the wire
// (issue #1058). The SDK does not validate this bound client-side, so the test
// stub calls this shared helper to mirror the service contract and make any
// future out-of-bound page size fail in CI. The real client never needs to
// reject: every list call emits microvmListPageSize by construction.
func validateListMaxResults(maxResults int32) error {
	if maxResults > microvmListPageSize {
		return serviceValidationError(fmt.Sprintf(
			"1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: Member must have value less than or equal to %d",
			maxResults, microvmListPageSize))
	}
	return nil
}

// serviceValidationError builds the error shape the lambdamicrovms service
// returns for invalid request parameters: the modeled ValidationException the
// SDK deserializes on the wire. Returning the concrete *types.ValidationException
// keeps stub rejections indistinguishable from a real service response —
// errors.As(*types.ValidationException) succeeds exactly as it does against the
// wire, and the error still classifies via smithy.APIError (issue #1058).
func serviceValidationError(message string) error {
	return &types.ValidationException{Message: aws.String(message)}
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
			MaxResults: aws.Int32(microvmListPageSize),
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
// page size (microvmListPageSize — the service's maxResults cap, issue #1058).
func (c *SDKClient) ListVersions(ctx context.Context, imageARN string) ([]Version, error) {
	var versions []Version
	var nextToken *string
	for {
		out, err := c.sdk.ListMicrovmImageVersions(ctx, &lambdamicrovms.ListMicrovmImageVersionsInput{
			ImageIdentifier: aws.String(imageARN),
			MaxResults:      aws.Int32(microvmListPageSize),
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
