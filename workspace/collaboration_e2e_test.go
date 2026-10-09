package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	ttypes "crude/types"
)

func TestWorkspacePeerMessageWakesExactIdleAgentWithoutPolling(t *testing.T) {
	peerRequest := make(chan []provider.ChatMessage, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		peerRequest <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"peer request handled\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", ttypes.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig(server.URL)

	first, err := workspace.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	firstSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	firstRoot, err := workspace.StartOrchestrator(first.SessionID, firstSender)
	if err != nil {
		t.Fatal(err)
	}
	first.AgentID = firstRoot.ID
	workspace.SessionsByAgent[firstRoot.ID] = first

	second, err := workspace.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	secondSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	secondRoot, err := workspace.StartOrchestrator(second.SessionID, secondSender)
	if err != nil {
		t.Fatal(err)
	}
	second.AgentID = secondRoot.ID
	workspace.SessionsByAgent[secondRoot.ID] = second

	published, err := workspace.Collaboration.Publish(t.Context(), collaboration.PublishRequest{
		SenderID: firstRoot.ID, IdempotencyKey: "peer-request-1",
		RecipientIDs: []string{secondRoot.ID}, Origin: collaboration.OriginPeer,
		Kind: collaboration.EventWorkRequest, Body: "Review the workspace parser.",
		RequestID: "request-parser", RequestRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitHistoryAgentDone(t, secondSender, secondRoot.ID)

	select {
	case messages := <-peerRequest:
		found := false
		for _, message := range messages {
			if strings.Contains(message.ContentString(), `origin="peer"`) &&
				strings.Contains(message.ContentString(), published.Event.ID) &&
				strings.Contains(message.ContentString(), "Review the workspace parser.") {
				found = true
			}
		}
		if !found {
			t.Fatalf("provider request did not contain attributed peer event: %+v", messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not receive the automatic peer turn")
	}

	var peerPersisted bool
	for _, message := range second.backend.MessagesWithMeta() {
		if message.Msg.EffectiveOrigin() == provider.OriginPeer &&
			message.Msg.Collaboration != nil &&
			message.Msg.Collaboration.EventID == published.Event.ID {
			peerPersisted = true
		}
	}
	if !peerPersisted {
		t.Fatal("accepted peer event was not persisted with its attribution")
	}
	page, err := workspace.Collaboration.Page(t.Context(), collaboration.PageQuery{
		RecipientID: secondRoot.ID, Limit: 10,
	})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != published.Event.ID {
		t.Fatalf("workspace history = %+v, %v", page, err)
	}
}

func TestWorkspaceHumanSteeringTargetsExactActiveRun(t *testing.T) {
	var requests atomic.Int32
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondRequest := make(chan []provider.ChatMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		if requests.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-releaseFirst
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"initial response\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		secondRequest <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"steering applied\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", ttypes.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	frontend, err := workspace.CreateSession(historyE2EConfig(server.URL), home)
	if err != nil {
		t.Fatal(err)
	}
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	root, err := workspace.StartOrchestrator(frontend.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	frontend.AgentID = root.ID
	workspace.SessionsByAgent[root.ID] = frontend
	if !frontend.backend.SubmitInput(root.ID, provider.PlainInput("begin work")) {
		t.Fatal("initial turn was not admitted")
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial provider request did not start")
	}

	steering := provider.InputWithRefs("prioritize the parser edge case", nil)
	steering.InternalID = "history-steering-1"
	if !workspace.SteerAgent(root.ID, steering) {
		t.Fatal("human steering was not durably published to the active run")
	}
	page, err := workspace.Collaboration.Page(t.Context(), collaboration.PageQuery{
		RecipientID: root.ID, Kind: collaboration.EventHumanSteering, Limit: 10,
	})
	if err != nil || len(page.Events) != 1 || page.Events[0].RunID == "" {
		t.Fatalf("human steering journal = %+v, %v", page, err)
	}
	steeringEvent := page.Events[0]
	close(releaseFirst)
	waitHistoryAgentDone(t, sender, root.ID)

	var requestMessages []provider.ChatMessage
	select {
	case requestMessages = <-secondRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("human steering did not trigger a safe-boundary continuation")
	}
	found := false
	for _, message := range requestMessages {
		content := message.ContentString()
		if strings.Contains(content, `origin="human"`) &&
			strings.Contains(content, `kind="human_steering"`) &&
			strings.Contains(content, steeringEvent.ID) &&
			strings.Contains(content, steeringEvent.RunID) &&
			strings.Contains(content, "prioritize the parser edge case") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request lacked attributed human steering: %+v", requestMessages)
	}
	for _, expected := range []string{"begin work", "initial response", "prioritize the parser edge case"} {
		count := 0
		for _, message := range requestMessages {
			if message.Role != "system" {
				count += strings.Count(message.ContentString(), expected)
			}
		}
		if count != 1 {
			t.Fatalf("next provider request contains %q %d times, want once", expected, count)
		}
	}

	persisted := false
	for _, message := range frontend.backend.MessagesWithMeta() {
		if message.Msg.InternalID == steering.InternalID &&
			message.Msg.EffectiveOrigin() == provider.OriginHumanSteering &&
			message.Msg.Collaboration != nil &&
			message.Msg.Collaboration.HistoryMessageID == steering.InternalID {
			persisted = true
		}
	}
	if !persisted {
		t.Fatal("admitted human steering was not retained in canonical session history")
	}
	current, present, err := sessiondb.LoadContextState(frontend.backend.DB(), frontend.backend.SessionID(), frontend.backend.SessionID())
	if err != nil || !present {
		t.Fatalf("load completed steering context: present=%v error=%v", present, err)
	}
	count := 0
	for _, message := range current.Messages {
		if message.InternalID == steering.InternalID {
			count++
		}
	}
	if count != 1 || requests.Load() != 2 {
		t.Fatalf("steering duplicated, lost or replayed: messages=%d requests=%d", count, requests.Load())
	}
}

func TestWorkspaceAgentsCollaborateToRepairAndVerifyProject(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module collaborationproof\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "sum.go"), []byte("package collaborationproof\n\nfunc Sum(values []int) int {\n\treturn 0\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "sum_test.go"), []byte("package collaborationproof\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {\n\tif got := Sum([]int{2, 5, -1}); got != 6 {\n\t\tt.Fatalf(\"Sum = %d, want 6\", got)\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var coordinatorID, implementerID atomic.Value
	var coordinatorRequests, implementerRequests atomic.Int32
	reportPublished := make(chan struct{})
	var reportOnce sync.Once
	writeTool := func(writer http.ResponseWriter, id, name string, arguments map[string]any) {
		encoded, _ := json.Marshal(arguments)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", id, name, string(encoded))
	}
	writeText := func(writer http.ResponseWriter, text string) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", text)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []provider.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
			return
		}
		var transcript strings.Builder
		for _, message := range body.Messages {
			transcript.WriteString(message.ContentString())
			transcript.WriteByte('\n')
		}
		text := transcript.String()
		switch {
		case strings.Contains(text, "Coordinate the Sum repair through workspace collaboration."):
			switch coordinatorRequests.Add(1) {
			case 1:
				writeTool(writer, "send-work", "agent_send", map[string]any{
					"recipient_ids":    []string{implementerID.Load().(string)},
					"kind":             "request",
					"request_id":       "repair-sum",
					"request_revision": 1,
					"body":             "Implement Sum in sum.go so the existing test passes. Publish a final report when verified.",
				})
			case 2:
				select {
				case <-reportPublished:
				case <-request.Context().Done():
					return
				}
				writeTool(writer, "safe-boundary", "get_plan", map[string]any{})
			case 3:
				if !strings.Contains(text, `kind="report_published"`) ||
					!strings.Contains(text, "Sum implementation complete") {
					t.Errorf("coordinator did not receive the report automatically at its next safe boundary:\n%s", text)
				}
				reference := regexp.MustCompile(`<reference kind="report" id="([^"]+)" revision="([0-9]+)"/>`).FindStringSubmatch(text)
				if len(reference) != 3 {
					t.Errorf("automatic report has no exact readable artifact reference")
					writeText(writer, "missing report reference")
					return
				}
				revision, err := strconv.Atoi(reference[2])
				if err != nil {
					t.Error(err)
					return
				}
				writeTool(writer, "read-peer-evidence", "artifact_read", map[string]any{
					"artifact_id": reference[1], "revision": revision,
				})
			case 4:
				if !strings.Contains(text, "Implemented the accumulator in sum.go.") {
					t.Errorf("coordinator did not read the complete peer report before verification")
				}
				writeTool(writer, "verify-project", "execute_command", map[string]any{
					"command": "go test ./...", "cwd": project,
				})
			default:
				if !strings.Contains(text, "ok") {
					t.Errorf("coordinator verification result missing from context:\n%s", text)
				}
				writeText(writer, "The peer repair is verified by the project test.")
			}
		case strings.Contains(text, "Implement Sum in sum.go so the existing test passes."):
			switch implementerRequests.Add(1) {
			case 1:
				writeTool(writer, "accept-sum", "agent_respond", map[string]any{
					"recipient_id": coordinatorID.Load().(string), "request_id": "repair-sum",
					"request_revision": 1, "disposition": "accepted", "body": "Implementing the exact requested repair.",
				})
			case 2:
				writeTool(writer, "edit-sum", "edit_file", map[string]any{
					"path":       filepath.Join(project, "sum.go"),
					"old_string": "\treturn 0",
					"new_string": "\ttotal := 0\n\tfor _, value := range values {\n\t\ttotal += value\n\t}\n\treturn total",
				})
			case 3:
				writeTool(writer, "check-sum", "execute_command", map[string]any{"command": "go test ./...", "cwd": project})
			case 4:
				writeTool(writer, "report-sum", "publish_report", map[string]any{
					"recipient_ids": []string{coordinatorID.Load().(string)},
					"kind":          "final",
					"request_id":    "repair-sum",
					"summary":       "Sum implementation complete",
					"body":          "Implemented the accumulator in sum.go. The coordinator should run the authoritative project test.",
					"references":    []map[string]any{{"kind": "file", "id": "sum.go"}},
				})
			default:
				reportOnce.Do(func() { close(reportPublished) })
				writeText(writer, "Implementation and report publication completed.")
			}
		default:
			t.Errorf("unrecognized agent request:\n%s", text)
			writeText(writer, "unrecognized")
		}
	}))
	defer server.Close()

	owner := NewWorkspace(project, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	defer owner.Close()
	owner.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig(server.URL)

	coordinatorSession, err := owner.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	coordinatorSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	coordinator, err := owner.StartOrchestrator(coordinatorSession.SessionID, coordinatorSender)
	if err != nil {
		t.Fatal(err)
	}
	coordinatorSession.AgentID = coordinator.ID
	owner.SessionsByAgent[coordinator.ID] = coordinatorSession
	coordinatorID.Store(coordinator.ID)
	coordinatorSession.backend.SetYolo(true)

	implementerSession, err := owner.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	implementerSender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	implementer, err := owner.StartOrchestrator(implementerSession.SessionID, implementerSender)
	if err != nil {
		t.Fatal(err)
	}
	implementerSession.AgentID = implementer.ID
	owner.SessionsByAgent[implementer.ID] = implementerSession
	implementerID.Store(implementer.ID)
	implementerSession.backend.SetYolo(true)

	if !coordinatorSession.backend.SubmitInput(coordinator.ID, provider.PlainInput("Coordinate the Sum repair through workspace collaboration.")) {
		t.Fatal("coordinator turn was not admitted")
	}
	waitHistoryAgentDone(t, implementerSender, implementer.ID)
	waitHistoryAgentDone(t, coordinatorSender, coordinator.ID)

	data, err := os.ReadFile(filepath.Join(project, "sum.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "total += value") {
		t.Fatalf("peer edit did not land:\n%s", data)
	}
	if coordinatorRequests.Load() < 5 || implementerRequests.Load() < 5 {
		t.Fatalf("request counts coordinator=%d implementer=%d", coordinatorRequests.Load(), implementerRequests.Load())
	}
	page, err := owner.Collaboration.Page(t.Context(), collaboration.PageQuery{Search: "Sum", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var requestFound, reportFound bool
	for _, event := range page.Events {
		requestFound = requestFound || event.Kind == collaboration.EventWorkRequest
		reportFound = reportFound || event.Kind == collaboration.EventReportPublished
	}
	if !requestFound || !reportFound {
		t.Fatalf("collaboration journal lacks request/report: %+v", page.Events)
	}
}
