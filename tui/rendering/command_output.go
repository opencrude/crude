package rendering

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// CommandOutputText is a safe audit-log projection, not another terminal
// emulator. x/ansi consumes complete CSI/OSC/DCS sequences before text hygiene;
// carriage-return progress updates become separate readable lines instead of
// being welded together or overwriting retained evidence. Raw bytes stay in the
// canonical command result. Interactive full-screen programs belong in terminals.
func CommandOutputText(output string) string {
	output = ansi.Strip(output)
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	return SanitizeForTerminal(output)
}
