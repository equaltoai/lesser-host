package soulemail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultInboundDomainForStage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		stage string
		want  string
	}{
		{name: "blank", stage: "", want: LabInboundDomain},
		{name: "lab", stage: "lab", want: LabInboundDomain},
		{name: "lab trimmed", stage: " LAB ", want: LabInboundDomain},
		{name: "live", stage: "live", want: LiveInboundDomain},
		{name: "live trimmed", stage: " Live ", want: LiveInboundDomain},
		{name: "unknown fails to lab", stage: "preview", want: LabInboundDomain},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := DefaultInboundDomainForStage(tc.stage); got != tc.want {
				t.Fatalf("DefaultInboundDomainForStage(%q) = %q, want %q", tc.stage, got, tc.want)
			}
		})
	}
}

func TestInboundDomainFromEnvOrStage(t *testing.T) {
	t.Parallel()

	if got := InboundDomainFromEnvOrStage(func(string) string { return "" }, "lab"); got != LabInboundDomain {
		t.Fatalf("lab default = %q, want %q", got, LabInboundDomain)
	}
	if got := InboundDomainFromEnvOrStage(func(string) string { return "" }, "live"); got != LiveInboundDomain {
		t.Fatalf("live default = %q, want %q", got, LiveInboundDomain)
	}
	if got := InboundDomainFromEnvOrStage(func(string) string { return " Custom.Example " }, "lab"); got != "custom.example" {
		t.Fatalf("env override = %q, want custom.example", got)
	}
}

func TestDocsAvoidLabStageForwardingToLiveBridge(t *testing.T) {
	t.Parallel()

	docs := []string{
		"instance-scoped-soul-email-m2-migration.md",
		"instance-scoped-soul-email-m4-runbook.md",
		"soul-surface.md",
	}
	for _, name := range docs {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
			if err != nil {
				t.Fatalf("read docs/%s: %v", name, err)
			}
			content := string(raw)
			if !strings.Contains(content, LabInboundDomain) {
				t.Fatalf("docs/%s must name the lab bridge domain %q", name, LabInboundDomain)
			}
			if !strings.Contains(content, LiveInboundDomain) {
				t.Fatalf("docs/%s must name the live bridge domain %q", name, LiveInboundDomain)
			}
			for _, block := range fencedCodeBlocks(content) {
				if strings.Contains(block, "--stage lab") && strings.Contains(block, LiveInboundDomain) {
					t.Fatalf("docs/%s has a lab command block that forwards to live bridge %q:\n%s", name, LiveInboundDomain, block)
				}
			}
		})
	}
}

func fencedCodeBlocks(markdown string) []string {
	var blocks []string
	var current []string
	inBlock := false
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "```") {
			if inBlock {
				blocks = append(blocks, strings.Join(current, "\n"))
				current = nil
				inBlock = false
				continue
			}
			inBlock = true
			current = nil
			continue
		}
		if inBlock {
			current = append(current, line)
		}
	}
	return blocks
}
