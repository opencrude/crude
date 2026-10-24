//go:build linux

package remote

// Opt-in network-impaired acceptance. scripts/protocol_netem.sh runs the core
// role and the client role in two network namespaces joined by a veth pair with
// netem on both ends; nothing here touches host interfaces. The client is the
// real shell port: QUIC carrier, pinned pairing, terminal replica and prediction.
import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/shellport"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

type netemRendezvous struct {
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Code        string `json:"code"`
	Root        string `json:"root"`
	Bulk        string `json:"bulk"`
}

func netemRole(t *testing.T, role string) string {
	t.Helper()
	if os.Getenv("CRUDE_NETEM_ROLE") != role {
		t.Skip("network-impaired acceptance: run scripts/protocol_netem.sh")
	}
	dir := os.Getenv("CRUDE_NETEM_DIR")
	if dir == "" {
		t.Fatal("CRUDE_NETEM_DIR is required")
	}
	return dir
}

func TestNetemCoreRole(t *testing.T) {
	dir := netemRole(t, "core")
	listen := os.Getenv("CRUDE_NETEM_LISTEN")
	server, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "netem", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	private := t.TempDir()
	if err := os.Chmod(private, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := coreprotocol.RemoteAccessConfig{Enabled: true, QUICAddress: listen, TLSAddress: listen}
	access, err := core.OpenRemoteAccess(private, core.RemoteAccessOptions{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetRemoteAccess(access); err != nil {
		t.Fatal(err)
	}
	l, err := corewire.ListenQUIC(listen, access.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureRemoteAccess(cfg, core.SSHUpgradeEndpoints{QUICAddress: l.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() { defer close(served); _ = core.ServeQUIC(ctx, server, l, server.RemoteAuthenticator()) }()
	defer func() { cancel(); _ = l.Close(); <-served }()
	code, err := access.CreatePairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Incompressible bulk data competes with terminal frames for the same
	// congestion window; it is the "other topic" the gate must not stall behind.
	bulk := make([]byte, 4<<20)
	for i := range bulk {
		bulk[i] = byte(rand.Uint32())
	}
	if err := os.WriteFile(filepath.Join(root, "bulk.bin"), bulk, 0600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(netemRendezvous{Address: l.Addr().String(), Fingerprint: access.Identity().Fingerprint, Code: code.Code, Root: root, Bulk: "bulk.bin"})
	if err := os.WriteFile(filepath.Join(dir, "rendezvous.tmp"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "rendezvous.tmp"), filepath.Join(dir, "rendezvous.json")); err != nil {
		t.Fatal(err)
	}
	t.Logf("core listening quic=%s", l.Addr())
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if _, err := os.Stat(filepath.Join(dir, "client.done")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("client role never finished")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// netemShell is the minimal Update loop: it admits frames in order, applies
// them through the replica and acknowledges, exactly as the TUI does.
type netemShell struct {
	port    *Workspace
	mu      sync.Mutex
	text    map[string]string
	changed chan struct{}
}

func (s *netemShell) run(ctx context.Context, messages <-chan any) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-messages:
			msg, ok := event.(shellport.TerminalFrameMsg)
			if !ok {
				continue
			}
			err := s.port.terminals.ApplyTerminalFrame(&msg)
			if err == nil && msg.Err == nil {
				s.mu.Lock()
				s.text[msg.ID] = netemText(msg.Projection)
				s.mu.Unlock()
				select {
				case s.changed <- struct{}{}:
				default:
				}
			}
			if msg.Applied != nil {
				msg.Applied <- err
			}
		}
	}
}

func (s *netemShell) wait(t *testing.T, id string, within time.Duration, ok func(string) bool) time.Time {
	t.Helper()
	timer := time.NewTimer(within)
	defer timer.Stop()
	for {
		s.mu.Lock()
		text := s.text[id]
		s.mu.Unlock()
		if ok(text) {
			return time.Now()
		}
		select {
		case <-s.changed:
		case <-timer.C:
			t.Fatalf("terminal %s did not reach the expected state within %v", id, within)
		}
	}
}

func netemText(p workspace.TerminalProjection) string {
	var b strings.Builder
	for _, row := range p.Grid.Cells {
		for _, c := range row {
			b.WriteString(c.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func netemPercentile(samples []time.Duration, p float64) time.Duration {
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	return sorted[min(len(sorted)-1, int(float64(len(sorted))*p))]
}

func TestNetemClientRole(t *testing.T) {
	dir := netemRole(t, "client")
	defer func() {
		if err := os.WriteFile(filepath.Join(dir, "client.done"), nil, 0600); err != nil {
			t.Error(err)
		}
	}()
	var rv netemRendezvous
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		data, err := os.ReadFile(filepath.Join(dir, "rendezvous.json"))
		if err == nil {
			if err := json.Unmarshal(data, &rv); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core role never published its rendezvous")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	credential, err := coreclient.PairRemote(ctx, rv.Address, rv.Fingerprint, "quic", "netem-pair", coreprotocol.RemotePairRequest{Code: rv.Code, DeviceName: "netem client"})
	if err != nil {
		t.Fatal(err)
	}
	quic := func(ctx context.Context) (corewire.Conn, error) {
		return corewire.DialQUIC(ctx, rv.Address, rv.Fingerprint)
	}
	port, err := Attach(ctx, Options{Root: rv.Root, Dial: func(ctx context.Context, id string) (*coreclient.Client, error) {
		return coreclient.DialWithOptions(ctx, quic, id, coreclient.DialOptions{Credential: credential.Bearer()})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer port.Close()
	messages := make(chan any, 1024)
	bus := workspace.NewEventBus()
	bus.SetSink(pushSink{messages})
	port.SetEventBus(bus)
	port.StartReplica()
	shell := &netemShell{port: port, text: map[string]string{}, changed: make(chan struct{}, 1)}
	pumpCtx, stopPump := context.WithCancel(ctx)
	defer stopPump()
	go shell.run(pumpCtx, messages)

	client, wsID := port.conn()
	carrier, _ := client.Carrier()
	if carrier != corewire.CarrierQUIC {
		t.Fatalf("carrier %q, want quic", carrier)
	}
	openEcho := func(label string) (workspace.TerminalInfo, workspace.TerminalLease) {
		info, err := port.terminals.Create(workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; exec cat"}, Cols: 200, Rows: 50})
		if err != nil {
			t.Fatal(label, err)
		}
		lease, err := port.terminals.Acquire(info.ID, workspace.TerminalController{})
		if err != nil {
			t.Fatal(label, err)
		}
		port.terminals.StartGrid(info.ID)
		shell.wait(t, info.ID, 20*time.Second, func(s string) bool { return strings.Contains(s, "READY") })
		return info, lease
	}
	// Keys arrive at human typing cadence; each sample is submit → the
	// authoritative echo applied by the shell's Update loop.
	measure := func(label string, keys int) (echo, predicted []time.Duration, predictedMisses int) {
		info, lease := openEcho(label)
		typed := "READY"
		for i := range keys {
			r := rune('a' + i%26)
			key := terminalvt.Key{Code: r, Text: string(r)}
			started := time.Now()
			if err := port.terminals.Key(lease, key); err != nil {
				t.Fatal(label, err)
			}
			if p, ok := port.terminals.PredictiveKey(info.ID, key); ok && strings.Contains(netemText(p), typed+string(r)) {
				predicted = append(predicted, time.Since(started))
			} else {
				predictedMisses++
			}
			typed += string(r)
			done := shell.wait(t, info.ID, 10*time.Second, func(s string) bool { return strings.Contains(s, typed) })
			echo = append(echo, done.Sub(started))
			time.Sleep(time.Duration(60+rand.IntN(60)) * time.Millisecond)
		}
		port.terminals.Close(info.ID)
		return echo, predicted, predictedMisses
	}
	report := func(label string, samples []time.Duration) {
		_, rtt := client.Carrier()
		t.Logf("%s keys=%d srtt=%v echo p50=%v p90=%v p99=%v max=%v", label, len(samples), rtt, netemPercentile(samples, .5), netemPercentile(samples, .9), netemPercentile(samples, .99), netemPercentile(samples, 1))
	}
	keys := 100
	type phase struct {
		label           string
		flood, bulk     bool
		echo, predicted []time.Duration
	}
	// Competing topics on the same carrier, separately and together: a
	// full-screen terminal flood and a continuous incompressible bulk download.
	phases := []phase{{label: "idle"}, {label: "flood", flood: true}, {label: "bulk", bulk: true}, {label: "flood+bulk", flood: true, bulk: true}}
	for i := range phases {
		ph := &phases[i]
		loadCtx, stopLoad := context.WithCancel(ctx)
		var loadWG sync.WaitGroup
		var flood workspace.TerminalInfo
		if ph.flood {
			flood, err = port.terminals.Create(workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "exec yes 'netem flood 0123456789 abcdefghijklmnopqrstuvwxyz ABCDEFGHIJKLMNOPQRSTUVWXYZ'"}, Cols: 200, Rows: 50})
			if err != nil {
				t.Fatal(err)
			}
			port.terminals.StartGrid(flood.ID)
		}
		var bulkBytes, bulkReads int
		var bulkErr error
		if ph.bulk {
			loadWG.Add(1)
			go func() {
				defer loadWG.Done()
				for loadCtx.Err() == nil {
					data, err := client.ReadClientFile(loadCtx, wsID, rv.Bulk, false)
					if err != nil {
						if loadCtx.Err() == nil {
							bulkErr = err
						}
						return
					}
					bulkBytes += len(data)
					bulkReads++
				}
			}()
		}
		started := time.Now()
		var misses int
		ph.echo, ph.predicted, misses = measure(ph.label, keys)
		elapsed := time.Since(started)
		stopLoad()
		loadWG.Wait()
		if ph.flood {
			port.terminals.Close(flood.ID)
		}
		report(ph.label, ph.echo)
		t.Logf("%s prediction shown=%d missed=%d bulk_reads=%d bulk_rate=%.2f MiB/s", ph.label, len(ph.predicted), misses, bulkReads, float64(bulkBytes)/(1<<20)/elapsed.Seconds())
		if bulkErr != nil {
			t.Errorf("%s bulk download failed under impairment: %v", ph.label, bulkErr)
		}
		if ph.bulk && bulkReads == 0 {
			t.Errorf("%s: no bulk download completed; the competing topic did not exercise the carrier", ph.label)
		}
	}
	_, srtt := client.Carrier()
	// Prediction follows mosh's adaptive policy: off below 60 ms SRTT, and
	// when on, every safe key shows locally without waiting for the network.
	wantPrediction := srtt/2 > 30*time.Millisecond
	for _, ph := range phases {
		p50, p99, worst := netemPercentile(ph.echo, .5), netemPercentile(ph.echo, .99), netemPercentile(ph.echo, 1)
		fmt.Fprintf(os.Stderr, "NETEM %s srtt_ms=%.2f echo_p50_ms=%.2f echo_p99_ms=%.2f echo_max_ms=%.2f predicted=%d/%d\n", ph.label,
			float64(srtt)/1e6, float64(p50)/1e6, float64(p99)/1e6, float64(worst)/1e6, len(ph.predicted), keys)
		if ceiling := netemEchoCeiling(srtt); p50 > ceiling {
			t.Errorf("%s echo p50 %v exceeds %v at SRTT %v", ph.label, p50, ceiling, srtt)
		}
		if worst > time.Second {
			t.Errorf("%s echo stalled %v", ph.label, worst)
		}
		switch {
		case wantPrediction && len(ph.predicted) != keys:
			t.Errorf("%s predicted %d of %d keys at SRTT %v", ph.label, len(ph.predicted), keys, srtt)
		case wantPrediction && netemPercentile(ph.predicted, 1) > 5*time.Millisecond:
			t.Errorf("%s predicted echo max %v, want immediate", ph.label, netemPercentile(ph.predicted, 1))
		case !wantPrediction && len(ph.predicted) != 0:
			t.Errorf("%s predicted %d keys below mosh's 60 ms SRTT trigger", ph.label, len(ph.predicted))
		}
	}
}

// "Echo ≈ RTT" is bounded by mosh's own schedule: one round trip plus at most
// one send interval, ceil(SRTT/2) clamped to 20–250 ms, while a frame waits.
func netemEchoCeiling(srtt time.Duration) time.Duration {
	return srtt + min(max(srtt/2, 20*time.Millisecond), 250*time.Millisecond)
}
