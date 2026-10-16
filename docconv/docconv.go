// Package docconv wraps conductor-oss/markitdown to convert binary document
// formats (PDF, DOCX, PPTX, XLSX, XLS, EPUB) to clean Markdown.
//
// It is used by the read_file and web_fetch tools so that users can read
// PDFs and Office documents without leaving the terminal; no CGO needed.
package docconv

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	markitdown "github.com/conductor-oss/markitdown"
)

// Meta holds information about a converted document.
type Meta struct {
	Format   string // human-readable format name, e.g. "PDF", "Word"
	FileSize int64  // file size in bytes
}

// binaryFormats lists the binary document extensions we can convert.
// Text-based formats (.txt, .md, .json, .csv, .html) are excluded because
// read_file already handles them natively.
var binaryFormats = map[string]string{
	".pdf":   "PDF",
	".docx":  "Word",
	".pptx":  "PowerPoint",
	".xlsx":  "Excel",
	".xls":   "Excel (legacy)",
	".epub":  "EPUB",
	".ipynb": "Jupyter Notebook",
}

// SupportedExt reports whether ext is a binary document format that
// read_file / web_fetch can auto-convert to Markdown.
func SupportedExt(ext string) bool {
	_, ok := binaryFormats[strings.ToLower(ext)]
	return ok
}

// FormatName returns the human-readable name for a supported extension,
// or "" if the extension is not supported.
func FormatName(ext string) string {
	return binaryFormats[strings.ToLower(ext)]
}

const noTextSentinel = "[No readable text content found in PDF]"

func isNoTextResult(md string) bool {
	return strings.TrimSpace(md) == noTextSentinel
}

// ConvertFile converts a binary document on disk to Markdown.
// It returns the markdown text and metadata about the conversion.
func ConvertFile(path string) (string, *Meta, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", nil, fmt.Errorf("docconv: stat %s: %w", path, err)
	}

	m := markitdown.New()
	result, err := m.ConvertFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("docconv: convert %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	meta := &Meta{
		Format:   binaryFormats[ext],
		FileSize: fi.Size(),
	}

	md := strings.TrimSpace(result.Markdown)
	if md == "" || isNoTextResult(md) {
		return "", meta, fmt.Errorf("docconv: no text content extracted from %s — the file may be image-only or empty", path)
	}

	return md, meta, nil
}

// ConvertBytes converts raw document bytes to Markdown.
// ext must include the leading dot (e.g. ".pdf").
func ConvertBytes(data []byte, ext string) (string, *Meta, error) {
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	ext = strings.ToLower(ext)

	m := markitdown.New()
	result, err := m.ConvertReader(bytes.NewReader(data), markitdown.StreamInfo{
		Extension: ext,
	})
	if err != nil {
		return "", nil, fmt.Errorf("docconv: convert %s bytes: %w", ext, err)
	}

	meta := &Meta{
		Format:   binaryFormats[ext],
		FileSize: int64(len(data)),
	}

	md := strings.TrimSpace(result.Markdown)
	if md == "" || isNoTextResult(md) {
		return "", meta, fmt.Errorf("docconv: no text content extracted from %s data — the file may be image-only or empty", ext)
	}

	return md, meta, nil
}

// ConvertReader converts a stream of bytes to Markdown.
// ext must include the leading dot (e.g. ".pdf").
func ConvertReader(r io.Reader, ext string) (string, *Meta, error) {
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	ext = strings.ToLower(ext)

	// Read all bytes first so we can measure size.
	data, err := io.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("docconv: read stream: %w", err)
	}

	return ConvertBytes(data, ext)
}

// Header builds a short annotation line describing the conversion.
// Example: "📄 Converted from PDF (report.pdf, 340 KB)"
func Header(path string, meta *Meta) string {
	var size string
	switch {
	case meta.FileSize < 1024:
		size = fmt.Sprintf("%d B", meta.FileSize)
	case meta.FileSize < 1024*1024:
		size = fmt.Sprintf("%d KB", meta.FileSize/1024)
	default:
		size = fmt.Sprintf("%.1f MB", float64(meta.FileSize)/(1024*1024))
	}

	label := meta.Format
	if label == "" {
		label = "document"
	}

	return fmt.Sprintf("📄 Converted from %s (%s, %s)\n\n", label, filepath.Base(path), size)
}
