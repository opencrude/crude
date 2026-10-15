package provider

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"crude/network"
	"crude/retry"

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
);
CREATE TABLE IF NOT EXISTS agent_output_limits (
 agent_profile TEXT NOT NULL, provider_scope TEXT NOT NULL,
 account_id TEXT NOT NULL, model_id TEXT NOT NULL,
 max_output_tokens INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 PRIMARY KEY(agent_profile, provider_scope, account_id, model_id)
 );
 CREATE TABLE IF NOT EXISTS response_media_replay_limitations (
  provider_scope TEXT PRIMARY KEY, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS connection_concurrency_refusals (
 provider_scope TEXT NOT NULL, account_id TEXT NOT NULL, model_id TEXT NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(provider_scope, account_id, model_id)
);`

const modelCatalogRefreshInterval = 6 * time.Hour
const modelCatalogMaxBytes = 32 << 20
const learnedOutputLimitLifetime = 7 * 24 * time.Hour
const learnedResponseMediaReplayLifetime = 7 * 24 * time.Hour

var activeModelCatalog atomic.Pointer[ModelCatalog]

var transientResponseMediaReplay = struct {
	sync.Mutex
	entries map[string]time.Time
}{entries: make(map[string]time.Time)}

// UseModelCatalog installs the app-owned catalog. All readers use its immutable
// memory snapshot; no model lookup performs database or network I/O.
func UseModelCatalog(catalog *ModelCatalog) { activeModelCatalog.Store(catalog) }

// LearnedOutputLimit returns the app-wide provider-confirmed output ceiling
// for an agent profile and an exact account/endpoint/protocol/model connection.
// It is queried only on the background extraction path, never during UI paint.
func LearnedOutputLimit(agentProfile string, connection *Provider, model string) (int, error) {
	catalog := activeModelCatalog.Load()
	if catalog == nil || connection == nil || agentProfile == "" {
		return 0, nil
	}
	var limit int
	err := catalog.db.QueryRow(`SELECT max_output_tokens FROM agent_output_limits WHERE agent_profile=? AND provider_scope=? AND account_id=? AND model_id=? AND updated_at>=?`,
		agentProfile, connection.ReplayScope(), connection.AccountID, model, time.Now().Add(-learnedOutputLimitLifetime).Unix()).Scan(&limit)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return limit, err
}

// RecordOutputLimit persists a proven upper bound without letting concurrent
// agents or a later, weaker observation raise it again.
func RecordOutputLimit(agentProfile string, connection *Provider, model string, limit int) error {
	catalog := activeModelCatalog.Load()
	if catalog == nil || connection == nil || agentProfile == "" || limit <= 0 {
		return nil
	}
	now := time.Now()
	_, err := catalog.db.Exec(`INSERT INTO agent_output_limits(agent_profile,provider_scope,account_id,model_id,max_output_tokens,updated_at)
 VALUES(?,?,?,?,?,?) ON CONFLICT(agent_profile,provider_scope,account_id,model_id) DO UPDATE SET
 max_output_tokens=CASE WHEN agent_output_limits.updated_at<? THEN excluded.max_output_tokens
                       ELSE MIN(agent_output_limits.max_output_tokens, excluded.max_output_tokens) END,
 updated_at=excluded.updated_at`,
		agentProfile, connection.ReplayScope(), connection.AccountID, model, limit, now.Unix(), now.Add(-learnedOutputLimitLifetime).Unix())
	return err
}

// LearnedResponseMediaReplayLimitation reports whether this exact
// endpoint/account/model/request scope has proven unable to reconstruct a
// locally valid inline image through previous_response_id.
func LearnedResponseMediaReplayLimitation(scope string) bool {
	if scope == "" {
		return false
	}
	if catalog := activeModelCatalog.Load(); catalog != nil {
		return catalog.hasResponseMediaReplayLimitation(scope)
	}
	transientResponseMediaReplay.Lock()
	defer transientResponseMediaReplay.Unlock()
	cutoff := time.Now().Add(-learnedResponseMediaReplayLifetime)
	observed, ok := transientResponseMediaReplay.entries[scope]
	if ok && observed.Before(cutoff) {
		delete(transientResponseMediaReplay.entries, scope)
		return false
	}
	return ok
}

// RecordResponseMediaReplayLimitation retains exact provider evidence for seven
// days. When the app-owned catalog is unavailable (library tests or embedding),
// a small process-local cache preserves adaptive behavior without disk I/O.
func RecordResponseMediaReplayLimitation(scope string) error {
	if scope == "" {
		return nil
	}
	now := time.Now()
	if catalog := activeModelCatalog.Load(); catalog != nil {
		return catalog.recordResponseMediaReplayLimitation(scope, now)
	}
	transientResponseMediaReplay.Lock()
	defer transientResponseMediaReplay.Unlock()
	if len(transientResponseMediaReplay.entries) >= 256 {
		cutoff := now.Add(-learnedResponseMediaReplayLifetime)
		for key, observed := range transientResponseMediaReplay.entries {
			if observed.Before(cutoff) || len(transientResponseMediaReplay.entries) >= 256 {
				delete(transientResponseMediaReplay.entries, key)
			}
			if len(transientResponseMediaReplay.entries) < 256 {
				break
			}
		}
	}
	transientResponseMediaReplay.entries[scope] = now
	return nil
}

type catalogSnapshot struct {
	models     map[string]ModelMeta
	aliases    map[string]string
	generation uint64
}

type ModelCatalog struct {
	db        *sql.DB
	sources   []ModelSource
	snapshot  atomic.Pointer[catalogSnapshot]
	updates   chan uint64
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	start     sync.Once
	close     sync.Once
	refreshMu sync.Mutex
	mediaMu   sync.RWMutex
	media     map[string]time.Time
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
	c := &ModelCatalog{
		db: db, sources: slices.Clone(sources),
		updates: make(chan uint64, 1), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		media: make(map[string]time.Time),
	}
	if err := c.loadResponseMediaReplayLimitations(ctx); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	if err := c.reload(ctx); err != nil {
		cancel()
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

func (c *ModelCatalog) loadResponseMediaReplayLimitations(ctx context.Context) error {
	cutoff := time.Now().Add(-learnedResponseMediaReplayLifetime).Unix()
	rows, err := c.db.QueryContext(ctx, `SELECT provider_scope,updated_at FROM response_media_replay_limitations WHERE updated_at>=?`, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := make(map[string]time.Time)
	for rows.Next() {
		var scope string
		var updated int64
		if err := rows.Scan(&scope, &updated); err != nil {
			return err
		}
		if scope != "" {
			loaded[scope] = time.Unix(updated, 0)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	c.mediaMu.Lock()
	c.media = loaded
	c.mediaMu.Unlock()
	return nil
}

func (c *ModelCatalog) hasResponseMediaReplayLimitation(scope string) bool {
	cutoff := time.Now().Add(-learnedResponseMediaReplayLifetime)
	c.mediaMu.RLock()
	observed, ok := c.media[scope]
	c.mediaMu.RUnlock()
	if !ok || !observed.Before(cutoff) {
		return ok
	}
	c.mediaMu.Lock()
	if current, exists := c.media[scope]; exists && current.Before(cutoff) {
		delete(c.media, scope)
	}
	c.mediaMu.Unlock()
	return false
}

func (c *ModelCatalog) recordResponseMediaReplayLimitation(scope string, now time.Time) error {
	c.mediaMu.Lock()
	c.media[scope] = now
	c.mediaMu.Unlock()
	_, err := c.db.Exec(`INSERT INTO response_media_replay_limitations(provider_scope,updated_at) VALUES(?,?)
 ON CONFLICT(provider_scope) DO UPDATE SET updated_at=excluded.updated_at`, scope, now.Unix())
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`DELETE FROM response_media_replay_limitations WHERE updated_at<?`,
		now.Add(-learnedResponseMediaReplayLifetime).Unix())
	return err
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
				wait = min(wait, c.recordFailure(source, err))
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

func (c *ModelCatalog) recordFailure(source ModelSource, failure error) time.Duration {
	// Cache maintenance never emits a product notification, status operation,
	// sound, or chat event. Error evidence remains in the catalog database.
	var failures int
	_ = c.db.QueryRowContext(c.ctx, `SELECT failures FROM sources WHERE id=?`, source.ID).Scan(&failures)
	delay := retry.Exponential(failures+1, 30*time.Second, retry.MaxCooldown)
	var status *StatusError
	if errors.As(failure, &status) {
		delay = max(delay, retry.Clamp(status.RetryAfter))
	}
	_, _ = c.db.ExecContext(c.ctx, `INSERT INTO sources(id,url,format,checked_at,next_refresh,failures,last_error)
 VALUES(?,?,?,?,?,1,?) ON CONFLICT(id) DO UPDATE SET checked_at=excluded.checked_at,
 next_refresh=excluded.next_refresh, failures=sources.failures+1, last_error=excluded.last_error`,
		source.ID, source.URL, source.Format, time.Now().Unix(), time.Now().Add(delay).Unix(), failure.Error())
	return delay
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
	// Capture current application transport policy for this refresh, not once
	// at catalog startup. In-flight requests keep their own immutable settings.
	client := network.NewClient(20 * time.Second)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
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
		return readStatusError(resp, 1<<20)
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
