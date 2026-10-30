package tree_sitter_test

import (
	"runtime"
	"strings"
	"testing"
	"time"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// A saved cgo callback must not retain its context/source after its native
// owner releases it. Finalizers observe reachability, not an allocation proxy.
type callbackLifetimeProbe struct{ data [1024]byte }

func callbackCollected(use func(*callbackLifetimeProbe)) <-chan struct{} {
	collected := make(chan struct{})
	probe := &callbackLifetimeProbe{}
	runtime.SetFinalizer(probe, func(*callbackLifetimeProbe) { close(collected) })
	use(probe)
	runtime.KeepAlive(probe)
	return collected
}

func requireCallbackCollected(t *testing.T, collected <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			return
		case <-deadline.C:
			t.Fatal("native owner released, but callback still retains its Go payload")
		default:
			runtime.Gosched()
		}
	}
}

func TestParseOptionsReleaseCallbackPayload(t *testing.T) {
	for _, encoding := range []string{"utf8", "utf16le", "utf16be", "custom-empty"} {
		t.Run(encoding, func(t *testing.T) {
			collected := callbackCollected(func(probe *callbackLifetimeProbe) {
				parser := ts.NewParser()
				defer parser.Close()
				if err := parser.SetLanguage(getLanguage("json")); err != nil {
					t.Fatal(err)
				}
				options := &ts.ParseOptions{ProgressCallback: func(ts.ParseState) bool {
					runtime.KeepAlive(probe)
					return false
				}}
				var tree *ts.Tree
				switch encoding {
				case "utf8":
					tree = parser.ParseWithOptions(func(int, ts.Point) []byte { return nil }, nil, options)
				case "utf16le":
					tree = parser.ParseUTF16LEWithOptions(func(int, ts.Point) []uint16 { return nil }, nil, options)
				case "utf16be":
					tree = parser.ParseUTF16BEWithOptions(func(int, ts.Point) []uint16 { return nil }, nil, options)
				case "custom-empty":
					// Invalid custom input still has to release callback payloads.
					tree = parser.ParseCustomEncoding(func(int, ts.Point) []byte { return nil }, nil, options, nil)
				}
				tree.Close()
			})
			requireCallbackCollected(t, collected)
		})
	}
}

func TestQueryOptionsReleaseCallbackPayload(t *testing.T) {
	for _, release := range []string{"close", "matches", "captures", "replace-options"} {
		t.Run(release, func(t *testing.T) {
			parser := ts.NewParser()
			defer parser.Close()
			if err := parser.SetLanguage(getLanguage("json")); err != nil {
				t.Fatal(err)
			}
			source := []byte("[" + strings.Repeat("0,", 2000) + "0]")
			tree := parser.Parse(source, nil)
			defer tree.Close()
			query, err := ts.NewQuery(getLanguage("json"), "(number) @number")
			if err != nil {
				t.Fatal(err)
			}
			defer query.Close()
			cursor := ts.NewQueryCursor()
			collected := callbackCollected(func(probe *callbackLifetimeProbe) {
				progressCalls := 0
				matches := cursor.MatchesWithOptions(query, tree.RootNode(), source, ts.QueryCursorOptions{
					ProgressCallback: func(ts.QueryCursorState) bool {
						progressCalls++
						runtime.KeepAlive(probe)
						return false
					},
				})
				// Query options outlive exec: force a GC before iteration to
				// exercise the retained C options storage and Go callback.
				runtime.GC()
				count := 0
				for matches.Next() != nil {
					count++
				}
				if count != 2001 || progressCalls == 0 {
					t.Fatalf("query callback/results lost after GC: %d results, %d callbacks", count, progressCalls)
				}
				switch release {
				case "close":
					cursor.Close()
				case "matches":
					cursor.Matches(query, tree.RootNode(), source)
				case "captures":
					cursor.Captures(query, tree.RootNode(), source)
				case "replace-options":
					cursor.MatchesWithOptions(query, tree.RootNode(), source, ts.QueryCursorOptions{
						ProgressCallback: func(ts.QueryCursorState) bool { return false },
					})
				}
			})
			if release != "close" {
				defer cursor.Close()
			}
			requireCallbackCollected(t, collected)
		})
	}
}

func TestOptionalCallbacksAndLoggerReadback(t *testing.T) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(getLanguage("json")); err != nil {
		t.Fatal(err)
	}
	tree := parser.ParseWithOptions(func(int, ts.Point) []byte { return nil }, nil, &ts.ParseOptions{})
	if tree == nil {
		t.Fatal("nil progress callback changed ordinary parsing")
	}
	defer tree.Close()
	query, err := ts.NewQuery(getLanguage("json"), "(number) @number")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	cursor := ts.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.MatchesWithOptions(query, tree.RootNode(), nil, ts.QueryCursorOptions{})
	if matches.Next() != nil {
		t.Fatal("empty JSON produced a number")
	}
	called := false
	parser.SetLogger(func(ts.LogType, string) { called = true })
	if logger := parser.Logger(); logger == nil {
		t.Fatal("logger handle not restored")
	} else {
		(*logger)(ts.LogTypeParse, "readback")
	}
	if !called {
		t.Fatal("restored logger is not the installed function")
	}
	parser.SetLogger(nil)
	if parser.Logger() != nil {
		t.Fatal("removed logger remains readable")
	}
}

func TestParserLoggerReleasesCallbackPayload(t *testing.T) {
	for _, release := range []string{"replace", "close"} {
		t.Run(release, func(t *testing.T) {
			collected := callbackCollected(func(probe *callbackLifetimeProbe) {
				parser := ts.NewParser()
				parser.SetLogger(func(ts.LogType, string) { runtime.KeepAlive(probe) })
				if release == "replace" {
					parser.SetLogger(nil)
				}
				parser.Close()
			})
			requireCallbackCollected(t, collected)
		})
	}
}
