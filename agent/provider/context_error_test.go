package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestContextWindowErrorsRequireCanonicalCapacityEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"local", &ContextWindowError{InputTokens: 497687, ContextWindow: 256000}, true},
		{"wrapped code", fmt.Errorf("request: %w", &StatusError{StatusCode: http.StatusBadRequest, Code: "context_length_exceeded"}), true},
		{"DeepSeek phrasing", &StatusError{StatusCode: http.StatusBadRequest, Message: "The input is longer than the model's context length"}, true},
		{"streamed", &APIError{Code: "context_window_exceeded"}, true},
		{"authorization", &StatusError{StatusCode: http.StatusUnauthorized, Message: "prompt is too long"}, false},
		{"payload size", &StatusError{StatusCode: http.StatusRequestEntityTooLarge, Message: "request entity too large"}, false},
		{"bad schema", &StatusError{StatusCode: http.StatusBadRequest, Message: "invalid arguments"}, false},
		{"untyped text", errors.New("context_length_exceeded"), false},
		{"cancellation", context.Canceled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsContextWindowExceeded(test.err); got != test.want {
				t.Fatalf("context classification=%t want %t", got, test.want)
			}
		})
	}
}

// The texts are the rejections providers, runtimes and proxies send for a
// request beyond a limit, as documented by each provider or collected in
// LiteLLM's exception mapping and Roo Code's context error handling.
func TestRequestTooLargeReadsTheLimitFromProviderEvidence(t *testing.T) {
	const nginx = "<html>\r\n<head><title>413 Request Entity Too Large</title></head>\r\n<body>\r\n<center><h1>413 Request Entity Too Large</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>"
	for _, test := range []struct {
		name    string
		err     error
		sent    int
		ok      bool
		limit   RejectedLimit
		maximum int
		counted int
	}{
		{"local estimate", &ContextWindowError{InputTokens: 497687, ContextWindow: 256000}, 0, true, RejectedContext, 256000, 497687},
		{"OpenAI context", &StatusError{StatusCode: 400, Code: "context_length_exceeded", Message: "This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens. Please reduce the length of the messages."}, 4096, true, RejectedContext, 128000, 130000},
		{"input and completion", &StatusError{StatusCode: 400, Message: "This model's maximum context length is 65536 tokens. However, you requested 70000 tokens (60000 in the messages, 10000 in the completion). Please reduce the length of the messages or completion."}, 10000, true, RejectedContext, 65536, 60000},
		{"Anthropic prompt", &StatusError{StatusCode: 400, Message: "invalid_request_error: prompt is too long: 205000 tokens > 200000 maximum"}, 8192, true, RejectedContext, 200000, 205000},
		{"Gemini input", &StatusError{StatusCode: 400, Message: "INVALID_ARGUMENT: The input token count (1200000) exceeds the maximum number of tokens allowed (1048576)."}, 8192, true, RejectedContext, 1048576, 1200000},
		{"vLLM output beyond context", &StatusError{StatusCode: 400, Message: "'max_tokens' or 'max_completion_tokens' is too large: 40000. This model's maximum context length is 32768 tokens and your request has 1000 input tokens (40000 > 32768 - 1000)."}, 40000, true, RejectedContext, 32768, 1000},
		{"TGI inputs", &StatusError{StatusCode: 422, Message: "Input validation error: `inputs` tokens + `max_new_tokens` must be <= 4097. Given: 4000 `inputs` tokens and 1000 `max_new_tokens`"}, 1000, true, RejectedContext, 4097, 4000},
		{"LM Studio", &StatusError{StatusCode: 400, Message: "Trying to keep the first 9000 tokens when context the overflows. However, the model is loaded with context length of only 8192 tokens"}, 0, true, RejectedContext, 8192, 0},
		{"llama.cpp", &StatusError{StatusCode: 400, APIType: "exceed_context_size_error", Message: "the request exceeds the available context size, try increasing it"}, 0, true, RejectedContext, 0, 0},
		{"Ollama overflow", &StatusError{StatusCode: 413, Message: "the input length exceeds the context length"}, 0, true, RejectedContext, 0, 0},
		{"DeepSeek phrasing", &StatusError{StatusCode: 400, Message: "The input is longer than the model's context length"}, 0, true, RejectedContext, 0, 0},
		{"streamed", &APIError{Code: "context_window_exceeded"}, 0, true, RejectedContext, 0, 0},
		{"tokens per minute", &StatusError{StatusCode: 429, Code: "rate_limit_exceeded", Message: "Request too large for gpt-4o in organization org-test on tokens per min (TPM): Limit 30000, Requested 45000. The input or output tokens must be reduced in order to run successfully."}, 4096, true, RejectedContext, 30000, 0},
		{"DeepSeek output", &StatusError{StatusCode: 400, Message: `invalid_request_error: {"code":400,"reason":"INVALID_REQUEST_BODY","message":"max_tokens (current value: 550463) must be between 0 and 393216 "}`}, 550463, true, RejectedOutput, 393215, 0},
		{"another output parameter", &StatusError{StatusCode: 422, Message: "max_completion_tokens (current value: 550463) must be between 0 and 393216"}, 550463, true, RejectedOutput, 393215, 0},
		{"OpenAI output", &StatusError{StatusCode: 400, Param: "max_tokens", Message: "max_tokens is too large: 100000. This model supports at most 16384 completion tokens, whereas you provided 100000."}, 100000, true, RejectedOutput, 16383, 0},
		{"Anthropic output", &StatusError{StatusCode: 400, Message: "max_tokens: 64000 > 32000, which is the maximum allowed number of output tokens for claude-test"}, 64000, true, RejectedOutput, 31999, 0},
		{"Gemini output range", &StatusError{StatusCode: 400, Message: "Unable to submit request because it has a maxOutputTokens value of 100000 but the supported range is from 1 (inclusive) to 65537 (exclusive)."}, 100000, true, RejectedOutput, 65536, 0},
		{"output without a ceiling", &StatusError{StatusCode: 400, Message: "max_tokens (current value: 550463) must be between 0 and 999999"}, 550463, true, RejectedOutput, 0, 0},
		{"another request's output value", &StatusError{StatusCode: 400, Message: "max_tokens (current value: 123) must be between 0 and 393216"}, 550463, false, 0, 0, 0},
		{"proxy page", &StatusError{StatusCode: 413, Message: nginx}, 4096, true, RejectedBody, 0, 0},
		{"empty 413", &StatusError{StatusCode: 413}, 4096, true, RejectedBody, 0, 0},
		{"Anthropic body", &StatusError{StatusCode: 413, Code: "request_too_large", Message: "Request exceeds the maximum size"}, 4096, true, RejectedBody, 0, 0},
		{"Gemini payload", &StatusError{StatusCode: 400, Message: "Request payload size exceeds the limit: 20971520 bytes."}, 4096, true, RejectedBody, 20971520, 0},
		{"rate limit", &StatusError{StatusCode: 429, Message: "Rate limit reached for requests"}, 4096, false, 0, 0, 0},
		{"authorization", &StatusError{StatusCode: 401, Message: "prompt is too long"}, 0, false, 0, 0, 0},
		{"bad schema", &StatusError{StatusCode: 400, Message: "invalid arguments"}, 0, false, 0, 0, 0},
		{"untyped text", errors.New("context_length_exceeded"), 0, false, 0, 0, 0},
		{"cancellation", context.Canceled, 0, false, 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := RequestTooLarge(fmt.Errorf("extract intent: %w", test.err), test.sent)
			if ok != test.ok || got.Limit != test.limit || got.Maximum != test.maximum || got.Counted != test.counted {
				t.Fatalf("rejection=%+v ok=%t, want limit=%d maximum=%d counted=%d ok=%t", got, ok, test.limit, test.maximum, test.counted, test.ok)
			}
		})
	}
}

func TestUnexplainedRejectionAndTimeoutsOfferOneSmallerRequest(t *testing.T) {
	for _, test := range []struct {
		name                string
		err                 error
		unexplained, waited bool
	}{
		{"bare invalid request", &StatusError{StatusCode: 400, Message: "Invalid request."}, true, false},
		{"unprocessable", &StatusError{StatusCode: 422, Message: "Unprocessable Entity"}, true, false},
		{"named parameter", &StatusError{StatusCode: 400, Param: "temperature", Message: "Invalid 'temperature'"}, false, false},
		{"content policy", &StatusError{StatusCode: 400, Code: "content_policy_violation", Message: "rejected"}, false, false},
		{"credential", &StatusError{StatusCode: 401, Message: "Invalid request."}, false, false},
		{"gateway timeout", &StatusError{StatusCode: 504}, false, true},
		{"origin timeout", &StatusError{StatusCode: 524}, false, true},
		{"request timeout", &StatusError{StatusCode: 408}, false, true},
		{"server error", &StatusError{StatusCode: 500}, false, false},
		{"deadline", fmt.Errorf("read: %w", context.DeadlineExceeded), false, true},
		{"interrupted stream", &StreamInterruptionError{Cause: errors.New("unexpected EOF")}, false, true},
		{"cancelled", context.Canceled, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := UnexplainedRejection(test.err); got != test.unexplained {
				t.Fatalf("unexplained=%t want %t", got, test.unexplained)
			}
			if got := TimedOut(test.err); got != test.waited {
				t.Fatalf("timed out=%t want %t", got, test.waited)
			}
		})
	}
}
