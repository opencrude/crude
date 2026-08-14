package workspace

import (
	"testing"

	"crude/fileio"
	ttypes "crude/types"
)

type workspaceSwitchDomainLogger struct{}

func (workspaceSwitchDomainLogger) Debug(string, string, ...any) {}
func (workspaceSwitchDomainLogger) Info(string, string, ...any)  {}
func (workspaceSwitchDomainLogger) Warn(string, string, ...any)  {}
func (workspaceSwitchDomainLogger) Error(string, string, ...any) {}

func TestWorkspaceSwitchCancelsSearchWithoutReusingRequestIdentity(t *testing.T) {
	setTestHome(t)
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	ws := NewWorkspace(oldRoot, "model", "user", ttypes.Settings{}, workspaceSwitchDomainLogger{})
	t.Cleanup(ws.Close)

	service := ws.ProjectSearch
	request := service.Begin(oldRoot, "old-query", fileio.ProjectSearchOptions{})
	oldFocus := ws.FocusStack

	if err := ws.SwitchProject(newRoot); err != nil {
		t.Fatal(err)
	}

	select {
	case <-request.ctx.Done():
	default:
		t.Fatal("workspace switch did not cancel the in-flight project search")
	}
	if ws.ProjectSearch != service {
		t.Fatal("switch replaced search identity service; request IDs could collide")
	}
	if service.Accept(ProjectSearchResult{RequestID: request.ID}) {
		t.Fatal("canceled old-project result was accepted")
	}

	next := service.Begin(ws.RootPath, "new-query", fileio.ProjectSearchOptions{})
	if next.ID <= request.ID {
		t.Fatalf("request identity reused across switch: old=%d new=%d", request.ID, next.ID)
	}
	if next.Root != newRoot {
		t.Fatalf("new request root=%q want %q", next.Root, newRoot)
	}
	if ws.FocusStack == oldFocus {
		t.Fatal("project-scoped focus stack survived domain switch")
	}
}
