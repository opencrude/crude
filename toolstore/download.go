package toolstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"crude/network"
)

const (
	defaultStallTimeout = 60 * time.Second
	maxRedirects        = 5
)

// errStalled is the cancel cause when a download sends no data for the stall
// timeout.
var errStalled = errors.New("download stalled")

// download streams url into dst and returns the SHA-256 digest and size. It
// uses the shared network client, accepts HTTPS only (loopback HTTP only when
// a test enables it), permits at most five safe redirects and stops when the
// response exceeds limit or sends no data for the stall timeout.
func (s *Store) download(parent context.Context, rawURL string, limit int64, dst string) (string, int64, error) {
	if err := s.checkURL(rawURL); err != nil {
		return "", 0, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	stall := s.stallTimeout
	watchdog := time.AfterFunc(stall, func() { cancel(errStalled) })
	defer watchdog.Stop()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("Accept", "application/octet-stream, */*")
	client := network.NewClient(0)
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		if len(previous) >= maxRedirects {
			return fmt.Errorf("the download redirected more than %d times", maxRedirects)
		}
		if err := s.checkURL(next.URL.String()); err != nil {
			return fmt.Errorf("the download redirected to an unsafe address: %w", err)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return "", 0, downloadError(parent, ctx, stall, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("the download server returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return "", 0, fmt.Errorf("the download is %d bytes, which is more than its budget of %d bytes", response.ContentLength, limit)
	}
	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	body := &progressReader{reader: response.Body, onProgress: func() { watchdog.Reset(stall) }}
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, limit+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, downloadError(parent, ctx, stall, copyErr)
	}
	if written > limit {
		return "", 0, fmt.Errorf("the download is more than its budget of %d bytes", limit)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return "", 0, fmt.Errorf("the download ended after %d of %d bytes", written, response.ContentLength)
	}
	if syncErr != nil {
		return "", 0, syncErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

// downloadError names cancellation and stalls in plain words.
func downloadError(parent, ctx context.Context, stall time.Duration, err error) error {
	if parent.Err() != nil {
		return fmt.Errorf("the download was cancelled: %w", parent.Err())
	}
	if errors.Is(context.Cause(ctx), errStalled) {
		return fmt.Errorf("the download sent no data for %s: %w", stall, errStalled)
	}
	return fmt.Errorf("the download failed: %w", err)
}

// checkURL accepts HTTPS URLs without user information. Loopback HTTP is
// accepted only when a test enables it.
func (s *Store) checkURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return errors.New("download address is not a valid URL")
	}
	if parsed.User != nil {
		return errors.New("download address must not contain a user name or password")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if s.allowLoopbackHTTP && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
			return nil
		}
	}
	return fmt.Errorf("download address %q must use HTTPS", parsed.Redacted())
}

type progressReader struct {
	reader     io.Reader
	onProgress func()
}

func (r *progressReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	if n > 0 {
		r.onProgress()
	}
	return n, err
}
