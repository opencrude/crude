package fileio

import (
	"context"
	"errors"
	"net/url"
	"time"
)

func OpenBrowserURL(ctx context.Context, rawURL string) error {
	parsed, err := WebURL(rawURL)
	if err != nil {
		return err
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("browser URL must use HTTPS or a loopback HTTP endpoint without embedded credentials")
	}
	return openBrowser(ctx, rawURL)
}

// WebURL parses a desktop web destination, never a path, shell argument or
// credential-bearing URL. The automated browser route is stricter about HTTP.
func WebURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("expected an HTTP or HTTPS address without embedded credentials")
	}
	return parsed, nil
}

// OpenConfirmedWebURL is for an explicit human acceptance of this exact
// destination. OAuth and tool actions continue to use OpenBrowserURL.
func OpenConfirmedWebURL(ctx context.Context, rawURL string) error {
	if _, err := WebURL(rawURL); err != nil {
		return err
	}
	return openBrowser(ctx, rawURL)
}

func openBrowser(ctx context.Context, rawURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return platformOpenBrowser(ctx, rawURL)
}
