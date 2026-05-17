package controlplane

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostedBoundSoulLaunchGateDocsCoverM4Requirements(t *testing.T) {
	t.Parallel()

	doc := strings.ToLower(readRepositoryFile(t, "docs/hosted-bound-soul-launch-gates.md"))
	required := []string{
		"public_launch_status: blocked",
		"hosted_offchain",
		"immutable_onchain",
		"capability_gate: false",
		"not a prerequisite",
		"payment obligations",
		"refund",
		"failure handling",
		"does not grant principal/operator authority",
		"email",
		"phone",
		"sms",
		"voice",
		"consent",
		"private comms reachability",
		"payment evidence",
		"tenant data",
		"wallet material",
	}
	for _, phrase := range required {
		if !strings.Contains(doc, phrase) {
			t.Fatalf("launch gate doc is missing %q", phrase)
		}
	}
}

func TestHostedBoundSoulLaunchGateDocsAreLinkedFromPublicContracts(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{
		"docs/soul-surface.md",
		"docs/soul-agent-first-client-contract.md",
		"docs/pricing-and-services.md",
		"docs/portal.md",
		"docs/soul-comm-mailbox-migration.md",
	} {
		doc := readRepositoryFile(t, rel)
		if !strings.Contains(doc, "docs/hosted-bound-soul-launch-gates.md") {
			t.Fatalf("%s does not link the hosted-bound-soul launch gates", rel)
		}
	}
}

func readRepositoryFile(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
