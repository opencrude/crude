package fileio

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// ShellExecute opens an executable without test flags. Detect only the owned
// fixture environment before the test runner starts; never launch a second suite.
func TestMain(m *testing.M) {
	if address := os.Getenv("CRUDE_TEST_DESKTOP_GATE"); address != "" {
		gate, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			os.Exit(2)
		}
		if err := gate.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			os.Exit(3)
		}
		if err := json.NewEncoder(gate).Encode(struct {
			PID  int
			Args []string
		}{os.Getpid(), os.Args}); err != nil {
			os.Exit(4)
		}
		_, err = io.Copy(io.Discard, gate)
		_ = gate.Close()
		if err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testDesktopNativeLauncherArguments(t *testing.T) {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	// A real native executable with shell metacharacters in its name must open
	// as one literal operand, not as a command string or a Unix script fixture.
	path := filepath.Join(t.TempDir(), "a space & notes.exe")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(file, source)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	gate, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if err := gate.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_TEST_DESKTOP_GATE", gate.Addr().String())
	if err := OpenDesktopPathContext(t.Context(), path, false); err != nil {
		t.Fatal(err)
	}
	child, err := gate.Accept()
	if err != nil {
		t.Fatal("native ShellExecute did not launch its literal file operand:", err)
	}
	defer child.Close()
	if err := child.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var observed struct {
		PID  int
		Args []string
	}
	if err := json.NewDecoder(child).Decode(&observed); err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(observed.PID))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		status, waitErr := windows.WaitForSingleObject(process, 0)
		if waitErr != nil {
			t.Error(waitErr)
		} else if status == uint32(windows.WAIT_TIMEOUT) {
			if err := windows.TerminateProcess(process, 1); err != nil {
				t.Error(err)
			}
			if status, err := windows.WaitForSingleObject(process, 10_000); err != nil || status != windows.WAIT_OBJECT_0 {
				t.Errorf("could not join the owned desktop helper: status=%d, error=%v", status, err)
			}
		}
		if err := windows.CloseHandle(process); err != nil {
			t.Error(err)
		}
	}()
	if len(observed.Args) != 1 || observed.Args[0] != path {
		t.Fatalf("native launch split or rewrote its path: %q, want [%q]", observed.Args, path)
	}
	_ = child.Close()
	status, err := windows.WaitForSingleObject(process, 10_000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned native desktop helper did not exit: status=%d, error=%v", status, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process, &code); err != nil || code != 0 {
		t.Fatalf("native desktop helper failed: exit=%d, error=%v", code, err)
	}
}

func TestExplorerSelectArgument(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{`C:\simple.txt`, `/select,"C:\simple.txt"`},
		{`C:\a space\file.txt`, `/select,"C:\a space\file.txt"`},
		{`C:\a,b.txt`, `/select,"C:\a,b.txt"`},
		{`C:\folder\`, `/select,"C:\folder\"`},
		{`C:\`, `/select,"C:\"`},
		{`\\server\share\a,b.txt`, `/select,"\\server\share\a,b.txt"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := explorerSelectArgument(tc.path)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := explorerSelectArgument(`C:\invalid"file`); err == nil {
		t.Fatal("invalid quote accepted")
	}
}
