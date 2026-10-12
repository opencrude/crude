package workspace

import (
	"os"
	"testing"

	"crude/internal/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }
