package trust

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

func TestHandleTrustAuthVerify_RequiresInstanceAuth(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, err := s.handleTrustAuthVerify(&apptheory.Context{}); err == nil {
		t.Fatal("expected unauthorized")
	}

	resp, err := s.handleTrustAuthVerify(&apptheory.Context{AuthIdentity: "demo"})
	if err != nil {
		t.Fatalf("handleTrustAuthVerify: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	var out trustAuthVerifyResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != "ok" || out.InstanceSlug != "demo" {
		t.Fatalf("unexpected response: %#v", out)
	}
}
