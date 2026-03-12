package manageddomain

import "testing"

func TestStageHelpers(t *testing.T) {
	t.Parallel()

	if got := StageForControlPlane("prod"); got != StageLive {
		t.Fatalf("expected live stage, got %q", got)
	}
	if got := StageForControlPlane("stage"); got != StageStaging {
		t.Fatalf("expected staging stage, got %q", got)
	}
	if got := StageForControlPlane("lab"); got != StageDev {
		t.Fatalf("expected dev stage, got %q", got)
	}

	if got := StageDomain("prod", "Example.com."); got != "example.com" {
		t.Fatalf("unexpected live domain: %q", got)
	}
	if got := StageDomain("lab", "Example.com."); got != "dev.example.com" {
		t.Fatalf("unexpected dev domain: %q", got)
	}

	if _, ok := BaseDomainFromStageDomain("prod", "example.com"); ok {
		t.Fatal("expected live domain not to be treated as alias")
	}
	if base, ok := BaseDomainFromStageDomain("lab", "dev.simulacrum.greater.website"); !ok || base != "simulacrum.greater.website" {
		t.Fatalf("expected managed alias to resolve, got %q %v", base, ok)
	}
	if _, ok := BaseDomainFromStageDomain("lab", "example.com"); ok {
		t.Fatal("expected base domain not to be treated as alias")
	}
}
