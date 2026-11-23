package transport

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sourcegraph/jsonrpc2"
)

// A server request without params (workspace/diagnostic/refresh) reaches its
// handler with empty params instead of panicking the connection reader.
func TestRouteRequestWithoutParams(t *testing.T) {
	router := NewRouter()
	called := false
	router.Handle("workspace/diagnostic/refresh", func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		called = params == nil
		return nil, nil
	})
	if _, err := router.Route(context.Background(), &jsonrpc2.Request{Method: "workspace/diagnostic/refresh", ID: jsonrpc2.ID{Num: 1}}); err != nil || !called {
		t.Fatalf("request without params: called=%v err=%v", called, err)
	}
	notified := false
	router.HandleNotification("exit", func(_ context.Context, _ string, params json.RawMessage) { notified = params == nil })
	if _, err := router.Route(context.Background(), &jsonrpc2.Request{Method: "exit", Notif: true}); err != nil || !notified {
		t.Fatalf("notification without params: called=%v err=%v", notified, err)
	}
}
