package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/microvmversions"
)

// stubClient is a minimal microvmversions.Client for exercising the CLI
// without any AWS calls.
type stubClient struct {
	found   bool
	image   microvmversions.Image
	version []microvmversions.Version
	deleted int
}

func (s *stubClient) ResolveImage(_ context.Context, name string) (microvmversions.Image, error) {
	if !s.found {
		return microvmversions.Image{}, microvmversions.ErrImageNotFound
	}
	return s.image, nil
}

func (s *stubClient) ListVersions(_ context.Context, _ string) ([]microvmversions.Version, error) {
	return append([]microvmversions.Version(nil), s.version...), nil
}

func (s *stubClient) DeleteVersion(_ context.Context, _, imageVersion string) error {
	for i, v := range s.version {
		if v.ImageVersion == imageVersion {
			s.version = append(s.version[:i], s.version[i+1:]...)
			s.deleted++
			return nil
		}
	}
	return nil
}

func stubFactory(s *stubClient) func() (microvmversions.Client, error) {
	return func() (microvmversions.Client, error) { return s, nil }
}

func newStubVersions(n int) []microvmversions.Version {
	vs := make([]microvmversions.Version, 0, n)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		vs = append(vs, microvmversions.Version{
			ImageVersion: "v" + strconv.Itoa(i),
			CreatedAt:    base.Add(time.Duration(i) * time.Hour),
			State:        "SUCCESSFUL",
		})
	}
	return vs
}

func TestRunPruneHappyPath(t *testing.T) {
	s := &stubClient{
		found:   true,
		image:   microvmversions.Image{Name: "lesser-host-lab_hosted_genesis", ARN: "arn:test"},
		version: newStubVersions(10),
	}
	out, err := run([]string{"prune", "lab"}, stubFactory(s))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(out, "deleted: 5") {
		t.Fatalf("output missing deletion count: %s", out)
	}
	if !strings.Contains(out, "remaining after prune: 5") {
		t.Fatalf("output missing remaining count: %s", out)
	}
	if !strings.Contains(out, "lesser-host-lab_hosted_genesis") {
		t.Fatalf("output missing image name: %s", out)
	}
}

func TestRunKeepNFlag(t *testing.T) {
	s := &stubClient{
		found:   true,
		image:   microvmversions.Image{Name: "lesser-host-live_hosted_genesis", ARN: "arn:test"},
		version: newStubVersions(10),
	}
	out, err := run([]string{"prune", "-keep-n", "3", "live"}, stubFactory(s))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if s.deleted != 7 {
		t.Fatalf("deleted = %d, want 7", s.deleted)
	}
	if !strings.Contains(out, "keep newest 3") {
		t.Fatalf("output missing keep-N: %s", out)
	}
}

func TestRunKeepNEnvOverride(t *testing.T) {
	t.Setenv("LESSER_HOST_MICROVM_KEEP_N", "2")
	s := &stubClient{
		found:   true,
		image:   microvmversions.Image{Name: "lesser-host-lab_hosted_genesis", ARN: "arn:test"},
		version: newStubVersions(10),
	}
	if _, err := run([]string{"prune", "lab"}, stubFactory(s)); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if s.deleted != 8 {
		t.Fatalf("deleted = %d, want 8", s.deleted)
	}
}

func TestRunRejectsInvalidKeepNEnv(t *testing.T) {
	t.Setenv("LESSER_HOST_MICROVM_KEEP_N", "abc")
	if _, err := run([]string{"prune", "lab"}, stubFactory(&stubClient{})); err == nil {
		t.Fatal("expected error for invalid LESSER_HOST_MICROVM_KEEP_N, got nil")
	}
}

func TestRunRejectsInvalidStage(t *testing.T) {
	if _, err := run([]string{"prune", "prod"}, stubFactory(&stubClient{})); err == nil {
		t.Fatal("expected error for invalid stage, got nil")
	}
}

func TestRunRejectsMissingArgs(t *testing.T) {
	if _, err := run([]string{"prune"}, stubFactory(&stubClient{})); err == nil {
		t.Fatal("expected error for missing stage, got nil")
	}
}

func TestRunImageNotFoundNoop(t *testing.T) {
	s := &stubClient{found: false}
	out, err := run([]string{"prune", "lab"}, stubFactory(s))
	if err != nil {
		t.Fatalf("expected no-op on missing image, got error: %v", err)
	}
	if s.deleted != 0 {
		t.Fatalf("deleted = %d, want 0", s.deleted)
	}
	if !strings.Contains(out, "image not found") {
		t.Fatalf("output missing not-found note: %s", out)
	}
}

func writeTemplate(t *testing.T, resources string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "template.json")
	tmpl := `{"Resources": {` + resources + `}}`
	if err := os.WriteFile(path, []byte(tmpl), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return path
}

func microvmImageResource(name string) string {
	return `"HostedGenesisMicrovmImage": {"Type": "AWS::Lambda::MicrovmImage", "Properties": {"Name": "` + name + `"}}`
}

func TestRunPruneTemplateDeclaresImagePrunes(t *testing.T) {
	s := &stubClient{
		found:   true,
		image:   microvmversions.Image{Name: "lesser-host-lab_hosted_genesis", ARN: "arn:test"},
		version: newStubVersions(10),
	}
	path := writeTemplate(t, microvmImageResource("lesser-host-lab_hosted_genesis"))
	out, err := run([]string{"prune", "lab", path}, stubFactory(s))
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if s.deleted != 5 {
		t.Fatalf("deleted = %d, want 5", s.deleted)
	}
	if !strings.Contains(out, "deleted: 5") {
		t.Fatalf("output missing deletion count: %s", out)
	}
}

func TestRunPruneFailClosedOnTemplateDeclaredImage(t *testing.T) {
	// The template declares the image but resolution fails: that is name drift
	// or out-of-band deletion of the whole image, not a first deploy. The
	// prune must fail closed naming both the expected name and the failure.
	s := &stubClient{found: false}
	path := writeTemplate(t, microvmImageResource("lesser-host-lab_hosted_genesis"))
	_, err := run([]string{"prune", "lab", path}, stubFactory(s))
	if err == nil {
		t.Fatal("expected fail-closed on template-declared-but-unresolvable image, got nil")
	}
	if !strings.Contains(err.Error(), "lesser-host-lab_hosted_genesis") {
		t.Fatalf("error must name the expected image: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must include the resolution failure: %v", err)
	}
	if s.deleted != 0 {
		t.Fatalf("deleted = %d, want 0", s.deleted)
	}
}

func TestRunPruneTemplateWithoutImageResourceNoop(t *testing.T) {
	// No AWS::Lambda::MicrovmImage resource in the template: a genuine first
	// deploy is possible, so image-not-found remains a clean no-op.
	s := &stubClient{found: false}
	path := writeTemplate(t, `"Bucket": {"Type": "AWS::S3::Bucket"}`)
	out, err := run([]string{"prune", "lab", path}, stubFactory(s))
	if err != nil {
		t.Fatalf("expected no-op when the template lacks the image resource, got error: %v", err)
	}
	if s.deleted != 0 {
		t.Fatalf("deleted = %d, want 0", s.deleted)
	}
	if !strings.Contains(out, "image not found") {
		t.Fatalf("output missing not-found note: %s", out)
	}
}

func TestRunPruneTemplateNameMismatchRejected(t *testing.T) {
	// The template declares a different image name than the stage-derived one:
	// refuse rather than prune a possibly wrong image.
	s := &stubClient{
		found:   true,
		image:   microvmversions.Image{Name: "lesser-host-lab_hosted_genesis", ARN: "arn:test"},
		version: newStubVersions(10),
	}
	path := writeTemplate(t, microvmImageResource("lesser-host-somewhere_hosted_genesis"))
	_, err := run([]string{"prune", "lab", path}, stubFactory(s))
	if err == nil {
		t.Fatal("expected rejection on template/stage image-name mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "lesser-host-somewhere_hosted_genesis") ||
		!strings.Contains(err.Error(), "lesser-host-lab_hosted_genesis") {
		t.Fatalf("error must name both names: %v", err)
	}
	if s.deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (mismatch must not delete)", s.deleted)
	}
}

func TestRunRejectsKeepNBelowOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
	}{
		{name: "flag zero", args: []string{"prune", "-keep-n", "0", "lab"}},
		{name: "env zero", args: []string{"prune", "lab"}, env: "0"},
		{name: "env negative", args: []string{"prune", "lab"}, env: "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("LESSER_HOST_MICROVM_KEEP_N", tc.env)
			}
			if _, err := run(tc.args, stubFactory(&stubClient{})); err == nil {
				t.Fatal("expected error for keep-N < 1, got nil")
			}
		})
	}
}
