package viewmodel

import tea "charm.land/bubbletea/v2"

// programMessageSink is the shell's adapter for the domain ingress contract.
// The workspace never needs to construct or retain a terminal program.
type programMessageSink struct {
	program *tea.Program
}

func (sink programMessageSink) Send(message any) {
	sink.program.Send(message)
}
