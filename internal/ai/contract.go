package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type CacheScope string

const (
	CacheScopeInstance CacheScope = "instance"
	CacheScopeGlobal   CacheScope = "global"
)

type JobStatus string

const (
	JobStatusQueued           JobStatus = "queued"
	JobStatusOK               JobStatus = "ok"
	JobStatusNotCheckedBudget JobStatus = "not_checked_budget"
	JobStatusError            JobStatus = "error"
)

// ModuleContract is the common envelope for all AI modules.
// It is the stable cache key and audit surface, not the module-specific payload schema.
type ModuleContract struct {
	Module        string    `json:"module"`
	PolicyVersion string    `json:"policy_version"`
	ModelSet      string    `json:"model_set"`
	InputsHash    string    `json:"inputs_hash"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// ModuleOutput is the normalized output shape for all AI modules.
// Result is module-specific JSON (validated by the module).
type ModuleOutput struct {
	Contract ModuleContract   `json:"contract"`
	Result   json.RawMessage  `json:"result"`
	Usage    models.AIUsage   `json:"usage,omitempty"`
	Errors   []models.AIError `json:"errors,omitempty"`
}

func CanonicalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func InputsHash(v any) (string, error) {
	s, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:]), nil
}

func CacheKey(scope CacheScope, scopeKey string, module string, policyVersion string, modelSet string, inputsHash string) (string, error) {
	module = strings.ToLower(strings.TrimSpace(module))
	policyVersion = strings.TrimSpace(policyVersion)
	modelSet = strings.TrimSpace(modelSet)
	inputsHash = strings.TrimSpace(inputsHash)
	if module == "" || policyVersion == "" || modelSet == "" || inputsHash == "" {
		return "", fmt.Errorf("invalid cache key fields")
	}

	scope = CacheScope(strings.ToLower(strings.TrimSpace(string(scope))))
	switch scope {
	case CacheScopeGlobal:
		scopeKey = ""
	case CacheScopeInstance:
		scopeKey = strings.TrimSpace(scopeKey)
		if scopeKey == "" {
			return "", fmt.Errorf("scopeKey is required for instance scope")
		}
	default:
		return "", fmt.Errorf("unsupported cache scope %q", scope)
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		"airesult",
		string(scope),
		scopeKey,
		module,
		policyVersion,
		modelSet,
		inputsHash,
	}, "|")))
	return hex.EncodeToString(sum[:]), nil
}
