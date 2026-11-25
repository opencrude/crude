package providersim

import (
	"cmp"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"net/http"
	"sync"
	"time"
)

type messageState struct {
	server *Server
	key    []byte
	mu     sync.Mutex
	sealed map[[32]byte]bool              // signed reasoning, fixture ciphertext and both compaction modes
	cache  map[[32]byte]messageCacheEntry // under server.mu, shared with prefix accounting
}

func newMessageState(s *Server) *messageState {
	return &messageState{server: s, key: []byte(rand.Text()), sealed: make(map[[32]byte]bool), cache: make(map[[32]byte]messageCacheEntry)}
}

// Synthetic server-local HMAC. Record the entire issued block, including
// caller-scripted thinking signatures. The real crypto is private. Threshold
// summary blocks do not have a signature field; authenticate those internally.
// https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand.md
// https://platform.claude.com/docs/en/build-with-claude/compaction-threshold.md
// https://platform.claude.com/docs/en/build-with-claude/thinking.md
func (s *messageState) digest(v messageObject) [32]byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(jsonBytes(v))
	return [32]byte(mac.Sum(nil))
}
func (s *messageState) issue(v messageObject, sign bool) messageObject {
	if sign {
		d := s.digest(v)
		v["signature"] = base64.RawStdEncoding.EncodeToString(d[:])
	}
	d := s.digest(v)
	s.mu.Lock()
	s.sealed[d] = true
	s.mu.Unlock()
	return v
}
func (s *messageState) verifyBlock(req *Request, cfg compactConfig, i, j int, path string, v messageObject) error {
	typ := jsonField[string](v, "type")
	switch typ {
	case "thinking", "redacted_thinking", "compaction":
	default:
		if v["signature"] != nil {
			return invalidMessage(path, "Unexpected signature on this block type")
		}
		return nil
	}
	if typ == "compaction" {
		if v["signature"] != nil {
			if !hasMessageBeta(req.Header, demandBeta) {
				return invalidMessage(path, "Signed compaction blocks require anthropic-beta: "+demandBeta)
			}
			if cfg.threshold > 0 {
				return invalidMessage(path, "A signed compaction block cannot be used with threshold compaction")
			}
			if i != 0 || j != 0 {
				return messageViolation{path, "The signed compaction block must be first", "compaction_block_misplaced"}
			}
		} else if cfg.threshold == 0 {
			return invalidMessage(path, "A threshold compaction block requires a compact_20260112 edit")
		}
		// cache_control on the summary is documented replay metadata, not part
		// of the authenticated response. No other extra fields are ignored.
		if _, ok := v["cache_control"]; ok {
			v = maps.Clone(v)
			delete(v, "cache_control")
		}
	}
	d := s.digest(v)
	s.mu.Lock()
	ok := s.sealed[d]
	s.mu.Unlock()
	if !ok {
		if typ == "compaction" {
			return messageViolation{path, "Invalid compaction block. Send the block exactly as returned.", "compaction_signature_invalid"}
		}
		return invalidMessage(path, "Invalid signature in "+typ+" block")
	}
	return nil
}

// The cache minima and compaction support come from the prompt-caching and
// compaction docs above (Claude API, not Bedrock). Exact model IDs only;
// unknown IDs have a synthetic 1024-token minimum and no compaction support.
type messageProfile struct {
	minimum           int
	demand, threshold bool
}

func messageModelProfile(id string) messageProfile {
	switch id {
	case "claude-fable-5-2", "claude-mythos-5-2", "claude-fable-5-1", "claude-mythos-5-1", "claude-fable-5", "claude-mythos-5", "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5", "claude-haiku-5-5":
		return messageProfile{512, true, true}
	case "claude-opus-4-8", "claude-sonnet-5", "claude-sonnet-4-6":
		return messageProfile{1024, true, true}
	case "claude-mythos-preview", "claude-opus-4-7":
		return messageProfile{2048, true, true}
	case "claude-opus-4-6":
		return messageProfile{4096, true, true}
	case "claude-haiku-4-5-20251001", "claude-haiku-4-5":
		return messageProfile{4096, false, true}
	case "claude-opus-4-5-20251101", "claude-opus-4-5":
		return messageProfile{4096, false, false}
	default:
		return messageProfile{minimum: 1024}
	}
}

// Models and inventory: https://platform.claude.com/docs/en/api/models/list.md
// and anthropic-sdk-python@f1edcd7 resources/beta/models.py::retrieve. Synthetic
// inventory metadata and non-compaction capabilities describe the simulator,
// not a complete account catalog. The compaction flags match the handler.
func (s *messageState) models(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Messages, r)
	if err != nil {
		s.reject(w, req, err)
		return
	}
	req.Model = r.PathValue("id")
	s.server.observe(req)
	entries, ok := s.server.inventory(w, r, Messages, "claude-sonnet-4-6")
	if !ok {
		return
	}
	beta := hasMessageBeta(r.Header, demandBeta)
	if req.Model != "" {
		for _, m := range entries {
			if m.ID == req.Model {
				writeJSON(w, 200, messageModelRow(m, beta))
				return
			}
		}
		writeJSON(w, 404, messageError(w, "not_found_error", "Model not found.", ""))
		return
	}
	models := make([]messageObject, 0, len(entries))
	var first, last any
	for _, m := range entries {
		models = append(models, messageModelRow(m, beta))
	}
	if len(entries) > 0 {
		first, last = entries[0].ID, entries[len(entries)-1].ID
	}
	writeJSON(w, 200, messageObject{"data": models, "first_id": first, "last_id": last, "has_more": false})
}

func messageModelRow(m Model, demand bool) messageObject {
	yes, no := messageObject{"supported": true}, messageObject{"supported": false}
	p := messageModelProfile(m.ID)
	caps := messageObject{"batch": no, "citations": no, "code_execution": no, "image_input": yes, "pdf_input": no, "structured_outputs": no,
		"server_tools": messageObject{"supported": false, "web_search": no, "code_execution": no}, "effort": messageObject{"supported": false, "high": no, "low": no, "max": no, "medium": no},
		"context_management": messageObject{"supported": p.threshold, "compact_20260112": messageObject{"supported": p.threshold}},
		"thinking":           messageObject{"supported": true, "types": messageObject{"enabled": yes, "disabled": yes, "adaptive": yes, "between_tools": no}}}
	if demand {
		caps["compaction"] = messageObject{"supported": p.demand, "summarize": messageObject{"supported": p.demand}}
	}
	row := messageObject{"id": m.ID, "type": "model", "created_at": "1970-01-01T00:00:00Z", "display_name": cmp.Or(m.DisplayName, m.ID), "lifecycle": "active", "capabilities": caps}
	if m.ContextWindow != 0 {
		row["max_input_tokens"] = m.ContextWindow
	}
	return row
}

type messageCacheEntry struct {
	ttl     time.Duration
	expires time.Time
}
type messageCachePoint struct {
	key           [32]byte
	tokens, group int
	ttl           time.Duration
}
type messageCachePlan struct {
	points     []messageCachePoint
	publish    map[[32]byte]time.Duration
	hourWrites int
}

// Synthetic cache keys and token counts: hash the model and each successive
// rendered block. Limits, lookback grouping, TTL, and A/B/C usage rules are
// from https://platform.claude.com/docs/en/build-with-claude/prompt-caching.md.
func (p messagePrompt) cachePlan(model string) messageCachePlan {
	plan := messageCachePlan{}
	if p.marks == 0 {
		has := false
		for _, m := range p.metadata {
			if m.ttl != 0 {
				has = true
				break
			}
		}
		if !has {
			return plan
		}
	}
	sum := sha256.Sum256([]byte(model))
	tokens, group := 0, 0
	prev := ""
	h := sha256.New()
	for i, b := range p.blocks {
		m := p.metadata[i]
		if m.typ != prev || m.typ != "tool_use" && m.typ != "tool_result" {
			group++
		}
		prev = m.typ
		h.Reset()
		h.Write(sum[:])
		h.Write([]byte(b.text))
		h.Sum(sum[:0])
		tokens += estimateTokens(b.text)
		plan.points = append(plan.points, messageCachePoint{sum, tokens, group, m.ttl})
	}
	return plan
}

// Runs under server.mu in dispatchWithPrefix. A shared prefix is necessary
// but not sufficient: a previous response must have published a breakpoint
// within the 20-position lookback. Never publish during request dispatch.
func (c *messageCachePlan) account(prefix *Prefix, profile CacheProfile, cache map[[32]byte]messageCacheEntry, now time.Time) {
	prefix.CachedTokens, prefix.CacheWriteTokens = 0, 0
	if profile.Granularity == 0 {
		return
	}
	last, hour := 0, 0
	hit, hourHit := -1, -1
	c.publish = make(map[[32]byte]time.Duration)
	for i, p := range c.points {
		if p.ttl == 0 {
			continue
		}
		eligible := profile.eligible(p.tokens)
		if eligible == 0 {
			continue
		}
		last = eligible
		if p.ttl == time.Hour {
			hour = eligible
		}
		c.publish[p.key] = p.ttl
		for j := min(i, prefix.SharedBlocks-1); j >= 0 && p.group-c.points[j].group < 20; j-- {
			entry, ok := cache[c.points[j].key]
			if !ok {
				continue
			}
			if !now.Before(entry.expires) {
				delete(cache, c.points[j].key)
				continue
			}
			hit = max(hit, j)
			if entry.ttl == time.Hour {
				hourHit = max(hourHit, j)
			}
		}
	}
	if hit >= 0 && c.points[hit].tokens < hour && cache[c.points[hit].key].ttl != time.Hour {
		hit = hourHit
	}
	if hit >= 0 {
		p := c.points[hit]
		prefix.CachedTokens = profile.eligible(p.tokens)
		c.publish[p.key] = cache[p.key].ttl
	}
	prefix.CacheWriteTokens = max(0, last-prefix.CachedTokens)
	c.hourWrites = max(0, hour-prefix.CachedTokens)
}
func (s *messageState) publishCache(c messageCachePlan) {
	if len(c.publish) == 0 {
		return
	}
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	now := time.Now()
	for key, ttl := range c.publish {
		if old, ok := s.cache[key]; ok && now.Before(old.expires) {
			ttl = old.ttl
		}
		s.cache[key] = messageCacheEntry{ttl, now.Add(ttl)}
	}
}
