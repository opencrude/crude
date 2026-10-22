package perfdash

import (
	"slices"

	"crude/metrics"
)

// Dashboard tabs.
const (
	SectionOverview = "overview"
	SectionUI       = "ui"
	SectionCore     = "core"
	SectionAgents   = "agents"
	SectionServices = "services"
	SectionRuntime  = "runtime"
	SectionEvents   = "events"
)

var sectionLabels = []struct{ id, label string }{
	{SectionOverview, "Overview"},
	{SectionUI, "UI"},
	{SectionCore, "Core"},
	{SectionAgents, "Agents"},
	{SectionServices, "Services"},
	{SectionRuntime, "Runtime"},
	{SectionEvents, "Events"},
}

// sectionOfGroup places a series nobody curated, so new instrumentation is
// visible without dashboard changes.
func sectionOfGroup(group string) string {
	switch group {
	case metrics.GroupUI, metrics.GroupRender:
		return SectionUI
	case metrics.GroupRPC, metrics.GroupCore:
		return SectionCore
	case metrics.GroupAgents, metrics.GroupTools:
		return SectionAgents
	case metrics.GroupRuntime:
		return SectionRuntime
	}
	return SectionServices
}

// seriesRef names one series in one process. A ref with name "ui.busy" is
// the derived UI busy share.
type seriesRef struct {
	process process
	name    string
}

const busyName = "ui.busy"

// curated is the reading order per section: cause before effect, the user's
// experience first. Uncurated series follow in name order.
var curated = map[string][]seriesRef{
	SectionUI: {
		{processShell, "ui.input"}, {processShell, "ui.prompt"}, {processShell, "ui.input.wait"}, {processShell, busyName},
		{processShell, "ui.update"}, {processShell, "ui.view"}, {processShell, "ui.present"},
		{processShell, "render.flush"}, {processShell, "render.bytes"}, {processShell, "ui.blocking"},
		{processShell, "ui.messages"},
	},
	SectionCore: {
		{processCore, "core.actor"}, {processCore, "core.queue"}, {processShell, "link.fetch"},
		{processCore, "core.changes"}, {processCore, "core.resync"}, {processShell, "link.resync"},
		{processCore, "core.actor.work"}, {processShell, "link.requests"}, {processCore, "core.requests"},
	},
	SectionAgents: {
		{processCore, "agent.streams"}, {processCore, "agent.output"}, {processCore, "agent.retries"},
		{processCore, "agent.first_output"}, {processCore, "agent.connect"}, {processCore, "agent.response"},
		{processCore, "tools.run"},
	},
	SectionServices: {
		{processCore, "storage.receipts"}, {processCore, "files.search"}, {processCore, "files.events"},
		{processCore, "lsp.diagnostics"}, {processCore, "files.watch"},
		{processCore, "storage.session"}, {processCore, "lsp.requests"}, {processCore, "lsp.start"}, {processCore, "git.commands"},
	},
	SectionRuntime: {
		{processShell, "runtime.cpu"}, {processCore, "runtime.cpu"},
		{processShell, "runtime.heap"}, {processCore, "runtime.heap"},
		{processShell, "runtime.gc.pause"}, {processCore, "runtime.gc.pause"},
		{processShell, "runtime.alloc"}, {processCore, "runtime.alloc"},
		{processShell, "runtime.memory"}, {processCore, "runtime.memory"},
		{processShell, "runtime.goroutines"}, {processCore, "runtime.goroutines"},
		{processShell, "runtime.gc.cycles"}, {processCore, "runtime.gc.cycles"},
	},
}

// overviewRefs are the headline charts.
var overviewRefs = []seriesRef{
	{processShell, "ui.input"}, {processShell, "ui.prompt"}, {processShell, busyName}, {processShell, "ui.view"},
	{processShell, "render.flush"}, {processCore, "core.actor"}, {processShell, "link.fetch"},
}

// sectionRefs lists a section's present series: curated first, then any
// other series whose group maps to the section.
func sectionRefs(d Data, section string) []seriesRef {
	seen := map[seriesRef]bool{}
	var out []seriesRef
	addIfPresent := func(ref seriesRef) {
		if seen[ref] || !d.has(ref) {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}
	for _, ref := range curated[section] {
		addIfPresent(d.resolve(ref))
	}
	for _, p := range d.processes() {
		s := d.snapshot(p)
		var names []struct{ name, group string }
		for _, x := range s.Latencies {
			names = append(names, struct{ name, group string }{x.Meta.Name, x.Meta.Group})
		}
		for _, x := range s.Counters {
			names = append(names, struct{ name, group string }{x.Meta.Name, x.Meta.Group})
		}
		for _, x := range s.Gauges {
			names = append(names, struct{ name, group string }{x.Meta.Name, x.Meta.Group})
		}
		for _, x := range s.Tables {
			names = append(names, struct{ name, group string }{x.Meta.Name, x.Meta.Group})
		}
		slices.SortFunc(names, func(a, b struct{ name, group string }) int {
			if a.name < b.name {
				return -1
			}
			if a.name > b.name {
				return 1
			}
			return 0
		})
		for _, n := range names {
			if sectionOfGroup(n.group) == section {
				addIfPresent(seriesRef{p, n.name})
			}
		}
	}
	return out
}

// resolve maps core refs to the shell snapshot when the core runs in this
// process.
func (d Data) resolve(ref seriesRef) seriesRef {
	if ref.process == processCore && d.Core == nil {
		ref.process = processShell
	}
	return ref
}

func (d Data) has(ref seriesRef) bool {
	if ref.name == busyName {
		_, ok := d.busySeries()
		return ok && ref.process == processShell
	}
	if ref.process == processCore && d.Core == nil {
		return false
	}
	if _, ok := d.latency(ref.process, ref.name); ok {
		return true
	}
	if _, ok := d.counter(ref.process, ref.name); ok {
		return true
	}
	if _, ok := d.gauge(ref.process, ref.name); ok {
		return true
	}
	_, ok := d.table(ref.process, ref.name)
	return ok
}

func (d Data) isTable(ref seriesRef) bool {
	_, ok := d.table(ref.process, ref.name)
	return ok
}
