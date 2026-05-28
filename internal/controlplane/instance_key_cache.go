package controlplane

import (
	"context"
	"sync"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// instanceKeyCacheTTL is the short TTL for cached instance API keys.
// Keys expire quickly to limit the window of a stale credential.
const instanceKeyCacheTTL = 60 * time.Second

// instanceKeyCacheEntry holds a resolved plaintext key and its expiry.
type instanceKeyCacheEntry struct {
	key    string
	expiry time.Time
}

// instanceKeyCache is an in-memory, mutex-protected cache of resolved
// managed-instance API keys. It is keyed by the stable secret ARN
// (LesserHostInstanceKeySecretARN). Entries expire after instanceKeyCacheTTL.
//
// Security invariant: plaintext keys are never logged or exposed. The cache
// exists only in process memory. Concurrent access is safe.
type instanceKeyCache struct {
	mu    sync.RWMutex
	items map[string]instanceKeyCacheEntry
}

// newInstanceKeyCache returns an initialized instance key cache.
func newInstanceKeyCache() *instanceKeyCache {
	return &instanceKeyCache{
		items: make(map[string]instanceKeyCacheEntry),
	}
}

// get returns the cached key and true if a valid (non-expired) entry exists
// for the given secret ARN. Returns ("", false) on miss or expiry.
func (c *instanceKeyCache) get(secretArn string) (string, bool) {
	c.mu.RLock()
	entry, ok := c.items[secretArn]
	c.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().UTC().After(entry.expiry) {
		return "", false
	}
	return entry.key, true
}

// set stores a key in the cache with the TTL.
func (c *instanceKeyCache) set(secretArn string, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[secretArn] = instanceKeyCacheEntry{
		key:    key,
		expiry: time.Now().UTC().Add(instanceKeyCacheTTL),
	}
}

// evict removes a key from the cache (used after a fetch failure to avoid
// caching a negative result that would prevent retry).
func (c *instanceKeyCache) evict(secretArn string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, secretArn)
}

// resolveInstanceKeyCached resolves the instance API key, using the short-TTL
// cache to avoid repeated STS/SecretsManager calls. On cache miss, it falls
// through to the underlying resolver and caches the result on success.
func (s *Server) resolveInstanceKeyCached(ctx context.Context, inst *models.Instance) (string, error) {
	secretArn := ""
	if inst != nil {
		secretArn = inst.LesserHostInstanceKeySecretARN
	}

	// Check cache if we have a cache and the secretArn is non-empty.
	if s != nil && s.instanceKeyCache != nil && secretArn != "" {
		if key, ok := s.instanceKeyCache.get(secretArn); ok {
			return key, nil
		}
	}

	key, err := s.resolvePortalCostInstanceKey(ctx, inst)
	if err != nil {
		// Evict on failure so a subsequent request retries.
		if s != nil && s.instanceKeyCache != nil && secretArn != "" {
			s.instanceKeyCache.evict(secretArn)
		}
		return "", err
	}

	// Cache the successful result.
	if s != nil && s.instanceKeyCache != nil && secretArn != "" {
		s.instanceKeyCache.set(secretArn, key)
	}

	return key, nil
}
