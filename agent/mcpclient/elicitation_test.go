package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func formPeer(calls *atomic.Int32) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "form peer", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "form", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		var args struct {
			State *string
			Kind  string
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
		if req.Params.InputResponses != nil {
			if !reflect.DeepEqual(args.State, req.Params.RequestState) {
				return nil, errors.New("request state changed")
			}
			r, ok := req.Params.InputResponses["data"].(*mcp.ElicitResult)
			if !ok {
				return nil, errors.New("no form response")
			}
			b, _ := json.Marshal(r)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
		}
		if req.Params.RequestState != nil {
			return nil, errors.New("state leaked to an initial request")
		}
		if args.Kind == "unrelated" {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "unrelated"}}}, nil
		}
		request := &mcp.ElicitParams{Message: "Choose your public display name.", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":2},"notify":{"type":"boolean","default":true}},"required":["name"]}`)}
		input := mcp.InputRequestMap{"data": request}
		switch args.Kind {
		case "private":
			request.Message = "Please enter an API key."
		case "sampling":
			input["other"] = &mcp.CreateMessageWithToolsParams{}
		case "busy":
			input = mcp.InputRequestMap{}
		}
		return &mcp.CallToolResult{RequestState: args.State, InputRequests: input}, nil
	})
	return s
}

func TestFormStdioHelper(t *testing.T) {
	if os.Getenv("CRUDE_FORM_PEER") != "enabled" {
		return
	}
	if err := formPeer(new(atomic.Int32)).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func formFixture(t *testing.T, stdio bool) (*Manager, captypes.ConnectionConfig, captypes.Tool, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	cfg := captypes.ConnectionConfig{ID: "peer", Name: "Form peer"}
	if stdio {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Transport, cfg.Command, cfg.LaunchApproved = "stdio", exe, true
		cfg.Args = []string{"-test.run=^TestFormStdioHelper$"}
		cfg.Environment = map[string]string{"CRUDE_FORM_PEER": "enabled"}
	} else {
		s := formPeer(calls)
		endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true}))
		t.Cleanup(endpoint.Close)
		cfg.Transport, cfg.URL = "http", endpoint.URL
	}
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.Configure(map[string]captypes.ConnectionConfig{cfg.ID: cfg})
	if err := m.Connect(t.Context(), cfg.ID); err != nil {
		t.Fatal(err)
	}
	return m, cfg, m.Catalog()[0].Tools[0], calls
}

func TestFormRequestStateOverManagedTransports(t *testing.T) {
	for _, stdio := range []bool{false, true} {
		t.Run(fmt.Sprintf("stdio=%t", stdio), func(t *testing.T) {
			m, _, tool, _ := formFixture(t, stdio)
			ctx, stop := context.WithTimeout(t.Context(), 8*time.Second)
			defer stop()
			states := []*string{nil, new(""), new("  opaque { =%\n☃  ")}
			var wg sync.WaitGroup
			ready := make(chan struct{}, len(states))
			release := make(chan struct{})
			for i, state := range states {
				wg.Go(func() {
					name := fmt.Sprintf("User %d", i)
					owner := captypes.WithFormHandler(ctx, tool.Name, func(ctx context.Context, f captypes.Form) captypes.FormReply {
						if f.ConnectionID != "peer" || f.Tool != "form" || len(f.Fields) != 2 {
							t.Errorf("unattributed form %+v", f)
						}
						ready <- struct{}{}
						select {
						case <-release:
						case <-ctx.Done():
						}
						return captypes.FormReply{Action: "accept", Values: map[string]string{"name": name}}
					})
					r, err := m.Call(owner, tool, map[string]any{"state": state})
					want := fmt.Sprintf(`{"action":"accept","content":{"name":%q}}`, name)
					if err != nil || len(r.Content) != 1 || r.Content[0].Text != want {
						t.Errorf("response=%+v err=%v want=%s", r, err, want)
					}
				})
			}
			for range states {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("missing form")
				}
			}
			r, err := m.Call(t.Context(), tool, map[string]any{"kind": "unrelated"})
			close(release)
			wg.Wait()
			if err != nil || len(r.Content) != 1 || r.Content[0].Text != "unrelated" {
				t.Fatalf("call during human wait: %+v %v", r, err)
			}
		})
	}
}

// A server deadline limits remote work, not how long a person may take to
// decide whether to send data. The owning caller still sets the overall deadline.
func TestFormWaitDoesNotConsumeServerTimeout(t *testing.T) {
	m, cfg, _, calls := formFixture(t, false)
	cfg.TimeoutSeconds = 1
	m.Configure(map[string]captypes.ConnectionConfig{cfg.ID: cfg})
	if err := m.Connect(t.Context(), cfg.ID); err != nil {
		t.Fatal(err)
	}
	tool := m.Catalog()[0].Tools[0]
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := captypes.WithFormHandler(ctx, tool.Name, func(questionCtx context.Context, _ captypes.Form) captypes.FormReply {
		select {
		case <-time.After(1100 * time.Millisecond):
			return captypes.FormReply{Action: "accept", Values: map[string]string{"name": "Robin"}}
		case <-questionCtx.Done():
			return captypes.FormReply{Action: "cancel"}
		}
	})
	result, err := m.Call(owner, tool, nil)
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != `{"action":"accept","content":{"name":"Robin"}}` || calls.Load() != 2 {
		t.Fatalf("a human answer outlived the server timeout: calls=%d, result=%+v, err=%v", calls.Load(), result, err)
	}
}

func TestFormScalarAndEnumAnswers(t *testing.T) {
	if _, err := newServerForm("peer", "profile", &mcp.ElicitParams{Message: "Choose a color.", RequestedSchema: json.RawMessage(`{"type":"object","properties":{"color":{"type":"string","oneOf":[null]}}}`)}); err == nil {
		t.Fatal("null enum entry accepted")
	}
	f, err := newServerForm("peer", "profile", &mcp.ElicitParams{Message: "Choose your public profile details.", RequestedSchema: json.RawMessage(`{
		"type":"object","properties":{
		"age":{"type":"integer","minimum":1},"score":{"type":"number"},"enabled":{"type":"boolean"},
		"nick":{"type":"string"},"color":{"type":"string","oneOf":[{"const":"#abc","title":"Blue"}]},
		"colors":{"type":"array","items":{"type":"string","enum":["red","green"]},"minItems":2},
		"omit":{"type":"boolean","default":true}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	reply := captypes.FormReply{Action: "accept", Values: map[string]string{"age": "21", "score": "2.5", "enabled": "false", "nick": "Fox", "color": "#abc", "colors": "green, red"}}
	got, err := f.answer(reply)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"age": int64(21), "score": 2.5, "enabled": false, "nick": "Fox", "color": "#abc", "colors": []string{"green", "red"}}
	if !reflect.DeepEqual(got.Content, want) {
		t.Fatalf("typed content = %#v", got.Content)
	}
	reply.Values["colors"] = "not listed"
	if _, err = f.answer(reply); err == nil {
		t.Fatal("out-of-schema enum sent")
	}
}

func TestFormDeclineAndCancelSendNoAnswers(t *testing.T) {
	m, _, tool, calls := formFixture(t, false)
	for _, action := range []string{"decline", "cancel"} {
		owner := captypes.WithFormHandler(t.Context(), tool.Name, func(context.Context, captypes.Form) captypes.FormReply {
			return captypes.FormReply{Action: action, Values: map[string]string{"name": "discarded"}}
		})
		r, err := m.Call(owner, tool, nil)
		if err != nil || len(r.Content) != 1 || r.Content[0].Text != fmt.Sprintf(`{"action":%q}`, action) {
			t.Fatalf("response %+v %v", r, err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("calls %d", calls.Load())
	}
}

func TestFormCannotBorrowAuthorityOrRepeatMutation(t *testing.T) {
	for _, mode := range []string{"no-owner", "other-call", "private", "sampling", "busy", "invalid", "credentials", "disabled", "cancelled", "replaced", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			m, cfg, tool, calls := formFixture(t, false)
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			name := tool.Name
			if mode == "other-call" {
				name += "X"
			}
			asked := false
			if mode != "no-owner" {
				ctx = captypes.WithFormHandler(ctx, name, func(context.Context, captypes.Form) captypes.FormReply {
					asked = true
					v := "User"
					switch mode {
					case "disabled":
						cfg.Grants = map[string]captypes.ToolGrant{"form": {Disabled: true}}
						m.Configure(map[string]captypes.ConnectionConfig{"peer": cfg})
					case "cancelled":
						stop()
					case "replaced":
						if err := m.Disconnect("peer"); err != nil {
							t.Fatal(err)
						}
						if err := m.Connect(t.Context(), "peer"); err != nil {
							t.Fatal(err)
						}
					case "invalid":
						v = "x"
					case "credentials":
						v = "Bearer private-input"
					}
					return captypes.FormReply{Action: "accept", Values: map[string]string{"name": v}}
				})
			}
			if mode == "revoked" {
				ctx = captypes.WithFormGuard(ctx, func(context.Context, string) error {
					if asked {
						return errors.New("revoked")
					}
					return nil
				})
			}
			r, err := m.Call(ctx, tool, map[string]any{"kind": mode})
			if err == nil || !r.IsError || calls.Load() != 1 || strings.Contains(err.Error(), "private-input") {
				t.Fatalf("unexpected replay, disclosure or success: calls=%d result=%+v error=%v", calls.Load(), r, err)
			}
			shouldAsk := mode == "disabled" || mode == "cancelled" || mode == "replaced" || mode == "revoked" || mode == "invalid" || mode == "credentials"
			if asked != shouldAsk {
				t.Fatalf("asked=%t", asked)
			}
		})
	}
}
