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

func permanentAPIError(code string) bool {
	code = strings.ToLower(code)
	for _, kind := range []string{"insufficient_quota", "billing", "credit_balance", "invalid_api_key", "authentication_error", "permission_denied", "invalid_request", "context_length_exceeded", "model_not_found"} {
		if strings.Contains(code, kind) {
			return true
		}
	}
	return false
}

// Retryable classifies recovery, not replay safety. The stream owner retries
// only before accepted output; the agent owns continuations of partial output.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
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
		code := strings.ToLower(api.Type + " " + api.Code)
		if permanentAPIError(code) {
			return false
		}
		if status, err := strconv.Atoi(api.Code); err == nil {
			return isRetryableStatus(status)
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
		if permanentAPIError(status.Code) && status.StatusCode == 429 {
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
