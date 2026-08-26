// Package-level file inspection helpers shared by the agent input pipeline
// and the TUI attachment previews: bounded reads, binary detection, MIME
// sniffing, and a readable hex-dump representation for binary files.
package fileio

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// DefaultHexHeadBytes is how many leading bytes of a binary file get rendered
// as a hex dump — enough context for an LLM or a human to identify the
// format without flooding the context window.
const DefaultHexHeadBytes = 512

// DefaultPreviewBytes bounds text-file previews loaded into the UI.
const DefaultPreviewBytes = 256 * 1024

// FilePreview is the result of a bounded file inspection.
type FilePreview struct {
	Path      string
	Size      int64  // full on-disk size in bytes
	MIME      string // sniffed from the first bytes ("", if undetectable)
	Binary    bool
	Truncated bool   // true when the file exceeded the read cap
	Text      string // decoded text (non-binary only, possibly truncated)
	HexDump   string // formatted hex dump head (binary only)
	HexHead   int    // how many bytes HexDump covers
}

// ReadableText renders the preview as human/LLM-readable text: raw content
// for text files, a metadata header plus hex dump for binaries, with a hint
// pointing at execute_command for full binary access.
func (p *FilePreview) ReadableText() string {
	if !p.Binary {
		if p.Truncated {
			return p.Text + fmt.Sprintf("\n\n[truncated — showing first %d of %d bytes]", len(p.Text), p.Size)
		}
		return p.Text
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Binary file (%s, %d bytes).\n\n", p.MIME, p.Size)
	if p.HexDump != "" {
		fmt.Fprintf(&b, "Hex dump of first %d bytes:\n\n%s\n\n", p.HexHead, p.HexDump)
	} else {
		b.WriteString("The file is empty.\n\n")
	}
	b.WriteString("Full contents are available via execute_command (e.g. xxd, base64, strings, or a format-specific tool).")
	return b.String()
}

// InspectFile reads up to maxTextBytes+1 bytes of path and classifies it.
// A cap larger than the read budget keeps the truncated flag honest without
// reading the whole file. Hex dumps are rendered for binary payloads.
func InspectFile(path string, maxTextBytes int64, hexHeadBytes int) (*FilePreview, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// One extra byte beyond the cap distinguishes "exactly cap sized" from
	// "truncated" without a second stat round-trip.
	budget := maxTextBytes
	if budget < 1 {
		budget = DefaultPreviewBytes
	}
	data, err := io.ReadAll(io.LimitReader(f, budget+1))
	if err != nil {
		return nil, err
	}
	truncated := int64(len(data)) > budget
	if truncated {
		data = data[:budget]
	}

	head := data
	if int64(len(head)) > 512 {
		head = head[:512]
	}
	mime := http.DetectContentType(head)

	p := &FilePreview{
		Path:      path,
		Size:      fi.Size(),
		MIME:      mime,
		Truncated: truncated,
		Binary:    IsBinary(data),
		HexHead:   hexHeadBytes,
	}
	if p.Binary {
		p.HexDump = HexDump(data, hexHeadBytes)
	} else {
		p.Text = string(data)
	}
	return p, nil
}

// HexDump renders at most maxBytes of data in classic xx d-style rows of 16
// bytes: offset, hex pairs, printable ASCII gutter.
func HexDump(data []byte, maxBytes int) string {
	if maxBytes <= 0 || maxBytes > len(data) {
		maxBytes = len(data)
	}
	var b strings.Builder
	for off := 0; off < maxBytes; off += 16 {
		end := off + 16
		if end > maxBytes {
			end = maxBytes
		}
		row := data[off:end]
		fmt.Fprintf(&b, "%08x ", off)
		for i := 0; i < 16; i++ {
			if i == 8 {
				b.WriteByte(' ')
			}
			if i < len(row) {
				fmt.Fprintf(&b, " %02x", row[i])
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString("  |")
		for _, c := range row {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	return b.String()
}
