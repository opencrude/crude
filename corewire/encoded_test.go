package corewire

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodedFrameRetainsImmutableValidatedPayload(t *testing.T) {
	f := Frame{Version: ProtocolVersion, Kind: KindResponse, ClientID: "client", RequestID: "id", Method: "snapshot", Payload: json.RawMessage(` { "data" : "<literal>" } `)}
	size, err := FrameSize(f)
	if err != nil {
		t.Fatal(err)
	}
	e, err := EncodeFrame(f)
	if err != nil || e.Len() != size {
		t.Fatalf("encoded size %d vs %d: %v", e.Len(), size, err)
	}
	if !bytes.Contains(e.data, f.Payload) {
		t.Fatal("immutable payload was compacted or escaped again")
	}
	copy(f.Payload, []byte("bad"))
	var decoded Frame
	if err := strictJSON(e.data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != f.Method || decoded.RequestID != f.RequestID {
		t.Fatal("envelope identity changed")
	}
	if _, err := EncodeFrame(Frame{Version: ProtocolVersion, Kind: KindRequest, Payload: json.RawMessage(`{"bad":`)}); err == nil {
		t.Fatal("invalid JSON prepared")
	}
}

func BenchmarkEncodePayloadFrame(b *testing.B) {
	f := Frame{Version: ProtocolVersion, Kind: KindResponse, Method: "payload.read", Payload: json.RawMessage(`{"data":"` + string(bytes.Repeat([]byte("A"), 256<<10)) + `"}`)}
	b.ReportAllocs()
	for range b.N {
		if _, err := EncodeFrame(f); err != nil {
			b.Fatal(err)
		}
	}
}

func TestJSONKeyScannerPreservesStrictContract(t *testing.T) {
	for _, body := range []string{
		`{"version":1,"kind":"event","payload":{"x":1,"\\u0078":2}}`,
		`{"version":1,"kind":"event","payload":[{"name":1,"name":2}]}`,
		`{"version":1,"kind":"event","payload":{"quoted\\\"key":1,"quoted\\\"key":2}}`,
	} {
		// The first spelling must compare decoded Unicode escape keys, too.
		if bytes.Contains([]byte(body), []byte(`\\u0078`)) {
			body = string(bytes.ReplaceAll([]byte(body), []byte(`\\u0078`), []byte(`\u0078`)))
		}
		if err := strictJSON([]byte(body), new(Frame)); err == nil {
			t.Fatalf("duplicate accepted: %s", body)
		}
	}
	for _, payload := range []string{`[true,null,-1.25,"escaped\\\"string",{"a":[{},[]]}]`, `{"a":"[{}]","b":"\\\\","c":"\u0078"}`} {
		body := []byte(`{"version":1,"kind":"event","payload":` + payload + `}`)
		if err := strictJSON(body, new(Frame)); err != nil {
			t.Fatalf("valid nested payload rejected: %s: %v", body, err)
		}
	}
}
