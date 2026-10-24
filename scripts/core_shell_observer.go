//go:build ignore

// core_shell_observer is acceptance-only tooling, not a product CLI entry point.
// JSON lines on stdin/stdout are a private Python subprocess interface. The core
// connection exclusively uses coreclient's typed v2 protocol and local carrier.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/corewire"
)

type request struct {
	Method    string          `json:"method"`
	Workspace string          `json:"workspace"`
	Payload   json.RawMessage `json:"payload"`
}

type response struct {
	Ready   bool   `json:"ready,omitempty"`
	Payload any    `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
}

func decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func call(ctx context.Context, client *coreclient.Client, req request) (any, error) {
	switch req.Method {
	case coreprotocol.CoreStatusMethod:
		var in coreprotocol.Empty
		var out coreprotocol.CoreStatus
		if err := decode(req.Payload, &in); err != nil {
			return nil, err
		}
		err := client.Call(ctx, "", coreprotocol.CoreStatusMethod, &in, &out)
		return out, err
	case coreprotocol.Attach:
		var in coreprotocol.AttachRequest
		var out coreprotocol.AttachResult
		if err := decode(req.Payload, &in); err != nil {
			return nil, err
		}
		err := client.Call(ctx, "", coreprotocol.Attach, &in, &out)
		return out, err
	case coreprotocol.Snapshot:
		var in coreprotocol.Empty
		var out coreprotocol.WorkspaceSnapshot
		if err := decode(req.Payload, &in); err != nil {
			return nil, err
		}
		err := client.Call(ctx, req.Workspace, coreprotocol.Snapshot, &in, &out)
		return out, err
	case coreprotocol.StartupInspect:
		var in coreprotocol.StartupInspectRequest
		var out coreprotocol.StartupSnapshot
		if err := decode(req.Payload, &in); err != nil {
			return nil, err
		}
		err := client.Call(ctx, "", coreprotocol.StartupInspect, &in, &out)
		return out, err
	default:
		return nil, fmt.Errorf("observer method not allowed: %s", req.Method)
	}
}

func run() (err error) {
	home := os.Getenv("CRUDE_HOME")
	if !filepath.IsAbs(home) {
		return errors.New("observer requires an absolute isolated CRUDE_HOME")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := coreclient.Dial(ctx, func(ctx context.Context) (corewire.Conn, error) {
		conn, err := corewire.DialLocal(ctx, filepath.Join(home, ".crude"))
		if err != nil {
			return nil, err
		}
		return corewire.NewMux(conn, corewire.CarrierLocal, true), nil
	}, "acceptance-observer-"+hex.EncodeToString(id[:]))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()
	out := json.NewEncoder(os.Stdout)
	if err := out.Encode(response{Ready: true}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var req request
		var result response
		if err := decode(scanner.Bytes(), &req); err != nil {
			result.Error = err.Error()
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			payload, err := call(ctx, client, req)
			cancel()
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Payload = payload
			}
		}
		if err := out.Encode(result); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
