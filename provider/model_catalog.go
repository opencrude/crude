package provider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const modelCatalogSchema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=3000;
CREATE TABLE IF NOT EXISTS sources (
 id TEXT PRIMARY KEY, url TEXT NOT NULL, format TEXT NOT NULL,
 etag TEXT NOT NULL DEFAULT '', modified TEXT NOT NULL DEFAULT '',
 checked_at INTEGER NOT NULL DEFAULT 0, succeeded_at INTEGER NOT NULL DEFAULT 0,
 next_refresh INTEGER NOT NULL DEFAULT 0, failures INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', digest TEXT NOT NULL DEFAULT '', body BLOB
);
CREATE TABLE IF NOT EXISTS models (
 source TEXT NOT NULL, model TEXT NOT NULL, metadata BLOB NOT NULL,
 PRIMARY KEY(source, model)
);`

const modelCatalogRefreshInterval = 6 * time.Hour
const modelCatalogMaxBytes = 32 << 20

var activeModelCatalog atomic.Pointer[ModelCatalog]

// UseModelCatalog installs the app-owned catalog. All readers use its immutable
// memory snapshot; no model lookup performs database or network I/O.
func UseModelCatalog(catalog *ModelCatalog) { activeModelCatalog.Store(catalog) }

type catalogSnapshot struct {
	models     map[string]ModelMeta
	aliases    map[string]string
	generation uint64
}

type ModelCatalog struct {
	db        *sql.DB
	sources   []ModelSource
	client    *http.Client
	snapshot  atomic.Pointer[catalogSnapshot]
	updates   chan uint64
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	start     sync.Once
	close     sync.Once
	refreshMu sync.Mutex
	closeErr  error
}

func OpenModelCatalog(path string, sources []ModelSource) (*ModelCatalog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(modelCatalogSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &ModelCatalog{db: db, sources: slices.Clone(sources), client: &http.Client{Timeout: 20 * time.Second}, updates: make(chan uint64, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if err := c.reload(ctx); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

func (c *ModelCatalog) Updates() <-chan uint64 { return c.updates }

func (c *ModelCatalog) Start() { c.start.Do(func() { go c.run() }) }

func (c *ModelCatalog) Close() error {
	c.close.Do(func() {
		c.cancel()
		c.Start()
		<-c.done
		c.refreshMu.Lock()
		defer c.refreshMu.Unlock()
		c.closeErr = c.db.Close()
		activeModelCatalog.CompareAndSwap(c, nil)
	})
	return c.closeErr
}

func (c *ModelCatalog) Lookup(id string) (ModelMeta, bool) {
	snapshot := c.snapshot.Load()
	if snapshot == nil {
		return ModelMeta{}, false
	}
	id = strings.ToLower(id)
	meta, ok := snapshot.models[id]
	if !ok {
		if alias := snapshot.aliases[stripProviderPrefix(id)]; alias != "" {
			meta, ok = snapshot.models[alias]
		}
	}
	return cloneModelMeta(meta), ok
}

func (c *ModelCatalog) Models() map[string]ModelMeta {
	snapshot := c.snapshot.Load()
	out := map[string]ModelMeta{}
	if snapshot != nil {
		for id, meta := range snapshot.models {
			out[id] = cloneModelMeta(meta)
		}
	}
	return out
}

func (c *ModelCatalog) run() {
	defer close(c.done)
	for c.ctx.Err() == nil {
		wait := modelCatalogRefreshInterval
		for _, source := range c.sources {
			if c.ctx.Err() != nil {
				return
			}
			var next int64
			var url string
			err := c.db.QueryRowContext(c.ctx, `SELECT next_refresh, url FROM sources WHERE id=?`, source.ID).Scan(&next, &url)
			if err == nil && url == source.URL && time.Now().Before(time.Unix(next, 0)) {
				wait = min(wait, time.Until(time.Unix(next, 0)))
				continue
			}
			// Sources update sequentially: bounded CPU, one HTTP request and one
			// writer, and each good dataset becomes usable before the next fetch.
			if err := c.refresh(c.ctx, source); err != nil && c.ctx.Err() == nil {
				c.recordFailure(source, err)
				wait = min(wait, 15*time.Minute)
			}
		}
		timer := time.NewTimer(max(time.Second, wait))
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *ModelCatalog) recordFailure(source ModelSource, failure error) {
	// Cache maintenance never emits a product notification, status operation,
	// sound, or chat event. Error evidence remains in the catalog database.
	_, _ = c.db.ExecContext(c.ctx, `INSERT INTO sources(id,url,format,checked_at,next_refresh,failures,last_error)
 VALUES(?,?,?,?,?,1,?) ON CONFLICT(id) DO UPDATE SET checked_at=excluded.checked_at,
 next_refresh=excluded.next_refresh, failures=sources.failures+1, last_error=excluded.last_error`,
		source.ID, source.URL, source.Format, time.Now().Unix(), time.Now().Add(15*time.Minute).Unix(), failure.Error())
}

func (c *ModelCatalog) refresh(ctx context.Context, source ModelSource) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var etag, modified, oldURL, oldDigest string
	err := c.db.QueryRowContext(ctx, `SELECT etag,modified,url,digest FROM sources WHERE id=?`, source.ID).Scan(&etag, &modified, &oldURL, &oldDigest)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Crude-ModelCatalog/1")
	if oldURL == source.URL {
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		if modified != "" {
			req.Header.Set("If-Modified-Since", modified)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	now := time.Now()
	if resp.StatusCode == http.StatusNotModified {
		if oldURL != source.URL || oldDigest == "" {
			return fmt.Errorf("source returned 304 without a cached dataset")
		}
		_, err = c.db.ExecContext(ctx, `UPDATE sources SET checked_at=?,succeeded_at=?,next_refresh=?,failures=0,last_error='' WHERE id=?`, now.Unix(), now.Unix(), now.Add(modelCatalogRefreshInterval).Unix(), source.ID)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", source.ID, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modelCatalogMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > modelCatalogMaxBytes {
		return fmt.Errorf("%s dataset exceeds %d bytes", source.ID, modelCatalogMaxBytes)
	}
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	if oldURL == source.URL && digest == oldDigest {
		_, err = c.db.ExecContext(ctx, `UPDATE sources SET checked_at=?,succeeded_at=?,next_refresh=?,failures=0,last_error='',etag=?,modified=? WHERE id=?`, now.Unix(), now.Unix(), now.Add(modelCatalogRefreshInterval).Unix(), resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), source.ID)
		return err
	}
	models, err := parseModelSource(source.Format, body)
	if err != nil {
		return fmt.Errorf("%s: %w", source.ID, err)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM models WHERE source=?`, source.ID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO models(source,model,metadata) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, meta := range models {
		encoded, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, source.ID, id, encoded); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sources(id,url,format,etag,modified,checked_at,succeeded_at,next_refresh,digest,body)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET url=excluded.url,format=excluded.format,etag=excluded.etag,modified=excluded.modified,
 checked_at=excluded.checked_at,succeeded_at=excluded.succeeded_at,next_refresh=excluded.next_refresh,digest=excluded.digest,body=excluded.body,failures=0,last_error=''`,
		source.ID, source.URL, source.Format, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), now.Unix(), now.Unix(), now.Add(modelCatalogRefreshInterval).Unix(), digest, body); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err := c.reload(ctx); err != nil {
		return err
	}
	select {
	case c.updates <- c.snapshot.Load().generation:
	default:
	}
	return nil
}

func (c *ModelCatalog) reload(ctx context.Context) error {
	type evidence struct {
		source string
		meta   ModelMeta
	}
	byID := map[string][]evidence{}
	for _, source := range c.sources {
		rows, err := c.db.QueryContext(ctx, `SELECT model,metadata FROM models WHERE source=? ORDER BY model`, source.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var data []byte
			if err := rows.Scan(&id, &data); err != nil {
				_ = rows.Close()
				return err
			}
			var meta ModelMeta
			if err := json.Unmarshal(data, &meta); err != nil {
				_ = rows.Close()
				return err
			}
			byID[id] = append(byID[id], evidence{source.ID, meta})
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	snapshot := &catalogSnapshot{models: map[string]ModelMeta{}, aliases: map[string]string{}, generation: 1}
	if old := c.snapshot.Load(); old != nil {
		snapshot.generation = old.generation + 1
	}
	qualifiedIDs := map[string]string{}
	for id := range byID {
		alias := stripProviderPrefix(id)
		if prior, exists := snapshot.aliases[alias]; !exists {
			snapshot.aliases[alias] = id
		} else if prior != id {
			snapshot.aliases[alias] = ""
		}
		if alias != id {
			if _, exists := qualifiedIDs[alias]; !exists {
				qualifiedIDs[alias] = id
			} else {
				qualifiedIDs[alias] = ""
			}
		}
	}
	for id := range byID {
		if stripProviderPrefix(id) == id {
			snapshot.aliases[id] = id
		}
	}
	for id, own := range byID {
		entries := slices.Clone(own)
		base := stripProviderPrefix(id)
		if base != id {
			entries = append(entries, byID[base]...)
		} else {
			// Join qualified evidence only when there is one unambiguous namespace.
			qualified := qualifiedIDs[id]
			if qualified != "" {
				entries = append(entries, byID[qualified]...)
			}
		}
		// Prefer documented source order per field, while retaining disagreement.
		rank := func(id string) int {
			for index, source := range c.sources {
				if source.ID == id {
					return index
				}
			}
			return len(c.sources)
		}
		slices.SortStableFunc(entries, func(a, b evidence) int { return rank(a.source) - rank(b.source) })
		meta := ModelMeta{ModalitySupport: map[string]bool{}}
		for _, entry := range entries {
			if !slices.Contains(meta.Sources, entry.source) {
				meta.Sources = append(meta.Sources, entry.source)
			}
			if meta.ContextWindow == 0 {
				meta.ContextWindow = entry.meta.ContextWindow
			}
			if meta.MaxOutput == 0 {
				meta.MaxOutput = entry.meta.MaxOutput
			}
			if !meta.PricingKnown && entry.meta.PricingKnown {
				meta.InputPrice, meta.OutputPrice, meta.CacheReadPrice, meta.PricingKnown = entry.meta.InputPrice, entry.meta.OutputPrice, entry.meta.CacheReadPrice, true
			}
			if !meta.ToolsKnown && entry.meta.ToolsKnown {
				meta.ToolsKnown, meta.SupportsTools = true, entry.meta.SupportsTools
			}
			for modality, supported := range entry.meta.ModalitySupport {
				if value, known := meta.ModalitySupport[modality]; known && value != supported {
					if !slices.Contains(meta.ConflictingModalities, modality) {
						meta.ConflictingModalities = append(meta.ConflictingModalities, modality)
					}
				} else if !known {
					meta.ModalitySupport[modality] = supported
				}
			}
		}
		for _, modality := range KnownModalities() {
			if meta.ModalitySupport[modality] {
				meta.Modalities = append(meta.Modalities, modality)
			}
		}
		snapshot.models[id] = meta
	}
	c.snapshot.Store(snapshot)
	return nil
}
