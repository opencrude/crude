package providersim

import (
	"encoding/base64"
	"fmt"
	"maps"
	"net/http"
	"slices"
)

const (
	interactionsReplySchema  = "Interaction"
	interactionsStreamSchema = "InteractionSSEEvent"
	interactionRoute         = googleAPIVersion + "/interactions"
	interactionCreated       = "interaction.created"
	interactionCompleted     = "interaction.completed"
	interactionStepStart     = "step.start"
	interactionStepDelta     = "step.delta"
	interactionStepStop      = "step.stop"
)

func init() {
	registerWire(Interactions, func(s *Server) http.Handler { return newInteractionWire(s).routes() })
}

func (s *interactionWire) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+interactionRoute, s.create)
	mux.HandleFunc("GET "+interactionRoute+"/{id}", s.retrieve)
	return mux
}

func (s *interactionWire) create(w http.ResponseWriter, r *http.Request) {
	req, err := receive(Interactions, r)
	if err != nil {
		s.reject(w, req, 400, "invalid_request", err)
		return
	}
	obj, err := googleRequestObject(req.Raw)
	if err != nil {
		s.reject(w, req, 400, "invalid_request", err)
		return
	}
	b, err := wireBundle(Interactions)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if code, err := validateInteraction(b, obj); err != nil {
		s.reject(w, req, 400, code, err)
		return
	}
	current := decodeInteraction(&req, obj)
	var history []interactionStep
	if _, present := obj["previous_interaction_id"].(string); present {
		row, ok := s.lookup(req.PreviousResponseID)
		if !ok {
			s.reject(w, req, 404, "not_found", fmt.Errorf("/previous_interaction_id: interaction %q not found", req.PreviousResponseID))
			return
		}
		history = row.window
	}
	window := slices.Concat(history, current)
	if err := s.validateThoughts(window, len(history)); err != nil {
		s.reject(w, req, 400, "invalid_request", err)
		return
	}
	// The service carries steps, not system instructions, generation settings,
	// or tools. The current request supplies those settings anew.
	req.Effective = req.Conversation
	req.Effective.Messages = projectInteraction(window)
	turn, ok := s.server.dispatch(&req, interactionBlocks(obj, window), googleCacheProfile(req.Model))
	if !ok {
		writeInteractionError(w, 500, "api_error", "No reply is scripted.")
		return
	}
	if turn.fault.mode == faultHTTP {
		if turn.gate.await(r.Context(), s.server.done) {
			// Synthetic retry-header injection: the public error guide does not
			// guarantee headers for the REST 429 error.
			if hint := turn.fault.hint; hint.header != "" {
				w.Header().Set(hint.header, hint.value)
			}
			status, code, message := interactionFault(turn.fault.kind)
			writeInteractionError(w, status, code, message)
		}
		return
	}
	if !req.Stream && (turn.fault.mode == faultCut || turn.fault.mode == faultStream) {
		s.internalError(w, fmt.Errorf("the scripted fault requires streaming"))
		return
	}
	reply, err := newInteractionReply(req, obj, turn)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var frames []frame
	if req.Stream || req.Store == nil || *req.Store {
		frames = reply.frames()
	}
	save := func() { s.save(req, reply, current, window, frames) }
	if req.Stream {
		s.server.sendStream(w, r, turn, interactionsStream, func(yield func(frame) bool) {
			for i, event := range frames {
				if i == len(frames)-1 && turn.fault.mode == faultNone {
					save() // Publish before the client can read interaction.completed.
				}
				if !yield(event) {
					return
				}
			}
		})
	} else if turn.gate.await(r.Context(), s.server.done) {
		save()
		writeJSON(w, 200, reply.body(reply.steps))
	}
}

func (s *interactionWire) reject(w http.ResponseWriter, req Request, status int, code string, err error) {
	req.Violation = err.Error()
	s.server.observe(req)
	s.server.report("simulate Interactions %s: %s", req.Path, req.Violation)
	writeInteractionError(w, status, code, req.Violation)
}

func (s *interactionWire) internalError(w http.ResponseWriter, err error) {
	s.server.report("simulate Interactions: %v", err)
	writeInteractionError(w, 500, "api_error", "Cannot produce the scripted reply.")
}

func interactionErrorDetail(code, message string) googleObject {
	return googleObject{"code": code, "message": message}
}

func writeInteractionError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, googleObject{"error": interactionErrorDetail(code, message)})
}

// Documented string codes: https://aistudio.google.com/docs/api-errors.md.
// Messages and the context-overflow code selection are synthetic.
func interactionFault(kind FaultKind) (int, string, string) {
	switch kind {
	case RateLimit:
		return 429, "rate_limit_exceeded", "Rate limit exceeded."
	case ContextOverflow:
		return 400, "invalid_request", "The input exceeds the context limit."
	case Overload:
		return 503, "service_unavailable", "The model is overloaded."
	case Auth:
		return 401, "authentication", "The API key is invalid."
	default:
		return 500, "api_error", "The request failed."
	}
}

var interactionsStream = streamCodec{
	contentType: "text/event-stream",
	write:       writeSSE,
	// Documented transport sentinel, not an InteractionSSEEvent JSON object.
	terminal: []frame{{event: "done", data: []byte("[DONE]")}},
	fault: func(kind FaultKind, _ int) frame {
		_, code, message := interactionFault(kind)
		return frame{event: "error", data: jsonBytes(googleObject{"event_type": "error", "error": interactionErrorDetail(code, message)})}
	},
}

type interactionReply struct {
	id, status string
	identity   googleObject
	previous   string
	steps      []interactionStep
	usage      googleObject
	blocked    bool
}

func newInteractionReply(req Request, obj googleObject, turn Turn) (interactionReply, error) {
	out := interactionReply{
		id: fmt.Sprintf("v1_sim_%d", req.Index), status: "completed", previous: req.PreviousResponseID,
		identity: googleObject{"model": req.Model}, usage: interactionUsage(googleUsage(req, turn)),
	}
	if agent := googleString(obj, "agent"); agent != "" {
		out.identity = googleObject{"agent": agent}
	}
	switch turn.finish() {
	case FinishToolCalls:
		out.status = "requires_action"
	case FinishLength:
		out.status = "incomplete"
	case FinishFiltered:
		out.status, out.blocked = "failed", true
	}
	add := func(body googleObject) {
		out.steps = append(out.steps, interactionStep{body, indexedGooglePath("/steps", len(out.steps))})
	}
	if thought := turn.Reasoning; thought.Text != "" || thought.Signature != "" {
		sig := thought.Signature
		if sig == "" {
			sig = base64.StdEncoding.EncodeToString([]byte(out.id)) // Synthetic opaque signature.
		}
		body := googleObject{"type": "thought", "signature": sig}
		if thought.Text != "" {
			body["summary"] = []any{interactionText(thought.Text)}
		}
		add(body)
	}
	if text := turn.Text + turn.Refusal; text != "" {
		add(googleObject{"type": "model_output", "content": []any{interactionText(text)}})
	}
	for i, call := range turn.Tools {
		args, err := googleArguments(call)
		if err != nil {
			return interactionReply{}, fmt.Errorf("function %q arguments: %w", call.Name, err)
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_sim_%d_%d", req.Index, i) // Synthetic call ID.
		}
		add(googleObject{"type": "function_call", "id": call.ID, "name": call.Name, "arguments": args})
	}
	return out, nil
}

func interactionUsage(u Usage) googleObject {
	return googleObject{
		"total_input_tokens": u.Input, "total_output_tokens": u.Output - u.Reasoning,
		"total_cached_tokens": u.Cached, "total_thought_tokens": u.Reasoning,
		"total_tokens": u.Input + u.Output,
	}
}

func (c interactionReply) identityBody(status string) googleObject {
	obj := maps.Clone(c.identity)
	obj["id"], obj["object"], obj["status"] = c.id, "interaction", status
	return obj
}

func (c interactionReply) body(steps []interactionStep) googleObject {
	const timestamp = "2025-01-01T00:00:00Z" // Synthetic fixture timestamp.
	obj := c.identityBody(c.status)
	obj["created"], obj["updated"], obj["steps"], obj["usage"] = timestamp, timestamp, interactionStepBodies(steps), c.usage
	if c.previous != "" {
		obj["previous_interaction_id"] = c.previous
	}
	if c.blocked {
		obj["errors"] = []googleObject{interactionErrorDetail("safety", "The output was blocked.")}
	}
	return obj
}

func interactionEventID(id string, i int) string { return fmt.Sprintf("%s_event_%d", id, i) }

func (c interactionReply) frames() []frame {
	var events []frame
	emit := func(name string, fields googleObject) {
		fields["event_type"] = name
		fields["event_id"] = interactionEventID(c.id, len(events)) // Synthetic resume token.
		events = append(events, frame{event: name, data: jsonBytes(fields)})
	}
	emit(interactionCreated, googleObject{"interaction": c.identityBody("in_progress")})
	// Synthetic chunk boundaries. Use documented typed deltas and put the
	// signature last in a thought step, as in the thinking guide.
	for i, item := range c.steps {
		step := item.body
		start := maps.Clone(step)
		switch googleString(step, "type") {
		case "thought":
			delete(start, "summary")
			start["signature"] = ""
		case "model_output":
			start["content"] = []any{}
		case "function_call":
			start["arguments"] = googleObject{}
		}
		emit(interactionStepStart, googleObject{"index": i, "step": start})
		delta := func(data googleObject) { emit(interactionStepDelta, googleObject{"index": i, "delta": data}) }
		switch googleString(step, "type") {
		case "thought":
			for _, item := range googleArray(step["summary"]) {
				delta(googleObject{"type": "thought_summary", "content": item})
			}
			delta(googleObject{"type": "thought_signature", "signature": step["signature"]})
		case "model_output":
			for _, item := range googleArray(step["content"]) {
				delta(item.(googleObject))
			}
		case "function_call":
			delta(googleObject{"type": "arguments_delta", "arguments": string(jsonBytes(step["arguments"]))})
		}
		emit(interactionStepStop, googleObject{"index": i})
	}
	end := c.identityBody(c.status)
	end["usage"] = c.usage
	emit(interactionCompleted, googleObject{"interaction": end})
	return events
}
