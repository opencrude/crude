package providersim

// OpenAI legacy completions return ErrorResponse (schemas/manifest.json).
// DeepSeek documents the OpenAI-compatible API + SDK on the completion page,
// the error envelope at https://api-docs.deepseek.com/guides/ocr.md#errors
// and statuses at https://api-docs.deepseek.com/quick_start/error_codes.md.
// Reusing OpenAI type/code tokens for DeepSeek faults is synthetic. In
// particular, the low-balance status is 402, not OpenAI's 429. The 404 and
// 502 pairings and use of 408 on FIM rather than uploads are synthetic.
func fimHTTPFault(wire Wire, f fault) (int, map[string]any) {
	if wire == FIMCodestral {
		return codestralFault(f)
	}
	status, body := chatFault(f)
	err := body["error"].(map[string]any)
	if f.kind == ContextOverflow {
		err["param"] = "prompt"
	}
	if wire == FIMDeepSeek && f.kind == Quota {
		return 402, chatError("unknown_error", "invalid_request_error", nil, f.text("Insufficient balance."))
	}
	return status, body
}

// https://docs.mistral.ai/resources/error-glossary documents the flat error
// object, string codes, the special 401 shape and status/type/code triples.
// Wording is synthetic. Pairing 502 and 408 with internal_server_error is
// synthetic; the glossary does not associate a four-digit code with them.
func codestralFault(f fault) (int, map[string]any) {
	envelope := func(status int, kind, code, message string) (int, map[string]any) {
		o := map[string]any{"object": "error", "raw_status_code": status, "message": f.text(message), "type": kind, "param": nil}
		if code != "" {
			o["code"] = code
		}
		return status, o
	}
	switch f.kind {
	case Auth:
		return 401, map[string]any{"detail": f.text("Invalid API Key")}
	case ModelNotFound:
		return envelope(400, "invalid_model", "1500", "Invalid model.")
	case InvalidRequest:
		return envelope(400, "invalid_request_invalid_args", "3051", "Invalid request.")
	case ContextOverflow:
		return envelope(400, "invalid_request_prompt_too_long", "3059", "The prompt exceeds the context limit.")
	case Quota:
		return envelope(402, "billing_wallet_depleted", "2308", "Out of credits.")
	case RateLimit:
		return envelope(429, "rate_limited", "1300", "Rate limit exceeded.")
	case Overload:
		return envelope(503, "internal_server_error", "3505", "Service unavailable.")
	case RequestTimeout:
		return envelope(408, "internal_server_error", "", requestTimedOut)
	case BadGateway:
		return envelope(502, "internal_server_error", "", "Bad gateway.")
	default:
		return envelope(500, "internal_server_error", "1000", "The request failed.")
	}
}

func (c fimReply) faultFrame(f fault) frame {
	// The pinned OpenAI SDK recognizes data-only legacy {error:{message,...}}
	// frames: openai/openai-python@aff223d/src/openai/_streaming.py.
	// DeepSeek and Codestral document an unsuccessful finish on the same
	// completion choice instead. The injection point is synthetic.
	var body map[string]any
	switch c.wire {
	case FIMDeepSeek:
		body = c.body(c.choice("", "insufficient_system_resource"))
		body["usage"] = c.usage
	case FIMCodestral:
		body = c.body(c.choice("", "error"))
	default:
		_, body = fimHTTPFault(c.wire, f)
	}
	return frame{data: jsonBytes(body)}
}
