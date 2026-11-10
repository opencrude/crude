package viewmodel

import (
	"os"
	"testing"

	"crude/internal/testhome"
	"crude/internal/testterminal"
)

func TestMain(m *testing.M) {
	if code, handled := testterminal.Run(); handled {
		os.Exit(code)
	}
	os.Exit(testhome.Run(m))
}
