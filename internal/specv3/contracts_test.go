package specv3

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestV3ContractFixturesValidate(t *testing.T) {
	t.Parallel()

	base := filepath.Join("..", "..", "docs", "spec", "v3")
	schemasDir := filepath.Join(base, "schemas")
	fixturesDir := filepath.Join(base, "fixtures")

	cases := []struct {
		name     string
		schema   string
		fixtures []string
	}{
		{
			name:   "communication_inbound_notification",
			schema: filepath.Join(schemasDir, "communication-inbound-notification.schema.json"),
			fixtures: []string{
				filepath.Join(fixturesDir, "communication-inbound-notification.email.example.json"),
				filepath.Join(fixturesDir, "communication-inbound-notification.sms.example.json"),
			},
		},
		{
			name:     "communication_outbound_notification",
			schema:   filepath.Join(schemasDir, "communication-outbound-notification.schema.json"),
			fixtures: []string{filepath.Join(fixturesDir, "communication-outbound-notification.email.example.json")},
		},
		{
			name:   "soul_comm_send_request",
			schema: filepath.Join(schemasDir, "soul-comm-send.request.schema.json"),
			fixtures: []string{
				filepath.Join(fixturesDir, "soul-comm-send.request.example.json"),
				filepath.Join(fixturesDir, "soul-comm-send.request.reply-boundary.example.json"),
			},
		},
		{
			name:     "soul_comm_send_response",
			schema:   filepath.Join(schemasDir, "soul-comm-send.response.schema.json"),
			fixtures: []string{filepath.Join(fixturesDir, "soul-comm-send.response.example.json")},
		},
		{
			name:   "soul_comm_send_error",
			schema: filepath.Join(schemasDir, "soul-comm-send.error.schema.json"),
			fixtures: []string{
				filepath.Join(fixturesDir, "soul-comm-send.error.preference-violation.example.json"),
				filepath.Join(fixturesDir, "soul-comm-send.error.boundary-violation.example.json"),
			},
		},
		{
			name:     "soul_agent_channels_response",
			schema:   filepath.Join(schemasDir, "soul-agent-channels.response.schema.json"),
			fixtures: []string{filepath.Join(fixturesDir, "soul-agent-channels.response.example.json")},
		},
		{
			name:   "soul_instance_bootstrap_error",
			schema: filepath.Join(schemasDir, "soul-instance-bootstrap.error.schema.json"),
			fixtures: []string{
				filepath.Join(fixturesDir, "soul-instance-bootstrap.error.boundary-violation.example.json"),
				filepath.Join(fixturesDir, "soul-instance-bootstrap.error.budget-exhausted.example.json"),
			},
		},
		{
			name:   "hosted_genesis_conversation_response",
			schema: filepath.Join(schemasDir, "hosted-genesis.conversation.response.schema.json"),
			fixtures: []string{
				filepath.Join(fixturesDir, "hosted-genesis.conversation.in-progress.example.json"),
				filepath.Join(fixturesDir, "hosted-genesis.conversation.assistant-turn-ready.example.json"),
				filepath.Join(fixturesDir, "hosted-genesis.conversation.completed-declaration-ready.example.json"),
				filepath.Join(fixturesDir, "hosted-genesis.conversation.published.example.json"),
				filepath.Join(fixturesDir, "hosted-genesis.conversation.failed.example.json"),
			},
		},
		{
			name:     "soul_instance_bootstrap_finalize_response",
			schema:   filepath.Join(schemasDir, "soul-instance-bootstrap.finalize.response.schema.json"),
			fixtures: []string{filepath.Join(fixturesDir, "soul-instance-bootstrap.finalize.response.hosted-offchain.example.json")},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema := mustCompileSchema(t, tc.schema)
			for _, fixturePath := range tc.fixtures {
				fixture := mustReadJSON(t, fixturePath)
				if err := schema.Validate(fixture); err != nil {
					t.Fatalf("fixture %s did not validate: %v", fixturePath, err)
				}
			}
		})
	}
}

func mustCompileSchema(t *testing.T, schemaPath string) *jsonschema.Schema {
	t.Helper()

	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema %s: %v", schemaPath, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse schema %s: %v", schemaPath, err)
	}

	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()

	for _, siblingPath := range mustSchemaFiles(t, filepath.Dir(schemaPath)) {
		siblingDoc := doc
		if siblingPath != schemaPath {
			siblingDoc = mustReadJSON(t, siblingPath)
		}
		if addErr := compiler.AddResource(siblingPath, siblingDoc); addErr != nil {
			t.Fatalf("add schema resource %s: %v", siblingPath, addErr)
		}
		if schemaID := schemaDocumentID(siblingDoc); schemaID != "" {
			if addErr := compiler.AddResource(schemaID, siblingDoc); addErr != nil {
				t.Fatalf("add schema resource %s: %v", schemaID, addErr)
			}
		}
	}

	schema, compileErr := compiler.Compile(schemaPath)
	if compileErr != nil {
		t.Fatalf("compile schema %s: %v", schemaPath, compileErr)
	}
	return schema
}

func mustSchemaFiles(t *testing.T, schemasDir string) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(schemasDir, "*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no schemas found in %s", schemasDir)
	}
	return files
}

func schemaDocumentID(doc any) string {
	obj, ok := doc.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := obj["$id"].(string)
	return id
}

func mustReadJSON(t *testing.T, path string) any {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json %s: %v", path, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse json %s: %v", path, err)
	}
	return doc
}
