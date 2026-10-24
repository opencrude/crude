package remote

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

func TestPrivateClipboardPresentationFencesWorkspaceConnectionAndLease(t *testing.T) {
	t.Setenv("CRUDE_HOME", t.TempDir())
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, b := net.Pipe()
	go func() { _ = core.ServeConnection(t.Context(), r, a) }()
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(b), "private-render")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	w := &Workspace{client: client, id: "ws", ctx: context.Background()}
	w.terminals = &terminalReplica{w: w, leases: map[string]workspace.TerminalLease{"term": {TerminalID: "term", Generation: 7, Owner: workspace.TerminalController{ClientID: client.Owner().ClientID, Epoch: client.Owner().Epoch}}}}
	base := coreclient.PrivateEffect{WorkspaceID: "ws", TerminalClipboard: &coreprotocol.TerminalClipboard{TerminalID: "term", Generation: 7, Clipboard: [][]byte{[]byte("explicit copy offer")}}}
	apply := func(client *coreclient.Client, effect coreclient.PrivateEffect) shellport.ReplicaApplied {
		return w.ApplyReplicaChanges(shellport.ReplicaChangesMsg{Owner: w, Batch: privateEffectsBatch{client: client, effect: effect}})
	}
	accepted := apply(client, base)
	if len(accepted.Follow) != 1 {
		t.Fatal("controller-local offer not presented", accepted)
	}
	message, ok := accepted.Follow[0].(workspace.TerminalEffectsMsg)
	if !ok || message.ID != "term" || string(message.Effects.Clipboard[0]) != "explicit copy offer" || len(message.Effects.Replies) != 0 {
		t.Fatal("wrong private presentation", accepted)
	}
	for name, change := range map[string]func(*coreclient.PrivateEffect){
		"workspace": func(e *coreclient.PrivateEffect) { e.WorkspaceID = "other" },
		"old lease": func(e *coreclient.PrivateEffect) {
			copy := *e.TerminalClipboard
			copy.Generation = 6
			e.TerminalClipboard = &copy
		},
		"uncontrolled terminal": func(e *coreclient.PrivateEffect) {
			copy := *e.TerminalClipboard
			copy.TerminalID = "peer"
			e.TerminalClipboard = &copy
		},
	} {
		t.Run(name, func(t *testing.T) {
			effect := base
			change(&effect)
			if got := apply(client, effect); len(got.Follow) != 0 {
				t.Fatal("stale/private authority escaped", got)
			}
		})
	}
	if got := apply(nil, base); len(got.Follow) != 0 {
		t.Fatal("old connection offer presented", got)
	}
	failure := apply(client, coreclient.PrivateEffect{WorkspaceID: "ws", Error: "clipboard inbox full"})
	if len(failure.Follow) != 1 {
		t.Fatal("admission refusal not visible", failure)
	}
	if _, ok := failure.Follow[0].(shellport.ReplicaFailureMsg); !ok {
		t.Fatal("refusal not a visible shell failure", failure)
	}
	w.terminals.leaseMu.Lock()
	delete(w.terminals.leases, "term")
	w.terminals.leaseMu.Unlock()
	if got := apply(client, base); len(got.Follow) != 0 {
		t.Fatal("released lease's queued clipboard offer presented", got)
	}
}
