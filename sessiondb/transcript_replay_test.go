package sessiondb

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"crude/provider"
)

// Providers supply RawMessage items with their own whitespace and escaping.
// SQLite stores json.Marshal's representation, not those in-memory byte slices.
func TestCompactionRetainsLiveProviderItems(t *testing.T) {
	for _, standard := range []string{"openai-responses", "anthropic", "openai"} {
		t.Run(standard, func(t *testing.T) {
			db := OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := CreateSessionRow(db, "session"); err != nil {
				t.Fatal(err)
			}
			message := provider.NewTextMessage("assistant", "")
			message.InternalID = "live-assistant"
			message.ProviderItems = []provider.ProviderItem{{
				Standard: standard, Type: "reasoning",
				Data:        json.RawMessage(`{ "type": "reasoning", "summary": [], "encrypted_content": "opaque<&>signature" }`),
				ReplayScope: "connection", SourceModel: "model", PrefixHash: "prefix",
			}}
			original := string(message.ProviderItems[0].Data)
			delta := ContextMessageDelta{Message: message, Turn: 3, CreatedAt: 77, AgentID: "producer", Meta: DisplayMeta{SnapshotVersion: 1, ProviderID: "provider"}}
			source, err := PublishContext(db, ContextPublication{
				SessionID: "session", AgentID: "root",
				Mutation: ContextMutation{Append: []ContextMessageDelta{delta}},
			})
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := provider.NewTextMessage("user", "compacted older context")
			checkpoint.InternalID, checkpoint.Origin, checkpoint.Visibility = "checkpoint", provider.OriginCheckpoint, provider.VisibilityContextOnly
			next, err := PublishContext(db, ContextPublication{
				SessionID: "session", AgentID: "root", ExpectedRevision: source.Revision,
				Mutation: ContextMutation{Replace: &ContextReplacement{
					Generation: 2, Reason: "automatic_compaction", Messages: []provider.ChatMessage{checkpoint, source.Messages[0]},
				}},
				ArchivePrevious: true,
			})
			if err != nil {
				t.Fatalf("compaction rejected an unchanged live provider item: %v", err)
			}
			if next.Generation != 2 || len(next.Messages) != 2 || !reflect.DeepEqual(next.Messages[1], source.Messages[0]) {
				t.Fatal("compaction changed the retained live message")
			}
			if string(message.ProviderItems[0].Data) != original {
				t.Fatal("comparison mutated opaque replay data")
			}
			if _, err := PublishContext(db, ContextPublication{
				SessionID: "session", AgentID: "root", ExpectedRevision: next.Revision,
				TranscriptAppend: []ContextMessageDelta{delta},
			}); err != nil {
				t.Fatalf("exact transcript replay rejected: %v", err)
			}
			before := publicationBytes(t, db)
			for _, mutate := range []func(*provider.ProviderItem){
				func(item *provider.ProviderItem) {
					item.Data = json.RawMessage(`{"type":"reasoning","encrypted_content":"changed"}`)
				},
				func(item *provider.ProviderItem) { item.ReplayScope = "other-connection" },
				func(item *provider.ProviderItem) { item.SourceModel = "other-model" },
				func(item *provider.ProviderItem) { item.PrefixHash = "other-prefix" },
				func(item *provider.ProviderItem) { item.Standard = "other-protocol" },
			} {
				changed := message
				changed.ProviderItems = append([]provider.ProviderItem(nil), message.ProviderItems...)
				mutate(&changed.ProviderItems[0])
				_, err := PublishContext(db, ContextPublication{
					SessionID: "session", AgentID: "root", ExpectedRevision: next.Revision,
					Mutation: ContextMutation{Replace: &ContextReplacement{
						Generation: 3, Reason: "changed", Messages: []provider.ChatMessage{checkpoint, changed},
					}},
					ArchivePrevious: true,
				})
				var conflict *ContextMessageConflictError
				if !errors.As(err, &conflict) || !reflect.DeepEqual(before, publicationBytes(t, db)) {
					t.Fatalf("changed replay identity was accepted or partially published: %v", err)
				}
			}
			rows, err := LoadMessagesWithMeta(db)
			if err != nil || len(rows) != 1 || rows[0].Turn != 3 || rows[0].CreatedAt != 77 || rows[0].Meta.ProviderID != "provider" {
				t.Fatalf("compaction/replay changed transcript provenance: %v", err)
			}
		})
	}
}

func TestPersistedMessageEqualPreservesNonFormattingDifferences(t *testing.T) {
	base := persistedMessage{
		Message: provider.ChatMessage{InternalID: "message", Role: "assistant",
			ProviderItems: []provider.ProviderItem{{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{ "signature": "<opaque>", "n": 9007199254740993 }`)}},
		},
		Meta: DisplayMeta{ProviderID: "provider"}, Turn: 3, CreatedAt: 77, AgentID: "producer",
	}
	canonical := base
	canonical.Message.ProviderItems = []provider.ProviderItem{{Standard: "openai-responses", Type: "reasoning", Data: json.RawMessage(`{"signature":"\u003copaque\u003e","n":9007199254740993}`)}}
	if !persistedMessageEqual(base, canonical) {
		t.Fatal("equivalent stored JSON representations differed")
	}
	for name, mutate := range map[string]func(*persistedMessage){
		"content":  func(v *persistedMessage) { v.Message.Content = provider.TextContent("changed") },
		"turn":     func(v *persistedMessage) { v.Turn++ },
		"time":     func(v *persistedMessage) { v.CreatedAt++ },
		"producer": func(v *persistedMessage) { v.AgentID = "other" },
		"metadata": func(v *persistedMessage) { v.Meta.ProviderID = "other" },
		"signature": func(v *persistedMessage) {
			v.Message.ProviderItems[0].Data = json.RawMessage(`{"signature":"changed","n":9007199254740993}`)
		},
		"large integer": func(v *persistedMessage) {
			v.Message.ProviderItems[0].Data = json.RawMessage(`{"signature":"\u003copaque\u003e","n":9007199254740992}`)
		},
		"invalid JSON": func(v *persistedMessage) { v.Message.ProviderItems[0].Data = json.RawMessage(`{`) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := canonical
			changed.Message.ProviderItems = append([]provider.ProviderItem(nil), canonical.Message.ProviderItems...)
			mutate(&changed)
			if persistedMessageEqual(base, changed) {
				t.Fatal("non-formatting change compared equal")
			}
		})
	}
}
