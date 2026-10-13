package mcpclient

import (
	"runtime"
	"strings"
	"testing"
)

func TestStdioBaseEnvironmentInheritsOnlyPlatformBaseline(t *testing.T) {
	t.Setenv("CRUDE_UNRELATED_SECRET", "do-not-leak")
	required := "HOME"
	if runtime.GOOS == "windows" {
		required = "SYSTEMROOT"
		t.Setenv("SYSTEMROOT", `C:\Windows`)
	} else {
		t.Setenv("HOME", "/home/fixture")
	}
	environment := strings.Join(stdioBaseEnvironment(), "\n")
	if !strings.Contains(environment, required+"=") || strings.Contains(environment, "do-not-leak") {
		t.Fatalf("stdio baseline environment: %s", environment)
	}
}
