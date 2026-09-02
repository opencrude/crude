package agent

import (
	"testing"

	"crude/provider"
)

func TestPrefixVerifierAllowsAppendRejectsMutationAndResetsForCompaction(t *testing.T) {
	base := []provider.ChatMessage{
		provider.NewTextMessage("system", "stable system"),
		provider.NewTextMessage("user", "frozen turn one"),
	}
	verifier, err := newPrefixVerifier(base)
	if err != nil {
		t.Fatal(err)
	}
	extended := append(append([]provider.ChatMessage(nil), base...), provider.NewTextMessage("assistant", "answer"))
	if err := verifier.check(extended); err != nil {
		t.Fatalf("append-only context rejected: %v", err)
	}
	mutated := append([]provider.ChatMessage(nil), extended...)
	mutated[1] = provider.NewTextMessage("user", "changed old turn")
	if err := verifier.check(mutated); err == nil {
		t.Fatal("mutated frozen turn was accepted")
	}
	verifier.reset()
	checkpoint := []provider.ChatMessage{
		provider.NewTextMessage("system", "stable system"),
		provider.NewTextMessage("system", "<context_checkpoint>new generation</context_checkpoint>"),
	}
	if err := verifier.check(checkpoint); err != nil {
		t.Fatalf("explicit compaction boundary rejected: %v", err)
	}
}
