package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This file proves the async LSP pipe fix end-to-end with a REAL subprocess
// language server. The fake server is the test binary itself (classic Go
// helper-process pattern), spawned BY the LSP client via ClientConfig:
//
//  1. it answers the initialize handshake,
//  2. then STOPS reading its stdin entirely, simulating a back-pressured
//     or wedged language server during the project-wide didOpen sync.
//
// Before the fix, a CloseFile issued once the pipe buffer was full blocked
// the calling (Bubble Tea update) goroutine indefinitely. The test asserts
// every LSP write returns promptly and that connection death is observable.

// fakeLSPMain is the wedged fake server body. It reads LSP-framed messages
// until it has answered initialize, then blocks forever without reading;
// the pipe fills and stays full.
func fakeLSPMain() {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	answeredInit := false

	readMessage := func() (map[string]any, error) {
		length := -1
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				length, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		if length < 0 {
			return nil, fmt.Errorf("no content-length")
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	}

	reply := func(id any, result any) error {
		resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		data, _ := json.Marshal(resp)
		if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
		return writer.Flush()
	}

	for {
		msg, err := readMessage()
		if err != nil {
			return
		}
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			_ = reply(msg["id"], map[string]any{"capabilities": map[string]any{}})
			answeredInit = true
		case "shutdown":
			_ = reply(msg["id"], nil)
		case "exit":
			return
		}
		if answeredInit {
			// WEDGE: stop reading stdin forever. The client's writes will
			// fill the pipe buffer and stay blocked (pre-fix behavior).
			// Sleep, not select{}: the runtime deadlock detector kills a
			// process whose only goroutines are in select{}.
			for {
				time.Sleep(time.Hour)
			}
		}
	}
}

// TestFakeLSPServerHelper is the subprocess entry point for the fake server.
func TestFakeLSPServerHelper(t *testing.T) {
	if os.Getenv("CRUDE_FAKE_LSP_SERVER") != "1" {
		return
	}
	fakeLSPMain()
	os.Exit(0)
}

// newFakeLSPServerIntegration builds an LSPIntegration whose server command
// IS this test binary running the helper. The client spawns it with the
// helper env var, exactly like a real language server subprocess.
func newFakeLSPServerIntegration(t *testing.T, root string) *LSPIntegration {
	t.Helper()
	uriPath := filepath.ToSlash(root)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	rootURI := (&url.URL{Scheme: "file", Path: uriPath}).String()
	integration := NewLSPIntegration(
		os.Args[0],
		[]string{"-test.run", "TestFakeLSPServerHelper"},
		rootURI,
		WithServerEnv(map[string]string{"CRUDE_FAKE_LSP_SERVER": "1"}),
	)
	integration.OnDiagnostics(func(string, []LSPDiagnostic) {})
	return integration
}

// TestLSPIntegration_CloseFileNeverBlocksOnWedgedServer is the user-facing
// regression: open a file on a real LSP server subprocess, wedge the server,
// and assert OpenFile/CloseFile (the tab-close path) return promptly instead
// of hanging the UI goroutine.
func TestLSPIntegration_CloseFileNeverBlocksOnWedgedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess LSP test")
	}
	root := t.TempDir()
	integration := newFakeLSPServerIntegration(t, root)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Open a large file so subsequent writes exceed the OS pipe buffer.
	big := strings.Repeat("package main\n\nfunc f() {}\n", 6000)
	openDone := make(chan struct{})
	go func() {
		integration.OpenFile(ctx, root+"/big.go", "go", big)
		close(openDone)
	}()
	select {
	case <-openDone:
	case <-time.After(3 * time.Second):
		t.Fatal("OpenFile blocked on the wedged server")
	}

	// The tab-close path: CloseFile must return promptly even though the
	// server is wedged and the pipe is (or becomes) full.
	closeDone := make(chan struct{})
	go func() {
		integration.CloseFile(ctx, root+"/big.go")
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("CloseFile blocked on the wedged server — this was the UI hang")
	}

	// Connection death must be observable so the manager can reap/restart.
	integration.Stop()
	select {
	case <-integration.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done did not close after server kill")
	}
}

// TestLSPIntegration_ServerDeathObservedWithoutWrites verifies a server that
// dies silently (no pending write) is still observed via the read side.
func TestLSPIntegration_ServerDeathObservedWithoutWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess LSP test")
	}
	root := t.TempDir()
	integration := newFakeLSPServerIntegration(t, root)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Kill the server process without any pending write activity.
	integration.Stop()
	select {
	case <-integration.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done did not close after the server process died")
	}
}
