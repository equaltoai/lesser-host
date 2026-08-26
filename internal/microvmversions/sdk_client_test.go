package microvmversions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	"github.com/aws/smithy-go"
)

// fakeAPIError is a minimal smithy.APIError for exercising error classification
// without any AWS calls.
type fakeAPIError struct {
	code    string
	message string
	fault   smithy.ErrorFault
}

func (e *fakeAPIError) Error() string                 { return e.message }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return e.fault }

func TestClassifyDeleteError(t *testing.T) {
	conflict := &fakeAPIError{code: "ConflictException", message: "image is updating", fault: smithy.FaultClient}
	if err := classifyDeleteError(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("ConflictException should classify as ErrConflict, got %v", err)
	}

	other := &fakeAPIError{code: "NotFoundException", message: "no such version", fault: smithy.FaultClient}
	if err := classifyDeleteError(other); errors.Is(err, ErrConflict) {
		t.Fatalf("non-conflict error should pass through, got %v", err)
	}

	if err := classifyDeleteError(nil); err != nil {
		t.Fatalf("nil error should stay nil, got %v", err)
	}
}

func TestImageNameForStage(t *testing.T) {
	cases := map[string]string{
		"lab":  "lesser-host-lab_hosted_genesis",
		"live": "lesser-host-live_hosted_genesis",
	}
	for stage, want := range cases {
		if got := ImageNameForStage(stage); got != want {
			t.Errorf("ImageNameForStage(%q) = %q, want %q", stage, got, want)
		}
	}
}

const (
	// validationExceptionCode is the error code the lambdamicrovms service
	// returns for invalid request parameters; the enforcing stub rejects
	// protocol-invalid calls with exactly this shape (issue #1058).
	validationExceptionCode = "ValidationException"

	testImageName = "lesser-host-lab_hosted_genesis"
	testImageARN  = "arn:aws:lambda-microvms:us-east-1:123456789012:image/lesser-host-lab_hosted_genesis"
)

// enforcingSDKStub implements the lambdamicrovms surface SDKClient calls and
// enforces the service's documented validation bounds the way the service
// does: a maxResults above microvmListPageSize is rejected with the service's
// ValidationException shape (via the shared validateListMaxResults helper), and
// the required ImageIdentifier / ImageVersion members must be non-empty (the
// SDK validates nil client-side but not empty strings). It records the
// maxResults of every list call so tests can prove the emitted page size is
// wire-valid on every page.
type enforcingSDKStub struct {
	mu              sync.Mutex
	images          []types.MicrovmImageSummary
	versions        []types.MicrovmImageVersionSummary
	imagePageSize   int
	versionPageSize int
	imageMR         []int32
	versionMR       []int32
}

// newEnforcingSDKStub returns a stub pre-loaded with one target image plus a
// decoy (for exact-name narrowing) and four image versions. Page sizes below
// the item count force pagination: each call serves one page and returns a
// NextToken while items remain, so a paging client must call again until the
// token is nil.
func newEnforcingSDKStub(imagePageSize, versionPageSize int) *enforcingSDKStub {
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	vs := make([]types.MicrovmImageVersionSummary, 4)
	for i := range vs {
		vs[i] = types.MicrovmImageVersionSummary{
			ImageVersion: aws.String(fmt.Sprintf("v%d", 10-i)),
			CreatedAt:    aws.Time(base.Add(time.Duration(i+1) * time.Hour)),
			State:        "SUCCESSFUL",
			Status:       "ACTIVE",
		}
	}
	return &enforcingSDKStub{
		imagePageSize:   imagePageSize,
		versionPageSize: versionPageSize,
		images: []types.MicrovmImageSummary{
			{
				Name:                     aws.String(testImageName),
				ImageArn:                 aws.String(testImageARN),
				LatestActiveImageVersion: aws.String("v10"),
			},
			{
				Name:                     aws.String("lesser-host-lab_other"),
				ImageArn:                 aws.String("arn:aws:lambda-microvms:us-east-1:123456789012:image/lesser-host-lab_other"),
				LatestActiveImageVersion: aws.String("v3"),
			},
		},
		versions: vs,
	}
}

func (s *enforcingSDKStub) ListMicrovmImages(_ context.Context, params *lambdamicrovms.ListMicrovmImagesInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImagesOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if params.MaxResults != nil {
		if err := validateListMaxResults(*params.MaxResults); err != nil {
			return nil, err
		}
		s.imageMR = append(s.imageMR, *params.MaxResults)
	} else {
		s.imageMR = append(s.imageMR, 0) // sentinel: MaxResults missing — fails the wire-valid assertion
	}
	page, next := takePage(s.images, s.imagePageSize)
	s.images = s.images[len(page):]
	return &lambdamicrovms.ListMicrovmImagesOutput{Items: page, NextToken: next}, nil
}

func (s *enforcingSDKStub) ListMicrovmImageVersions(_ context.Context, params *lambdamicrovms.ListMicrovmImageVersionsInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.ListMicrovmImageVersionsOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRequiredMember(params.ImageIdentifier, "ImageIdentifier"); err != nil {
		return nil, err
	}
	if params.MaxResults != nil {
		if err := validateListMaxResults(*params.MaxResults); err != nil {
			return nil, err
		}
		s.versionMR = append(s.versionMR, *params.MaxResults)
	} else {
		s.versionMR = append(s.versionMR, 0) // sentinel: MaxResults missing — fails the wire-valid assertion
	}
	page, next := takePage(s.versions, s.versionPageSize)
	s.versions = s.versions[len(page):]
	return &lambdamicrovms.ListMicrovmImageVersionsOutput{Items: page, NextToken: next}, nil
}

func (s *enforcingSDKStub) DeleteMicrovmImageVersion(_ context.Context, params *lambdamicrovms.DeleteMicrovmImageVersionInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.DeleteMicrovmImageVersionOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRequiredMember(params.ImageIdentifier, "ImageIdentifier"); err != nil {
		return nil, err
	}
	if err := validateRequiredMember(params.ImageVersion, "ImageVersion"); err != nil {
		return nil, err
	}
	return &lambdamicrovms.DeleteMicrovmImageVersionOutput{}, nil
}

// validateRequiredMember pins the service's documented required members
// (ImageIdentifier on list/delete; ImageVersion on delete). The SDK validates
// nil client-side but not empty strings, so the stub rejects both with the
// service's ValidationException shape.
func validateRequiredMember(v *string, field string) error {
	if v == nil || *v == "" {
		return serviceValidationError(fmt.Sprintf("1 validation error detected: missing required field %s", field))
	}
	return nil
}

// takePage serves up to n items from items, returning a NextToken while items
// remain so a paginating client keeps calling until the token is nil.
func takePage[T any](items []T, n int) ([]T, *string) {
	if n <= 0 || len(items) <= n {
		return items, nil
	}
	return items[:n], aws.String("next")
}

func TestSDKClientListCallsEmitWireValidPageSize(t *testing.T) {
	// Issue #1058 regression guard through the real construction path: the
	// deploy invokes NewSDKClient over the lambdamicrovms client (the prune
	// CLI's newClient does exactly this). Here the same constructor is driven
	// over the enforcing stub, which rejects any maxResults above the service
	// cap with the service's ValidationException — if a list call ever emitted
	// an out-of-bound page size again, ResolveImage or ListVersions would fail
	// in CI instead of aborting a deploy mid-runbook.
	stub := newEnforcingSDKStub(1, 2) // force pagination on both lists
	client := NewSDKClient(stub)

	img, err := client.ResolveImage(context.Background(), testImageName)
	if err != nil {
		t.Fatalf("ResolveImage: %v", err)
	}
	if img.ARN == "" || img.LatestActiveImageVersion == "" {
		t.Fatalf("ResolveImage returned a partial image: %+v", img)
	}
	if _, err := client.ListVersions(context.Background(), img.ARN); err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if err := client.DeleteVersion(context.Background(), img.ARN, "v1"); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	for i, mr := range stub.imageMR {
		if mr != microvmListPageSize {
			t.Fatalf("ListMicrovmImages page %d sent maxResults=%d, want %d (service cap: >%d is a ValidationException)",
				i+1, mr, microvmListPageSize, microvmListPageSize)
		}
	}
	for i, mr := range stub.versionMR {
		if mr != microvmListPageSize {
			t.Fatalf("ListMicrovmImageVersions page %d sent maxResults=%d, want %d",
				i+1, mr, microvmListPageSize)
		}
	}
	if len(stub.imageMR) < 2 {
		t.Fatalf("image list should paginate (got %d pages); the page-size guard must be exercised across pages", len(stub.imageMR))
	}
	if len(stub.versionMR) < 2 {
		t.Fatalf("version list should paginate (got %d pages)", len(stub.versionMR))
	}
}

func TestStubRejectsOutOfBoundPageSize(t *testing.T) {
	// Class guard for issue #1058: a page size above the service cap must fail
	// against the enforcing stub with the service's ValidationException shape —
	// the same failure the wire produces — so a protocol-invalid call can never
	// pass CI the way the original maxResults=100 defect did.
	for _, bad := range []int32{51, 100} {
		err := validateListMaxResults(bad)
		if err == nil {
			t.Fatalf("validateListMaxResults(%d) = nil, want ValidationException", bad)
		}
		if !isValidationException(err) {
			t.Fatalf("validateListMaxResults(%d) = %v, want a ValidationException", bad, err)
		}
	}
	if err := validateListMaxResults(microvmListPageSize); err != nil {
		t.Fatalf("validateListMaxResults(%d) = %v, want nil (the cap itself is wire-valid)", microvmListPageSize, err)
	}

	// End-to-end through the stub: the same input shape the deploy path builds.
	stub := newEnforcingSDKStub(0, 0)
	_, err := stub.ListMicrovmImageVersions(context.Background(), &lambdamicrovms.ListMicrovmImageVersionsInput{
		ImageIdentifier: aws.String(testImageARN),
		MaxResults:      aws.Int32(100),
	})
	if err == nil {
		t.Fatal("ListMicrovmImageVersions with maxResults=100 must fail against the enforcing stub")
	}
	if !isValidationException(err) {
		t.Fatalf("stub rejection must be a ValidationException, got %v", err)
	}
}

func TestStubRejectsEmptyRequiredMembers(t *testing.T) {
	// The service documents ImageIdentifier (and ImageVersion on delete) as
	// required; the SDK validates nil client-side but not empty strings, so the
	// stub pins non-empty to keep the wire contract CI-visible.
	stub := newEnforcingSDKStub(0, 0)
	_, err := stub.ListMicrovmImageVersions(context.Background(), &lambdamicrovms.ListMicrovmImageVersionsInput{
		ImageIdentifier: aws.String(""),
	})
	if !isValidationException(err) {
		t.Fatalf("empty ImageIdentifier must be rejected as a ValidationException, got %v", err)
	}
	_, err = stub.DeleteMicrovmImageVersion(context.Background(), &lambdamicrovms.DeleteMicrovmImageVersionInput{
		ImageIdentifier: aws.String(testImageARN),
		ImageVersion:    aws.String(""),
	})
	if !isValidationException(err) {
		t.Fatalf("empty ImageVersion must be rejected as a ValidationException, got %v", err)
	}
}

// isValidationException reports whether err is a smithy API error carrying the
// service's ValidationException code.
func isValidationException(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == validationExceptionCode
}
