package provider

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/network"
)

func preserveNetworkDefaults(t *testing.T) {
	t.Helper()
	before := network.Current()
	t.Cleanup(func() {
		if err := network.ConfigureDefault(before); err != nil {
			t.Error(err)
		}
	})
}

func TestProviderTrafficUsesSavedProxyAndCapturesRequestPolicy(t *testing.T) {
	preserveNetworkDefaults(t)
	var firstCalls, secondCalls atomic.Int32
	proxy := func(label string, calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			if request.URL.Host != "provider.invalid" || request.URL.Path != "/v1/chat/completions" {
				t.Errorf("proxy received wrong destination: %s", request.URL)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, label)
		}))
	}
	first := proxy("first", &firstCalls)
	defer first.Close()
	second := proxy("second", &secondCalls)
	defer second.Close()
	settings := network.DefaultSettings()
	settings.ProxyMode, settings.ProxyURL = network.ProxyModeCustom, first.URL
	if err := network.ConfigureDefault(settings); err != nil {
		t.Fatal(err)
	}
	connection := FromPreset("openai-chat", "key", "http://provider.invalid/v1")
	oldClient, err := connection.Client()
	if err != nil {
		t.Fatal(err)
	}
	defer oldClient.hc.CloseIdleConnections()
	settings.ProxyURL = second.URL
	if err := network.ConfigureDefault(settings); err != nil {
		t.Fatal(err)
	}
	newClient, err := connection.Client()
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.hc.CloseIdleConnections()
	for _, client := range []*Client{oldClient, newClient} {
		response, err := client.Complete(context.Background(), &ChatRequest{Model: "sample", Messages: []ChatMessage{NewTextMessage("user", "hello")}})
		if err != nil || len(response.Choices) != 1 {
			t.Fatalf("saved proxy was bypassed: response=%+v err=%v", response, err)
		}
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("policy crossed request boundaries: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
}

func TestProviderUsesCustomCAAndFailsClosedOnUnreadableTrust(t *testing.T) {
	preserveNetworkDefaults(t)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"trusted"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	path := filepath.Join(t.TempDir(), "endpoint-ca.pem")
	if err := os.WriteFile(path, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	settings := network.DefaultSettings()
	settings.ProxyMode, settings.CAFile = network.ProxyModeDirect, path
	if err := network.ConfigureDefault(settings); err != nil {
		t.Fatal(err)
	}
	connection := FromPreset("openai-chat", "key", server.URL+"/v1")
	client, err := connection.Client()
	if err != nil {
		t.Fatal(err)
	}
	defer client.hc.CloseIdleConnections()
	if _, err := client.Complete(context.Background(), &ChatRequest{Model: "sample"}); err != nil {
		t.Fatalf("provider ignored custom trust: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Client(); err == nil || calls.Load() != 1 {
		t.Fatal("unreadable trust fell back to an unconfigured transport")
	}
}

func TestModelCatalogFailureUsesFiveMinuteRetryCeiling(t *testing.T) {
	catalog, err := OpenModelCatalog(filepath.Join(t.TempDir(), "catalog.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	source := ModelSource{ID: "unavailable", URL: "https://catalog.invalid", Format: "openrouter"}
	failure := &StatusError{StatusCode: 503, Message: "temporarily unavailable", RetryAfter: 24 * time.Hour}
	started := time.Now()
	if delay := catalog.recordFailure(source, failure); delay != 5*time.Minute {
		t.Fatalf("catalog retry cooldown=%s", delay)
	}
	var next int64
	if err := catalog.db.QueryRow(`SELECT next_refresh FROM sources WHERE id=?`, source.ID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Unix(next, 0).Sub(started); remaining > 5*time.Minute || remaining < 4*time.Minute {
		t.Fatalf("durable retry schedule=%s", remaining)
	}
	if _, err := catalog.db.Exec(`UPDATE sources SET failures=1000000 WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if delay := catalog.recordFailure(source, fmt.Errorf("network unavailable")); delay != 5*time.Minute {
		t.Fatalf("repeated catalog failures escaped the ceiling: %s", delay)
	}
}
