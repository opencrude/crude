package coreclient

import (
	"context"
	"errors"
	"io"
	"sync"

	"crude/coreprotocol"
	"crude/corewire"
)

// RelaySSH terminates only the SSH carrier, not a core session. It translates
// carrier compression and credit at the authenticated stdio boundary; application
// streams and their receipts remain end-to-end. The only changed fact is the
// initial local Hello's SSHRelay bit, which narrows OS-authenticated authority.
func RelaySSH(parent context.Context, incoming, local corewire.Conn) (result error) {
	defer func() {
		// Closing SSH (including before Hello) is ordinary shell teardown.
		// Framing/decoding faults remain visible, not a successful disconnect.
		if parent.Err() != nil || errors.Is(result, corewire.ErrClosed) {
			result = nil
		}
	}()
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	defer incoming.Close()
	defer local.Close()
	stop := context.AfterFunc(ctx, func() { _ = incoming.Close(); _ = local.Close() })
	defer stop()
	control, err := incoming.AcceptStream(ctx)
	if err != nil {
		return err
	}
	if control.Class() != corewire.ClassControl {
		return errors.New("SSH relay requires a control handshake")
	}
	reader := corewire.NewReader(control, 64<<10)
	body, err := reader.Next()
	if err != nil {
		return err
	}
	var hello coreprotocol.Hello
	if err = coreprotocol.Decode(body, &hello); err != nil {
		return err
	}
	if hello.Pair != nil || hello.Credential != "" {
		return errors.New("SSH relay does not accept pairing or device credentials")
	}
	hello.SSHRelay = true
	upstream, err := local.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		return err
	}
	if err = sendValues(upstream, &hello); err != nil {
		return err
	}
	failures := make(chan error, 1)
	finish := func(err error) {
		select {
		case failures <- err:
		default:
		}
		cancel()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		// Reader may already hold bytes after Hello. It must remain the sole
		// decoder until clean FIN; forwarding from control directly loses them.
		for {
			body, err := reader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					if err = upstream.CloseWrite(); err != nil {
						finish(err)
					}
				} else {
					finish(err)
				}
				return
			}
			if err = corewire.WriteMessage(upstream, body); err != nil {
				finish(err)
				return
			}
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := io.Copy(control, upstream)
		if err == nil {
			err = control.CloseWrite()
		} else {
			_ = control.Close()
		}
		finish(err)
	}()
	for {
		s, err := incoming.AcceptStream(ctx)
		if err != nil {
			select {
			case failure := <-failures:
				return failure
			default:
				return err
			}
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			u, err := local.OpenStream(ctx, s.Class())
			if err != nil {
				_ = s.Close()
				return
			}
			bridgeStreams(s, u)
		}()
	}
}

func bridgeStreams(a, b corewire.Stream) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 1)
	go func() {
		_, err := io.Copy(b, a)
		if err == nil {
			_ = b.CloseWrite()
		} else {
			_ = a.Close()
			_ = b.Close()
		}
		done <- struct{}{}
	}()
	_, err := io.Copy(a, b)
	if err == nil {
		_ = a.CloseWrite()
	} else {
		_ = a.Close()
		_ = b.Close()
	}
	<-done
}
