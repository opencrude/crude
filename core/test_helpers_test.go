package core

import (
	"context"
	"crude/coreprotocol"
	"crude/corewire"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tinylib/msgp/msgp"
)

func setCoreTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// wireText returns value as it appears inside a JSON request body; Windows
// path separators are escaped there.
func wireText(value string) string {
	encoded, _ := json.Marshal(value)
	return strings.Trim(string(encoded), `"`)
}

// testPayload encodes a request value for tests that build requests by hand;
// the value or a pointer to it must have a generated codec.
func testPayload(v any) ([]byte, error) {
	value, ok := v.(coreprotocol.Value)
	if !ok {
		p := reflect.New(reflect.TypeOf(v))
		p.Elem().Set(reflect.ValueOf(v))
		if value, ok = p.Interface().(coreprotocol.Value); !ok {
			return nil, fmt.Errorf("%T has no protocol codec", v)
		}
	}
	return coreprotocol.Marshal(value)
}

// testDecode decodes a result payload into out, a pointer with a codec.
func testDecode(b []byte, out any) error { return coreprotocol.Decode(b, out.(coreprotocol.Target)) }

// emptyPayload is the encoding of coreprotocol.Empty.
var emptyPayload = func() []byte { b, _ := coreprotocol.Marshal(&coreprotocol.Empty{}); return b }()

func mustTestPayload(v any) []byte {
	b, err := testPayload(v)
	if err != nil {
		panic(err)
	}
	return b
}

// withExtraField appends one string element to an encoded tuple: the shape a
// peer sending an undeclared field produces.
func withExtraField(tuple []byte, value string) []byte {
	if tuple[0] < 0x90 || tuple[0] >= 0x9f {
		panic("withExtraField needs a short fixarray tuple")
	}
	out := append([]byte{tuple[0] + 1}, tuple[1:]...)
	return msgp.AppendString(out, value)
}

// fakeCore speaks the session protocol over conn and answers exactly count
// commands with serve, standing in for a runtime the test wires by hand.
func fakeCore(ctx context.Context, conn net.Conn, count int, serve func(coreprotocol.Request) coreprotocol.Response) error {
	carrier := corewire.NewMux(conn, corewire.CarrierLocal, false)
	defer carrier.Close()
	control, err := carrier.AcceptStream(ctx)
	if err != nil {
		return err
	}
	body, err := corewire.NewReader(control, 1<<16).Next()
	if err != nil {
		return err
	}
	var hello coreprotocol.Hello
	if err := coreprotocol.Decode(body, &hello); err != nil {
		return err
	}
	owner := coreprotocol.Owner{ClientID: hello.ClientID, Epoch: 1}
	welcome := coreprotocol.Welcome{Owner: owner, SessionID: "fake", Secret: "fake", Schema: coreprotocol.Schema, Build: hello.Build, Grace: time.Second}
	if err := send(control, &welcome); err != nil {
		return err
	}
	for served := 0; served < count; {
		s, err := carrier.AcceptStream(ctx)
		if err != nil {
			return err
		}
		body, err := corewire.NewReader(s, corewire.MaxMessage).Next()
		if err != nil {
			return err
		}
		var open coreprotocol.Open
		rest, err := coreprotocol.DecodePrefix(body, &open)
		if err != nil {
			return err
		}
		if open.Kind != coreprotocol.StreamCommand {
			_ = s.Close()
			continue
		}
		result := serve(coreprotocol.Request{ID: open.ID, Owner: owner, WorkspaceID: open.WorkspaceID, Method: open.Method, Payload: rest})
		reply := coreprotocol.Reply{Final: true, Fault: result.Error}
		m := corewire.NewMessage()
		m.B, _ = reply.MarshalMsg(m.B)
		m.B = append(m.B, result.Payload...)
		err = m.Send(s)
		m.Release()
		_ = s.CloseWrite()
		if err != nil {
			return err
		}
		served++
	}
	return nil
}
