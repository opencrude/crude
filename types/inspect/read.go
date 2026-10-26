// Package inspect supplies bounded retrieval of immutable inspection evidence.
// It owns no product state, authorization, sampling or background work. Each
// service owns its Store and must authenticate the owner before calling Read.
package inspect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DefaultLimit        = 12
	MaxLimit            = 64
	DefaultBytes        = 16 << 10
	MaxBytes            = 48 << 10 // leave room for the semantic control envelope
	maxObservationBytes = 4 << 20
	maxRetainedBytes    = 16 << 20
	maxSnapshots        = 16
	maxOwnerSnapshots   = 4
	retention           = 2 * time.Minute
)

// Request starts a read at offset zero, or continues an owner-bound observation.
// View and Filter may be omitted on continuation; if supplied they must match.
type Request struct {
	View       string `json:"view,omitempty"`
	Filter     string `json:"filter,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	MaxBytes   int    `json:"max_bytes,omitempty"`
}

func (r Request) Normalize() (Request, error) {
	if r.Offset < 0 || r.Limit < 0 || r.Limit > MaxLimit || r.MaxBytes < 0 || r.MaxBytes > MaxBytes || r.MaxBytes > 0 && r.MaxBytes < 1024 {
		return r, fmt.Errorf("offset must be nonnegative, limit 1–%d, max_bytes 1024–%d; omit limit/max_bytes for defaults", MaxLimit, MaxBytes)
	}
	if len(r.View) > 64 || len(r.Filter) > 256 || len(r.SnapshotID) > 64 || strings.ContainsAny(r.View+r.Filter+r.SnapshotID, "\x00\r\n") {
		return r, errors.New("inspection selectors exceed their bounds or contain control characters")
	}
	if r.SnapshotID == "" && r.Offset != 0 {
		return r, errors.New("start at offset 0 without snapshot_id; echo the returned next request for subsequent pages")
	}
	if r.Limit == 0 {
		r.Limit = DefaultLimit
	}
	if r.MaxBytes == 0 {
		r.MaxBytes = DefaultBytes
	}
	return r, nil
}

type Record struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}

// Observation is detached evidence collected by the service, not live pointers.
// Source carries identity, units, coverage and collection errors from that owner.
type Observation struct {
	Source  json.RawMessage
	Records []Record
	Notes   []string
}

type Page struct {
	SnapshotID  string          `json:"snapshot_id"`
	View        string          `json:"view"`
	Source      json.RawMessage `json:"source"`
	CollectedAt time.Time       `json:"collected_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	Offset      int             `json:"offset"`
	Total       int             `json:"total"`
	Returned    int             `json:"returned"`
	Complete    bool            `json:"complete"`
	Records     []Record        `json:"records"`
	Next        *Request        `json:"next,omitempty"`
	Notes       []string        `json:"notes,omitempty"`
	Guidance    string          `json:"guidance"`
}

type saved struct {
	owner       string
	request     Request
	observation Observation
	at          time.Time
	bytes       int
}

// Store retains only bounded, short-lived immutable read evidence. Eviction is
// explicit to the reader (expired snapshot), never a replay of a product effect.
// The zero value is usable. There is no timer, persistence or periodic collection.
type Store struct {
	mu         sync.Mutex
	items      map[string]saved
	bytes      int
	collecting int
}

func (s *Store) Read(ctx context.Context, owner string, request Request, collect func(context.Context) (Observation, error)) (Page, error) {
	r, err := request.Normalize()
	if err != nil {
		return Page{}, err
	}
	if owner == "" {
		return Page{}, errors.New("authenticated inspection owner is required")
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	var value saved
	id := r.SnapshotID
	if id != "" {
		s.mu.Lock()
		var ok bool
		value, ok = s.items[id]
		s.mu.Unlock()
		if !ok || value.owner != owner || time.Since(value.at) >= retention {
			return Page{}, errors.New("inspection snapshot expired or belongs to another owner; start a fresh read at offset 0 without snapshot_id")
		}
		if r.View != "" && r.View != value.request.View || r.Filter != "" && r.Filter != value.request.Filter {
			return Page{}, fmt.Errorf("snapshot_id %s contains view=%q filter=%q, not the requested view/filter. To change views or filters send a NEW read with snapshot_id omitted and offset 0. To continue this observation echo its next object unchanged", id, value.request.View, value.request.Filter)
		}
	} else {
		if collect == nil {
			return Page{}, errors.New("inspection collector unavailable")
		}
		s.mu.Lock()
		if s.collecting >= 4 {
			s.mu.Unlock()
			return Page{}, errors.New("inspection collection capacity occupied; retry a narrower read after current requests finish")
		}
		s.collecting++
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.collecting--; s.mu.Unlock() }()
		observation, err := collect(ctx)
		if err != nil {
			return Page{}, err
		}
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		raw, err := json.Marshal(observation)
		if err != nil {
			return Page{}, fmt.Errorf("inspection evidence cannot be encoded: %w", err)
		}
		if len(raw) > maxObservationBytes {
			return Page{}, errors.New("inspection observation exceeds 4 MiB; select a narrower view/filter or fewer history points")
		}
		// Decode the private copy: later owner mutations cannot change any page.
		var detached Observation
		if err := json.Unmarshal(raw, &detached); err != nil {
			return Page{}, err
		}
		if len(detached.Source) == 0 {
			detached.Source = json.RawMessage(`{}`)
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return Page{}, err
		}
		id = hex.EncodeToString(token[:])
		value = saved{owner: owner, request: r, observation: detached, at: time.Now(), bytes: len(raw)}
		s.mu.Lock()
		if s.items == nil {
			s.items = make(map[string]saved)
		}
		for key, item := range s.items {
			if time.Since(item.at) >= retention {
				s.remove(key)
			}
		}
		for {
			ownerCount := 0
			for _, item := range s.items {
				if item.owner == owner {
					ownerCount++
				}
			}
			if len(s.items) < maxSnapshots && s.bytes+value.bytes <= maxRetainedBytes && ownerCount < maxOwnerSnapshots {
				break
			}
			oldest := ""
			for key, item := range s.items {
				if ownerCount >= maxOwnerSnapshots && item.owner != owner {
					continue
				}
				if oldest == "" || item.at.Before(s.items[oldest].at) {
					oldest = key
				}
			}
			if oldest == "" {
				s.mu.Unlock()
				return Page{}, errors.New("inspection retention capacity occupied")
			}
			s.remove(oldest)
		}
		s.items[id] = value
		s.bytes += value.bytes
		s.mu.Unlock()
	}
	if r.Offset > len(value.observation.Records) {
		return Page{}, fmt.Errorf("offset %d exceeds %d records; valid offsets are 0–%d for this snapshot", r.Offset, len(value.observation.Records), len(value.observation.Records))
	}
	p := Page{SnapshotID: id, View: value.request.View, Source: value.observation.Source, CollectedAt: value.at, ExpiresAt: value.at.Add(retention), Offset: r.Offset, Total: len(value.observation.Records), Records: []Record{}, Notes: value.observation.Notes,
		Guidance: "Echo next as arguments.page to continue this immutable observation; omit snapshot_id and use offset 0 for a fresh read. Complete covers this record set, not all source retention or OS permissions; check source and notes for partial/warmup/omission. Expired snapshots require a fresh read, not guessed offsets."}
	setNext := func() {
		p.Returned = len(p.Records)
		p.Complete = p.Offset+p.Returned == p.Total
		p.Next = nil
		if !p.Complete {
			p.Next = &Request{View: value.request.View, Filter: value.request.Filter, SnapshotID: id, Offset: p.Offset + p.Returned, Limit: r.Limit, MaxBytes: r.MaxBytes}
		}
	}
	setNext()
	for _, record := range value.observation.Records[r.Offset:] {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		if len(p.Records) >= r.Limit {
			break
		}
		p.Records = append(p.Records, record)
		setNext()
		raw, err := json.Marshal(p)
		if err != nil {
			return Page{}, err
		}
		if len(raw) > r.MaxBytes {
			p.Records = p.Records[:len(p.Records)-1]
			setNext()
			if len(p.Records) == 0 {
				retry := Request{View: value.request.View, Filter: value.request.Filter, SnapshotID: id, Offset: r.Offset, Limit: r.Limit, MaxBytes: MaxBytes}
				next, _ := json.Marshal(retry)
				return Page{}, fmt.Errorf("inspection record %q (%s) does not fit max_bytes=%d. Retry this SAME retained observation with arguments.page=%s. If it still cannot fit 49152 bytes, start a narrower view/filter without snapshot_id", record.ID[:min(len(record.ID), 120)], record.Kind, r.MaxBytes, next)
			}
			break
		}
	}
	setNext()
	raw, err := json.Marshal(p)
	if err != nil {
		return Page{}, err
	}
	if len(raw) > r.MaxBytes {
		return Page{}, errors.New("inspection metadata exceeds max_bytes; increase max_bytes or select a narrower view")
	}
	// Pages are detached too; caller edits cannot corrupt retained evidence.
	var detached Page
	err = json.Unmarshal(raw, &detached)
	return detached, err
}

func (s *Store) remove(id string) { s.bytes -= s.items[id].bytes; delete(s.items, id) }
