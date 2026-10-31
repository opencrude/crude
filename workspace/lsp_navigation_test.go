package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"crude/document"
	"crude/types"
)

// This fake uses real didOpen/didChange synchronization. It deliberately lies
// about adapter provenance and ignores RPC cancellation: neither is authority.
type navigationClient struct {
	lifecycleClient
	navMu       sync.Mutex
	raw         string
	unsupported bool
	err         error
	wireDelta   int
	wrongKind   bool
	requests    []navigationCall
	entered     chan struct{}
	release     <-chan struct{}
}

type navigationCall struct {
	path, kind         string
	line, col          int
	includeDeclaration bool
}

func (c *navigationClient) RequestNavigation(_ context.Context, path, kind string, line, col int, include bool) (types.LSPNavigationResponse, error) {
	c.mu.Lock()
	wire := c.versions[path]
	c.mu.Unlock()
	c.navMu.Lock()
	c.requests = append(c.requests, navigationCall{path, kind, line, col, include})
	response := types.LSPNavigationResponse{
		Boundary: types.InsightBoundary{Source: types.InsightSource{Path: "forged-provider-path"}},
		Kind:     kind, WireVersion: wire + c.wireDelta, JSON: []byte(c.raw), Unsupported: c.unsupported,
	}
	if c.wrongKind {
		response.Kind = "wrong-kind"
	}
	err, entered, release := c.err, c.entered, c.release
	c.navMu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return response, err
}

type navigationRuntime struct{ client *navigationClient }

func (r navigationRuntime) NewClient(_ types.LSPServerSpec, _ string, admission func() error) LSPClient {
	r.client.startAdmission = admission
	return r.client
}

type navigationFixture struct {
	w      *Workspace
	client *navigationClient
	owner  document.Owner
	source types.InsightSource
	path   string
	disk   string
}

func newNavigationFixture(t *testing.T, text string) *navigationFixture {
	t.Helper()
	root := t.TempDir()
	c := &navigationClient{lifecycleClient: lifecycleClient{
		runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})},
	}, raw: "[]"}
	m := NewLSPManager(nil, navigationRuntime{c})
	trustAnalysis(t, m, root)
	t.Cleanup(m.Close)
	db, err := openWorkspaceDBAt(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	docs := NewDocumentService(root, nil, m, db.DB)
	t.Cleanup(docs.Close)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "navigation-owner", Epoch: 1}
	snap, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, m, &c.lifecycleClient, snap)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snap.ID])
	docs.mu.Unlock()
	// No save authority and no Local History: read-only navigation needs neither.
	return &navigationFixture{&Workspace{LiveDocuments: docs, LSPMgr: m}, c, owner, source, path, text}
}

func navigationTestURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func navigationLocation(uri string) string {
	return fmt.Sprintf(`{"uri":%q,"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}}}`, uri)
}

func navigationTestRange(line, start, end int) types.LSPNavigationRange {
	return types.LSPNavigationRange{Start: types.LSPNavigationPosition{Line: line, Character: start},
		End: types.LSPNavigationPosition{Line: line, Character: end}}
}

func (f *navigationFixture) request(t *testing.T, raw, kind string, include bool) types.LSPNavigation {
	t.Helper()
	f.client.navMu.Lock()
	f.client.raw = raw
	f.client.navMu.Unlock()
	result, err := f.w.RequestLSPNavigation(t.Context(), f.owner, f.source, kind, 0, 3, include)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *navigationFixture) inspect(ctx context.Context, result types.LSPNavigation, target types.LSPNavigationTarget) (types.LSPNavigationLanding, error) {
	return f.w.InspectLSPNavigationTarget(ctx, f.owner, result.Boundary, result.WireVersion, result.Kind, target)
}

func (f *navigationFixture) unchanged(t *testing.T, before document.Snapshot) {
	t.Helper()
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != f.disk {
		t.Fatalf("navigation changed disk: %q %v", disk, err)
	}
	after, err := f.w.LiveDocuments.Snapshot(before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("navigation changed canonical text/savepoint/revision: before=%+v after=%+v err=%v", before, after, err)
	}
	for _, call := range f.client.snapshot() {
		if strings.HasPrefix(call, "willSave:") || strings.HasPrefix(call, "didSave:") {
			t.Fatalf("navigation issued a save notification: %q", call)
		}
	}
}

func TestNavigationKindsNullEmptyUnsupportedRetainCanonicalBoundary(t *testing.T) {
	f := newNavigationFixture(t, "a😀e\u0301\r\nz\r\n")
	before, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"definition", "type-definition", "implementation", "references"} {
		for _, answer := range []string{"null", "empty", "unsupported", "targets"} {
			t.Run(kind+"/"+answer, func(t *testing.T) {
				raw := map[string]string{"null": "null", "empty": "[]", "unsupported": "",
					"targets": "[" + navigationLocation(navigationTestURI(f.path)) + "]"}[answer]
				f.client.navMu.Lock()
				f.client.unsupported = answer == "unsupported"
				f.client.navMu.Unlock()
				result := f.request(t, raw, kind, kind == "references")
				if result.Answer != answer || result.Kind != kind || result.Line != 0 || result.Col != 3 ||
					result.IncludeDeclaration != (kind == "references") || result.WireVersion != 1 ||
					result.Boundary.Source != f.source || result.Boundary.Producer.InstanceID == "" ||
					result.Boundary.Context.Generation == 0 || len(result.Boundary.Context.UnknownInputs) == 0 {
					t.Fatalf("answer lost its exact query and canonical qualification: %+v", result)
				}
				wantTargets := 0
				if answer == "targets" {
					wantTargets = 1
				}
				if len(result.Targets) != wantTargets {
					t.Fatalf("answer %s has %d targets", answer, len(result.Targets))
				}
				f.client.navMu.Lock()
				call := f.client.requests[len(f.client.requests)-1]
				f.client.navMu.Unlock()
				if call != (navigationCall{f.path, kind, 0, 3, kind == "references"}) {
					t.Fatal("wire request changed literal UTF-16 coordinates or references flag", call)
				}
			})
		}
	}
	f.unchanged(t, before)
}

func TestNavigationDefinitionFamilyAndReferencesHaveSeparateSettings(t *testing.T) {
	f := newNavigationFixture(t, "a😀z\n")
	for _, enabled := range []string{"definition", "references"} {
		settings := f.w.LSPMgr.Settings()
		settings.Definition, settings.References = enabled == "definition", enabled == "references"
		f.w.LSPMgr.SetSettings(settings)
		for _, kind := range []string{"definition", "type-definition", "implementation", "references"} {
			want := (kind == "references") == (enabled == "references")
			f.client.navMu.Lock()
			calls := len(f.client.requests)
			f.client.navMu.Unlock()
			result, err := f.w.RequestLSPNavigation(t.Context(), f.owner, f.source, kind, 0, 3, false)
			if want && (err != nil || result.Answer != "empty") ||
				!want && (err == nil || !reflect.DeepEqual(result, types.LSPNavigation{})) {
				t.Fatalf("settings %s, kind %s: %+v %v", enabled, kind, result, err)
			}
			f.client.navMu.Lock()
			gotCalls := len(f.client.requests) - calls
			f.client.navMu.Unlock()
			if gotCalls != map[bool]int{false: 0, true: 1}[want] {
				t.Fatal("disabled request reached provider", enabled, kind, gotCalls)
			}
		}
	}
}

func TestNavigationStrictSourcePositionAndAdmissionReturnNoPayload(t *testing.T) {
	f := newNavigationFixture(t, "a😀e\u0301\r\nz\r\n")
	for _, tc := range []struct {
		name string
		line int
		col  int
	}{
		{"negative-line", -1, 0}, {"negative-col", 0, -1}, {"split-surrogate", 0, 2},
		{"past-line", 0, 6}, {"past-document", 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := f.w.RequestLSPNavigation(t.Context(), f.owner, f.source, "definition", tc.line, tc.col, false)
			if err == nil || !reflect.DeepEqual(out, types.LSPNavigation{}) {
				t.Fatal("invalid source coordinate returned evidence", out, err)
			}
		})
	}
	for _, invalid := range []string{"owner", "revision", "lifetime", "noncanonical", "kind", "include-declaration", "cancel", "unsynchronized"} {
		t.Run(invalid, func(t *testing.T) {
			owner, source, kind, include := f.owner, f.source, "definition", false
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch invalid {
			case "owner":
				owner.ClientID = "foreign"
			case "revision":
				source.Revision++
			case "lifetime":
				source.Lifetime += "-foreign"
			case "noncanonical":
				source.Kind = "disk"
			case "kind":
				kind = "unknown"
			case "include-declaration":
				include = true
			case "cancel":
				cancel()
			case "unsynchronized":
				f.w.LSPMgr.mu.Lock()
				f.w.LSPMgr.documents[f.path].syncPending = true
				f.w.LSPMgr.mu.Unlock()
			}
			result, err := f.w.RequestLSPNavigation(ctx, owner, source, kind, 0, 3, include)
			if err == nil || !reflect.DeepEqual(result, types.LSPNavigation{}) {
				t.Fatal("inadmissible source returned evidence", result, err)
			}
		})
	}
	f.client.navMu.Lock()
	defer f.client.navMu.Unlock()
	if len(f.client.requests) != 0 {
		t.Fatal("inadmissible query reached provider")
	}
}

func TestNavigationProviderFailuresHaveNoPartialResults(t *testing.T) {
	for _, failure := range []string{"rpc", "wrong-wire", "wrong-kind", "malformed", "partial", "unsupported-data", "origin-surrogate", "origin-outside"} {
		t.Run(failure, func(t *testing.T) {
			f := newNavigationFixture(t, "a😀z\n")
			f.client.raw = "[" + navigationLocation(navigationTestURI(f.path)) + "]"
			switch failure {
			case "rpc":
				f.client.err = errors.New("provider failed")
			case "wrong-wire":
				f.client.wireDelta = 1
			case "wrong-kind":
				f.client.wrongKind = true
			case "malformed":
				f.client.raw = `{"uri":`
			case "partial":
				f.client.raw = "[" + navigationLocation(navigationTestURI(f.path)) + `,{"uri":"file:///bad"}]`
			case "unsupported-data":
				f.client.unsupported = true
			case "origin-surrogate", "origin-outside":
				col := 2
				if failure == "origin-outside" {
					col = 99
				}
				f.client.raw = fmt.Sprintf(`[{"targetUri":%q,"targetRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}},"targetSelectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"originSelectionRange":{"start":{"line":0,"character":%d},"end":{"line":0,"character":%d}}}]`, navigationTestURI(f.path), col, col)
			}
			result, err := f.w.RequestLSPNavigation(t.Context(), f.owner, f.source, "definition", 0, 3, false)
			if err == nil || !reflect.DeepEqual(result, types.LSPNavigation{}) {
				t.Fatal("provider failure leaked qualified/partial evidence", result, err)
			}
		})
	}
}

func TestNavigationLocalURIPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space # target.go")
	valid := navigationTestURI(path)
	for _, raw := range []string{valid, strings.Replace(valid, "file:///", "file://localhost/", 1)} {
		got, err := navigationURIPath(raw)
		if err != nil || got != path {
			t.Fatalf("local escaped path %q = %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"", "relative.go", "file:relative.go", "file://remote/tmp/target.go",
		"https://example.invalid/target.go", "file://user@localhost/tmp/target.go",
		"file:///tmp/%zz", valid + "?version=1", valid + "?", valid + "#position", valid + "#",
		"file:////server/share/target.go", "file:///tmp/%5Cserver%5Cshare", "file:///tmp/%00.go",
		"file:///tmp/%0A.go", "file:///tmp/\t.go", "file:///tmp/" + string([]byte{0xff}),
	} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			if got, err := navigationURIPath(raw); err == nil || got != "" {
				t.Fatalf("untrusted URI became a local path: %q => %q %v", raw, got, err)
			}
		})
	}
}

func TestNavigationListNeverReadsTargetsAndSelectionEnforcesURI(t *testing.T) {
	f := newNavigationFixture(t, "a😀z\n")
	external := t.TempDir()
	missing := filepath.Join(external, "missing.go")
	invalid := filepath.Join(external, "invalid.go")
	if err := os.WriteFile(invalid, []byte{0xff, 0xfe}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, invalid, external} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			result := f.request(t, "["+navigationLocation(navigationTestURI(path))+"]", "definition", false)
			if len(result.Targets) != 1 || result.Targets[0].Path != path || !result.Targets[0].External ||
				result.Targets[0].UnavailableReason != "" {
				t.Fatal("listing inspected file content instead of preserving an explicit selected-file candidate", result)
			}
			landing, err := f.inspect(t.Context(), result, result.Targets[0])
			if err == nil || !reflect.DeepEqual(landing, types.LSPNavigationLanding{}) {
				t.Fatal("missing/invalid/special selected target returned payload", landing, err)
			}
		})
	}
	for _, uri := range []string{"https://example.invalid/file", "file://remote/tmp/file", "file:///tmp/%zz", navigationTestURI(f.path) + "?v=1"} {
		result := f.request(t, "["+navigationLocation(uri)+"]", "definition", false)
		if len(result.Targets) != 1 || result.Targets[0].Path != "" || result.Targets[0].UnavailableReason == "" {
			t.Fatal("unsupported URI row was hidden or granted path authority", result)
		}
		target := result.Targets[0]
		target.Path, target.External, target.UnavailableReason = f.path, false, ""
		if landing, err := f.inspect(t.Context(), result, target); err == nil || !reflect.DeepEqual(landing, types.LSPNavigationLanding{}) {
			t.Fatal("caller presentation fields overrode URI policy", landing, err)
		}
	}
}

func TestNavigationUnsavedCanonicalSourceAndSelectedRetainedTarget(t *testing.T) {
	f := newNavigationFixture(t, "a😀disk\n")
	docs := f.w.LiveDocuments
	receipt, err := docs.Apply(document.Change{DocumentID: f.source.DocumentID, Owner: f.owner,
		RequestID: "unsaved-source", BaseRevision: f.source.Revision,
		Edits: []document.Edit{{Start: 5, End: 9, Text: "source"}}})
	if err != nil {
		t.Fatal(err)
	}
	sourceSnap, err := docs.Snapshot(f.source.DocumentID)
	if err != nil || string(sourceSnap.Text) != "a😀source\n" || !sourceSnap.Dirty || receipt.Revision != sourceSnap.Revision {
		t.Fatalf("unsaved source fixture failed: %+v %v", sourceSnap, err)
	}
	waitDiagnosticSend(t, f.w.LSPMgr, &f.client.lifecycleClient, sourceSnap)
	docs.mu.Lock()
	f.source = docs.analysisIdentityLocked(docs.docs[sourceSnap.ID])
	docs.mu.Unlock()
	targetPath := filepath.Join(filepath.Dir(f.path), "target.go")
	if err := os.WriteFile(targetPath, []byte("a😀disk\n"), 0600); err != nil {
		t.Fatal(err)
	}
	targetOwner := document.Owner{ClientID: "target-editor", Epoch: 1}
	targetSnap, err := docs.Open(targetOwner, targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Apply(document.Change{DocumentID: targetSnap.ID, Owner: targetOwner,
		RequestID: "unsaved-target", BaseRevision: targetSnap.Revision,
		Edits: []document.Edit{{Start: 5, End: 9, Text: "retained"}}}); err != nil {
		t.Fatal(err)
	}
	if err := docs.CloseView(targetOwner, targetSnap.ID); err != nil {
		t.Fatal(err)
	}
	targetSnap, err = docs.Snapshot(targetSnap.ID)
	if err != nil || string(targetSnap.Text) != "a😀retained\n" {
		t.Fatal("target fixture failed", targetSnap, err)
	}
	raw := fmt.Sprintf(`[{"targetUri":%q,"targetRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":11}},"targetSelectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"originSelectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}}}]`, navigationTestURI(targetPath))
	result := f.request(t, raw, "type-definition", false)
	if len(result.Targets) != 1 {
		t.Fatal(result)
	}
	row := result.Targets[0]
	if !row.Link || row.Range != navigationTestRange(0, 0, 11) || row.Selection != navigationTestRange(0, 1, 3) ||
		row.Origin == nil || *row.Origin != navigationTestRange(0, 1, 3) {
		t.Fatal("LocationLink lost source/target range metadata", row)
	}
	// Even with no view, current retained work wins over the shorter disk text.
	// No target revision was supplied by the server: this is a current structural
	// check, not an assertion that its producer analyzed this target version.
	row.Path, row.External, row.UnavailableReason = "forged", true, "forged"
	landing, err := f.inspect(t.Context(), result, row)
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte("a😀retained\n")))
	if err != nil || landing.TextHash != wantHash || landing.Preview != "a😀retained" ||
		landing.Source.Kind != "canonical" || landing.Source.DocumentID != targetSnap.ID ||
		landing.Source.Revision != targetSnap.Revision || landing.Target.Path != targetPath ||
		landing.Target.External || landing.Target.UnavailableReason != "" {
		t.Fatalf("selected target did not observe current retained text: %+v %v", landing, err)
	}
	docs.mu.Lock()
	owners, journal, history := len(docs.docs[targetSnap.ID].owners), len(docs.docs[targetSnap.ID].journal), docs.docs[targetSnap.ID].historyBytes
	docs.mu.Unlock()
	if owners != 0 || journal != 1 || history == 0 {
		t.Fatal("inspection acquired a view or changed retained history", owners, journal, history)
	}
	f.unchanged(t, sourceSnap)
	afterTarget, err := docs.Snapshot(targetSnap.ID)
	targetDisk, diskErr := os.ReadFile(targetPath)
	if err != nil || !reflect.DeepEqual(targetSnap, afterTarget) || diskErr != nil || string(targetDisk) != "a😀disk\n" {
		t.Fatal("inspection changed target source/savepoint", afterTarget, err, string(targetDisk), diskErr)
	}
	// The source's existing Undo must still restore exactly its original bytes.
	if _, err := docs.Undo(f.owner, sourceSnap.ID, "source-undo", sourceSnap.Revision); err != nil {
		t.Fatal(err)
	}
	restored, err := docs.Snapshot(sourceSnap.ID)
	if err != nil || string(restored.Text) != "a😀disk\n" || restored.Dirty {
		t.Fatal("navigation edited source history", restored, err)
	}
}

func TestNavigationSelectedDiskTargetRangesAndBoundedPreview(t *testing.T) {
	f := newNavigationFixture(t, "a😀z\n")
	path := filepath.Join(t.TempDir(), "target.go")
	text := "a😀e\u0301\r\nsecond\r\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	result := f.request(t, "["+navigationLocation(navigationTestURI(path))+"]", "implementation", false)
	target := result.Targets[0]
	landing, err := f.inspect(t.Context(), result, target)
	if err != nil || landing.Source.Kind != "disk" || landing.TextHash != fmt.Sprintf("%x", sha256.Sum256([]byte(text))) ||
		landing.Preview != "a😀e\u0301\r" || !landing.Target.External {
		t.Fatal("disk landing lost exact bytes/hash/UTF-16 selection", landing, err)
	}
	for _, invalid := range []string{"range-surrogate", "selection-surrogate", "range-line", "selection-col", "range-reversed", "selection-reversed", "containment", "location-selection", "origin"} {
		t.Run(invalid, func(t *testing.T) {
			row := target
			row.Link = true
			row.Range, row.Selection = navigationTestRange(0, 0, 5), navigationTestRange(0, 1, 3)
			switch invalid {
			case "range-surrogate":
				row.Range.Start.Character = 2
			case "selection-surrogate":
				row.Selection.Start.Character = 2
			case "range-line":
				row.Range.End.Line = 99
			case "selection-col":
				row.Selection.End.Character = 99
			case "range-reversed":
				row.Range = navigationTestRange(0, 5, 0)
			case "selection-reversed":
				row.Selection = navigationTestRange(0, 3, 1)
			case "containment":
				row.Range = navigationTestRange(0, 3, 5)
			case "location-selection":
				row.Link = false
			case "origin":
				origin := navigationTestRange(0, 2, 3)
				row.Origin = &origin
			}
			if got, err := f.inspect(t.Context(), result, row); err == nil || !reflect.DeepEqual(got, types.LSPNavigationLanding{}) {
				t.Fatal("invalid selected range returned payload", got, err)
			}
		})
	}
	longPath := filepath.Join(t.TempDir(), "long.go")
	longText := strings.Repeat("😀", 600) + "\n"
	if err := os.WriteFile(longPath, []byte(longText), 0600); err != nil {
		t.Fatal(err)
	}
	target.URI = navigationTestURI(longPath)
	target.Range, target.Selection = navigationTestRange(0, 0, 2), navigationTestRange(0, 0, 2)
	long, err := f.inspect(t.Context(), result, target)
	if err != nil || len(long.Preview) > 1024 || !utf8.ValidString(long.Preview) ||
		long.TextHash != fmt.Sprintf("%x", sha256.Sum256([]byte(longText))) {
		t.Fatal("preview was unbounded or hash covered only preview bytes", long, err)
	}
}

func TestNavigationSelectedUnsafeFilesReturnNoPayload(t *testing.T) {
	f := newNavigationFixture(t, "a😀z\n")
	result := f.request(t, "[]", "definition", false)
	for _, unsafe := range []string{"missing", "directory", "invalid-utf8", "oversized", "symlink", "unreadable"} {
		t.Run(unsafe, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "target.go")
			switch unsafe {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "invalid-utf8":
				if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				truncateErr := file.Truncate(17 << 20)
				closeErr := file.Close()
				if truncateErr != nil || closeErr != nil {
					t.Fatal(truncateErr, closeErr)
				}
			case "symlink":
				if err := os.Symlink(f.path, path); err != nil {
					t.Skip("symlink creation unavailable:", err)
				}
			case "unreadable":
				if err := os.WriteFile(path, []byte("a😀z\n"), 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0600) })
				if file, err := os.Open(path); err == nil {
					_ = file.Close()
					t.Skip("current principal can read mode-000 files")
				}
			}
			row := types.LSPNavigationTarget{URI: navigationTestURI(path), Range: navigationTestRange(0, 1, 3), Selection: navigationTestRange(0, 1, 3)}
			landing, err := f.inspect(t.Context(), result, row)
			if err == nil || !reflect.DeepEqual(landing, types.LSPNavigationLanding{}) {
				t.Fatal("unsafe selected file returned bytes", landing, err)
			}
		})
	}
}

func mutateNavigationSource(t *testing.T, f *navigationFixture, change string) {
	t.Helper()
	docs := f.w.LiveDocuments
	switch change {
	case "peer-edit", "equal-byte-undo":
		peer := document.Owner{ClientID: "navigation-peer", Epoch: 1}
		snap, err := docs.Open(peer, f.path)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := docs.Apply(document.Change{DocumentID: snap.ID, Owner: peer,
			RequestID: "peer-edit", BaseRevision: snap.Revision, Edits: []document.Edit{{Text: "X"}}})
		if err != nil {
			t.Fatal(err)
		}
		if change == "equal-byte-undo" {
			if _, err := docs.Undo(peer, snap.ID, "peer-undo", receipt.Revision); err != nil {
				t.Fatal(err)
			}
			restored, err := docs.Snapshot(snap.ID)
			if err != nil || string(restored.Text) != f.disk || restored.Revision <= f.source.Revision {
				t.Fatal("fixture failed to restore equal bytes with newer history", restored, err)
			}
		}
	case "close-reopen":
		if err := docs.CloseView(f.owner, f.source.DocumentID); err != nil {
			t.Fatal(err)
		}
		if _, err := docs.Open(f.owner, f.path); err != nil {
			t.Fatal(err)
		}
	case "detach":
		docs.Detach(f.owner)
	case "provider":
		f.w.LSPMgr.SetRuntime(&lifecycleRuntime{})
	case "context":
		f.w.LSPMgr.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(f.path), "go.mod"), 2)
	case "reconfigure":
		settings := f.w.LSPMgr.Settings()
		settings.Enabled = false
		f.w.LSPMgr.SetSettings(settings)
	default:
		t.Fatal("unknown test mutation", change)
	}
}

func TestNavigationLateAnswersAndSelectedLandingRejectStaleSource(t *testing.T) {
	for _, answer := range []string{"null", "[]", "targets"} {
		for _, change := range []string{"cancel", "peer-edit", "equal-byte-undo", "close-reopen", "detach", "provider", "context", "reconfigure"} {
			t.Run(answer+"/"+change, func(t *testing.T) {
				f := newNavigationFixture(t, "a😀z\n")
				raw := answer
				if answer == "targets" {
					raw = "[" + navigationLocation(navigationTestURI(f.path)) + "]"
				}
				// Retain a real completed answer too: explicit inspection must
				// reject its stale source even when a late RPC is also rejected.
				previous := f.request(t, raw, "definition", false)
				selected := types.LSPNavigationTarget{URI: navigationTestURI(f.path),
					Range: navigationTestRange(0, 1, 3), Selection: navigationTestRange(0, 1, 3)}
				entered, release := make(chan struct{}, 1), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				f.client.navMu.Lock()
				f.client.entered, f.client.release = entered, release
				f.client.navMu.Unlock()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				type outcome struct {
					value types.LSPNavigation
					err   error
				}
				done := make(chan outcome, 1)
				go func() {
					value, err := f.w.RequestLSPNavigation(ctx, f.owner, f.source, "definition", 0, 3, false)
					done <- outcome{value, err}
				}()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("request did not reach synchronized provider")
				}
				if change == "cancel" {
					cancel()
				} else {
					mutateNavigationSource(t, f, change)
				}
				unblock()
				var got outcome
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("released request did not retire")
				}
				if got.err == nil || !reflect.DeepEqual(got.value, types.LSPNavigation{}) {
					t.Fatal("late provider returned stale evidence", got)
				}
				inspectCtx := t.Context()
				if change == "cancel" {
					inspectCtx = ctx
				}
				if landing, err := f.inspect(inspectCtx, previous, selected); err == nil ||
					!reflect.DeepEqual(landing, types.LSPNavigationLanding{}) {
					t.Fatal("explicit selection returned cancelled/stale payload", landing, err)
				}
			})
		}
	}
}
