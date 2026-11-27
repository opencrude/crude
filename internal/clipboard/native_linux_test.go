//go:build linux && !android

package clipboard

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if code, ok := RunHelper(os.Args[1:]); ok {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// The test display is disposable: this test intentionally replaces its selection.
func TestNativeSelectionAcrossProcesses(t *testing.T) {
	if os.Getenv("CRUDE_TEST_DISPOSABLE_DISPLAY") != "1" {
		t.Skip("requires an isolated X11 server or Wayland compositor")
	}
	t.Setenv("PATH", t.TempDir())
	for _, want := range []string{"日本語 π\nline two", strings.Repeat("κ", 220000), "final owner"} {
		if err := WriteText(want); err != nil {
			t.Fatal(err)
		}
		until := time.Now().Add(2 * time.Second)
		for {
			if got := string(Read(FmtText)); got == want {
				break
			}
			if time.Now().After(until) {
				t.Fatal("another native client could not read the current selection")
			}
		}
	}
}
