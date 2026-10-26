package control

// DiagnosticCommands adapts current owners. Discovery is on demand; none of
// these commands starts a monitor, opens a dashboard or grants host authority.
func DiagnosticCommands() []Descriptor {
	page := `{"type":"object","additionalProperties":false,"properties":{"view":{"type":"string","description":"For a different view start a NEW read: omit snapshot_id, offset 0. Do not change view on an old snapshot."},"filter":{"type":"string","maxLength":256},"snapshot_id":{"type":"string","description":"Continuation ONLY of the same command, view and filter. Omit for a fresh/different read. Echo previous next unchanged as page."},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":64,"default":12},"max_bytes":{"type":"integer","minimum":1024,"maximum":49152,"default":16384}}}`
	var out []Descriptor
	add := func(id string, scope Scope, description, properties string) {
		d := catalogCommand(id, scope, Read, description, properties)
		d.Output.Document = []byte(`{"type":"object","description":"Bounded immutable inspection page: source identity/times, total/offset/returned, records, completeness and next. Missing measurements are not zero; echo next for more from the same observation. Snapshots expire after two minutes or bounded retention pressure. Errors explain invalid ranges and how to narrow a read."}`)
		out = append(out, d)
	}
	performanceProps := `{"page":` + page + `,"seconds":{"type":"integer","minimum":1,"maximum":300,"default":120},"points":{"type":"integer","minimum":0,"maximum":60,"default":0,"description":"Zero gives compact summaries; request history points only for a relevant metric."}}`
	for _, source := range []struct {
		id    string
		scope Scope
		role  string
	}{{"performance.core", Domain, "core server"}, {"performance.shell", Shell, "exact originating shell client"}} {
		add(source.id, source.scope, "Read performance metrics of the "+source.role+" from its existing process registry, including the Performance dashboard's measurements and findings. Start with {} for a bounded summary; page.view: summary, series, history, tables, events, resources. page.filter narrows metric/group/owner. For dense output echo result.next as page (omit seconds/points). A different view/filter needs a NEW read without snapshot_id. Related commands: performance.core and performance.shell are distinct; connection.inspect reads the exact shell link; diagnostics.core/workspace cover operational health. No dashboard opening or automatic polling.", performanceProps)
	}
	add("diagnostics.core", Domain, "Inspect retained core server, client connections, workspace capacity, request pressure and replication/applied ACK evidence. Start with {}; page.view: summary, clients, workspaces, replication, requests, listeners. listeners separates configured addresses from actual live endpoints. Current-workspace detail; other clients' payloads/results and resume secrets are absent. Echo next as page. An unavailable actor produces partial evidence, not a healthy empty result.", `{"page":`+page+`}`)
	add("diagnostics.workspace", Domain, "Read canonical workspace issues, operations, notifications, agent status, language-server health and capability failures without opening panels, connecting services, clearing diagnostics or advancing read markers. page.view: summary, issues, operations, notifications, agents, lsp, capabilities. Use page.filter and echo next as page for dense output.", `{"page":`+page+`}`)
	add("connection.inspect", Shell, "Inspect the exact originating shell's existing connection-to-core state, carrier, observed round trip and safe notice. Link responsiveness is not replica freshness or provider health. No probe, reconnect, connection switch or private credential read.", `{}`)
	// This deliberately small scalar result is not a paged observation.
	out[len(out)-1].Output.Document = []byte(`{"type":"object","description":"Exact shell identity, observation time, connection state, carrier, RTT and safe notice. This is not replication freshness or provider health."}`)
	return out
}
