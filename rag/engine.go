package rag

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"crude/embed"
)

const (
	maxChunkSize  = 1000
	chunkOverlap  = 200
	batchSize     = 20
)

// Chunk is a piece of text with its embedding vector.
type Chunk struct {
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	Embedding []float32 `json:"embedding"`
}

// Engine provides RAG search over a collection of text documents.
type Engine struct {
	client       *embed.Client
	mu           sync.RWMutex
	persisted    []Chunk            // embedded from AddDocuments (cached on disk)
	observations []Chunk            // ephemeral per-turn observations
	cacheDir     string
	hashes       map[string]string  // title → SHA256(title::content)
}

// New creates a RAG engine backed by the given embedding client.
// cacheDir is where embeddings are persisted (~/.crude/rag/).
func New(client *embed.Client, cacheDir string) *Engine {
	return &Engine{
		client:   client,
		cacheDir: cacheDir,
		hashes:   make(map[string]string),
	}
}

// AddDocuments embeds a map of {title: content} documents using SHA256 cache.
// If all documents match the cached hash, embeddings are loaded from disk.
func (e *Engine) AddDocuments(docs map[string]string) error {
	if len(docs) == 0 {
		return nil
	}

	// Compute hashes for incoming docs.
	incoming := make(map[string]string, len(docs))
	for title, content := range docs {
		h := sha256.Sum256([]byte(title + "::" + content))
		incoming[title] = fmt.Sprintf("%x", h)
	}

	// Try loading from cache if hashes match.
	if e.tryLoadCache(incoming) {
		return nil
	}

	// Chunk and embed all documents.
	var allChunks []Chunk
	var allTexts []string
	var chunkMeta []struct{ title, text string }

	for title, content := range docs {
		chunks := chunkText(title, content)
		for _, c := range chunks {
			chunkMeta = append(chunkMeta, struct{ title, text string }{title, c})
			allTexts = append(allTexts, c)
		}
	}

	// Embed in batches of batchSize.
	embeddings, err := embedBatched(e.client, allTexts)
	if err != nil {
		return fmt.Errorf("rag: embed documents: %w", err)
	}

	for i, meta := range chunkMeta {
		allChunks = append(allChunks, Chunk{
			Title:     meta.title,
			Text:      meta.text,
			Embedding: embeddings[i],
		})
	}

	e.mu.Lock()
	e.persisted = append(e.persisted, allChunks...)
	for k, v := range incoming {
		e.hashes[k] = v
	}
	e.mu.Unlock()

	// Save to cache.
	e.saveCache()
	return nil
}

// AddObservation adds an ephemeral observation (not cached, cleared per turn).
func (e *Engine) AddObservation(title, content string) error {
	vec, err := e.client.Embed(title + "\n" + content)
	if err != nil {
		return fmt.Errorf("rag: embed observation: %w", err)
	}
	e.mu.Lock()
	e.observations = append(e.observations, Chunk{
		Title:     title,
		Text:      content,
		Embedding: vec,
	})
	e.mu.Unlock()
	return nil
}

// Search returns the top-k chunks with cosine similarity ≥ minScore.
func (e *Engine) Search(query string, topK int, minScore float32) ([]Chunk, error) {
	vec, err := e.client.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("rag: embed query: %w", err)
	}

	e.mu.RLock()
	all := make([]Chunk, 0, len(e.persisted)+len(e.observations))
	all = append(all, e.persisted...)
	all = append(all, e.observations...)
	e.mu.RUnlock()

	type scored struct {
		chunk Chunk
		score float32
	}
	var results []scored
	for _, c := range all {
		if len(c.Embedding) == 0 {
			continue
		}
		s := embed.CosineSim(vec, c.Embedding)
		if s >= minScore {
			results = append(results, scored{c, s})
		}
	}

	// Sort descending by score.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	out := make([]Chunk, len(results))
	for i, r := range results {
		out[i] = r.chunk
	}
	return out, nil
}

// BuildContext searches for the top-3 relevant chunks and formats them.
func (e *Engine) BuildContext(query string) (string, error) {
	chunks, err := e.Search(query, 3, 0.3)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "", nil
	}
	var parts []string
	for _, c := range chunks {
		parts = append(parts, "Title: "+c.Title+"\nContent: "+c.Text)
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}

// ResetObservations clears per-turn observations but keeps persisted docs.
func (e *Engine) ResetObservations() {
	e.mu.Lock()
	e.observations = e.observations[:0]
	e.mu.Unlock()
}

// ── Cache helpers ─────────────────────────────────────────────────────────────

type cacheMetadata struct {
	DocumentHashes map[string]string `json:"document_hashes"`
}

func (e *Engine) tryLoadCache(incoming map[string]string) bool {
	metaPath := filepath.Join(e.cacheDir, "rag_metadata.json")
	embPath := filepath.Join(e.cacheDir, "rag_embeddings.json")

	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	var meta cacheMetadata
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return false
	}

	// Check all incoming docs match.
	for title, hash := range incoming {
		if meta.DocumentHashes[title] != hash {
			return false
		}
	}

	embData, err := os.ReadFile(embPath)
	if err != nil {
		return false
	}
	var chunks []Chunk
	if err := json.Unmarshal(embData, &chunks); err != nil {
		return false
	}

	e.mu.Lock()
	e.persisted = append(e.persisted, chunks...)
	for k, v := range incoming {
		e.hashes[k] = v
	}
	e.mu.Unlock()
	return true
}

func (e *Engine) saveCache() {
	if err := os.MkdirAll(e.cacheDir, 0700); err != nil {
		return
	}

	e.mu.RLock()
	chunks := make([]Chunk, len(e.persisted))
	copy(chunks, e.persisted)
	hashes := make(map[string]string, len(e.hashes))
	for k, v := range e.hashes {
		hashes[k] = v
	}
	e.mu.RUnlock()

	if data, err := json.MarshalIndent(chunks, "", "  "); err == nil {
		os.WriteFile(filepath.Join(e.cacheDir, "rag_embeddings.json"), data, 0600)
	}
	meta := cacheMetadata{DocumentHashes: hashes}
	if data, err := json.MarshalIndent(meta, "", "  "); err == nil {
		os.WriteFile(filepath.Join(e.cacheDir, "rag_metadata.json"), data, 0600)
	}
}

// ── Chunking ──────────────────────────────────────────────────────────────────

// chunkText splits content into overlapping chunks of at most maxChunkSize chars.
// Each chunk is prefixed with its title, matching the C# ChunkText logic.
func chunkText(title, content string) []string {
	paragraphs := strings.Split(content, "\n\n")
	var chunks []string
	var current strings.Builder
	var carry string // last chunkOverlap chars of previous chunk

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			chunks = append(chunks, title+"\n"+text)
		}
		// Keep the last chunkOverlap chars as carry-over.
		if len(text) > chunkOverlap {
			carry = text[len(text)-chunkOverlap:]
		} else {
			carry = text
		}
		current.Reset()
		if carry != "" {
			current.WriteString(carry)
		}
	}

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if current.Len()+len(para)+2 > maxChunkSize && current.Len() > 0 {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
		if current.Len() >= maxChunkSize {
			flush()
		}
	}
	if current.Len() > 0 {
		flush()
	}

	return chunks
}

// embedBatched embeds texts in batches of batchSize with retries handled by the client.
func embedBatched(client *embed.Client, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := client.EmbedBatch(texts[start:end])
		if err != nil {
			return nil, err
		}
		for i, v := range vecs {
			result[start+i] = v
		}
	}
	return result, nil
}
