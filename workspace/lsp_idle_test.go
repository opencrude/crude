package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"crude/types"
)

func TestLSPIdleReasonsExplainLazyStartupAndPolicy(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	if got := m.ProjectInsights(); got.IdleStatus != "Waiting for source file" || !strings.Contains(got.IdleReason, "Open a supported source file") {
		t.Fatalf("enabled unused workspace reason=%+v", got)
	}
	settings := types.DefaultLSPSettings()
	settings.Enabled = false
	m.SetSettings(settings)
	if got := m.ProjectInsights(); got.IdleStatus != "Language intelligence off" {
		t.Fatalf("disabled reason=%+v", got)
	}
	path := filepath.Join(root, "main.go")
	m.OpenFile(t.Context(), path, "package main\n")
	if len(runtime.snapshot()) != 0 {
		t.Fatal("disabled editor launched a server")
	}
	settings.Enabled, settings.StartOnOpen = true, false
	m.SetSettings(settings)
	if got := m.ProjectInsights(); got.IdleStatus != "Automatic start off" || !strings.Contains(got.IdleReason, "Start Language Servers") {
		t.Fatalf("manual startup reason=%+v", got)
	}
	m.StartForWorkspace(root)
	awaitLSP(t, m.ServerReady)
	if got := m.ProjectInsights(); got.IdleStatus != "" || got.IdleReason != "" {
		t.Fatalf("connected server retained idle explanation=%+v", got)
	}
	m.Stop()
	if got := m.ProjectInsights(); got.IdleStatus != "Language servers paused" {
		t.Fatalf("paused reason=%+v", got)
	}
}

func TestLSPSettingsEnableStartsAlreadyOpenDocumentWithoutReopen(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	settings := types.DefaultLSPSettings()
	settings.Enabled = false
	m.SetSettings(settings)
	path := filepath.Join(root, "main.go")
	m.OpenFile(t.Context(), path, "package main\nvar _ = acceptedUnsavedText\n")
	settings.Enabled = true
	m.SetSettings(settings)
	awaitLSP(t, func() bool {
		clients := runtime.snapshot()
		return len(clients) == 1 && len(clients[0].snapshot()) == 1
	})
	if got := runtime.snapshot()[0].snapshot()[0]; got != "open:main.go:go:package main\nvar _ = acceptedUnsavedText\n" {
		t.Fatalf("enable lost retained buffer=%q", got)
	}
}

func TestLSPIdleResumeWithoutDocumentsRefreshesReason(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	m.Stop()
	m.StartForWorkspace(root)
	if got := m.ProjectInsights(); got.IdleStatus != "Waiting for source file" {
		t.Fatalf("resume retained paused reason=%+v", got)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatal("resume eagerly launched a server without documents")
	}
}

func TestLSPUnsupportedDocumentExplainsNoMatchingServer(t *testing.T) {
	m, runtime, root := lifecycleManager(t)
	m.OpenFile(t.Context(), filepath.Join(root, "readme.md"), "# No configured Markdown server\n")
	if got := m.ProjectInsights(); got.IdleStatus != "No matching language server" || !strings.Contains(got.IdleReason, "Settings → Language Servers") {
		t.Fatalf("unmatched source reason=%+v", got)
	}
	if len(runtime.snapshot()) != 0 {
		t.Fatal("unsupported file launched an arbitrary server")
	}
}
