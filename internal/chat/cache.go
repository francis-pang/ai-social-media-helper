package chat

// cache.go implements Gemini Context Caching for reusing system prompts and
// media context across multiple GenerateContent calls within a session.
// See DDR-065: Gemini Context Caching and Batch API Integration.

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// DefaultCacheTTL is the time-to-live for cached context entries.
// 1 hour is sufficient for a user session to complete triage → selection → description.
const DefaultCacheTTL = 1 * time.Hour

// MinCacheTokens is the minimum token count required by the Gemini API
// for context caching to be effective.
const MinCacheTokens = 4096

// CacheConfig controls how context caching is applied to a GenerateContent call.
type CacheConfig struct {
	// SessionID uniquely identifies the user session for cache keying.
	SessionID string

	// Operation identifies the pipeline stage (e.g., "selection", "triage", "description").
	Operation string

	// TTL overrides the default cache TTL. Zero uses DefaultCacheTTL.
	TTL time.Duration
}

// CacheManager manages Gemini cached content entries for a client.
// It is safe for concurrent use.
type CacheManager struct {
	client *genai.Client
	mu     sync.Mutex
	caches map[string]*genai.CachedContent // cacheKey -> cached content
}

// NewCacheManager creates a CacheManager for the given Gemini client.
func NewCacheManager(client *genai.Client) *CacheManager {
	return &CacheManager{
		client: client,
		caches: make(map[string]*genai.CachedContent),
	}
}

// cacheKey returns the lookup key for a given session and operation.
func cacheKey(sessionID, operation string) string {
	return sessionID + ":" + operation
}

// GetOrCreate returns an existing cached content entry or creates a new one.
// systemInstruction is the system prompt to cache.
// contents are the user-role parts (media + prompt context) to cache.
// modelName is the Gemini model the cache will be used with.
//
// If caching fails (e.g., token count below minimum), returns ("", nil)
// and the caller should fall back to inline context.
func (cm *CacheManager) GetOrCreate(
	ctx context.Context,
	cfg CacheConfig,
	modelName string,
	systemInstruction *genai.Content,
	contents []*genai.Content,
) (string, error) {
	key := cacheKey(cfg.SessionID, cfg.Operation)

	cm.mu.Lock()
	if cached, ok := cm.caches[key]; ok {
		cm.mu.Unlock()
		log.Debug().
			Str("cache_key", key).
			Str("cache_name", cached.Name).
			Msg("Reusing existing Gemini context cache")
		return cached.Name, nil
	}
	cm.mu.Unlock()

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultCacheTTL
	}

	log.Info().
		Str("cache_key", key).
		Str("model", modelName).
		Dur("ttl", ttl).
		Int("content_parts", countParts(contents)).
		Msg("Creating Gemini context cache")

	createStart := time.Now()
	cached, err := cm.client.Caches.Create(ctx, modelName, &genai.CreateCachedContentConfig{
		SystemInstruction: systemInstruction,
		Contents:          contents,
		TTL:               ttl,
		DisplayName:       key,
	})
	createDuration := time.Since(createStart)

	if err != nil {
		log.Warn().
			Err(err).
			Str("cache_key", key).
			Dur("duration", createDuration).
			Msg("Failed to create Gemini context cache — falling back to inline context")
		return "", nil
	}

	log.Info().
		Str("cache_key", key).
		Str("cache_name", cached.Name).
		Dur("duration", createDuration).
		Msg("Gemini context cache created")

	cm.mu.Lock()
	cm.caches[key] = cached
	cm.mu.Unlock()

	return cached.Name, nil
}

// Get returns the cache name for an existing entry, or empty string if not cached.
func (cm *CacheManager) Get(sessionID, operation string) string {
	key := cacheKey(sessionID, operation)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cached, ok := cm.caches[key]; ok {
		return cached.Name
	}
	return ""
}

// Delete removes a cached content entry from both Gemini and the local map.
func (cm *CacheManager) Delete(ctx context.Context, sessionID, operation string) {
	key := cacheKey(sessionID, operation)

	cm.mu.Lock()
	cached, ok := cm.caches[key]
	if ok {
		delete(cm.caches, key)
	}
	cm.mu.Unlock()

	if !ok {
		return
	}

	if _, err := cm.client.Caches.Delete(ctx, cached.Name, nil); err != nil {
		log.Warn().
			Err(err).
			Str("cache_key", key).
			Str("cache_name", cached.Name).
			Msg("Failed to delete Gemini context cache")
	} else {
		log.Debug().
			Str("cache_key", key).
			Str("cache_name", cached.Name).
			Msg("Gemini context cache deleted")
	}
}

// DeleteAll removes all cached content entries for a session.
func (cm *CacheManager) DeleteAll(ctx context.Context, sessionID string) {
	cm.mu.Lock()
	var toDelete []struct {
		key  string
		name string
	}
	for k, v := range cm.caches {
		if len(k) > len(sessionID) && k[:len(sessionID)+1] == sessionID+":" {
			toDelete = append(toDelete, struct {
				key  string
				name string
			}{k, v.Name})
			delete(cm.caches, k)
		}
	}
	cm.mu.Unlock()

	for _, entry := range toDelete {
		if _, err := cm.client.Caches.Delete(ctx, entry.name, nil); err != nil {
			log.Warn().
				Err(err).
				Str("cache_key", entry.key).
				Msg("Failed to delete Gemini context cache during session cleanup")
		} else {
			log.Debug().
				Str("cache_key", entry.key).
				Msg("Gemini context cache deleted during session cleanup")
		}
	}
}

// GenerateWithCache calls GenerateContent using a cached context if available,
// or falls back to inline context. It handles the full flow:
// 1. Try to get/create cache with the provided system instruction and media.
// 2. If cached, call GenerateContent with CachedContent reference.
// 3. If not cached, call GenerateContent with inline system instruction.
//
// userParts are the parts specific to this request (e.g., the text prompt).
// cacheContents are the parts to cache (e.g., media files) — only used if cache doesn't exist yet.
func (cm *CacheManager) GenerateWithCache(
	ctx context.Context,
	cfg CacheConfig,
	modelName string,
	systemInstruction *genai.Content,
	cacheContents []*genai.Content,
	userParts []*genai.Part,
	extraConfig *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	cacheName, err := cm.GetOrCreate(ctx, cfg, modelName, systemInstruction, cacheContents)
	if err != nil {
		log.Warn().Err(err).Msg("Cache creation error — proceeding with inline context")
	}

	config := &genai.GenerateContentConfig{}
	if extraConfig != nil {
		*config = *extraConfig
	}

	var contents []*genai.Content

	if cacheName != "" {
		config.CachedContent = cacheName
		config.SystemInstruction = nil
		contents = []*genai.Content{{Role: "user", Parts: userParts}}

		log.Debug().
			Str("cache_name", cacheName).
			Msg("Using cached context for GenerateContent")
	} else {
		config.SystemInstruction = systemInstruction
		allParts := make([]*genai.Part, 0, len(userParts))
		for _, c := range cacheContents {
			allParts = append(allParts, c.Parts...)
		}
		allParts = append(allParts, userParts...)
		contents = []*genai.Content{{Role: "user", Parts: allParts}}

		log.Debug().Msg("Using inline context for GenerateContent (cache unavailable)")
	}

	return cm.client.Models.GenerateContent(ctx, modelName, contents, config)
}

// countParts returns the total number of parts across all content entries.
func countParts(contents []*genai.Content) int {
	n := 0
	for _, c := range contents {
		n += len(c.Parts)
	}
	return n
}
