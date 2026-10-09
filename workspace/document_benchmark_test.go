package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/document"
)

// Includes SQLite publication and growing undo/receipt metadata, rather than
// benchmarking the ephemeral text helper. Each sample begins after 100 edits.
func BenchmarkDocumentDurableEditAfterHundredOps(b *testing.B) {
	for _, size := range []int{4096, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			root := b.TempDir()
			path := filepath.Join(root, "text.txt")
			if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0600); err != nil {
				b.Fatal(err)
			}
			db, err := openWorkspaceDBAt(filepath.Join(b.TempDir(), "workspace.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					b.Error(err)
				}
			}()
			service := NewDocumentService(root, nil, nil, db.DB)
			defer service.Close()
			owner := document.Owner{ClientID: "durable-benchmark", Epoch: 1}
			snap, err := service.Open(owner, path)
			if err != nil {
				b.Fatal(err)
			}
			revision := snap.Revision
			edit := func(i int) {
				receipt, err := service.Apply(document.Change{Owner: owner, DocumentID: snap.ID, RequestID: fmt.Sprint(i), BaseRevision: revision, Edits: []document.Edit{{Start: 0, End: 1, Text: string(rune('a' + i%26))}}})
				if err != nil || !receipt.Accepted {
					b.Fatalf("durable mutation rejected: %v", err)
				}
				revision = receipt.Revision
			}
			for i := range 100 {
				edit(i)
			}
			storageBytes := func() int64 {
				var pages, pageSize int64
				if err := db.DB.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
					b.Fatal(err)
				}
				if err := db.DB.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
					b.Fatal(err)
				}
				return pages * pageSize
			}
			before := storageBytes()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				edit(100 + i)
			}
			b.StopTimer()
			if size == 4<<20 && b.N >= 10 && b.Elapsed()/time.Duration(b.N) >= 16*time.Millisecond {
				b.Fatal("4 MiB ordinary durable edit exceeded 16 ms latency budget")
			}
			after := storageBytes()
			b.ReportMetric(float64(after), "sqlite-bytes")
			b.ReportMetric(float64(after-before)/float64(b.N), "sqlite-growth-B/op")
			final, err := service.Snapshot(snap.ID)
			if err != nil || final.Revision != uint64(101+b.N) || len(final.Text) != size || final.Text[0] != byte('a'+(99+b.N)%26) {
				b.Fatal("final authoritative document differs from accepted edits")
			}
		})
	}
}
