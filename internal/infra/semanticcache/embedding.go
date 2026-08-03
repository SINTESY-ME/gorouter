package semanticcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// GorouterEmbeddingProvider generates embeddings by calling the gorouter's
// own /v1/embeddings endpoint. It uses an in-memory LRU cache to skip
// redundant embedding calls for identical prompts.
type GorouterEmbeddingProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client

	mu       sync.Mutex
	cache    map[string]cachedEmbedding
	cacheCap int
}

// SetModel changes the embedding model used for future requests.
func (p *GorouterEmbeddingProvider) SetModel(model string) {
	if model == "" {
		return
	}
	p.mu.Lock()
	p.model = model
	// Model changed; clear cached vectors tied to the old model.
	p.cache = make(map[string]cachedEmbedding)
	p.mu.Unlock()
}

type cachedEmbedding struct {
	vec       []float32
	createdAt time.Time
}

const embeddingCacheTTL = 30 * time.Minute

// NewGorouterEmbeddingProvider creates an embedding provider that calls
// the gorouter's own embeddings endpoint. baseURL should be the full base
// (e.g. "http://localhost:20128"). apiKey is the client key for auth.
// model is the embedding model ID (e.g. "openai/text-embedding-3-small").
func NewGorouterEmbeddingProvider(baseURL, apiKey, model string) *GorouterEmbeddingProvider {
	return &GorouterEmbeddingProvider{
		baseURL:  baseURL,
		apiKey:   apiKey,
		model:    model,
		client:   &http.Client{Timeout: 10 * time.Second},
		cache:    make(map[string]cachedEmbedding),
		cacheCap: 512,
	}
}

// Embed returns the embedding vector for the given text. Results are
// cached by SHA-256 hash of the text to avoid redundant API calls.
func (p *GorouterEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	h := sha256.Sum256([]byte(text))
	hashKey := hex.EncodeToString(h[:])

	p.mu.Lock()
	if entry, ok := p.cache[hashKey]; ok && time.Since(entry.createdAt) < embeddingCacheTTL {
		vec := entry.vec
		p.mu.Unlock()
		return vec, nil
	}
	p.mu.Unlock()

	reqBody, err := json.Marshal(map[string]any{
		"model": p.model,
		"input": text,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(buf))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned no data")
	}
	vec := result.Data[0].Embedding

	p.mu.Lock()
	if len(p.cache) >= p.cacheCap {
		// Evict one random entry (simplistic LRU for small cache sizes).
		for k := range p.cache {
			delete(p.cache, k)
			break
		}
	}
	p.cache[hashKey] = cachedEmbedding{vec: vec, createdAt: time.Now()}
	p.mu.Unlock()

	return vec, nil
}
