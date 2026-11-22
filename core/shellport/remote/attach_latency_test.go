package remote

import (
	"context"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/types/statuscenter"
)

func TestAttachPipelinesInitialReads(t *testing.T) {
	root := t.TempDir()
	const workspaceID = "remote-workspace"
	const sessionID = "retained-session"
	issue := statuscenter.OperationIssue{Title: "Check workspace settings"}
	answers := map[string]coreprotocol.Value{
		coreprotocol.ListSessions:       coreprotocol.Ptr(coreprotocol.SessionRegistry{{ID: sessionID}}),
		coreprotocol.SettingsFetch:      &coreprotocol.SettingsFetchResult{WorkspaceRevision: "settings-revision"},
		coreprotocol.WorkspaceOverrides: &coreprotocol.OverridesResult{},
		coreprotocol.WorkspaceStartup:   &coreprotocol.StartupResult{Issues: []statuscenter.OperationIssue{issue}},
		coreprotocol.StatusSnapshotMethod: &coreprotocol.StatusSnapshot{
			Version: 5,
		},
		coreprotocol.TerminalList:      coreprotocol.Ptr(coreprotocol.TerminalInfos{{ID: "terminal", Alive: true}}),
		coreprotocol.LSPSnapshotMethod: &coreprotocol.LSPSnapshot{Version: 7},
	}

	// A slow path may delay every initial reply. The shell should send the
	// independent requests before any reply crosses that path, so the delay
	// is paid once rather than testing a wall-clock threshold.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	received := make(chan string, len(answers))
	release := make(chan struct{})
	a, b := net.Pipe()
	holdingCore(t, a, "", nil, func(open coreprotocol.Open, _ []byte) coreprotocol.Value {
		if open.Method == coreprotocol.Attach {
			return &coreprotocol.AttachResult{WorkspaceID: workspaceID, View: coreprotocol.WorkspaceSnapshot{ID: workspaceID, Root: root}}
		}
		answer, ok := answers[open.Method]
		if !ok {
			t.Errorf("unexpected attach command: %s", open.Method)
			return &coreprotocol.Empty{}
		}
		select {
		case received <- open.Method:
		case <-ctx.Done():
			return answer
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return answer
	})
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		return coreclient.Dial(ctx, coreclient.SingleCarrier(b), id)
	}
	type attachment struct {
		w   *Workspace
		err error
	}
	result := make(chan attachment, 1)
	go func() {
		w, err := Attach(ctx, Options{Root: root, Dial: dial})
		result <- attachment{w, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			if a := <-result; a.w != nil {
				_ = a.w.Close()
			}
		}
	}()
	seen := make(map[string]bool, len(answers))
	for len(seen) < len(answers) {
		select {
		case method := <-received:
			if seen[method] {
				t.Fatalf("duplicate initial read: %s", method)
			}
			seen[method] = true
		case <-ctx.Done():
			t.Fatalf("initial reads waited for an earlier reply; received only %v", seen)
		}
	}
	close(release)
	aResult := <-result
	joined = true
	if aResult.err != nil {
		t.Fatal(aResult.err)
	}
	w := aResult.w
	defer w.Close()
	if sessions := w.ListSessions(); w.WorkspaceID() != workspaceID || w.RootPath() != root || len(sessions) != 1 || sessions[0].ID != sessionID {
		t.Fatalf("shell did not receive its workspace and session: %s %s %+v", w.WorkspaceID(), w.RootPath(), sessions)
	}
	if issues := w.TakeStartupIssues(); len(issues) != 1 || issues[0] != issue {
		t.Fatalf("startup issues were not installed: %+v", issues)
	}
	if w.settings.current.fetch.WorkspaceRevision != "settings-revision" || w.status.core.Version != 5 ||
		len(w.terminals.infos) != 1 || w.terminals.infos[0].ID != "terminal" || w.lsp.snapshot.Version != 7 {
		t.Fatal("initial replicas were not populated from the core replies")
	}
}

func TestStartupTransfersOnlyTheSelectedRootSession(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same=%v", same), func(t *testing.T) {
			root := t.TempDir()
			selected := root
			if !same {
				selected = t.TempDir()
			}
			var ids []string
			opts := Options{Root: root, ClientID: "profile", Dial: func(ctx context.Context, id string) (*coreclient.Client, error) {
				ids = append(ids, id)
				a, b := net.Pipe()
				holdingCore(t, a, "", nil, func(open coreprotocol.Open, _ []byte) coreprotocol.Value {
					switch open.Method {
					case coreprotocol.StartupInspect:
						return &coreprotocol.StartupSnapshot{Root: root}
					case coreprotocol.Attach:
						return &coreprotocol.AttachResult{WorkspaceID: "workspace", View: coreprotocol.WorkspaceSnapshot{ID: "workspace", Root: selected}}
					case coreprotocol.ListSessions:
						return &coreprotocol.SessionRegistry{}
					case coreprotocol.WorkspaceStartup:
						return &coreprotocol.StartupResult{}
					case coreprotocol.SettingsFetch:
						return &coreprotocol.SettingsFetchResult{}
					case coreprotocol.WorkspaceOverrides:
						return &coreprotocol.OverridesResult{}
					case coreprotocol.StatusSnapshotMethod:
						return &coreprotocol.StatusSnapshot{}
					case coreprotocol.TerminalList:
						return &coreprotocol.TerminalInfos{}
					case coreprotocol.LSPSnapshotMethod:
						return &coreprotocol.LSPSnapshot{}
					}
					return &coreprotocol.Empty{}
				})
				return coreclient.Dial(ctx, coreclient.SingleCarrier(b), id)
			}}
			nav, err := ConnectStartup(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer nav.Close()
			if _, err = nav.Inspect(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			workspace, err := nav.AttachWorkspace(t.Context(), selected)
			if err != nil {
				t.Fatal(err)
			}
			defer workspace.Close()
			original := opts.attachmentID()
			opts.Root = selected
			want := []string{original}
			if !same {
				want = append(want, opts.attachmentID())
			}
			if !slices.Equal(ids, want) {
				t.Fatalf("dialed %v; want %v", ids, want)
			}
			_ = nav.Close()
			c, _ := workspace.conn()
			if c.Err() != nil || workspace.RootPath() != selected {
				t.Fatal("closing navigation lost the selected workspace")
			}
		})
	}
}
