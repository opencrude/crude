package jsonrpc2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRequestOriginalRaw(t *testing.T) {
	values := []string{
		"{ \"z\":900719925474099312345, \"a\":1.2300e+09,\n \"a\":-0, \"s\":\"\\u0061\\/\\ud800\" }",
		`[{"nested":1,"nested":2}, 1E-9, "\udc00", "\uD83D\uDE00"]`,
		"{\"invalid\":\"\xff\xc0\xaf\"}",
		`"opaque\u0061"`, `true`, `123456789012345678901234567890`, `null`,
	}
	for _, value := range values {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			data := []byte(`{"method":"m","params": ` + value + `, "meta": ` + value + ` }`)
			var r Request
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			params, meta := r.Params, r.Meta
			for i := range data {
				data[i] = 'x'
			}
			checkRaw(t, params, value)
			checkRaw(t, meta, value)
			if err := r.UnmarshalJSON([]byte(`{"method":"next"}`)); err != nil {
				t.Fatal(err)
			}
			if r.Params != nil || r.Meta != nil || !r.Notif || r.ID != (ID{}) {
				t.Fatalf("stale state: %+v", r)
			}
			checkRaw(t, params, value)
			checkRaw(t, meta, value)
			(*params)[0] = 'x'
			checkRaw(t, meta, value) // independently owned, even explicit null
		})
	}
}

func checkRaw(t *testing.T, got *json.RawMessage, want string) {
	t.Helper()
	if got == nil || !bytes.Equal(*got, []byte(want)) {
		t.Fatalf("original raw lost: got %v, want %q", got, want)
	}
}

func TestRequestEnvelopeSemantics(t *testing.T) {
	for _, tc := range []struct {
		id    string
		want  ID
		notif bool
	}{
		{"", ID{}, true}, {`,"id":null`, ID{}, true},
		{`,"id":""`, ID{IsString: true}, false},
		{`,"id":"a\u0062"`, ID{Str: "ab", IsString: true}, false},
		{`,"id":0`, ID{}, false}, {`,"id":9223372036854775807`, ID{Num: 9223372036854775807}, false},
		{`,"id":-1`, ID{Num: ^uint64(0)}, false},
	} {
		var r Request
		if err := r.UnmarshalJSON([]byte(`{"method":""` + tc.id + `,"jsonrpc":false,"params":null}`)); err != nil {
			t.Fatal(err)
		}
		if r.Method != "" || r.ID != tc.want || r.Notif != tc.notif || r.Meta != nil || len(r.ExtraFields) != 0 {
			t.Fatalf("%s: %+v", tc.id, r)
		}
		checkRaw(t, r.Params, "null")
	}
	var r Request
	if err := r.UnmarshalJSON([]byte(`{"method":"m","ext":{"n":9007199254740993,"e":[1.00E+99],"n":42},"meta":null}`)); err != nil {
		t.Fatal(err)
	}
	want := []RequestField{{Name: "ext", Value: map[string]interface{}{
		"n": json.Number("42"), "e": []interface{}{json.Number("1.00E+99")},
	}}}
	if !reflect.DeepEqual(r.ExtraFields, want) || r.Params != nil {
		t.Fatalf("extension semantics: %+v", r)
	}
	checkRaw(t, r.Meta, "null")
	old := r.ExtraFields
	if err := r.UnmarshalJSON([]byte(`{"method":"new","ext2":null}`)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.ExtraFields, []RequestField{{Name: "ext2"}}) || !reflect.DeepEqual(old, want) || r.Meta != nil {
		t.Fatalf("reuse mutated extensions: %+v", r)
	}
	encoded, err := json.Marshal(Request{Method: "m", Notif: true})
	if err != nil || string(encoded) != `{"jsonrpc":"2.0","method":"m"}` {
		t.Fatalf("notification marshal: %s, %v", encoded, err)
	}
}

func TestRequestRefusalAtomic(t *testing.T) {
	bad := []string{
		`null`, `[]`, `"m"`, `0`, `true`, `{}`, `{"params":null}`,
		`{"method":null}`, `{"method":1}`, `{"method":{}}`,
		`{"method":"new","id":true}`, `{"method":"new","id":[]}`,
		`{"method":"new","id":1.0}`, `{"method":"new","id":1e0}`,
		`{"method":"new","id":9223372036854775808}`,
		`{"method":"new","params":{"x":}}`, `{"method":"new","meta":[}`,
		`{"method":"new"`, `{"method":"new",}`, `{"method":"new"]`,
		`{"method":"new"} {}`, `{"method":"new"} x`,
		`{"method":"new","meth\u006fd":"other"}`,
	}
	for _, name := range []string{"params", "meta", "id", "jsonrpc", "ext"} {
		bad = append(bad, `{"method":"new","`+name+`":null,"`+name+`":null}`)
	}
	for _, data := range bad {
		t.Run(data, func(t *testing.T) {
			var r, want Request
			for _, target := range []*Request{&r, &want} {
				if err := target.UnmarshalJSON([]byte(`{"method":"old","id":"keep","params":{"p":1},"meta":[2],"ext":{"x":3}}`)); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.UnmarshalJSON([]byte(data)); err == nil {
				t.Fatal("ambiguous/invalid envelope accepted")
			}
			if !reflect.DeepEqual(r, want) {
				t.Fatalf("error partially mutated receiver: %+v", r)
			}
		})
	}
}

func TestConnOriginalFramedNotification(t *testing.T) {
	server, peer := net.Pipe()
	if err := peer.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	received := make(chan *Request, 2)
	conn := NewConn(context.Background(), NewBufferedStream(server, VSCodeObjectCodec{}),
		HandlerWithError(func(_ context.Context, _ *Conn, r *Request) (interface{}, error) {
			received <- r
			return nil, nil
		}), SetLogger(log.New(io.Discard, "", 0)))
	t.Cleanup(func() {
		if err := peer.Close(); err != nil {
			t.Error(err)
		}
		select {
		case <-conn.DisconnectNotify():
		case <-time.After(5 * time.Second):
			t.Error("reader did not stop")
		}
	})
	value := "{ \"diagnostics\":[{\"data\":{\"k\":1,\"k\":2},\"s\":\"\\ud800\xff\",\"n\":1.230E+90}] }"
	body := `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":` + value + `}`
	if _, err := fmt.Fprintf(peer, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-received:
		if !r.Notif || r.Method != "textDocument/publishDiagnostics" {
			t.Fatalf("wrong notification: %+v", r)
		}
		checkRaw(t, r.Params, value)
	case <-conn.DisconnectNotify():
		t.Fatal("original notification refused")
	case <-time.After(5 * time.Second):
		t.Fatal("handler not reached")
	}
	body = `{"method":"m","params":{},"par\u0061ms":[]}`
	if _, err := fmt.Fprintf(peer, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil && !strings.Contains(err.Error(), "closed") {
		t.Fatal(err)
	}
	select {
	case <-received:
		t.Fatal("duplicate envelope reached handler")
	case <-conn.DisconnectNotify():
	case <-time.After(5 * time.Second):
		t.Fatal("duplicate envelope did not close reader")
	}
}
