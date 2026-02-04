package ai

import "testing"

func TestInputsHash_IsDeterministicForMaps(t *testing.T) {
	t.Parallel()

	a := map[string]any{
		"b": 2,
		"a": "x",
		"nested": map[string]any{
			"y": true,
			"x": 1,
		},
	}
	b := map[string]any{
		"a": "x",
		"nested": map[string]any{
			"x": 1,
			"y": true,
		},
		"b": 2,
	}

	ha, err := InputsHash(a)
	if err != nil {
		t.Fatalf("InputsHash(a) error: %v", err)
	}
	hb, err := InputsHash(b)
	if err != nil {
		t.Fatalf("InputsHash(b) error: %v", err)
	}
	if ha != hb {
		t.Fatalf("expected equal hashes, got %q vs %q", ha, hb)
	}
}

func TestCacheKey_InstanceScopeRequiresScopeKey(t *testing.T) {
	t.Parallel()

	_, err := CacheKey(CacheScopeInstance, "", "m", "v1", "deterministic", "abc")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestCacheKey_GlobalScopeIgnoresScopeKey(t *testing.T) {
	t.Parallel()

	k1, err := CacheKey(CacheScopeGlobal, "inst-a", "m", "v1", "deterministic", "abc")
	if err != nil {
		t.Fatalf("CacheKey error: %v", err)
	}
	k2, err := CacheKey(CacheScopeGlobal, "inst-b", "m", "v1", "deterministic", "abc")
	if err != nil {
		t.Fatalf("CacheKey error: %v", err)
	}
	if k1 != k2 {
		t.Fatalf("expected same cache key, got %q vs %q", k1, k2)
	}
}
