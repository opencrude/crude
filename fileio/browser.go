package fileio

import (
	"context"
	"errors"
	"net/url"
	"time"
)

func OpenBrowserURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Scheme != "https" && (parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		return errors.New("browser URL must use HTTPS or a loopback HTTP endpoint without embedded credentials")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return platformOpenBrowser(ctx, rawURL)
}
