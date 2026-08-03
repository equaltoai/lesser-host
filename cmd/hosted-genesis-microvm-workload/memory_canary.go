package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

const (
	processMemoryCanaryName          = "hosted-genesis-microvm-process-memory-canary/v1"
	processMemoryCanaryMetadataValue = "process-memory"
	processMemoryCanaryNonceBytes    = 32
)

var processMemoryCanaries = newProcessMemoryCanaryStore()

type processMemoryCanaryStore struct {
	mu      sync.Mutex
	entries map[string]processMemoryCanaryEntry
}

type processMemoryCanaryEntry struct {
	nonce []byte
	hash  string
}

type processMemoryCanaryResult struct {
	Canary           string `json:"canary"`
	RequestID        string `json:"request_id"`
	TenantID         string `json:"tenant_id"`
	Namespace        string `json:"namespace"`
	SessionID        string `json:"session_id"`
	CorrelationID    string `json:"correlation_id"`
	NonceHash        string `json:"nonce_hash"`
	CheckpointMarker string `json:"checkpoint_marker,omitempty"`
	Initialized      bool   `json:"initialized"`
	MemoryPreserved  *bool  `json:"memory_preserved,omitempty"`
}

func newProcessMemoryCanaryStore() *processMemoryCanaryStore {
	return &processMemoryCanaryStore{entries: map[string]processMemoryCanaryEntry{}}
}

func (s *processMemoryCanaryStore) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]processMemoryCanaryEntry{}
}

func (s *processMemoryCanaryStore) initializeOrObserve(event runtimemicrovm.LifecycleEvent, expectedHash string) (processMemoryCanaryResult, int, error) {
	if s == nil {
		return processMemoryCanaryResult{}, http.StatusInternalServerError, errors.New("process memory canary store is unavailable")
	}
	if !processMemoryCanaryStageAllowed() {
		return processMemoryCanaryResult{}, http.StatusForbidden, errors.New("process-memory canary is lab-only")
	}
	event = normalizeCanaryEvent(event)
	if err := validateProcessMemoryCanaryEvent(event); err != nil {
		return processMemoryCanaryResult{}, http.StatusBadRequest, err
	}
	checkpointMarker, err := sanitizeCanaryCheckpointMarker(event.Metadata["checkpoint_marker"])
	if err != nil {
		return processMemoryCanaryResult{}, http.StatusBadRequest, err
	}
	expectedHash = normalizeCanaryHash(expectedHash)
	if strings.TrimSpace(event.Metadata["expected_nonce_hash"]) != "" && expectedHash == "" {
		return processMemoryCanaryResult{}, http.StatusBadRequest, errors.New("expected nonce hash is invalid")
	}
	key := processMemoryCanaryKey(event)

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[key]
	if expectedHash != "" {
		if !ok {
			return missingProcessMemoryCanaryResult(event, checkpointMarker, expectedHash), http.StatusOK, nil
		}
		observedHash, intact := entry.observedHash()
		if !intact {
			return missingProcessMemoryCanaryResult(event, checkpointMarker, expectedHash), http.StatusOK, nil
		}
		if observedHash != expectedHash {
			return processMemoryCanaryResult{}, http.StatusConflict, errors.New("expected nonce hash does not match process-memory canary state")
		}
		preserved := true
		return processMemoryCanaryResult{
			Canary:           processMemoryCanaryName,
			RequestID:        strings.TrimSpace(event.RequestID),
			TenantID:         strings.TrimSpace(event.TenantID),
			Namespace:        strings.TrimSpace(event.Namespace),
			SessionID:        strings.TrimSpace(event.SessionID),
			CorrelationID:    canaryCorrelationID(observedHash),
			NonceHash:        observedHash,
			CheckpointMarker: checkpointMarker,
			MemoryPreserved:  &preserved,
		}, http.StatusOK, nil
	}

	if ok {
		observedHash, intact := entry.observedHash()
		if !intact {
			return processMemoryCanaryResult{}, http.StatusConflict, errors.New("process-memory canary state is incomplete")
		}
		return processMemoryCanaryResult{
			Canary:           processMemoryCanaryName,
			RequestID:        strings.TrimSpace(event.RequestID),
			TenantID:         strings.TrimSpace(event.TenantID),
			Namespace:        strings.TrimSpace(event.Namespace),
			SessionID:        strings.TrimSpace(event.SessionID),
			CorrelationID:    canaryCorrelationID(observedHash),
			NonceHash:        observedHash,
			CheckpointMarker: checkpointMarker,
			Initialized:      false,
		}, http.StatusOK, nil
	}

	nonce := make([]byte, processMemoryCanaryNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return processMemoryCanaryResult{}, http.StatusInternalServerError, errors.New("generate process-memory canary nonce")
	}
	hash := hashCanaryNonce(nonce)
	s.entries[key] = processMemoryCanaryEntry{nonce: nonce, hash: hash}
	return processMemoryCanaryResult{
		Canary:           processMemoryCanaryName,
		RequestID:        strings.TrimSpace(event.RequestID),
		TenantID:         strings.TrimSpace(event.TenantID),
		Namespace:        strings.TrimSpace(event.Namespace),
		SessionID:        strings.TrimSpace(event.SessionID),
		CorrelationID:    canaryCorrelationID(hash),
		NonceHash:        hash,
		CheckpointMarker: checkpointMarker,
		Initialized:      true,
	}, http.StatusOK, nil
}

func (e processMemoryCanaryEntry) observedHash() (string, bool) {
	if len(e.nonce) != processMemoryCanaryNonceBytes {
		return "", false
	}
	observed := hashCanaryNonce(e.nonce)
	if e.hash != "" && e.hash != observed {
		return "", false
	}
	return observed, true
}

func missingProcessMemoryCanaryResult(event runtimemicrovm.LifecycleEvent, checkpointMarker string, expectedHash string) processMemoryCanaryResult {
	preserved := false
	return processMemoryCanaryResult{
		Canary:           processMemoryCanaryName,
		RequestID:        strings.TrimSpace(event.RequestID),
		TenantID:         strings.TrimSpace(event.TenantID),
		Namespace:        strings.TrimSpace(event.Namespace),
		SessionID:        strings.TrimSpace(event.SessionID),
		CorrelationID:    canaryCorrelationID(expectedHash),
		NonceHash:        expectedHash,
		CheckpointMarker: checkpointMarker,
		MemoryPreserved:  &preserved,
	}
}

func (s *hookServer) handleProcessMemoryCanaryEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	event, err := decodeLifecycleEvent(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_canary_event"})
		return
	}
	expectedHash := ""
	if event.Metadata != nil {
		expectedHash = event.Metadata["expected_nonce_hash"]
	}
	result, status, err := processMemoryCanaries.initializeOrObserve(event, expectedHash)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, result)
}

func normalizeCanaryEvent(event runtimemicrovm.LifecycleEvent) runtimemicrovm.LifecycleEvent {
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.TenantID = strings.TrimSpace(event.TenantID)
	event.Namespace = strings.TrimSpace(event.Namespace)
	event.SessionID = strings.TrimSpace(event.SessionID)
	event.State = runtimemicrovm.LifecycleState(strings.TrimSpace(string(event.State)))
	if len(event.Metadata) > 0 {
		metadata := make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			key = strings.TrimSpace(key)
			if key != "" {
				metadata[key] = strings.TrimSpace(value)
			}
		}
		event.Metadata = metadata
	}
	return event
}

func validateProcessMemoryCanaryEvent(event runtimemicrovm.LifecycleEvent) error {
	if event.RequestID == "" || event.TenantID == "" || event.Namespace == "" || event.SessionID == "" {
		return errors.New("process-memory canary event binding is incomplete")
	}
	if event.Namespace != hostedgenesis.MicroVMNamespace {
		return fmt.Errorf("process-memory canary namespace %q is not %s", event.Namespace, hostedgenesis.MicroVMNamespace)
	}
	if event.State != runtimemicrovm.StateRunning && event.State != runtimemicrovm.StateReady {
		return fmt.Errorf("process-memory canary state %q is not invokable", event.State)
	}
	if strings.TrimSpace(event.Metadata["canary"]) != processMemoryCanaryMetadataValue {
		return errors.New("process-memory canary metadata marker is missing")
	}
	return nil
}

func sanitizeCanaryCheckpointMarker(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 128 {
		return "", errors.New("process-memory canary checkpoint marker is too long")
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{
		"authorization",
		"bearer",
		"credential",
		"endpoint-token",
		"instance-api-key",
		"nonce_hash",
		"nonce-plaintext",
		"nonce_plaintext",
		"nonce-value",
		"nonce_value",
		"provider-secret",
		"raw-transcript",
		"secret",
		"ssm",
		"token",
		"wallet-signature",
		"x-aws-proxy-auth",
	} {
		if strings.Contains(lower, forbidden) {
			return "", errors.New("process-memory canary checkpoint marker is not Host-safe metadata")
		}
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '-', '_', ':', '.', '/':
			continue
		default:
			return "", errors.New("process-memory canary checkpoint marker has unsupported characters")
		}
	}
	return value, nil
}

func processMemoryCanaryStageAllowed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("STAGE")), "lab")
}

func processMemoryCanaryKey(event runtimemicrovm.LifecycleEvent) string {
	return strings.TrimSpace(event.TenantID) + "|" + strings.TrimSpace(event.Namespace) + "|" + strings.TrimSpace(event.SessionID)
}

func hashCanaryNonce(nonce []byte) string {
	sum := sha256.Sum256(append([]byte(processMemoryCanaryName+":"), nonce...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeCanaryHash(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return "sha256:" + value
}

func canaryCorrelationID(hash string) string {
	hash = normalizeCanaryHash(hash)
	if hash == "" {
		return ""
	}
	trimmed := strings.TrimPrefix(hash, "sha256:")
	if len(trimmed) > 16 {
		trimmed = trimmed[:16]
	}
	return "canary-" + trimmed
}

func marshalCanaryResult(result processMemoryCanaryResult) ([]byte, error) {
	return json.Marshal(result)
}
