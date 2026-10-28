package tools

import (
	"testing"

	"crude/types"
)

func TestProjectPathsRelativeAcrossPlatforms(t *testing.T) {
	for _, tc := range []struct{ name, root, path, want string }{
		{"posix file", "/home/me/project", "/home/me/project/src/main.go", "src/main.go"},
		{"posix project", "/home/me/project/", "/home/me/project", "."},
		{"posix filesystem root", "/", "/src/main.go", "src/main.go"},
		{"posix prefix sibling", "/home/me/project", "/home/me/project-old/main.go", "/home/me/project-old/main.go"},
		{"posix other", "/home/me/project", "/etc/config", "/etc/config"},
		{"posix case sensitive", "/home/me/project", "/home/me/Project/file", "/home/me/Project/file"},
		{"posix already relative", "/home/me/project", "src/main.go", "src/main.go"},
		{"posix parent", "/home/me/project", "/home/me/project//link/../outside", "/home/me/project//link/../outside"},
		{"posix backslash is a filename", "/home/me/project", `/home/me/project/src\file.go`, `src\file.go`},
		{"spaces and unicode", "/home/me/my project", "/home/me/my project/界/a file.go", "界/a file.go"},
		{"drive", `C:\Users\Me\Project`, `C:\Users\Me\Project\src\main.go`, `src\main.go`},
		{"drive case and mixed separators", `C:\Users\Me\Project`, `c:/users/ME/project/src\file.go`, `src\file.go`},
		{"drive root", `C:\`, `c:\src\main.go`, `src\main.go`},
		{"drive project", `C:\Project\`, `c:/project/`, "."},
		{"different drive", `C:\Project`, `D:\Project\main.go`, `D:\Project\main.go`},
		{"drive relative", `C:\Project`, `C:Project\main.go`, `C:Project\main.go`},
		{"windows prefix sibling", `C:\Project`, `C:\Project-old\main.go`, `C:\Project-old\main.go`},
		{"windows parent", `C:\Project`, `C:\Project\link\..\outside`, `C:\Project\link\..\outside`},
		{"unc", `\\Server\Share\Project`, `\\server\SHARE\project\src\a.go`, `src\a.go`},
		{"unc forward slashes", "//server/share/project", "//SERVER/share/project/src/a.go", "src/a.go"},
		{"different unc share", `\\Server\Share\Project`, `\\Server\Other\Project\a.go`, `\\Server\Other\Project\a.go`},
		{"invalid root", "project", "/home/me/project/file", "/home/me/project/file"},
		{"invalid unc", `\\Server`, `\\Server\Share\a`, `\\Server\Share\a`},
		{"root parent traversal", "/home/me/../project", "/home/me/../project/file", "/home/me/../project/file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewProjectPaths(tc.root).Relative(tc.path); got != tc.want {
				t.Fatalf("root %q path %q: got %q, want %q", tc.root, tc.path, got, tc.want)
			}
		})
	}
}

func TestProjectPathsOnlyFormatsPathOperands(t *testing.T) {
	p := NewProjectPaths("/home/me/project")
	for _, tc := range []struct{ name, input, scope, operand, want string }{
		{"read_file", "", "", "/home/me/project/src/a.go line 2-5", "src/a.go line 2-5"},
		{"write_file", "", "", "/home/me/project/src/a.go", "src/a.go"},
		{"grep_files", "in /home/me/project", "/home/me/project/src", `"in /home/me/project" in /home/me/project/src`, `"in /home/me/project" in src`},
		{"grep_files", "", "/home/me/project/src", "/home/me/project/src", "src"},
		{"grep_files", "", "", `"/home/me/project"`, `"/home/me/project"`},
		{"move_file", "", "", "/home/me/project/a → /elsewhere/b", "a → /elsewhere/b"},
		{"copy_file", "", "", "/elsewhere/a → /home/me/project/b", "/elsewhere/a → b"},
		{"apply_patch", "", "", "/home/me/project/a, /elsewhere/b, /home/me/project/c", "a, /elsewhere/b, c"},
		{"lsp", "", "", "definition · Symbol · /home/me/project/src/a.go", "definition · Symbol · src/a.go"},
		{"lsp", "", "", "/home/me/project/src/a.go", "/home/me/project/src/a.go"},
		{"execute_command", "", "", "cd /home/me/project && go test", "cd /home/me/project && go test"},
		{"web_search", "", "", "/home/me/project", "/home/me/project"},
		{"glob_files", "", "", "/home/me/project/**/*.go", "/home/me/project/**/*.go"},
		{"unknown_tool", "", "", "/home/me/project/private", "/home/me/project/private"},
	} {
		t.Run(tc.name+"/"+tc.operand, func(t *testing.T) {
			msg := types.ChatMessage{ToolName: tc.name, ToolInput: tc.input, ToolScope: tc.scope}
			if got := p.ToolOperand(&msg, tc.operand); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	streaming := types.ChatMessage{ToolName: "lsp", ToolProgressLabel: "/home/me/project/src/a.go"}
	if got := p.ToolOperand(&streaming, streaming.ToolProgressLabel); got != "src/a.go" {
		t.Fatalf("streaming LSP path = %q", got)
	}
}

func TestProjectPathSimpleOperandDoesNotAllocate(t *testing.T) {
	p := NewProjectPaths("/home/me/project")
	msg := types.ChatMessage{ToolName: "read_file"}
	if allocs := testing.AllocsPerRun(100, func() {
		if got := p.ToolOperand(&msg, "/home/me/project/src/main.go"); got != "src/main.go" {
			t.Fatal(got)
		}
	}); allocs != 0 {
		t.Fatalf("simple path formatting allocated %v times", allocs)
	}
}

func BenchmarkProjectPathSimpleOperand(b *testing.B) {
	p := NewProjectPaths("/home/me/project")
	msg := types.ChatMessage{ToolName: "read_file"}
	b.ReportAllocs()
	for b.Loop() {
		_ = p.ToolOperand(&msg, "/home/me/project/src/main.go")
	}
}
