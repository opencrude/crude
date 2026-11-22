package session

import (
	"fmt"
	"testing"

	"crude/agent/provider"
)

func BenchmarkDenseRecoverySnapshot(b *testing.B) {
	msgs := make([]provider.ChatMessage, 2000)
	for i := range msgs {
		msgs[i] = provider.NewTextMessage("assistant", "Accepted tool round.")
		msgs[i].InternalID = fmt.Sprint(i)
	}
	run := newRecoveryRunState(provider.PlainInput("continue"), nil)
	run.snapshot(msgs, msgs)
	b.ReportAllocs()
	for b.Loop() {
		run.snapshot(msgs, msgs)
	}
}
