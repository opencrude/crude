//go:build linux

package core

// These opt-in journeys run real applications in the production PTY, VT,
// carrier and SDK. They do not claim to measure an outer emulator's presentation.
import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
	"crude/terminalvt"
	"crude/workspace"

	"golang.org/x/sys/unix"
)

func requireProtocolTorture(t *testing.T) {
	t.Helper()
	if os.Getenv("CRUDE_PROTOCOL_TORTURE") != "1" {
		t.Skip("real-process torture: set CRUDE_PROTOCOL_TORTURE=1")
	}
	t.Setenv("LC_ALL", "C")
	t.Setenv("CRUDE_HOME", t.TempDir())
	t.Logf("environment go=%s os=%s arch=%s GOMAXPROCS=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0))
}

func tortureBinary(t *testing.T, env, fallback string) string {
	t.Helper()
	path := os.Getenv(env)
	if path == "" {
		path = fallback
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		t.Fatalf("required application %s: %v", path, err)
	}
	return resolved
}

// The wrapper counts actual stream bytes read from the carrier, including the
// application length prefix, not a re-marshaled estimate. Mux framing/credit is
// excluded and must not be claimed as measured by this stream wrapper.
type tortureCountStream struct {
	corewire.Stream
	bytes    atomic.Int64
	terminal atomic.Bool
	opened   bool // the stream has exactly one writer
}

func (s *tortureCountStream) Write(p []byte) (int, error) {
	if !s.opened {
		s.opened = true
		_, n := binary.Uvarint(p)
		if n > 0 {
			var open coreprotocol.Open
			if _, err := coreprotocol.DecodePrefix(p[n:], &open); err == nil {
				s.terminal.Store(open.Kind == coreprotocol.StreamTopic && open.Topic == coreprotocol.TopicTerminal)
			}
		}
	}
	return s.Stream.Write(p)
}

func (s *tortureCountStream) Read(p []byte) (int, error) {
	n, err := s.Stream.Read(p)
	s.bytes.Add(int64(n))
	return n, err
}

type tortureCarrier struct {
	corewire.Conn
	mu      sync.Mutex
	streams []*tortureCountStream
}

func (c *tortureCarrier) OpenStream(ctx context.Context, class corewire.Class) (corewire.Stream, error) {
	s, err := c.Conn.OpenStream(ctx, class)
	if err != nil {
		return nil, err
	}
	w := &tortureCountStream{Stream: s}
	c.mu.Lock()
	c.streams = append(c.streams, w)
	c.mu.Unlock()
	return w, nil
}

type tortureClient struct {
	c       *coreclient.Client
	mu      sync.Mutex
	latest  *tortureCarrier
	servers sync.WaitGroup
}

func newTortureClient(t *testing.T, r *Runtime, id string) *tortureClient {
	t.Helper()
	w := &tortureClient{}
	dial := func(ctx context.Context) (corewire.Conn, error) {
		a, b := net.Pipe()
		w.servers.Add(1)
		go func() { defer w.servers.Done(); _ = ServeConnection(t.Context(), r, b) }()
		conn := &tortureCarrier{Conn: corewire.NewMux(a, corewire.CarrierLocal, true)}
		w.mu.Lock()
		w.latest = conn
		w.mu.Unlock()
		return conn, nil
	}
	var err error
	w.c, err = coreclient.Dial(t.Context(), dial, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.c.Close(); w.servers.Wait() })
	return w
}

func (w *tortureClient) call(t *testing.T, ws, method string, input coreprotocol.Value, out coreprotocol.Target) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := w.c.Call(ctx, ws, method, input, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func tortureTerminal(t *testing.T, w *tortureClient, root string, opts workspace.TerminalCreateOpts) (string, workspace.TerminalInfo, workspace.TerminalLease) {
	t.Helper()
	var attached coreprotocol.AttachResult
	w.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	var info workspace.TerminalInfo
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalCreate, &opts, &info)
	var lease workspace.TerminalLease
	w.call(t, attached.WorkspaceID, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease)
	return attached.WorkspaceID, info, lease
}

func tortureFrames(t *testing.T, w *tortureClient, ws, id string) <-chan workspace.TerminalProjection {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	frames := make(chan workspace.TerminalProjection, 256)
	sub := w.c.SubscribeTerminal(ctx, ws, id, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			t.Errorf("terminal subscription: %v", err)
			return
		}
		select {
		case frames <- p:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	return frames
}

func tortureWait(t *testing.T, frames <-chan workspace.TerminalProjection, predicate func(workspace.TerminalProjection) bool) workspace.TerminalProjection {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case p := <-frames:
			if predicate(p) {
				return p
			}
		case <-timer.C:
			t.Fatal("terminal never reached expected state")
		}
	}
}

func tortureSubmit(t *testing.T, input *coreclient.TerminalInputStream, item workspace.TerminalInput) {
	t.Helper()
	if err := input.Submit(item); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolTortureHtopNativeExit(t *testing.T) {
	requireProtocolTorture(t)
	htop := tortureBinary(t, "CRUDE_TORTURE_HTOP", "htop")
	config := filepath.Join(t.TempDir(), "htoprc")
	if err := os.WriteFile(config, []byte("enable_mouse=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTOPRC", config)
	r, root := resourceRuntime(t)
	w := newTortureClient(t, r, "torture-htop")
	ws, info, lease := tortureTerminal(t, w, root, workspace.TerminalCreateOpts{Command: htop, Args: []string{"--delay=1"}, Cols: 200, Rows: 50})
	frames := tortureFrames(t, w, ws, info.ID)
	p := tortureWait(t, frames, func(p workspace.TerminalProjection) bool {
		return p.Grid.Modes.MouseTracking && strings.Contains(projectionText(p), "Quit")
	})
	t.Logf("real htop pid=%d tracking=%v alt=%v", info.PID, p.Grid.Modes.MouseTracking, p.Grid.Modes.AltScreen)
	fd, err := unix.PidfdOpen(info.PID, 0)
	if err != nil {
		t.Fatal("native exit observation unavailable", err)
	}
	defer unix.Close(fd)
	input := w.c.OpenTerminalInput(t.Context(), ws, info.ID, func(err error) { t.Errorf("input: %v", err) })
	defer input.Close()
	started := time.Now()
	for i := range 300 {
		tortureSubmit(t, input, workspace.TerminalInput{Lease: lease, Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i % 190, Y: i % 45}})
	}
	key := time.Now()
	tortureSubmit(t, input, workspace.TerminalInput{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: 'q', Text: "q"}})
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(poll, 2000)
	elapsed := time.Since(key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || poll[0].Revents&unix.POLLIN == 0 {
		t.Fatal("htop did not exit after q")
	}
	t.Logf("htop motions=300 q_to_native_exit=%v flood_plus_exit=%v", elapsed, time.Since(started))
	if elapsed >= 100*time.Millisecond {
		t.Errorf("q→native process exit %v, gate <100ms", elapsed)
	}
}

func TestProtocolTortureCarrierCutsThreeViewers(t *testing.T) {
	requireProtocolTorture(t)
	r, root := resourceRuntime(t)
	w := newTortureClient(t, r, "torture-cuts")
	// This fixture checks exactly-once bytes; htop above is the real quit gate.
	ws, info, lease := tortureTerminal(t, w, root, workspace.TerminalCreateOpts{Command: "/bin/sh", Args: []string{"-c", "stty raw -echo; printf READY; cat"}, Cols: 200, Rows: 50})
	frames := tortureFrames(t, w, ws, info.ID)
	tortureWait(t, frames, func(p workspace.TerminalProjection) bool { return strings.Contains(projectionText(p), "READY") })
	peer := newTortureClient(t, r, "torture-fast-peer")
	slow := newTortureClient(t, r, "torture-held-peer")
	for _, client := range []*tortureClient{peer, slow} {
		var attached coreprotocol.AttachResult
		client.call(t, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached)
	}
	peerFrames := tortureFrames(t, peer, ws, info.ID)
	holdCtx, release := context.WithCancel(t.Context())
	blocked := make(chan struct{}, 1)
	held := slow.c.SubscribeTerminal(holdCtx, ws, info.ID, func(_ workspace.TerminalProjection, err error) {
		if err == nil {
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-holdCtx.Done() // deliberately do not release the application ACK
		}
	})
	defer func() { release(); held.Close() }()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("third viewer not held")
	}
	input := w.c.OpenTerminalInput(t.Context(), ws, info.ID, func(err error) { t.Errorf("input: %v", err) })
	defer input.Close()
	want := "READY"
	for cut := range 12 {
		for i := range 300 {
			tortureSubmit(t, input, workspace.TerminalInput{Lease: lease, Kind: "mouse", Mouse: terminalvt.Mouse{Action: terminalvt.MouseMotion, X: i % 190}})
		}
		token := fmt.Sprintf("[%02d]", cut)
		w.mu.Lock()
		conn := w.latest
		w.mu.Unlock()
		started := time.Now()
		tortureSubmit(t, input, workspace.TerminalInput{Lease: lease, Kind: "paste", Data: []byte(token)})
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		want += token
		p := tortureWait(t, frames, func(p workspace.TerminalProjection) bool { return strings.Contains(projectionText(p), want) })
		q := tortureWait(t, peerFrames, func(p workspace.TerminalProjection) bool { return strings.Contains(projectionText(p), want) })
		if strings.Count(projectionText(p), token) != 1 || strings.Count(projectionText(q), token) != 1 {
			t.Fatal("lost or duplicate PTY input")
		}
		if w.c.Owner() != (coreprotocol.Owner{ClientID: lease.Owner.ClientID, Epoch: lease.Owner.Epoch}) {
			t.Fatal("controller owner changed on resume")
		}
		t.Logf("carrier_cut=%d ordered_bytes=%d resumed_and_peer_applied=%v held_ACK=true", cut, len(want)-5, time.Since(started))
	}
}

func tortureFingerprint(p workspace.TerminalProjection) uint64 {
	h := fnv.New64a()
	for _, row := range p.Grid.Cells {
		for _, c := range row {
			_, _ = h.Write([]byte(c.Text))
			_, _ = h.Write([]byte{c.FG.R, c.FG.G, c.FG.B, c.BG.R, c.BG.G, c.BG.B})
		}
	}
	return h.Sum64()
}

func TestProtocolTortureMPVPTYResponse(t *testing.T) {
	requireProtocolTorture(t)
	ffmpeg := tortureBinary(t, "CRUDE_TORTURE_FFMPEG", "/home/lenovo/tools/ffmpeg/ffmpeg")
	mpv := tortureBinary(t, "CRUDE_TORTURE_MPV", "/home/lenovo/tools/mpv/mpv")
	mediaDir, err := os.MkdirTemp(os.Getenv("CRUDE_TEMP_DIR"), "crude-video-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(mediaDir); err != nil {
			t.Error(err)
		}
	})
	video := filepath.Join(mediaDir, "fixture.mkv")
	cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30", "-t", "6", "-c:v", "mpeg4", "-q:v", "3", "-threads", "1", video)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %.2000s", err, output)
	}
	inputConf := filepath.Join(mediaDir, "input.conf")
	if err := os.WriteFile(inputConf, []byte("t cycle mute\nq quit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Only the socket requires a short pathname (Linux sockaddr_un limit).
	socketDir, err := os.MkdirTemp("", "crude-mpv-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(socketDir, "ipc")
	r, root := resourceRuntime(t)
	w := newTortureClient(t, r, "torture-mpv")
	ws, info, lease := tortureTerminal(t, w, root, workspace.TerminalCreateOpts{Command: mpv, Args: []string{"--vo=tct", "--no-audio", "--no-config", "--really-quiet", "--input-terminal=yes", "--input-conf=" + inputConf, "--input-ipc-server=" + socket, "--loop-file=inf", video}, Cols: 200, Rows: 50})
	var observed struct {
		sync.Mutex
		measuring bool
		last      uint64
		stamps    []time.Time
	}
	ready := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	sub := w.c.SubscribeTerminal(ctx, ws, info.ID, func(p workspace.TerminalProjection, err error) {
		if err != nil {
			t.Errorf("mpv subscription: %v", err)
			return
		}
		hash := tortureFingerprint(p)
		observed.Lock()
		if observed.measuring && hash != observed.last {
			observed.stamps = append(observed.stamps, time.Now())
		}
		observed.last = hash
		observed.Unlock()
		if p.Generation > 2 {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		// SDK releases ACK only after this application callback completes.
	})
	t.Cleanup(func() { cancel(); sub.Close() })
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("mpv produced no active grid")
	}
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		t.Fatal("mpv IPC unavailable (required application-response oracle)", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, `{"command":["observe_property",1,"mute"]}`); err != nil {
		t.Fatal(err)
	}
	type muteEvent struct {
		at    time.Time
		value bool
	}
	events := make(chan muteEvent, 256)
	go func() {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var event struct {
				Event string `json:"event"`
				Name  string `json:"name"`
				Data  bool   `json:"data"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Event == "property-change" && event.Name == "mute" {
				events <- muteEvent{time.Now(), event.Data}
			}
		}
	}()
	var state bool
	select {
	case event := <-events:
		state = event.value
	case <-time.After(3 * time.Second):
		t.Fatal("no mpv mute observation")
	}
	input := w.c.OpenTerminalInput(t.Context(), ws, info.ID, func(err error) { t.Errorf("input: %v", err) })
	defer input.Close()
	start := time.Now()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	samples := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}}
	metrics.Read(samples)
	baseline := [3]float64{samples[0].Value.Float64(), samples[1].Value.Float64(), samples[2].Value.Float64()}
	observed.Lock()
	observed.measuring = true
	observed.Unlock()
	var latencies []time.Duration
	for range 100 {
		key := time.Now()
		tortureSubmit(t, input, workspace.TerminalInput{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: 't', Text: "t"}})
		select {
		case event := <-events:
			if event.value == state {
				t.Error("mpv reported no mute transition")
			}
			state = event.value
			latencies = append(latencies, event.at.Sub(key))
		case <-time.After(time.Second):
			t.Fatal("PTY key did not change real mpv property within 1s")
		}
		// Timed workload pacing, not a substitute for the application event above.
		timer := time.NewTimer(max(time.Nanosecond, 40*time.Millisecond-time.Since(key)))
		<-timer.C
	}
	observed.Lock()
	observed.measuring = false
	stamps := append([]time.Time(nil), observed.stamps...)
	observed.Unlock()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	metrics.Read(samples)
	gc := samples[0].Value.Float64() - baseline[0]
	busy := samples[1].Value.Float64() - baseline[1] - (samples[2].Value.Float64() - baseline[2])
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) != 100 {
		t.Errorf("only %d/100 app-side PTY responses", len(latencies))
	}
	if len(latencies) > 0 {
		p99 := latencies[(len(latencies)*99-1)/100]
		t.Logf("mpv PTY-key→IPC-property-event samples=%d p50=%v p99=%v max=%v (includes app reaction and IPC observation; not input admission)", len(latencies), latencies[len(latencies)/2], p99, latencies[len(latencies)-1])
		if p99 >= 20*time.Millisecond {
			t.Errorf("mpv actual app-response p99=%v, gate <20ms", p99)
		}
	}
	t.Logf("mpv distinct_changed_replica_applications=%d duration=%v rate=%.2f/s allocated_bytes=%d allocations=%d GCs=%d combined_core_and_SDK_GC_busy_CPU_percent=%.2f", len(stamps), elapsed, float64(len(stamps))/elapsed.Seconds(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, after.NumGC-before.NumGC, 100*gc/max(busy, 1e-9))
	w.mu.Lock()
	carrier := w.latest
	w.mu.Unlock()
	carrier.mu.Lock()
	for i, s := range carrier.streams {
		t.Logf("carrier_stream=%d class=%d actual_received_bytes=%d", i, s.Class(), s.bytes.Load())
	}
	carrier.mu.Unlock()
	t.Log("UNVERIFIED: 30fps outer presentation; 3ms key→PTY write; shell-only GC<5%; network netem and remote clients. Replica timestamps are not outer presented stamps.")
}

func TestProtocolTorturePlainFrameWireBytes(t *testing.T) {
	requireProtocolTorture(t)
	r, root := resourceRuntime(t)
	w := newTortureClient(t, r, "torture-text-frame")
	ws, info, _ := tortureTerminal(t, w, root, workspace.TerminalCreateOpts{Command: "/bin/sleep", Args: []string{"60"}, Cols: 200, Rows: 50})
	r.mu.Lock()
	service := r.workspaces[ws].ws.Terminals
	r.mu.Unlock()
	var text strings.Builder
	text.WriteString("\x1b[?7l") // Fill the last column without scrolling the fixture.
	for row := range 50 {
		fmt.Fprintf(&text, "\x1b[%d;1H", row+1)
		for col := range 200 {
			text.WriteByte(byte('a' + (row*7+col)%26))
		}
	}
	service.Send(workspace.TerminalOutputMsg{ID: info.ID, Data: []byte(text.String())})
	frames := tortureFrames(t, w, ws, info.ID)
	p := tortureWait(t, frames, func(p workspace.TerminalProjection) bool {
		return len(p.Grid.Cells) == 50 && p.Grid.Cells[0][0].Text == "a"
	})
	if p.Grid.Cols != 200 || p.Grid.GridRows != 50 {
		t.Fatal("wrong frame dimensions")
	}
	w.mu.Lock()
	carrier := w.latest
	w.mu.Unlock()
	carrier.mu.Lock()
	defer carrier.mu.Unlock()
	var largest int64
	for _, s := range carrier.streams {
		if s.terminal.Load() {
			largest = max(largest, s.bytes.Load())
		}
	}
	if largest == 0 {
		t.Fatal("terminal stream was not identified/countable")
	}
	t.Logf("plain_text_200x50 actual_terminal_stream_received_bytes=%d (includes length prefix, header, metadata, rows; excludes carrier framing)", largest)
	if largest >= 16<<10 {
		t.Errorf("plain 200x50 frame=%d bytes, gate <16384", largest)
	}
}

// FuzzTerminalGridAuthorityStream compares each installed SDK frame against the
// sole engine's direct detached snapshot across VT, synchronized output, scroll
// and resize boundaries. Its oracle never goes through the packed frame helper.
func FuzzTerminalGridAuthorityStream(f *testing.F) {
	f.Add([]byte("\x18")) // native fuzz minimized a control-only/no-dirty-row failure
	f.Add([]byte("hello\n\x1b[?2026h\x1b[2Jpartial\x1b[?2026l"))
	f.Add([]byte("\x1b[38;2;12;23;34m界🙂é\r\n\x1b[2;4r\n\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1024 {
			data = data[:1024]
		}
		e, err := terminalvt.New(terminalvt.Options{Cols: 30, Rows: 6})
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		oracle, err := terminalvt.New(terminalvt.Options{Cols: 30, Rows: 6})
		if err != nil {
			t.Fatal(err)
		}
		defer oracle.Close()
		var view terminalViewState
		var base terminalViewSnapshot
		var replica coreclient.TerminalGrid
		apply := func() {
			t.Helper()
			direct, err := oracle.Rows(terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			grid, err := e.StreamRows(terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			reset := base.seq == 0 || grid.Cols != base.projection.Grid.Cols || grid.GridRows != base.projection.Grid.GridRows || grid.HistoryEpoch != base.projection.Grid.HistoryEpoch
			frame, next := view.build(base, workspace.TerminalProjection{Grid: grid}, reset)
			got, err := replica.Apply(coreprotocol.TopicHeader{Seq: next.seq, Reset: reset}, frame)
			if err != nil {
				for _, row := range frame.Rows {
					t.Logf("changed row=%d packed_prefix=%x first_cell=%+v", row.Index, row.Data[:min(32, len(row.Data))], grid.Cells[row.Index][0])
				}
				t.Fatalf("Apply: %v seq=%d base=%d reset=%v grid=%dx%d rows=%d styleBase=%d styles=%d", err, next.seq, frame.Base, reset, grid.Cols, grid.GridRows, len(frame.Rows), frame.StyleBase, len(frame.Styles))
			}
			if !reflect.DeepEqual(got.Grid.Cells, direct.Cells) || !reflect.DeepEqual(got.Grid.Wrapped, direct.Wrapped) || got.Grid.Cursor != direct.Cursor || got.Grid.Modes != direct.Modes {
				t.Fatal("replica diverged from direct authoritative snapshot")
			}
			view.current, base = next, next
		}
		apply()
		for start := 0; start < len(data); {
			count := min(1+int(data[start]%31), len(data)-start)
			e.Feed(data[start : start+count])
			oracle.Feed(data[start : start+count])
			if data[start]%17 == 0 {
				if err := e.Resize(8+int(data[start]%50), 2+int(data[start]%9)); err != nil {
					t.Fatal(err)
				}
				if err := oracle.Resize(8+int(data[start]%50), 2+int(data[start]%9)); err != nil {
					t.Fatal(err)
				}
			}
			apply()
			start += count
		}
	})
}
