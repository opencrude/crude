package textencoding

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestTextEncodingExactBytes(t *testing.T) {
	text := []byte("Aé😀\r\n")
	for _, test := range []struct {
		encoding Encoding
		disk     []byte
	}{
		{UTF8, []byte("Aé😀\r\n")},
		{UTF8BOM, []byte("\xef\xbb\xbfAé😀\r\n")},
		{UTF16LE, []byte{0xff, 0xfe, 0x41, 0, 0xe9, 0, 0x3d, 0xd8, 0, 0xde, 13, 0, 10, 0}},
		{UTF16BE, []byte{0xfe, 0xff, 0, 0x41, 0, 0xe9, 0xd8, 0x3d, 0xde, 0, 0, 13, 0, 10}},
	} {
		t.Run(test.encoding.Label(), func(t *testing.T) {
			raw, err := EncodeText(text, test.encoding)
			if err != nil || !bytes.Equal(raw, test.disk) {
				t.Fatalf("disk bytes = %x, error %v; want %x", raw, err, test.disk)
			}
			decoded, encoding, err := DecodeText(test.disk)
			if err != nil || encoding != test.encoding || !bytes.Equal(decoded, text) {
				t.Fatalf("decoded = %q, %q, %v", decoded, encoding, err)
			}
		})
	}
	for _, raw := range [][]byte{{0xff}, {0xff, 0xfe, 1}, {0xff, 0xfe, 0, 0xd8}, {0xfe, 0xff, 0xdc, 0}} {
		if _, _, err := DecodeText(raw); err == nil {
			t.Fatalf("malformed bytes were silently replaced: %x", raw)
		}
	}
	if _, err := EncodeText(text, Encoding("unknown")); err == nil {
		t.Fatal("unknown encoding accepted")
	}
}

func TestEncodingStreamsRetainExactCharactersAcrossReadBoundaries(t *testing.T) {
	for _, encoding := range []Encoding{UTF8, UTF8BOM, UTF16LE, UTF16BE} {
		t.Run(encoding.Label(), func(t *testing.T) {
			text := []byte(strings.Repeat("é😀\r\n", 2000) + "\ufffd")
			raw, err := EncodeText(text, encoding)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []io.Reader{bytes.NewReader(raw), iotest.OneByteReader(bytes.NewReader(raw))} {
				reader, err := NewReader(source, encoding)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(actual, text) {
					t.Fatalf("fragmented decode = %d bytes, %v; want %d bytes", len(actual), err, len(text))
				}
			}
		})
	}
}

func TestUnsupportedUTF32IsNotReadAsUTF16(t *testing.T) {
	for _, data := range [][]byte{
		{0xff, 0xfe, 0, 0, 'A', 0, 0, 0},
		{0, 0, 0xfe, 0xff, 0, 0, 0, 'A'},
	} {
		if _, _, err := DecodeText(data); err == nil || !strings.Contains(err.Error(), "UTF-32") {
			t.Fatalf("unsupported signature accepted: %x, %v", data, err)
		}
		reader, err := NewReader(iotest.OneByteReader(bytes.NewReader(data)), UTF16LE)
		if err == nil {
			_, err = io.ReadAll(reader)
		}
		if err == nil || !strings.Contains(err.Error(), "UTF-32") {
			t.Fatalf("stream accepted unsupported signature: %x, %v", data, err)
		}
	}
}
