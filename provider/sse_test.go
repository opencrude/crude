package provider

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEReaderParsesNamedMultilineEvents(t *testing.T) {
	input := "\ufeff: keepalive\r\n" +
		"event: response.output_text.delta\r\n" +
		"id: evt-1\r\n" +
		"data:{\"first\":1}\r\n" +
		"data: {\"second\":2}\r\n\r\n"

	reader := NewSSEReader(strings.NewReader(input))
	event, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if event.Event != "response.output_text.delta" {
		t.Fatalf("Event = %q", event.Event)
	}
	if event.ID != "evt-1" {
		t.Fatalf("ID = %q", event.ID)
	}
	if got, want := string(event.Data), "{\"first\":1}\n{\"second\":2}"; got != want {
		t.Fatalf("Data = %q, want %q", got, want)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want EOF", err)
	}
}

func TestSSEReaderDispatchesFinalEventAtEOF(t *testing.T) {
	reader := NewSSEReader(strings.NewReader("data: final"))
	event, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if string(event.Data) != "final" {
		t.Fatalf("Data = %q", event.Data)
	}
}

func TestSSEReaderRejectsOversizedEvent(t *testing.T) {
	reader := NewSSEReader(strings.NewReader("data: 123456789\n\n"))
	reader.SetMaxEventBytes(8)
	if _, err := reader.Next(); err == nil {
		t.Fatal("Next() succeeded for oversized event")
	}
}

func TestSSEReaderIgnoresUnknownFieldsAndComments(t *testing.T) {
	reader := NewSSEReader(strings.NewReader(": ping\nunknown: value\ndata: ok\n\n"))
	event, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if string(event.Data) != "ok" {
		t.Fatalf("Data = %q", event.Data)
	}
}

func TestSSEReaderDoesNotDispatchEventsWithoutData(t *testing.T) {
	reader := NewSSEReader(strings.NewReader(
		"id: heartbeat\nretry: 1000\nevent: ping\n\n" +
			"event: message\ndata: payload\n\n",
	))
	event, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if event.Event != "message" || string(event.Data) != "payload" {
		t.Fatalf("event = %+v", event)
	}
	if event.ID != "heartbeat" {
		t.Fatalf("last event ID was not retained: %+v", event)
	}
}

func TestSSEReaderSupportsBareCRFraming(t *testing.T) {
	reader := NewSSEReader(strings.NewReader(
		"event: first\rdata: one\r\rdata: two\r\r",
	))
	first, err := reader.Next()
	if err != nil {
		t.Fatalf("first Next(): %v", err)
	}
	if first.Event != "first" || string(first.Data) != "one" {
		t.Fatalf("first event = %+v", first)
	}
	second, err := reader.Next()
	if err != nil {
		t.Fatalf("second Next(): %v", err)
	}
	if string(second.Data) != "two" {
		t.Fatalf("second event = %+v", second)
	}
}
