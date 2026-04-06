package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIConfig holds settings for a generic HTTP/REST backend.
type APIConfig struct {
	BaseURL    string            `json:"base_url"`
	Headers    map[string]string `json:"headers,omitempty"`
	AuthHeader string            `json:"auth_header,omitempty"`
	AuthValue  string            `json:"auth_value,omitempty"`
	Timeout    int               `json:"timeout_seconds,omitempty"`
}

// API executes CRUDE operations against a generic HTTP/REST API.
type API struct {
	cfg    APIConfig
	client *http.Client
}

// NewAPI creates an API backend.
func NewAPI(cfg APIConfig) *API {
	timeout := 30 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	return &API{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

func (a *API) Type() string { return "api" }

func (a *API) doRequest(method, path string, body any) ([]byte, error) {
	url := strings.TrimRight(a.cfg.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	for k, v := range a.cfg.Headers {
		req.Header.Set(k, v)
	}
	if a.cfg.AuthHeader != "" && a.cfg.AuthValue != "" {
		req.Header.Set(a.cfg.AuthHeader, a.cfg.AuthValue)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("api: HTTP %d: %s", resp.StatusCode, truncateBytes(respBody, 500))
	}
	return respBody, nil
}

// Execute makes an API request. cmd format: "METHOD /path".
func (a *API) Execute(cmd string, opts ExecOptions) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) < 2 {
		return "", fmt.Errorf("api: Execute format: 'METHOD /path'; got %q", cmd)
	}
	method := strings.ToUpper(parts[0])
	path := parts[1]

	body, err := a.doRequest(method, path, nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ReadFile performs a GET request to the given path.
func (a *API) ReadFile(path string) (string, error) {
	body, err := a.doRequest("GET", path, nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// WriteFile performs a PUT request with the content as JSON body.
func (a *API) WriteFile(path, content string) error {
	var body any
	if err := json.Unmarshal([]byte(content), &body); err != nil {
		body = content
	}
	_, err := a.doRequest("PUT", path, body)
	return err
}

// EditFile performs a PATCH request.
func (a *API) EditFile(path, oldStr, newStr string) (int, error) {
	patchBody := map[string]string{"old": oldStr, "new": newStr}
	_, err := a.doRequest("PATCH", path, patchBody)
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// DeleteFile performs a DELETE request.
func (a *API) DeleteFile(path string) error {
	_, err := a.doRequest("DELETE", path, nil)
	return err
}

// MoveFile is not supported for generic APIs.
func (a *API) MoveFile(src, dst string) error {
	return fmt.Errorf("api: MoveFile not supported")
}

// CopyFile is not supported for generic APIs.
func (a *API) CopyFile(src, dst string) error {
	return fmt.Errorf("api: CopyFile not supported")
}

// ListDir performs a GET request and parses the response as a list.
func (a *API) ListDir(path string) ([]DirEntry, error) {
	body, err := a.doRequest("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var items []string
	if err := json.Unmarshal(body, &items); err != nil {
		// Try as array of objects with "name" field.
		var objItems []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &objItems); err != nil {
			return []DirEntry{{Name: string(body), IsDir: false}}, nil
		}
		for _, item := range objItems {
			items = append(items, item.Name)
		}
	}

	entries := make([]DirEntry, 0, len(items))
	for _, name := range items {
		entries = append(entries, DirEntry{Name: name, IsDir: false})
	}
	return entries, nil
}

// GlobFiles is not supported for generic APIs.
func (a *API) GlobFiles(baseDir, pattern string) ([]string, error) {
	return nil, fmt.Errorf("api: GlobFiles not supported")
}

// GrepFiles is not supported for generic APIs.
func (a *API) GrepFiles(pattern, baseDir string, recursive bool) ([]GrepMatch, error) {
	return nil, fmt.Errorf("api: GrepFiles not supported")
}

// Post performs a POST request with a JSON body.
func (a *API) Post(path string, body any) (string, error) {
	resp, err := a.doRequest("POST", path, body)
	if err != nil {
		return "", err
	}
	return string(resp), nil
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// Ensure API implements Executor.
var _ Executor = (*API)(nil)
