package viewmodel

import (
	"testing"

	"crude/workspace"
)

func TestServiceNoticePresentationNeverRepublishesCanonicalFeedback(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	center := wsOf(app).Status
	before := center.Version()
	for range 200 {
		for _, notice := range []workspace.AgentNotice{
			{Type: workspace.NoticeAttachmentExcluded, AgentID: "root", Reason: "Attachment could not be read"},
			{Type: workspace.NoticeMemorySaved, AgentID: "root", Count: 2, Titles: []string{"Saved fact"}},
			{Type: workspace.NoticeResponseContinuationUnavailable, AgentID: "root", Reason: "response_not_stored"},
		} {
			app.presentAgentNotice(notice)
		}
	}
	if center.Version() != before || len(center.Notifications(true)) != 0 || len(center.Operations()) != 0 {
		t.Fatalf("service presentation republished canonical feedback: version %d -> %d; notifications=%+v",
			before, center.Version(), center.Notifications(true))
	}
}
