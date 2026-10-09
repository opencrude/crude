package history

import (
	"reflect"
	"testing"
	"time"

	"crude/sessiondb"
)

func TestInstallReplicaPreservesAuthoritativeEntries(t *testing.T) {
	entries := []Entry{
		{ID: "core-old", Text: "repeat", Count: 7, FirstSeen: 20, LastSeen: 30},
		{ID: "core-media", Text: "[image]", Count: 3, FirstSeen: 40, LastSeen: 50, Attachments: []Attachment{{Start: 0, End: 7, Reference: sessiondb.Reference{ID: "exact", FilePath: "/remote-only/image"}, Unavailable: true}}},
	}
	s := New()
	s.max = 1 // Replica installation must not apply client-side retention.
	s.Record("obsolete", time.Now())
	s.InstallReplica(entries)
	if got := s.Entries(); !reflect.DeepEqual(got, entries) {
		t.Fatalf("replica changed entries: %#v", got)
	}
	entries[1].Attachments[0].Reference.ID = "mutated"
	if s.Entries()[1].Attachments[0].Reference.ID != "exact" {
		t.Fatal("replica aliases input")
	}
	s.Record("repeat", time.Unix(60, 0))
	if got := s.Entries(); len(got) != 2 || got[1].ID != "core-old" || got[1].Count != 8 {
		t.Fatalf("index not rebuilt: %#v", got)
	}
	s.InstallReplica(nil)
	if s.Len() != 0 {
		t.Fatal("empty authoritative snapshot retained entries")
	}
}
