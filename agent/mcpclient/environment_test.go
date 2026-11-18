package mcpclient

import (
	"reflect"
	"slices"
	"testing"

	captypes "crude/types/capability"
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

func TestMissingConfigurationUsesActualTransportInputs(t *testing.T) {
	t.Setenv("CRUDE_MCP_READY_TEST", "host-value")
	base := captypes.ConnectionConfig{Transport: "stdio", RequiredEnv: []string{"CRUDE_MCP_READY_TEST"}}
	for _, test := range []struct {
		name string
		edit func(*captypes.ConnectionConfig)
		want []string
	}{
		{"inherited", func(*captypes.ConnectionConfig) {}, nil},
		{"explicit-empty-overrides", func(c *captypes.ConnectionConfig) { c.Environment = map[string]string{"CRUDE_MCP_READY_TEST": ""} }, []string{"environment CRUDE_MCP_READY_TEST"}},
		{"credential-reference", func(c *captypes.ConnectionConfig) {
			c.SecretRefs = map[string]string{"CRUDE_MCP_READY_TEST": "env:OTHER"}
		}, nil},
		{"shell-defers-to-host", func(c *captypes.ConnectionConfig) { c.Host = "shell"; c.RequiredEnv = []string{"ABSENT_ON_CORE"} }, nil},
		{"http-does-not-inherit-headers", func(c *captypes.ConnectionConfig) {
			c.Transport = "http"
			c.RequiredEnv = nil
			c.Headers = map[string]string{"CRUDE_MCP_READY_TEST": ""}
		}, []string{"header CRUDE_MCP_READY_TEST"}},
		{"header-reference", func(c *captypes.ConnectionConfig) {
			c.Transport = "http"
			c.RequiredEnv = nil
			c.Headers = map[string]string{"Authorization": ""}
			c.SecretRefs = map[string]string{"Authorization": "env:TOKEN"}
		}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.edit(&config)
			if got := MissingConfiguration(config); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("missing = %v, want %v", got, test.want)
			}
		})
	}
}
