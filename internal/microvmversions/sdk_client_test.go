package microvmversions

import (
	"errors"
	"testing"

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
