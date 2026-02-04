package llm

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGoldens = flag.Bool("update-goldens", false, "update golden fixtures")

func TestGoldenSchemasAndPrompts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		filename string
		content  []byte
	}{
		{
			name:     "render_summary_schema_v1",
			filename: "render_summary_schema_v1.json",
			content:  mustJSON(renderSummaryJSONSchemaV1()),
		},
		{
			name:     "render_summary_system_v1",
			filename: "render_summary_system_v1.txt",
			content:  []byte(renderSummarySystemPromptV1() + "\n"),
		},
		{
			name:     "moderation_schema_v1",
			filename: "moderation_schema_v1.json",
			content:  mustJSON(moderationJSONSchemaV1()),
		},
		{
			name:     "moderation_system_v1",
			filename: "moderation_system_v1.txt",
			content:  []byte(moderationSystemPromptV1() + "\n"),
		},
		{
			name:     "claim_verify_schema_v1",
			filename: "claim_verify_schema_v1.json",
			content:  mustJSON(claimVerifyJSONSchemaV1()),
		},
		{
			name:     "claim_verify_system_v1",
			filename: "claim_verify_system_v1.txt",
			content:  []byte(claimVerifySystemPromptV1() + "\n"),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("testdata", tc.filename)

			if *updateGoldens {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(path, tc.content, 0o644); err != nil {
					t.Fatalf("write golden %s: %v", path, err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}

			if string(want) != string(tc.content) {
				t.Fatalf("golden mismatch for %s; re-run with -update-goldens", tc.filename)
			}
		})
	}
}

func mustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	// Stable trailing newline.
	b = append(b, '\n')
	return b
}
