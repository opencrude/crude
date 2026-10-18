package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

func TestConnectedTerminalRowsCarryAnchoredHistoryAndRecoverReset(t *testing.T) {
	var retained *workspace.Workspace
	server, err := core.New(func(root string) (*workspace.Workspace, error) {
		retained = workspace.NewWorkspace(root, "test-model", "tester", types.Settings{}, nil)
		retained.SetAppDefaults(types.AppSettings{Terminal: types.TerminalSettings{ScrollbackLines: 100}, LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		return retained, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		a, b := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), server, a) }()
		return coreclient.Dial(ctx, b, id)
	}
	port, err := Attach(t.Context(), Options{Root: t.TempDir(), Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := port.Close(); err != nil {
			t.Error(err)
		}
	})
	service, err := port.EnsureTerminals()
	if err != nil {
		t.Fatal(err)
	}
	opts := workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"300"}, Cols: 30, Rows: 4}
	if runtime.GOOS == "windows" {
		opts.Command, opts.Args = "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Write-Output CONNECTED_CHILD_READY; Start-Sleep -Seconds 300"}
	}
	info, err := service.Create(opts)
	if errors.Is(err, terminalvt.ErrNoCGO) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	text := func(p workspace.TerminalProjection) string {
		var b strings.Builder
		for _, row := range p.Grid.Cells {
			for _, c := range row {
				b.WriteString(c.Text)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}
	if runtime.GOOS == "windows" {
		// Do not race injected history against ConPTY's startup clear/cursor
		// publication. A fixed delay may expire before the native child starts
		// under full-suite/race load; wait for output through the actual wire.
		deadline := time.Now().Add(10 * time.Second)
		for {
			ready, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text(ready), "CONNECTED_CHILD_READY") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("native child never completed startup")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// The connected viewer holds no controller lease. Only the retained core
	// owns ingestion; detached Rows passes through real client/wire/actor/service.
	var source strings.Builder
	for i := 0; i < 160; i++ {
		fmt.Fprintf(&source, "connected-%05d\r\n", i)
	}
	retained.Terminals.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte("\x1bc" + source.String())})
	p, err := service.Rows(info.ID, terminalvt.RowRequest{Start: 16})
	if err != nil {
		t.Fatal(err)
	}
	want := text(p)
	if !strings.Contains(want, "connected-00073") || !strings.Contains(want, "connected-00076") {
		t.Fatalf("unexpected initial source rows: %q", want)
	}
	anchoredRow := uint64(p.Grid.Start) + p.Grid.HistoryBase
	initialBase := p.Grid.HistoryBase
	for i := 160; i < 168; i++ {
		retained.Terminals.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte(fmt.Sprintf("connected-%05d\r\n", i))})
		q, err := service.Rows(info.ID, terminalvt.RowRequest{Start: p.Grid.Start, HistoryEpoch: p.Grid.HistoryEpoch, HistoryBase: p.Grid.HistoryBase})
		// ConPTY can publish additional startup/cursor output between the
		// synthetic ingress and RPC. The connected contract is exact source
		// identity plus its published eviction displacement, not one eviction
		// per synthetic call while an independent native producer is alive.
		if err != nil || text(q) != want || uint64(q.Grid.Start)+q.Grid.HistoryBase != anchoredRow || q.Grid.HistoryBase < p.Grid.HistoryBase || q.Grid.HistoryEpoch != p.Grid.HistoryEpoch {
			t.Fatalf("connected history lost request identity: start=%d->%d epoch=%d->%d base=%d->%d before=%q after=%q err=%v", p.Grid.Start, q.Grid.Start, p.Grid.HistoryEpoch, q.Grid.HistoryEpoch, p.Grid.HistoryBase, q.Grid.HistoryBase, want, text(q), err)
		}
		p = q
	}
	if p.Grid.HistoryBase <= initialBase {
		t.Fatal("fixture never exercised anchored eviction over the wire")
	}
	retained.Terminals.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte("\x1bcreset-row\r\nother-row")})
	q, err := service.Rows(info.ID, terminalvt.RowRequest{Start: p.Grid.Start, Count: 1, HistoryEpoch: p.Grid.HistoryEpoch, HistoryBase: p.Grid.HistoryBase})
	if err != nil || q.Grid.HistoryEpoch == p.Grid.HistoryEpoch || len(q.Grid.Cells) != 4 || !strings.Contains(text(q), "reset-row") {
		t.Fatalf("connected old epoch did not recover whole current viewport: rows=%d text=%q err=%v", len(q.Grid.Cells), text(q), err)
	}
}
