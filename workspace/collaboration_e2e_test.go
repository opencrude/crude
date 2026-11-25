package workspace

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/tools"
	"crude/internal/providersim"
	"crude/types/command"
	"crude/types/setting"
)

func TestWorkspacePeerMessageWakesExactIdleAgentWithoutPolling(t *testing.T) {
	peerRequest := make(chan providersim.Conversation, 4)
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		peerRequest <- request.Conversation
		return providersim.Text("peer request handled")
	})

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", setting.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
	cfg := historyE2EConfig(server.BaseURL(providersim.Chat))

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
	case dialog := <-peerRequest:
		found := false
		for _, text := range simConversationTexts(dialog) {
			if strings.Contains(text, `origin="peer"`) &&
				strings.Contains(text, published.Event.ID) &&
				strings.Contains(text, "Review the workspace parser.") {
				found = true
			}
		}
		if !found {
			t.Fatalf("provider request did not contain attributed peer event: %+v", dialog)
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
	const memoryModel = "steering-memory-fixture"
	var requests atomic.Int32
	firstGate := providersim.NewGate()
	defer firstGate.Release()
	secondRequest := make(chan providersim.Conversation, 1)
	memoryRequest := make(chan struct{}, 1)
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		if request.Model == memoryModel {
			select {
			case memoryRequest <- struct{}{}:
			default:
			}
			return providersim.Text(`{"memories":[]}`)
		}
		if request.Model != "history-model" {
			t.Errorf("unexpected provider request for model %q", request.Model)
			return providersim.Turn{}
		}
		if requests.Add(1) == 1 {
			return providersim.Text("initial response").WithGate(firstGate)
		}
		select {
		case secondRequest <- request.Conversation:
		default:
			// An unexpected extra root request must be reported by the
			// assertion below, not block its provider handler indefinitely.
		}
		return providersim.Text("steering applied")
	})

	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := NewWorkspace(project, "model", "user", setting.Settings{}, &testLogger{})
	defer workspace.Close()
	workspace.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
	cfg := historyE2EConfig(server.BaseURL(providersim.Chat))
	// Force real background extraction to overlap the assertions. Memory
	// normally inherits the main model, so counting every provider request
	// as an agent turn made this test fail when maintenance ran.
	cfg.Memory.ExtractEveryTurns = 1
	cfg.Memory.MemoryCategory = setting.Category{AccountID: "test-account", Model: memoryModel}
	frontend, err := workspace.CreateSession(cfg, home)
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
	firstWait, cancelFirstWait := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelFirstWait()
	if err := firstGate.Wait(firstWait); err != nil {
		t.Fatal("initial provider request did not start", err)
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
	firstGate.Release()
	waitHistoryAgentDone(t, sender, root.ID)
	select {
	case <-memoryRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("background memory extraction did not reach its provider")
	}

	var dialog providersim.Conversation
	select {
	case dialog = <-secondRequest:
	case <-time.After(2 * time.Second):
		t.Fatal("human steering did not trigger a safe-boundary continuation")
	}
	found := false
	for _, content := range simConversationTexts(dialog) {
		if strings.Contains(content, `origin="human"`) &&
			strings.Contains(content, `kind="human_steering"`) &&
			strings.Contains(content, steeringEvent.ID) &&
			strings.Contains(content, steeringEvent.RunID) &&
			strings.Contains(content, "prioritize the parser edge case") {
			found = true
		}
	}
	if !found {
		t.Fatalf("second request lacked attributed human steering: %+v", dialog)
	}
	for _, expected := range []string{"begin work", "initial response", "prioritize the parser edge case"} {
		count := 0
		for _, message := range dialog.Messages {
			count += strings.Count(simMessageText(message), expected)
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

	// Prove that the original project fails for the intended reason. This also
	// keeps a cold Go toolchain build out of the bounded collaboration wait.
	preflight, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	broken := exec.CommandContext(preflight, "go", "test", "-count=1", "./...")
	broken.Dir = project
	out, err := broken.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Sum = 0, want 6") {
		t.Fatalf("the fixture must fail before repair: %v\n%s", err, out)
	}
	const verify = "go test -count=1 ./..."
	const verifyTimeout = 20 * time.Second // stay below synchronous command promotion
	checkResult := func(messages []providersim.Message, callID string) {
		for _, msg := range messages {
			if msg.Role != "tool" {
				continue
			}
			for _, part := range msg.Parts {
				if part.Kind != providersim.PartToolResult || part.CallID != callID {
					continue
				}
				result, ok := tools.ParseCommandResult("execute_command", part.Text)
				fields := strings.Fields(result.Output)
				if !ok || result.Command != verify || result.OutputDescriptor.Status != command.Completed || result.ExitCode == nil || *result.ExitCode != 0 ||
					len(fields) < 2 || fields[0] != "ok" || fields[1] != "collaborationproof" {
					t.Errorf("%s did not return a passing project test: %s", callID, part.Text)
				}
				return
			}
		}
		t.Errorf("provider did not receive the verification result for %s", callID)
	}

	var coordinatorID, implementerID atomic.Value
	var coordinatorRequests, implementerRequests atomic.Int32
	reportPublished := providersim.NewGate()
	defer reportPublished.Release()
	toolTurn := func(id, name string, arguments map[string]any) providersim.Turn {
		encoded, _ := json.Marshal(arguments)
		return providersim.CallTools(providersim.ToolCall{ID: id, Name: name, Arguments: string(encoded)})
	}
	runVerification := func(id string) providersim.Turn {
		return toolTurn(id, "execute_command", map[string]any{
			"command": verify, "cwd": project, "timeout_seconds": int(verifyTimeout / time.Second),
		})
	}
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		text := strings.Join(simConversationTexts(request.Conversation), "\n")
		switch {
		case strings.Contains(text, "Coordinate the Sum repair through workspace collaboration."):
			switch coordinatorRequests.Add(1) {
			case 1:
				return toolTurn("send-work", "agent_send", map[string]any{
					"recipient_ids":    []string{implementerID.Load().(string)},
					"kind":             "request",
					"request_id":       "repair-sum",
					"request_revision": 1,
					"body":             "Implement Sum in sum.go so the existing test passes. Publish a final report when verified.",
				})
			case 2:
				return toolTurn("safe-boundary", "get_plan", map[string]any{}).WithGate(reportPublished)
			case 3:
				if !strings.Contains(text, `kind="report_published"`) ||
					!strings.Contains(text, "Sum implementation complete") {
					t.Errorf("coordinator did not receive the report automatically at its next safe boundary:\n%s", text)
				}
				reference := regexp.MustCompile(`<reference kind="report" id="([^"]+)" revision="([0-9]+)"/>`).FindStringSubmatch(text)
				if len(reference) != 3 {
					t.Errorf("automatic report has no exact readable artifact reference")
					return providersim.Text("missing report reference")
				}
				revision, err := strconv.Atoi(reference[2])
				if err != nil {
					t.Error(err)
					return providersim.Turn{}
				}
				return toolTurn("read-peer-evidence", "artifact_read", map[string]any{
					"artifact_id": reference[1], "revision": revision,
				})
			case 4:
				if !strings.Contains(text, "Implemented the accumulator in sum.go.") {
					t.Errorf("coordinator did not read the complete peer report before verification")
				}
				return runVerification("verify-project")
			default:
				checkResult(request.Messages, "verify-project")
				return providersim.Text("The peer repair is verified by the project test.")
			}
		case strings.Contains(text, "Implement Sum in sum.go so the existing test passes."):
			switch implementerRequests.Add(1) {
			case 1:
				return toolTurn("accept-sum", "agent_respond", map[string]any{
					"recipient_id": coordinatorID.Load().(string), "request_id": "repair-sum",
					"request_revision": 1, "disposition": "accepted", "body": "Implementing the exact requested repair.",
				})
			case 2:
				return toolTurn("edit-sum", "edit_file", map[string]any{
					"path":       filepath.Join(project, "sum.go"),
					"old_string": "\treturn 0",
					"new_string": "\ttotal := 0\n\tfor _, value := range values {\n\t\ttotal += value\n\t}\n\treturn total",
				})
			case 3:
				return runVerification("check-sum")
			case 4:
				checkResult(request.Messages, "check-sum")
				return toolTurn("report-sum", "publish_report", map[string]any{
					"recipient_ids": []string{coordinatorID.Load().(string)},
					"kind":          "final",
					"request_id":    "repair-sum",
					"summary":       "Sum implementation complete",
					"body":          "Implemented the accumulator in sum.go. The coordinator should run the authoritative project test.",
					"references":    []map[string]any{{"kind": "file", "id": "sum.go"}},
				})
			default:
				reportPublished.Release()
				return providersim.Text("Implementation and report publication completed.")
			}
		default:
			t.Errorf("unrecognized agent request:\n%s", text)
			return providersim.Text("unrecognized")
		}
	})

	owner := NewWorkspace(project, "history-model", "tester", setting.Settings{}, &testLogger{})
	defer owner.Close()
	owner.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
	cfg := historyE2EConfig(server.BaseURL(providersim.Chat))

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
	waitHistoryAgentDoneAfterCommand(t, implementerSender, implementer.ID, "check-sum", verifyTimeout)
	waitHistoryAgentDoneAfterCommand(t, coordinatorSender, coordinator.ID, "verify-project", verifyTimeout)

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
