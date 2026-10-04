package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
	return planResponseContinuation(req, true)
}

func planResponseContinuation(req *ChatRequest, applyLearnedLimitations bool) ResponseContinuationPlan {
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
	if applyLearnedLimitations &&
		req.MediaReplayRecovery &&
		LearnedResponseMediaReplayLimitation(req.ResponseContinuationScope) &&
		responsePrefixHasInlineImage(req.Messages[:last]) {
		return ResponseContinuationPlan{Reason: "media_replay_limitation_learned"}
	}
	return ResponseContinuationPlan{PreviousResponseID: ref.ID, Start: last + 1, Reason: "delta"}
}

func responsePrefixHasInlineImage(messages []ChatMessage) bool {
	for _, message := range messages {
		for _, part := range message.Content.Parts() {
			if part.Type != "image_url" {
				continue
			}
			if len(part.Data) > 0 {
				return true
			}
			if part.ImageURL != nil {
				value := strings.TrimSpace(part.ImageURL.URL)
				if len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:") {
					return true
				}
			}
		}
	}
	return false
}

func responsePrefixHasValidInlineImage(messages []ChatMessage) bool {
	for _, message := range messages {
		for _, part := range message.Content.Parts() {
			if part.Type != "image_url" {
				continue
			}
			if len(part.Data) > 0 {
				if inlineImageBytesMatchMIME(part.MimeType, part.Data) {
					return true
				}
				continue
			}
			if part.ImageURL == nil {
				continue
			}
			value := strings.TrimSpace(part.ImageURL.URL)
			if len(value) >= len("data:") &&
				strings.EqualFold(value[:len("data:")], "data:") &&
				validInlineImageDataURL(value) {
				return true
			}
		}
	}
	return false
}

func validInlineImageDataURL(value string) bool {
	if validateOpenAIImageURL(value) != nil {
		return false
	}
	metadata, payload, ok := strings.Cut(value[len("data:"):], ",")
	if !ok {
		return false
	}
	mediaType, _, _ := strings.Cut(metadata, ";")
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	return err == nil && inlineImageBytesMatchMIME(mediaType, data)
}

func inlineImageBytesMatchMIME(mediaType string, data []byte) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return false
	}
	sniffed := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
	return sniffed == mediaType
}

func continuationMediaReplayRejected(req *ChatRequest, plan ResponseContinuationPlan, cause error) bool {
	if req == nil || !req.MediaReplayRecovery {
		return false
	}
	if plan.PreviousResponseID == "" || plan.Start < 1 ||
		!responsePrefixHasValidInlineImage(req.Messages[:plan.Start-1]) {
		return false
	}
	message := ""
	var status *StatusError
	if errors.As(cause, &status) {
		if status.StatusCode != http.StatusBadRequest && status.StatusCode != http.StatusUnprocessableEntity {
			return false
		}
		message = status.Message
	} else {
		var api *APIError
		if !errors.As(cause, &api) ||
			(api.Type != "" && api.Type != "invalid_request_error") {
			return false
		}
		message = api.Message
	}
	message = strings.ToLower(message)
	return strings.Contains(message, "image_url") &&
		strings.Contains(message, "base64") &&
		(strings.Contains(message, "invalid") || strings.Contains(message, "expected"))
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
