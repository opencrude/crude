package tools

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"crude/types/command"
)

// CommandOutput never mistakes transport base64 for a command's text. Unknown
// encodings and binary bytes remain explicit, inspectable evidence.
func CommandOutput(result command.Result) string {
	output := result.Output
	if result.OutputEncoding != "" {
		if result.OutputEncoding != "base64" {
			return "Unsupported output encoding: " + result.OutputEncoding
		}
		raw, err := base64.StdEncoding.DecodeString(output)
		if err != nil {
			return "Command output could not be decoded: " + err.Error()
		}
		output = string(raw)
	}
	if !utf8.ValidString(output) || strings.ContainsRune(output, '\x00') {
		return fmt.Sprintf("Binary or non-UTF-8 output (%d bytes; hexadecimal):\n%s", len(output), hex.Dump([]byte(output)))
	}
	return output
}

func CommandMetadata(result command.Result) string {
	d := result.OutputDescriptor
	parts := []string{string(d.Status)}
	if result.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *result.ExitCode))
	} else if d.Status == command.Running {
		parts = append(parts, "exit unknown")
	} else if d.Status == command.Killed {
		parts = append(parts, "exit unavailable")
	}
	if d.Handle != "" {
		parts = append(parts, "handle "+d.Handle)
	} else if result.SessionID > 0 {
		parts = append(parts, fmt.Sprintf("session %d", result.SessionID))
	}
	if result.Page != nil {
		parts = append(parts, fmt.Sprintf("bytes %d–%d of %d", result.Page.SinceByte, result.Page.NextByte, d.TotalBytes))
		if result.Page.HasMore {
			parts = append(parts, fmt.Sprintf("more available at byte %d", result.Page.NextByte))
		}
	} else if d.TotalBytes > 0 || result.Output == "" {
		parts = append(parts, fmt.Sprintf("%d output bytes", d.TotalBytes))
	}
	if d.OmittedBytes > 0 {
		parts = append(parts, fmt.Sprintf("%d bytes omitted", d.OmittedBytes))
	}
	return strings.Join(parts, " · ")
}

func CommandDetails(result command.Result) string {
	var details []string
	if result.Command != "" {
		details = append(details, "Command:\n"+result.Command)
	}
	if result.CWD != "" {
		details = append(details, "Directory: "+result.CWD)
	}
	if result.PID > 0 {
		details = append(details, fmt.Sprintf("PID: %d", result.PID))
	}
	if len(result.OutputDescriptor.ShownRanges) > 0 {
		var ranges []string
		for _, r := range result.OutputDescriptor.ShownRanges {
			ranges = append(ranges, fmt.Sprintf("%d–%d", r.StartByte, r.EndByte))
		}
		details = append(details, "Shown byte ranges: "+strings.Join(ranges, ", "))
	}
	if result.OutputDescriptor.RetentionError != "" {
		details = append(details, "Output retention failed: "+result.OutputDescriptor.RetentionError)
	}
	return strings.Join(details, "\n")
}
