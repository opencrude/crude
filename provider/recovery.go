package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
)

// StreamInterruptionKind identifies typed evidence that an accepted streaming
// response ended before its protocol terminal event. It classifies recovery;
// the stream owner still decides whether replay is safe from accepted output.
type StreamInterruptionKind string

const (
	StreamInterruptionMissingTerminal StreamInterruptionKind = "missing_terminal_event"
	StreamInterruptionProtocolError   StreamInterruptionKind = "protocol_disconnect_event"
)

// StreamInterruptionError preserves protocol/transport evidence for a stream
// that did not reach its terminal event. Cause remains available to errors.Is
// and errors.As (for example io.ErrUnexpectedEOF or a structured APIError).
type StreamInterruptionError struct {
	Protocol string
	Event    string
	Kind     StreamInterruptionKind
	Cause    error
}

func (e *StreamInterruptionError) Error() string {
	if e == nil {
		return ""
	}
	prefix := "provider stream interrupted"
	if e.Protocol != "" {
		prefix = e.Protocol + " stream interrupted"
	}
	if e.Event != "" {
		prefix += " at " + e.Event
	}
	if e.Cause != nil {
		return prefix + ": " + e.Cause.Error()
	}
	return prefix
}

func (e *StreamInterruptionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func permanentAPIError(code string) bool {
	code = strings.ToLower(code)
	if quotaAPIError(code) {
		return true
	}
	for _, kind := range []string{"invalid_api_key", "authentication_error", "permission_denied", "permission_error", "invalid_request", "context_length_exceeded", "model_not_found", "not_found_error", "request_too_large"} {
		if strings.Contains(code, kind) {
			return true
		}
	}
	return false
}

func quotaAPIError(code string) bool {
	code = strings.ToLower(code)
	return strings.Contains(code, "insufficient_quota") || strings.Contains(code, "billing") || strings.Contains(code, "credit_balance")
}

// retryableAPIStatus gives explicit HTTP/status evidence precedence over a
// generic error-envelope label. Gateways use invalid_request_error for timeouts,
// throttling and upstream outages too. Specific permanent codes, an identified
// invalid parameter and explicit retry vetoes still fail closed. Do not inspect
// diagnostic prose or make this policy depend on the chosen wire protocol.
func retryableAPIStatus(status int, codes, param, retryHint string) bool {
	if retryHint == "false" || param != "" {
		return false
	}
	for _, code := range strings.Fields(strings.ToLower(codes)) {
		if (code == "invalid_request" || code == "invalid_request_error") && param == "" {
			continue
		}
		if permanentAPIError(code) {
			return false
		}
	}
	switch status {
	case 400, 401, 402, 403, 404, 413, 422:
		return false
	}
	return retryHint == "true" || isRetryableStatus(status)
}

// Retryable classifies recovery, not replay safety. The stream owner retries
// only before accepted output; the agent owns continuations of partial output.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var veto *streamRetryVetoError
	if errors.As(err, &veto) {
		return false
	}
	var interrupted *StreamInterruptionError
	if errors.As(err, &interrupted) {
		return true
	}
	var exhausted *ExhaustedError
	if errors.As(err, &exhausted) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		return status.Retryable()
	}
	var api *APIError
	if errors.As(err, &api) {
		if api.Param != "" {
			return false
		}
		code := strings.ToLower(api.Type + " " + api.Code)
		if status, err := strconv.Atoi(api.Code); err == nil {
			return retryableAPIStatus(status, code, api.Param, "")
		}
		if permanentAPIError(code) {
			return false
		}
		for _, kind := range []string{"overload", "rate_limit", "temporar", "api_error", "server_error", "internal_error", "timeout", "unavailable", "gateway_stream_terminated", "stream_error"} {
			if strings.Contains(code, kind) {
				return true
			}
		}
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &unknownAuthority) || errors.As(err, &certificateInvalid) || errors.As(err, &hostname) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return !dns.IsNotFound
	}
	var stalled *ProgressStallError
	if errors.As(err, &stalled) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var network net.Error
	if errors.As(err, &network) {
		return true
	}
	for _, code := range []error{syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ECONNABORTED, syscall.EPIPE, syscall.ETIMEDOUT, syscall.ENETUNREACH, syscall.EHOSTUNREACH} {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}

// ErrorSummary is for quiet user-facing lifecycle chrome. The original error
// remains available in diagnostic logs, including its provider request ID.
func ErrorSummary(err error) string {
	var exhausted *ExhaustedError
	if errors.As(err, &exhausted) {
		err = exhausted.Cause
	}
	var status *StatusError
	if errors.As(err, &status) {
		if quotaAPIError(status.Code+" "+status.APIType+" "+status.APICode) && status.StatusCode == 429 {
			return "Provider quota or billing limit reached"
		}
		switch status.StatusCode {
		case 401:
			return "Provider authentication failed"
		case 403:
			return "Provider access denied"
		case 404:
			return "Provider endpoint or model not found"
		case 413:
			return "Request is too large for the provider"
		case 429:
			return "Provider rate limit reached"
		case 408, 504:
			return "Provider response timed out"
		case 400, 422:
			return "Provider rejected the request"
		default:
			if status.StatusCode >= 500 {
				return "Provider is temporarily unavailable"
			}
		}
	}
	var interrupted *StreamInterruptionError
	if errors.As(err, &interrupted) {
		return "Connection to the provider was interrupted"
	}
	var api *APIError
	if errors.As(err, &api) {
		code := strings.ToLower(api.Type + " " + api.Code)
		if strings.Contains(code, "rate_limit") {
			return "Provider rate limit reached"
		}
		if Retryable(api) {
			return "Provider stream was interrupted"
		}
		return "Provider rejected the request"
	}
	var stalled *ProgressStallError
	if errors.As(err, &stalled) || errors.Is(err, context.DeadlineExceeded) {
		return "Provider response timed out"
	}
	if Retryable(err) {
		return "Connection to the provider was interrupted"
	}
	return "Provider request failed"
}
