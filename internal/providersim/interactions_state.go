package providersim

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"
)

type interactionWire struct {
	server   *Server
	mu       sync.Mutex
	stored   map[string]storedInteraction
	thoughts map[string]map[string]bool
}

type storedInteraction struct {
	body    googleObject
	window  []interactionStep
	frames  []frame
	expires time.Time
}

func newInteractionWire(s *Server) *interactionWire {
	return &interactionWire{server: s, stored: make(map[string]storedInteraction), thoughts: make(map[string]map[string]bool)}
}

func (s *interactionWire) lookup(id string) (storedInteraction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.stored[id]
	if ok && !time.Now().Before(row.expires) {
		delete(s.stored, id)
		return storedInteraction{}, false
	}
	return row, ok
}

func (s *interactionWire) save(req Request, reply interactionReply, current, window []interactionStep, events []frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep the original thought for stateless replay, even when store=false
	// keeps the interaction itself unavailable by ID.
	for i, step := range reply.steps {
		if googleString(step.body, "type") != "thought" {
			continue
		}
		key := string(jsonBytes(interactionStepBodies(slices.Concat(window, reply.steps[:i]))))
		known := s.thoughts[key]
		if known == nil {
			known = make(map[string]bool)
			s.thoughts[key] = known
		}
		known[string(jsonBytes(step.body))] = true
	}
	if req.Store != nil && !*req.Store {
		return
	}
	// Use the documented free-tier retention for this synthetic account.
	// https://aistudio.google.com/docs/interactions-overview.md#data-storage-retention
	s.stored[reply.id] = storedInteraction{
		body: reply.body(slices.Concat(current, reply.steps)), window: slices.Concat(window, reply.steps),
		frames: events, expires: time.Now().Add(24 * time.Hour),
	}
}

// Synthetic exact replay check for thought steps emitted at a known position.
// The service documents verbatim replay but not the error for a mutated block.
// https://aistudio.google.com/docs/thinking#stateless-mode
func (s *interactionWire) validateThoughts(window []interactionStep, from int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := from; i < len(window); i++ {
		step := window[i]
		if googleString(step.body, "type") != "thought" {
			continue
		}
		if known := s.thoughts[string(jsonBytes(interactionStepBodies(window[:i])))]; len(known) != 0 && !known[string(jsonBytes(step.body))] {
			return fmt.Errorf("%s: resend the original thought step unchanged", step.path)
		}
	}
	return nil
}

func (s *interactionWire) retrieve(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Interactions, r)
	if err != nil {
		s.reject(w, req, 400, "invalid_request", err)
		return
	}
	query := r.URL.Query()
	switch query.Get("stream") {
	case "true":
		req.Stream = true
	case "", "false":
	default:
		s.reject(w, req, 400, "invalid_request", fmt.Errorf("/stream: expected a boolean"))
		return
	}
	after := query.Get("last_event_id")
	if after != "" && !req.Stream {
		s.reject(w, req, 400, "invalid_request", fmt.Errorf("/last_event_id: requires streaming"))
		return
	}
	row, ok := s.lookup(r.PathValue("id"))
	if !ok {
		s.server.observe(req)
		writeInteractionError(w, 404, "not_found", "Interaction not found.")
		return
	}
	if !req.Stream {
		s.server.observe(req)
		writeJSON(w, 200, row.body)
		return
	}
	events := row.frames
	if after != "" {
		id := r.PathValue("id")
		at := -1
		for i := range events {
			if interactionEventID(id, i) == after {
				at = i
				break
			}
		}
		if at < 0 {
			s.reject(w, req, 400, "invalid_request", fmt.Errorf("/last_event_id: event not found for interaction %q", id))
			return
		}
		events = events[at+1:]
	}
	s.server.observe(req)
	s.server.sendStream(w, r, Turn{}, interactionsStream, slices.Values(events))
}
