package control

// ProductCommands describes concrete shell owners, not key simulation or a
// blanket grant. The caller must supply the current shell inspection fences.
// Composer commands instead use the private draft revision and draft epoch from
// composer.inspect, and require the exact selected root session and agent.
func ProductCommands() []Descriptor {
	var out []Descriptor
	add := func(id string, effect Effect, description, properties string, session, resource bool, required ...string) {
		d := catalogCommand(id, Shell, effect, description, properties, required...)
		d.Requires = Requirements{Session: session, Resource: resource, Revision: effect != Read, OpenEpoch: effect != Read}
		out = append(out, d)
	}
	add("tab.move", Write, "Move/reorder one existing tab into an exact group; zero before_tab_id appends. Target resource is the decimal tab ID.", `{"tab_id":{"type":"integer","minimum":1},"group_id":{"type":"integer","minimum":1},"before_tab_id":{"type":"integer","minimum":0}}`, false, true, "tab_id", "group_id", "before_tab_id")
	for _, id := range []string{"window.move", "window.resize"} {
		required := []string{"id", "epoch", "expected_x", "expected_y", "expected_width", "expected_height"}
		if id == "window.move" {
			required = append(required, "x", "y")
		} else {
			required = append(required, "width", "height")
		}
		add(id, Write, "Move/resize the exact foreground nonmodal floating owner. Echo current bounds and Host lifetime; coordinates and dimensions use terminal cells.", `{"id":{"type":"string"},"epoch":{"type":"integer","minimum":1},"expected_x":{"type":"integer"},"expected_y":{"type":"integer"},"expected_width":{"type":"integer"},"expected_height":{"type":"integer"},"x":{"type":"integer","minimum":0},"y":{"type":"integer","minimum":0},"width":{"type":"integer","minimum":1},"height":{"type":"integer","minimum":1}}`, false, true, required...)
	}
	for _, id := range []string{"terminal.dock", "terminal.float"} {
		add(id, Write, "Change only this shell's placement of the retained terminal; never restart or close the PTY. Target resource equals id.", `{"id":{"type":"string"}}`, false, true, "id")
	}
	for _, id := range []string{"agent.select", "agent.minimize", "agent.restore"} {
		add(id, Write, "Select/minimize/restore the exact agent in this shell without changing retained execution. Target session and resource must match the agent owner.", `{"agent_id":{"type":"string"}}`, true, true, "agent_id")
	}
	add("agent.recover", Execute, "Continue the exact incident/generation using its normal recovery owner; never submits a draft or replays historical effects.", `{"agent_id":{"type":"string"},"incident_id":{"type":"string"},"generation":{"type":"integer","minimum":1}}`, true, true, "agent_id", "incident_id", "generation")
	add("job.stop", Execute, "Stop only the exact running command identity; current handle, PID and start time are checked by the retained command owner.", `{"agent_id":{"type":"string"},"handle":{"type":"string"},"pid":{"type":"integer","minimum":1},"started_at":{"type":"string","format":"date-time"}}`, true, true, "agent_id", "handle", "pid", "started_at")
	add("editor.find", Write, "Open normal find/replace for the focused document. Does not replace content.", `{"replace":{"type":"boolean"}}`, false, true, "replace")
	add("project.search_open", Write, "Open normal project find/replace UX without performing replacement.", `{"replace":{"type":"boolean"}}`, false, false, "replace")
	add("composer.inspect", Read, "Explicitly inspect only this shell's bound selected root-session composer. Generic shell inspection still omits all draft text.", `{}`, true, true)
	add("composer.set", Write, "Edit the exact own bound draft; stage only, never send. Preserves existing reference identities.", `{"text":{"type":"string","maxLength":32768}}`, true, true, "text")
	add("composer.reference", Write, "Stage an already-existing exact session reference in the own composer, not arbitrary external context or approval.", `{"id":{"type":"string"}}`, true, true, "id")
	add("composer.send", Communication, "Submit the captured exact own draft through normal immutable input admission; report durable admission, not model completion.", `{}`, true, true)
	add("capability.picker", Write, "Open the searchable Skills/MCPs activation modal; opening never changes the composer, launches a server or grants access.", `{}`, true, true)
	add("chat.follow", Write, "Jump the selected exact agent chat to its actual tail and arm following.", `{}`, true, true)
	add("chat.scroll", Write, "Scroll the selected exact chat in rows and disarm following.", `{"offset":{"type":"integer","minimum":0}}`, true, true, "offset")
	return out
}
