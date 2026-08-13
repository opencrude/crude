package provider

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

const DefaultMaxSSEEventBytes = 64 << 20

// SSEReader parses Server-Sent Events according to the event-stream grammar.
// It supports comments, named events, CRLF, data fields without a space, and
// multiline data payloads.
type SSEReader struct {
	reader        *bufio.Reader
	maxEventBytes int
	firstLine     bool
	lastEventID   string
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{
		reader:        bufio.NewReader(r),
		maxEventBytes: DefaultMaxSSEEventBytes,
		firstLine:     true,
	}
}

func (r *SSEReader) SetMaxEventBytes(limit int) {
	if limit > 0 {
		r.maxEventBytes = limit
	}
}

func (r *SSEReader) Next() (StreamEvent, error) {
	event := StreamEvent{ID: r.lastEventID}
	var data strings.Builder
	dataField := false
	eventBytes := 0

	for {
		line, err := r.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !dataField {
					return StreamEvent{}, io.EOF
				}
				event.Data = []byte(data.String())
				return event, nil
			}
			return StreamEvent{}, err
		}

		if r.firstLine {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			r.firstLine = false
		}

		eventBytes += len(line) + 1
		if eventBytes > r.maxEventBytes {
			return StreamEvent{}, fmt.Errorf("SSE event exceeds %d bytes", r.maxEventBytes)
		}

		if len(line) == 0 {
			if !dataField {
				event = StreamEvent{ID: r.lastEventID}
				eventBytes = 0
				continue
			}
			event.Data = []byte(data.String())
			return event, nil
		}
		if line[0] == ':' {
			continue
		}

		field, value := splitSSEField(line)
		switch field {
		case "event":
			event.Event = value
		case "data":
			if dataField {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			dataField = true
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				event.ID = value
				r.lastEventID = value
			}
		case "retry":
			// Reconnection timing is a transport concern; this reader only
			// yields semantic events that contain data.
		default:
			// Unknown fields are ignored by the SSE specification.
		}
	}
}

func (r *SSEReader) readLine() ([]byte, error) {
	var line []byte
	for {
		next, err := r.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
		switch next {
		case '\n':
			return line, nil
		case '\r':
			if following, peekErr := r.reader.Peek(1); peekErr == nil && following[0] == '\n' {
				_, _ = r.reader.ReadByte()
			}
			return line, nil
		default:
			line = append(line, next)
			if len(line) > r.maxEventBytes {
				return nil, fmt.Errorf("SSE line exceeds %d bytes", r.maxEventBytes)
			}
		}
	}
}

func splitSSEField(line []byte) (field, value string) {
	idx := bytes.IndexByte(line, ':')
	if idx < 0 {
		return string(line), ""
	}
	rawValue := line[idx+1:]
	if len(rawValue) > 0 && rawValue[0] == ' ' {
		rawValue = rawValue[1:]
	}
	return string(line[:idx]), string(rawValue)
}
