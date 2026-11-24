package lspclient

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"

	"crude/internal/metrics"
	"crude/types/insight"
)

// pullRecorder collects the publications a pull fixture produces.
type pullRecorder struct {
	mu       sync.Mutex
	open     map[string][]insight.DiagnosticPublication
	unopened map[string][]insight.DiagnosticPublication
}

func recordPulls(l *LSPIntegration) *pullRecorder {
	r := &pullRecorder{open: map[string][]insight.DiagnosticPublication{}, unopened: map[string][]insight.DiagnosticPublication{}}
	l.OnDiagnostics(func(p insight.DiagnosticPublication) {
		r.mu.Lock()
		r.open[p.Boundary.Source.Path] = append(r.open[p.Boundary.Source.Path], p)
		r.mu.Unlock()
	})
	l.OnUnopenedDiagnostics(func(path string, p insight.DiagnosticPublication) {
		r.mu.Lock()
		r.unopened[path] = append(r.unopened[path], p)
		r.mu.Unlock()
	})
	return r
}

// waitMessage waits until the latest publication for path carries want.
func (r *pullRecorder) waitMessage(t *testing.T, path, want string) insight.DiagnosticPublication {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		list := r.open[path]
		r.mu.Unlock()
		if len(list) > 0 {
			latest := list[len(list)-1]
			for _, d := range latest.Diagnostics {
				if d.Message == want {
					return latest
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no publication with %q for %s; got %+v", want, filepath.Base(path), r.open[path])
	return insight.DiagnosticPublication{}
}

// protocolTrace returns what the fixture server received, in order.
func protocolTrace(t *testing.T, l *LSPIntegration, ctx context.Context, path string) []map[string]any {
	t.Helper()
	value, err := l.RequestHover(ctx, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var trace []map[string]any
	if err := json.Unmarshal([]byte(value), &trace); err != nil {
		t.Fatal(err)
	}
	return trace
}

func pullRequests(trace []map[string]any, path string) []map[string]any {
	var out []map[string]any
	uri := string(protocol.URIFromPath(path))
	for _, msg := range trace {
		if msg["method"] != "textDocument/diagnostic" {
			continue
		}
		params := msg["params"].(map[string]any)
		if params["textDocument"].(map[string]any)["uri"] == uri {
			out = append(out, params)
		}
	}
	return out
}

func startPullFixture(t *testing.T, mode string) (*LSPIntegration, *pullRecorder, context.Context, string) {
	t.Helper()
	root := t.TempDir()
	l := protocolIntegration(t, mode, root)
	recorder := recordPulls(l)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return l, recorder, ctx, root
}

// A server that offers document pull and stops pushing still reaches
// Problems: Crude pulls on open and on every change, an unchanged answer
// repeats the kept report, the original items reach code actions, and the
// other open documents are pulled again after a change because the server
// declares inter-file dependencies. Nothing is pulled before didOpen.
func TestPullDiagnosticsFollowDocumentVersions(t *testing.T) {
	l, recorder, ctx, root := startPullFixture(t, "pull-static")
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	openTestFile(t, l, ctx, a, "go", "package a")
	recorder.waitMessage(t, a, "pulled v1 fixture a.go")
	openTestFile(t, l, ctx, b, "go", "package b")
	recorder.waitMessage(t, b, "pulled v2 fixture b.go")
	if publication, settled := l.AwaitDiagnostics(ctx, a); !settled || len(publication.Diagnostics) != 1 {
		t.Fatalf("a pulled document did not settle: %+v %v", publication, settled)
	}

	boundary := testBoundary(a)
	boundary.Source.Revision++
	if _, err := l.UpdateFile(ctx, a, "package a // edited", boundary); err != nil {
		t.Fatal(err)
	}
	latest := recorder.waitMessage(t, a, "pulled v3 fixture a.go")
	if latest.ReportedVersion == nil || *latest.ReportedVersion != 3 || latest.WireVersion != 3 {
		t.Fatalf("pulled report is not bound to its version: %+v", latest)
	}
	l.mu.Lock()
	adjunct := l.openFiles[a].actionDiagnostics
	l.mu.Unlock()
	if adjunct.detail != "" || adjunct.count != 1 || !strings.Contains(string(adjunct.diagnostics[0].raw), `"data":{"fix":1}`) {
		t.Fatalf("code actions lost the pulled diagnostic: %+v", adjunct)
	}

	// Inter-file dependencies: b is pulled again after a's change.
	deadline := time.Now().Add(5 * time.Second)
	for len(pullRequests(protocolTrace(t, l, ctx, a), b)) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the other open document was not pulled after a change")
		}
		time.Sleep(50 * time.Millisecond)
	}

	l.NotifySave(ctx, a, "package a // edited", 1, false)
	deadline = time.Now().Add(5 * time.Second)
	for {
		requests := pullRequests(protocolTrace(t, l, ctx, a), a)
		if last := requests[len(requests)-1]; len(requests) >= 3 && last["previousResultId"] == "r3" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("save did not pull with the kept result: %+v", requests)
		}
		time.Sleep(50 * time.Millisecond)
	}
	recorder.waitMessage(t, a, "pulled v3 fixture a.go")

	trace := protocolTrace(t, l, ctx, a)
	opened := map[string]bool{}
	for _, msg := range trace {
		switch msg["method"] {
		case "textDocument/didOpen":
			opened[msg["params"].(map[string]any)["textDocument"].(map[string]any)["uri"].(string)] = true
		case "textDocument/diagnostic":
			if uri := msg["params"].(map[string]any)["textDocument"].(map[string]any)["uri"].(string); !opened[uri] {
				t.Fatalf("pulled %s before the server opened it", uri)
			}
		}
	}
}

// Pyright registers its provider dynamically. The registration starts pulls
// for matching open documents only, and other registrations stay refused.
func TestPullDiagnosticsDynamicRegistration(t *testing.T) {
	l, recorder, ctx, root := startPullFixture(t, "pull-dynamic")
	goFile, text := filepath.Join(root, "main.go"), filepath.Join(root, "notes.txt")
	openTestFile(t, l, ctx, goFile, "go", "package main")
	openTestFile(t, l, ctx, text, "plaintext", "notes")
	recorder.waitMessage(t, goFile, "pulled v1 dyn main.go")
	trace := protocolTrace(t, l, ctx, goFile)
	if len(pullRequests(trace, text)) != 0 {
		t.Fatal("a document outside the provider's selector was pulled")
	}
	answers := map[string]map[string]any{}
	for _, msg := range trace {
		if id, ok := msg["id"].(string); ok && strings.HasPrefix(id, "register-") && msg["method"] == nil {
			answers[id] = msg
		}
	}
	if _, failed := answers["register-1"]["error"]; failed || answers["register-1"] == nil {
		t.Fatalf("diagnostic registration was refused: %+v", answers)
	}
	if _, failed := answers["register-2"]["error"]; !failed {
		t.Fatalf("an unsupported registration was accepted: %+v", answers)
	}
}

// ServerCancelled with retriggerRequest pulls again, and
// workspace/diagnostic/refresh (a request without params) pulls every open
// document again.
func TestPullDiagnosticsRetriggerAndRefresh(t *testing.T) {
	l, recorder, ctx, root := startPullFixture(t, "pull-refresh")
	path := filepath.Join(root, "a.go")
	openTestFile(t, l, ctx, path, "go", "package a")
	recorder.waitMessage(t, path, "pulled v1 fixture a.go")
	deadline := time.Now().Add(5 * time.Second)
	for len(pullRequests(protocolTrace(t, l, ctx, path), path)) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("retrigger and refresh did not pull again: %+v", pullRequests(protocolTrace(t, l, ctx, path), path))
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, msg := range protocolTrace(t, l, ctx, path) {
		if msg["id"] == "refresh-1" && msg["method"] == nil {
			if _, failed := msg["error"]; failed {
				t.Fatalf("refresh request failed: %+v", msg)
			}
		}
	}
}

// A server that offers pull but never answers keeps its pushed results, the
// pushed document settles, and after repeated silence the provider is no
// longer asked.
func TestPullWithoutAnswerKeepsPushedDiagnostics(t *testing.T) {
	previous := pullTimeout
	pullTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pullTimeout = previous })
	l, recorder, ctx, root := startPullFixture(t, "pull-silent")
	for index, name := range []string{"a.s", "b.s", "c.s", "d.s"} {
		path := filepath.Join(root, name)
		openTestFile(t, l, ctx, path, "asm", "mov")
		recorder.waitMessage(t, path, "pushed")
		if publication, settled := l.AwaitDiagnostics(ctx, path); !settled || len(publication.Diagnostics) != 1 {
			t.Fatalf("pushed document %d did not settle: %+v", index, publication)
		}
		time.Sleep(3 * pullTimeout / 2)
	}
	requests := 0
	for _, msg := range protocolTrace(t, l, ctx, filepath.Join(root, "a.s")) {
		if msg["method"] == "textDocument/diagnostic" {
			requests++
		}
	}
	if requests != pullTimeoutLimit {
		t.Fatalf("pulls to a silent provider = %d, want %d", requests, pullTimeoutLimit)
	}
	for _, name := range []string{"a.s", "d.s"} {
		recorder.waitMessage(t, filepath.Join(root, name), "pushed")
	}
}

// Workspace diagnostics report files that are not open through the unopened
// path, including streamed partial results; a report about an open document
// gives way to its own pull; the next request sends the kept result IDs.
func TestWorkspaceDiagnosticsReportClosedFiles(t *testing.T) {
	l, recorder, ctx, root := startPullFixture(t, "pull-workspace")
	open := filepath.Join(root, "open.go")
	openTestFile(t, l, ctx, open, "go", "package open")
	recorder.waitMessage(t, open, "pulled v1 ws open.go")
	if !l.WorkspaceDiagnostics() {
		t.Fatal("workspace diagnostics capability not seen")
	}
	if err := l.PullWorkspaceDiagnostics(ctx); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	closedA, closedB, openReports := recorder.unopened[filepath.Join(root, "closed-a.go")], recorder.unopened[filepath.Join(root, "closed-b.go")], recorder.unopened[open]
	recorder.mu.Unlock()
	if len(closedA) != 1 || closedA[0].Diagnostics[0].Message != "closed a" || len(closedB) != 1 || closedB[0].Diagnostics[0].Message != "closed b" {
		t.Fatalf("closed files were not reported: a=%+v b=%+v", closedA, closedB)
	}
	if len(openReports) != 0 {
		t.Fatalf("a workspace report replaced an open document's pull: %+v", openReports)
	}
	recorder.waitMessage(t, open, "pulled v1 ws open.go")
	if err := l.PullWorkspaceDiagnostics(ctx); err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	for _, msg := range protocolTrace(t, l, ctx, open) {
		if msg["method"] == "workspace/diagnostic" {
			second = msg["params"].(map[string]any)
		}
	}
	previous, _ := json.Marshal(second["previousResultIds"])
	if strings.Contains(string(previous), `"value":"open"`) {
		t.Fatal("an ignored workspace report became a previous result", string(previous))
	}
	if !strings.Contains(string(previous), `"value":"a1"`) || !strings.Contains(string(previous), `"value":"b1"`) {
		t.Fatalf("kept result IDs not sent: %s", previous)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.unopened[filepath.Join(root, "closed-a.go")]) != 1 {
		t.Fatal("an unchanged workspace report published again")
	}
}

// Overlapping registrations describe the same requests on the wire. Adding
// one and removing another must not cancel the pull or clear its diagnostics.
func TestOverlappingRegistrationsKeepOneDiagnosticProducer(t *testing.T) {
	totals := func() (requests, cancelled uint64) {
		for _, c := range metrics.Take(metrics.Query{}).Counters {
			switch c.Meta.Name {
			case "lsp.document_pulls":
				requests = c.Total
			case "lsp.document_pulls_cancelled":
				cancelled = c.Total
			}
		}
		return
	}
	before, stopped := totals()
	l, rec, ctx, root := startPullFixture(t, "pull-overlap")
	path := filepath.Join(root, "selected.fixture")
	openTestFile(t, l, ctx, path, "fixture", "ab")
	register := func(id string) {
		t.Helper()
		if err := l.registerCapabilities(l.launch, json.RawMessage(`{"registrations":[{"id":"`+id+`","method":"textDocument/diagnostic","registerOptions":{"identifier":"shared","documentSelector":[{"language":"fixture"}]}}]}`)); err != nil {
			t.Fatal(err)
		}
	}
	register("first")
	rec.waitMessage(t, path, "pulled v1 shared selected.fixture")
	register("second")
	if err := l.unregisterCapabilities(l.launch, json.RawMessage(`{"unregisterations":[{"id":"first","method":"textDocument/diagnostic"}]}`)); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	report := l.openFiles[path].publication
	l.mu.Unlock()
	if len(report.Diagnostics) != 1 {
		t.Fatal("removing an overlapping selector erased the reported problem")
	}
	newer := testBoundary(path)
	newer.Source.Revision++
	if _, err := l.UpdateFile(ctx, path, "abc", newer); err != nil {
		t.Fatal(err)
	}
	rec.waitMessage(t, path, "pulled v2 shared selected.fixture")
	trace := protocolTrace(t, l, ctx, path)
	pulls := pullRequests(trace, path)
	if len(pulls) != 2 {
		t.Fatalf("got %d diagnostic pulls for two versions; want one per version", len(pulls))
	}
	for _, msg := range trace {
		if msg["method"] == "$/cancelRequest" {
			t.Fatal("registration churn cancelled a current request")
		}
	}
	if after, cancelled := totals(); after-before != 2 || cancelled != stopped {
		t.Fatalf("performance report counts %d requests and %d cancellations for two completed pulls", after-before, cancelled-stopped)
	}
}
