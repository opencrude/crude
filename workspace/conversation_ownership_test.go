package workspace

import (
	"reflect"
	"testing"

	"crude/provider"
	"crude/types/command"
	"crude/types/recovery"
)

func ownedMessageFixture() Message {
	return Message{
		InternalID: "message", Role: MessageRoleUser, Content: "accepted text",
		ReferenceLabels: []string{"attachment"},
		Collaboration: &provider.CollaborationAttribution{
			EventID: "event", SenderID: "sender", RecipientIDs: []string{"recipient"},
			RecipientNames: []string{"Recipient"},
			Command:        &command.Outcome{Handle: "build", Command: "go build ./..."},
		},
		Recovery: &recovery.Status{ID: "incident", Detail: "original diagnostic"},
	}
}

func mutateMessageReferences(message Message) {
	message.ReferenceLabels[0] = "changed attachment"
	message.Collaboration.EventID = "changed event"
	message.Collaboration.RecipientIDs[0] = "changed recipient"
	message.Collaboration.RecipientNames[0] = "changed name"
	message.Collaboration.Command.Handle = "changed command"
	message.Recovery.Detail = "changed diagnostic"
}

func TestConversationOwnsAppendedNestedData(t *testing.T) {
	conversation := NewConversation()
	source := ownedMessageFixture()
	conversation.AppendMessage(source)
	version := conversation.Version()
	mutateMessageReferences(source)
	if got := conversation.Messages()[0]; !reflect.DeepEqual(got, ownedMessageFixture()) {
		t.Fatalf("producer changed accepted conversation through nested references: %+v", got)
	}
	if conversation.Version() != version {
		t.Fatal("detaching input changed the conversation revision")
	}
}

func TestConversationObserversCannotMutateEachOther(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendMessage(ownedMessageFixture())
	first, second := conversation.Messages(), conversation.Messages()
	version := conversation.Version()
	mutateMessageReferences(first[0])
	if !reflect.DeepEqual(second[0], ownedMessageFixture()) {
		t.Fatal("one observer changed another observer's retained snapshot")
	}
	if got := conversation.Messages()[0]; !reflect.DeepEqual(got, ownedMessageFixture()) {
		t.Fatal("observer changed the canonical conversation")
	}
	if conversation.Version() != version {
		t.Fatal("reading snapshots mutated the source revision")
	}
}

func TestConversationSettlementReturnsDetachedEvidence(t *testing.T) {
	conversation := NewConversation()
	conversation.AppendPendingSteering(ownedMessageFixture())
	settled := conversation.SettlePendingSteeringMessages([]string{"message"})
	if len(settled) != 1 || settled[0].PendingSteering {
		t.Fatal("steering was not settled")
	}
	mutateMessageReferences(settled[0])
	if got := conversation.Messages()[0]; !reflect.DeepEqual(got, ownedMessageFixture()) {
		t.Fatal("settlement result changed retained canonical evidence")
	}
}

func TestConversationActiveThinkingReturnsDetachedEvidence(t *testing.T) {
	conversation := NewConversation()
	message := ownedMessageFixture()
	message.Role, message.ThinkingActive = MessageRoleThinking, true
	conversation.AppendMessage(message)
	thought, ok := conversation.ActiveThinking()
	if !ok {
		t.Fatal("active thought missing")
	}
	mutateMessageReferences(thought)
	want := ownedMessageFixture()
	want.Role, want.ThinkingActive = MessageRoleThinking, true
	if got, ok := conversation.ActiveThinking(); !ok || !reflect.DeepEqual(got, want) {
		t.Fatal("thinking projection changed canonical evidence")
	}
}
