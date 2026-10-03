package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// ResponseReference belongs to one completed assistant message. Local history
// remains complete; the reference only permits omitting an exact server prefix.
type ResponseReference struct {
	ID         string `json:"id"`
	Scope      string `json:"scope"`
	InputHash  string `json:"input_hash"`
	OutputHash string `json:"output_hash"`
}

type ResponseContinuationPlan struct {
	PreviousResponseID string
	Start              int
	Reason             string
}

func (p *Provider) ResponseContinuationScope(model string) string {
	if p == nil || p.ProtocolID() != "openai-responses" {
		return ""
	}
	if p.protocolSettings != nil && !p.protocolSettings.ResolveStore(true) {
		return ""
	}
	extras := p.requestExtras()
	if extras["store"] == false || extras["truncation"] == "auto" {
		return ""
	}
	for _, key := range []string{"input", "instructions", "model", "conversation", "previous_response_id"} {
		if _, ok := extras[key]; ok {
			return ""
		}
	}
	credential := ""
	if p.AccountID == "" {
		credential = p.APIKey
	}
	data, err := json.Marshal(struct {
		Provider, Endpoint, Account, Model, Credential string
		Extras                                         map[string]any
		Sampling                                       Sampling
	}{p.ID, p.BaseURL, p.AccountID, model, credential, extras, p.Sampling()})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ResponseInputHash(messages []ChatMessage, tools []ToolDef) string {
	h := newPrefixHasher(tools)
	for _, message := range messages {
		// Include complete attachment bytes, not their display labels.
		h.addContent(message, string(message.Content))
	}
	return h.value()
}

func BindResponseReference(message *ChatMessage, reference *ResponseReference) {
	if reference == nil {
		return
	}
	copy := *reference
	copy.OutputHash = ResponseInputHash([]ChatMessage{*message}, nil)
	message.ResponseReference = &copy
}

func PlanResponseContinuation(req *ChatRequest) ResponseContinuationPlan {
	if req.ResponseContinuationScope == "" {
		return ResponseContinuationPlan{Reason: "disabled"}
	}
	last := -1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "assistant" {
			last = i
			break
		}
	}
	if last < 0 {
		return ResponseContinuationPlan{Reason: "no_completed_response"}
	}
	message := req.Messages[last]
	ref := message.ResponseReference
	if ref == nil || ref.ID == "" {
		return ResponseContinuationPlan{Reason: "response_id_unavailable"}
	}
	if ref.Scope != req.ResponseContinuationScope {
		return ResponseContinuationPlan{Reason: "connection_changed"}
	}
	if ref.InputHash != ResponseInputHash(req.Messages[:last], req.Tools) {
		return ResponseContinuationPlan{Reason: "input_prefix_changed"}
	}
	if ref.OutputHash != ResponseInputHash([]ChatMessage{message}, nil) {
		return ResponseContinuationPlan{Reason: "accepted_output_changed"}
	}
	return ResponseContinuationPlan{PreviousResponseID: ref.ID, Start: last + 1, Reason: "delta"}
}

func responseContinuation(req *ChatRequest) (string, int) {
	plan := PlanResponseContinuation(req)
	return plan.PreviousResponseID, plan.Start
}

func continuationUnavailable(err error) bool {
	var status *StatusError
	if errors.As(err, &status) {
		return (status.StatusCode == http.StatusBadRequest || status.StatusCode == http.StatusNotFound || status.StatusCode == http.StatusUnprocessableEntity) &&
			(status.Param == "previous_response_id" || status.APICode == "previous_response_not_found")
	}
	var api *APIError
	return errors.As(err, &api) && (api.Param == "previous_response_id" || api.Code == "previous_response_not_found")
}
