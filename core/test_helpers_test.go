package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func setCoreTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// wireText returns value as it appears inside a JSON request body; Windows
// path separators are escaped there.
func wireText(value string) string {
	encoded, _ := json.Marshal(value)
	return strings.Trim(string(encoded), `"`)
}
