package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/core/shellport/remote"
	"crude/internal/providersim"
	captypes "crude/types/capability"
	cmdtypes "crude/types/command"
	"crude/types/control"
	"crude/types/setting"
	"crude/workspace"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func shellJourneyImage() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return b.Bytes()
}

// This is a real SDK executable, not a core transport stub. Its evidence exists
// only in the explicitly chosen shell-local directory.
func TestCapabilityHostJourneySDKHelper(t *testing.T) {
	if os.Getenv("CRUDE_HOST_JOURNEY_HELPER") != "yes" {
		return
	}
	appendLog := func(message string) {
		f, err := os.OpenFile("host.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(3)
		}
		_, _ = fmt.Fprintln(f, message)
		_ = f.Close()
	}
	cwd, _ := os.Getwd()
	appendLog("launch:" + cwd)
	server := mcp.NewServer(&mcp.Implementation{Name: "shell-journey", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "picture", Description: "Read shell-only image evidence", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		appendLog("call")
		data, err := os.ReadFile("shell-only.txt")
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}, &mcp.ImageContent{MIMEType: "image/png", Data: shellJourneyImage()}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

// journeyActivation reads the activation result the way the model receives
// it: the settled job's outcome carries the activation result as its output.
func journeyActivation(dialog providersim.Conversation, id string) (captypes.ActivationResult, bool) {
	for _, message := range dialog.Messages {
		text := html.UnescapeString(simMessageText(message))
		start := strings.Index(text, "{")
		if start < 0 {
			continue
		}
		var outcome cmdtypes.Result
		if json.NewDecoder(strings.NewReader(text[start:])).Decode(&outcome) != nil {
			continue
		}
		for _, line := range strings.Split(outcome.Output, "\n") {
			var activation captypes.ActivationResult
			if json.Unmarshal([]byte(line), &activation) == nil && activation.ConnectionID == id {
				return activation, true
			}
		}
	}
	return captypes.ActivationResult{}, false
}

func TestCapabilityHostFramedTwoShellModelJourney(t *testing.T) {
	t.Setenv("CRUDE_HOST_JOURNEY_HELPER", "yes")
	root, directory := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "shell-only.txt"), []byte("EXACT_SHELL_LOCAL_EVIDENCE"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connection := captypes.ConnectionConfig{ID: "shell-picture", Name: "Shell picture", Host: "shell", Directory: directory, Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestCapabilityHostJourneySDKHelper$"}, SecretRefs: map[string]string{"CRUDE_HOST_JOURNEY_HELPER": "env:CRUDE_HOST_JOURNEY_HELPER"}, Scope: captypes.ScopeWorkspace}
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{connection.ID: connection}})
	var requests atomic.Int32
	finalRequest := make(chan providersim.Request, 1)
	gate := providersim.NewGate()
	defer gate.Release()
	endpoint := providersim.New(t)
	endpoint.Respond(func(request providersim.Request) providersim.Turn {
		step := requests.Add(1)
		// The model acts on what its history shows. Launching the shell server
		// is a background job, so the activation result arrives as that job's
		// outcome, and the model waits for it as an agent would.
		called := map[string]bool{}
		for _, message := range request.Messages {
			for _, part := range message.Parts {
				if part.Kind == providersim.PartToolCall {
					called[part.Call.Name] = true
				}
			}
		}
		_, observed := journeyActivation(request.Conversation, connection.ID)
		var dynamic string
		for _, tool := range request.Tools {
			if strings.HasPrefix(tool.Name, "mcp_") && strings.Contains(tool.Name, "picture") {
				dynamic = tool.Name
			}
		}
		name := "capability_activate"
		args := map[string]any{"id": connection.ID}
		switch {
		case !called["capability_activate"]:
		case !observed:
			if step > 6 {
				t.Errorf("activation outcome never reached the model: %+v", request.Messages)
				return providersim.Turn{}
			}
			name, args = "wait", map[string]any{"seconds": 20}
		case dynamic != "" && !called[dynamic]:
			name, args = dynamic, map[string]any{}
		case dynamic == "":
			t.Errorf("dynamic SDK tool not offered: %+v", request.Tools)
			return providersim.Turn{}
		default:
			finalRequest <- request
			return providersim.Text("Inspected.").WithGate(gate)
		}
		raw, _ := json.Marshal(args)
		return providersim.CallTools(providersim.ToolCall{ID: fmt.Sprintf("host-%d", step), Name: name, Arguments: string(raw)})
	})
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	profile.Categories["default"] = session.CategoryConfig{AccountID: "test", Model: "host-media", ModelSupportsTools: true, ModelModalities: []string{provider.ModalityImage}}
	agents.Profiles[agents.ActiveProfile] = profile
	cfg := &session.Config{Agents: agents, Capabilities: settings, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: endpoint.BaseURL(providersim.Chat), APIStandard: "openai", AuthType: provider.AuthProtocol}}}}
	var ws *workspace.Workspace
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		ws = workspace.NewWorkspace(root, "", "test", setting.Settings{Capabilities: settings}, coreTestLogger{})
		ws.SetAppDefaults(setting.App{Capabilities: settings})
		ws.SetBackendConfig(cfg, t.TempDir())
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var clients []*coreclient.Client
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		c := controlWireClient(t, runtime, id)
		clients = append(clients, c)
		return c, nil
	}
	for _, id := range []string{"target", "peer"} {
		shell, err := remote.Attach(ctx, remote.Options{Root: root, ClientID: id, Dial: dial})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = shell.Close() }()
		shell.SetEventBus(workspace.NewEventBus())
		shell.StartReplica()
	}
	target, peer := clients[0], clients[1]
	// Ordered registration acknowledgement removes worker-start scheduling from
	// the acceptance input. StartReplica itself remains the worker owner.
	var attached coreprotocol.AttachResult
	if err := target.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := target.RegisterCapabilityHost(ctx, attached.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := target.Call(ctx, attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{}, &created); err != nil || created.Error != "" {
		t.Fatal(created, err)
	}
	ws.BackendSession(created.SessionID).SetYolo(true)
	if err := target.Call(ctx, attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Activate shell-picture, inspect the image.", DraftRevision: 1}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-finalRequest:
		wantImage := "data:image/png;base64," + base64.StdEncoding.EncodeToString(shellJourneyImage())
		foundImage := false
		for _, message := range request.Messages {
			for _, part := range message.Parts {
				foundImage = foundImage || part.Kind == providersim.PartImage && part.ImageURL == wantImage
			}
		}
		if !foundImage || !simConversationContains(request.Conversation, "EXACT_SHELL_LOCAL_EVIDENCE") {
			t.Fatal("next actual model request lost shell image or evidence", request.Messages)
		}
	case <-ctx.Done():
		t.Fatal("model journey did not reach image evidence", ctx.Err())
	}
	owner := control.ShellOwner{ClientID: target.Owner().ClientID, Epoch: target.Owner().Epoch}
	participant, err := ws.Collaboration.Agent(ctx, created.OrchID)
	if err != nil || participant.CurrentRunID == "" {
		t.Fatal("actual root runtime unavailable", err)
	}
	request := captypes.HostRequest{WorkspaceID: attached.WorkspaceID, Actor: captypes.RuntimeActor{ID: created.OrchID, SessionID: created.SessionID, RunID: participant.CurrentRunID, RuntimeEpoch: participant.RuntimeEpoch, TurnGeneration: 1, ExecutionRoot: root, WorkspaceID: attached.WorkspaceID, ShellOwner: &owner, Effects: []captypes.Effect{captypes.EffectRead}}, Connection: connection, Operation: "prepare"}
	for i := 0; i < 300; i++ {
		request.ID = fmt.Sprintf("host-prepare-%d", i)
		reply, err := runtime.DispatchHost(ctx, request)
		if err != nil || reply.Error != "" {
			t.Fatalf("framed prepare %d: %v %s", i, err, reply.Error)
		}
	}
	if err := peer.ReplyCapabilityHost(ctx, attached.WorkspaceID, captypes.HostReply{ID: request.ID}); err == nil {
		t.Fatal("peer accepted target response")
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	request.ID = "after-disconnect"
	if reply, err := runtime.DispatchHost(ctx, request); err == nil && reply.Error == "" {
		t.Fatal("disconnected target fell back")
	}
	log, err := os.ReadFile(filepath.Join(directory, "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(log) != "launch:"+directory+"\ncall\n" {
		t.Fatalf("shell launch/call replay or wrong cwd: %q", log)
	}
}
