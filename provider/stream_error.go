package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// streamErrorDecoder keeps gateway error translation at the streaming wire
// boundary. Ordinary HTTP and non-streaming API errors never acquire this
// evidence, and the stream owner (not this classifier) owns replay safety.
type streamErrorDecoder struct {
	protocol string
	inner    StreamDecoder
}

// A successful HTTP stream may still carry an explicit retry veto. Retain its
// diagnostic cause without letting typed disconnect evidence override it.
type streamRetryVetoError struct{ cause error }

func (e *streamRetryVetoError) Error() string { return e.cause.Error() }
func (e *streamRetryVetoError) Unwrap() error { return e.cause }

func (d *streamErrorDecoder) Decode(event StreamEvent) (StreamUpdate, error) {
	var envelope struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(event.Data, &envelope) == nil && len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		// Gateways can translate an upstream structured failure into Ollama's
		// string error, or retain the object while translating the data stream.
		var message string
		if json.Unmarshal(envelope.Error, &message) == nil && message != "" {
			return StreamUpdate{}, classifyStreamError(d.protocol, envelope.Type, &APIError{Message: message})
		}
		var wire ChatAPIError
		if json.Unmarshal(envelope.Error, &wire) == nil && (wire.Message != "" || wire.Type != "" || wire.Code != nil || wire.Param != "") {
			code := ""
			if wire.Code != nil {
				code = fmt.Sprint(wire.Code)
			}
			return StreamUpdate{}, classifyStreamError(d.protocol, envelope.Type, &APIError{Type: wire.Type, Code: code, Param: wire.Param, Message: wire.Message})
		}
	}
	update, err := d.inner.Decode(event)
	if api, ok := err.(*APIError); ok {
		err = classifyStreamError(d.protocol, envelope.Type, api)
	}
	return update, err
}

func classifyStreamError(protocol, event string, api *APIError) error {
	// Ollama documents these exact string-only model-execution failures. They
	// carry no HTTP status after streaming starts. Do not generalize arbitrary
	// prose (missing models, memory requirements or invalid inputs) into retries.
	if protocol == "ollama" && api.Type == "" && api.Code == "" && api.Param == "" {
		switch strings.ToLower(strings.TrimSpace(api.Message)) {
		case "an error was encountered while running the model", "the model failed to generate a response":
			return &APIError{Type: "api_error", Message: api.Message}
		}
	}
	if streamDisconnectEvidence(api) {
		return &StreamInterruptionError{Protocol: protocol, Event: event, Kind: StreamInterruptionProtocolError, Cause: api}
	}
	return api
}

func streamDisconnectEvidence(api *APIError) bool {
	if api == nil || api.Param != "" {
		return false
	}
	for _, code := range strings.Fields(strings.ToLower(api.Type + " " + api.Code)) {
		if code != "invalid_request" && code != "invalid_request_error" && permanentAPIError(code) {
			return false
		}
		if code == "400" || code == "401" || code == "402" || code == "403" || code == "404" || code == "413" || code == "422" {
			return false
		}
	}
	if api.Code == "stream_error" || api.Type == "gateway_stream_terminated" {
		return true
	}
	// This is a closed diagnostic, not substring-based retry of arbitrary
	// invalid requests. The response.completed name belongs to the upstream
	// protocol; a gateway can emit it on any downstream wire protocol.
	if api.Type != "" && api.Type != "invalid_request" && api.Type != "invalid_request_error" {
		return false
	}
	if api.Code != "" {
		return false
	}
	return strings.ToLower(strings.TrimSpace(api.Message)) == "stream error: stream disconnected before completion: stream closed before response.completed"
}
