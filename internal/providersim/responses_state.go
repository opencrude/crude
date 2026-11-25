package providersim

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

type responseObject = map[string]any

// State belongs to one simulator and wire. Immutable wire items preserve the
// exact input window; previous top-level instructions and tools do not carry.
type responseState struct {
	server    *Server
	wire      Wire
	key       []byte
	mu        sync.Mutex
	stored    map[string]storedResponse
	artifacts map[string]sealedWindow
}

type storedResponse struct {
	body    responseObject
	window  []responseObject
	expires time.Time
}
type sealedWindow struct {
	item      responseObject
	originals []responseObject
	summary   responseObject
}

func newResponseState(s *Server, wire Wire) *responseState {
	return &responseState{server: s, wire: wire, key: []byte(rand.Text()), stored: map[string]storedResponse{}, artifacts: map[string]sealedWindow{}}
}

func (s *responseState) lookup(id string) (storedResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.stored[id]
	if ok && !time.Now().Before(row.expires) {
		delete(s.stored, id)
		return storedResponse{}, false
	}
	return row, ok
}

func (s *responseState) save(id string, body responseObject, window []responseObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// OpenAI conversation-state guide: Responses are saved for 30 days by default.
	s.stored[id] = storedResponse{body, window, time.Now().Add(30 * 24 * time.Hour)}
}

// Synthetic: the seal is a keyed digest, not provider ciphertext. Only this
// handler can resolve it. Bind the item ID as well as every compacted item.
func (s *responseState) seal(id string, window []responseObject) responseObject {
	raw := jsonBytes(window)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id))
	mac.Write([]byte{0})
	mac.Write(raw)
	item := responseObject{"type": "compaction", "id": id, "encrypted_content": base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
	// Synthetic lossless summary: one message containing canonical item JSON.
	// Keep originals separately so repeated compaction never escapes old summaries.
	saved := sealedWindow{item: item, originals: window, summary: responseObject{"role": "assistant", "content": string(raw)}}
	s.mu.Lock()
	s.artifacts[id] = saved
	s.mu.Unlock()
	return item
}

// The compaction guide permits replay of either the full window or just the
// suffix beginning at the latest compaction. Never count the prefix twice.
type responseWindow struct {
	rendered  []responseObject
	effective []responseObject
	originals []responseObject
}

func (s *responseState) resolve(items []responseObject) (responseWindow, error) {
	var w responseWindow
	for i, item := range items {
		// The declared type alone must not authenticate a modified item:
		// a compaction id relabeled as item_reference must fail as well.
		var a sealedWindow
		var known bool
		if id := jsonField[string](item, "id"); id != "" {
			s.mu.Lock()
			a, known = s.artifacts[id]
			s.mu.Unlock()
		}
		if item["type"] != "compaction" && !known {
			if s.wire == Codex && item["type"] == "compaction_trigger" {
				continue
			}

			w.rendered = append(w.rendered, item)
			w.effective = append(w.effective, item)
			w.originals = append(w.originals, item)
			continue
		}
		if !known || !bytes.Equal(jsonBytes(item), jsonBytes(a.item)) {
			return w, fmt.Errorf("/input/%d: invalid compaction item", i)
		}
		w = responseWindow{rendered: []responseObject{item}, effective: []responseObject{a.summary}, originals: append([]responseObject{}, a.originals...)}
	}
	return w, nil
}

// Synthetic standalone retention policy: keep the last plain user message
// verbatim after the sealed prefix. Unlike extracting only the seal, replaying
// the entire returned window then preserves all context. Output uses ItemField.
func (s *responseState) compact(index int, items []responseObject) []responseObject {
	id := fmt.Sprintf("cmp_sim_%d", index)
	if len(items) > 1 {
		last := items[len(items)-1]
		text, ok := last["content"].(string)
		if last["role"] == "user" && ok {
			return []responseObject{s.seal(id, items[:len(items)-1]), {"id": fmt.Sprintf("msg_sim_%d_retained", index), "type": "message", "role": "user", "status": "completed", "content": []any{responseObject{"type": "input_text", "text": text}}}}
		}
	}
	return []responseObject{s.seal(id, items)}
}
