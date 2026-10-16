package chatprojection

import (
	"testing"

	"crude/workspace"
)

func paneOpenPayloads(spans []RowSpan) []ChatAgentOpenPayload {
	var out []ChatAgentOpenPayload
	for _, span := range spans {
		if span.Action != nil && span.Action.ID == ChatAgentOpenActionID {
			out = append(out, span.Action.Payload.(ChatAgentOpenPayload))
		}
	}
	return out
}

// A sub-agent pane offers explicit tab and window controls, right-aligned
// within the row width; thinking panes and cramped rows offer none.
func TestAgentPaneHeaderOffersTabAndWindow(t *testing.T) {
	header := workspace.SubPaneHeaderView{ID: "agent-7", Title: "Nova sub Ada", Type: workspace.PaneAgent}
	line := BuildSubPaneHeaderLine(header, 80, 0)
	got := paneOpenPayloads(line.Native.Spans)
	if len(got) != 2 || got[0] != (ChatAgentOpenPayload{AgentID: "agent-7"}) || got[1] != (ChatAgentOpenPayload{AgentID: "agent-7", Window: true}) {
		t.Fatalf("pane controls = %+v", got)
	}
	if width := line.Native.VisibleWidth(); width != 80 {
		t.Fatalf("header width = %d, want 80", width)
	}
	header.Done = true
	if got := paneOpenPayloads(BuildSubPaneHeaderLine(header, 80, 0).Native.Spans); len(got) != 2 {
		t.Fatalf("finished pane lost its controls: %+v", got)
	}
	summary := BuildSubPaneSummaryLine(workspace.SubPaneView{Header: header, Summary: &workspace.SubPaneSummaryView{Text: "Nova for 5s"}}, 80)
	if got := paneOpenPayloads(summary.Native.Spans); len(got) != 2 || summary.Native.VisibleWidth() != 80 {
		t.Fatalf("collapsed summary controls = %+v width %d", got, summary.Native.VisibleWidth())
	}
	if got := paneOpenPayloads(BuildSubPaneHeaderLine(header, 30, 0).Native.Spans); len(got) != 0 {
		t.Fatalf("cramped pane kept controls over its title: %+v", got)
	}
	thinking := workspace.SubPaneHeaderView{ID: "think-1", Title: "Thinking", Type: workspace.PaneThinking}
	if got := paneOpenPayloads(BuildSubPaneHeaderLine(thinking, 80, 0).Native.Spans); len(got) != 0 {
		t.Fatalf("thinking pane offered agent controls: %+v", got)
	}
}
