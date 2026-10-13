package mcpclient

import (
	"slices"
	"testing"
)

// Desktop servers (screenshots, input, notifications) crashed with "Could not
// open main display" because DISPLAY was withheld. The launch environment is
// the complete host environment, never a curated name list.
func TestStdioBaseEnvironmentInheritsCompleteHostEnvironment(t *testing.T) {
	t.Setenv("DISPLAY", ":42")
	t.Setenv("CRUDE_ARBITRARY_TOOLCHAIN_SETTING", "kept")
	environment := stdioBaseEnvironment()
	for _, entry := range []string{"DISPLAY=:42", "CRUDE_ARBITRARY_TOOLCHAIN_SETTING=kept"} {
		if !slices.Contains(environment, entry) {
			t.Fatalf("stdio environment omitted %q", entry)
		}
	}
}
