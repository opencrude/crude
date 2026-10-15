package mcpclient

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExecutionRootStdioHelper(t *testing.T) {
	if os.Getenv("CRUDE_EXECUTION_ROOT_HELPER") != "execution-root-helper-enabled" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "execution-root", Version: "1"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{"2025-11-25"}})
	server.AddTool(&mcp.Tool{Name: "where", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		pwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		//nolint:staticcheck // Fixture explicitly negotiates 2025-11-25 to verify the still-supported roots contract, not the newer protocol.
		roots, err := request.Session.ListRoots(ctx, nil)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(map[string]any{"pwd": pwd, "roots": roots.Roots})
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestOfficialSDKBindsCwdAndRootsWithoutRebasingAbsoluteDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	first, second, absolute := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{first, second} {
		if err := os.Mkdir(filepath.Join(root, "relative"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{"", "relative", absolute} {
			manager := New(root)
			t.Setenv("CRUDE_EXECUTION_ROOT_HELPER", "execution-root-helper-enabled")
			config := captypes.ConnectionConfig{ID: "root", Name: "Root fixture", Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestExecutionRootStdioHelper$"}, RequiredEnv: []string{"CRUDE_EXECUTION_ROOT_HELPER"}, Directory: directory, LaunchApproved: true}
			manager.Configure(map[string]captypes.ConnectionConfig{"root": config})
			if err := manager.Connect(t.Context(), "root"); err != nil {
				manager.Close()
				t.Fatal(err)
			}
			tool := manager.States()[0].Tools[0]
			tool.Selected, tool.Effect, tool.Scope = true, captypes.EffectRead, "fixture"
			grant := captypes.ToolGrant{Fingerprint: tool.Fingerprint, Effect: tool.Effect, Scope: tool.Scope}
			result, err := manager.CallAuthorized(t.Context(), tool, nil, config, grant)
			manager.Close()
			if err != nil {
				t.Fatal(err)
			}
			var evidence struct {
				Pwd   string `json:"pwd"`
				Roots []struct {
					URI string `json:"uri"`
				} `json:"roots"`
			}
			if err := json.Unmarshal([]byte(result.Content[0].Text), &evidence); err != nil {
				t.Fatal(err)
			}
			expected := root
			if directory == "relative" {
				expected = filepath.Join(root, directory)
			}
			if directory == absolute {
				expected = absolute
			}
			if evidence.Pwd != expected || len(evidence.Roots) != 1 || evidence.Roots[0].URI != (&url.URL{Scheme: "file", Path: filepath.ToSlash(root)}).String() {
				t.Fatalf("root=%s directory=%s evidence=%+v", root, directory, evidence)
			}
		}
	}
}
