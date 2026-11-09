// Package textencoding owns the IDE's lossless disk-text boundary. Byte-oriented
// I/O, hashes and history never pass through this package implicitly.
package textencoding

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Encoding describes disk bytes. Editor text and edit offsets always use UTF-8.
// The zero value is UTF-8 without a byte order mark.
type Encoding string

const (
	UTF8    Encoding = ""
	UTF8BOM Encoding = "utf-8-bom"
	UTF16LE Encoding = "utf-16-le"
	UTF16BE Encoding = "utf-16-be"
)

func Choices() []Encoding { return []Encoding{UTF8, UTF8BOM, UTF16LE, UTF16BE} }

// Key supplies a nonempty choice ID for native form controls.
func (e Encoding) Key() string {
	if e == UTF8 {
		return "utf-8"
	}
	return string(e)
}

func FromKey(key string) Encoding {
	if key == "utf-8" {
		return UTF8
	}
	return Encoding(key)
}

func (e Encoding) Valid() bool {
	return e == UTF8 || e == UTF8BOM || e == UTF16LE || e == UTF16BE
}

func (e Encoding) Label() string {
	switch e {
	case UTF8:
		return "UTF-8"
	case UTF8BOM:
		return "UTF-8 BOM"
	case UTF16LE:
		return "UTF-16 LE"
	case UTF16BE:
		return "UTF-16 BE"
	default:
		return "Unknown encoding"
	}
}

// EncodeText never substitutes unrepresentable or malformed input. UTF-16
// always includes a BOM so a fresh core can reopen it without guessing.
func EncodeText(text []byte, encoding Encoding) ([]byte, error) {
	if !utf8.Valid(text) {
		return nil, errors.New("document text is not UTF-8")
	}
	switch encoding {
	case UTF8:
		return bytes.Clone(text), nil
	case UTF8BOM:
		return append([]byte{0xef, 0xbb, 0xbf}, text...), nil
	case UTF16LE:
		return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes(text)
	case UTF16BE:
		return unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder().Bytes(text)
	default:
		return nil, errors.New("unsupported file encoding")
	}
}

// DecodeText recognizes explicit Unicode signatures, otherwise requires UTF-8.
// Roundtrip validation rejects truncated words and unpaired UTF-16 surrogates;
// a decoder's replacement character must never silently destroy source bytes.
func DecodeText(data []byte) ([]byte, Encoding, error) {
	encoding, _ := DetectBOM(data)
	if encoding == UTF8 {
		if !utf8.Valid(data) {
			return nil, encoding, errors.New("file has invalid UTF-8 text")
		}
		return bytes.Clone(data), encoding, nil
	}
	reader, err := NewReader(bytes.NewReader(data), encoding)
	if err != nil {
		return nil, encoding, err
	}
	text, err := io.ReadAll(reader)
	return text, encoding, err
}

// NewReader decodes Unicode text in a stream. The validator checks each consumed
// source span against its exact encoded output, so malformed input cannot turn
// into replacement characters in search results.
func NewReader(r io.Reader, encoding Encoding) (io.Reader, error) {
	probe := bufio.NewReader(r)
	head, err := probe.Peek(4)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if detected, found := DetectBOM(head); found {
		encoding = detected
	}
	r = probe
	switch encoding {
	case UTF8, UTF8BOM:
		// UTF-8 validation must span read boundaries too.
		return transform.NewReader(r, &strictDecoder{encoding: encoding, decoder: unicode.UTF8.NewDecoder()}), nil
	case UTF16LE:
		return transform.NewReader(r, &strictDecoder{encoding: encoding, decoder: unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder()}), nil
	case UTF16BE:
		return transform.NewReader(r, &strictDecoder{encoding: encoding, decoder: unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder()}), nil
	default:
		if encoding == "utf-32-le" || encoding == "utf-32-be" {
			return nil, errors.New("UTF-32 is not supported. Convert this file to UTF-8 or UTF-16 before opening it")
		}
		return nil, errors.New("unsupported file encoding")
	}
}

type strictDecoder struct {
	encoding Encoding
	decoder  transform.Transformer
	started  bool
}

func (d *strictDecoder) Reset() {
	d.started = false
	d.decoder.Reset()
}

func (d *strictDecoder) Transform(dst, src []byte, atEOF bool) (int, int, error) {
	nDst, nSrc, err := d.decoder.Transform(dst, src, atEOF)
	if nSrc > 0 {
		// The UTF-8 decoder leaves a BOM in the text; consume it as metadata.
		if !d.started && d.encoding == UTF8BOM && bytes.HasPrefix(dst[:nDst], []byte{0xef, 0xbb, 0xbf}) {
			copy(dst, dst[3:nDst])
			nDst -= 3
		}
		encoded, encodeErr := EncodeText(dst[:nDst], d.encoding)
		if encodeErr != nil {
			return 0, 0, encodeErr
		}
		if d.started {
			switch d.encoding {
			case UTF8BOM:
				encoded = encoded[3:]
			case UTF16LE, UTF16BE:
				encoded = encoded[2:]
			}
		}
		if !bytes.Equal(src[:nSrc], encoded) {
			return 0, 0, fmt.Errorf("file has invalid %s text", d.encoding.Label())
		}
		d.started = true
	}
	return nDst, nSrc, err
}

// DetectBOM reads signatures only. It never guesses a legacy encoding.
func DetectBOM(data []byte) (Encoding, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe, 0, 0}):
		return Encoding("utf-32-le"), true
	case bytes.HasPrefix(data, []byte{0, 0, 0xfe, 0xff}):
		return Encoding("utf-32-be"), true
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		return UTF8BOM, true
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		return UTF16LE, true
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		return UTF16BE, true
	default:
		return UTF8, false
	}
}
