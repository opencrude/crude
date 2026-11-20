package coreclient

import (
	"context"
	"errors"

	"crude/buildinfo"
	"crude/core/protocol"
)

// SwitchCarrier upgrades the existing session using its independent resume
// secret. attempt dials the replacement carrier once; only after it resumes the
// session does reconnect become the dial policy for later carrier loss.
// Failure or rejection leaves the current carrier and dial policy unchanged; no
// command, attachment or input is replayed to establish the link.
func (c *Client) SwitchCarrier(ctx context.Context, attempt, reconnect Dialer) error {
	if attempt == nil || reconnect == nil {
		return errors.New("carrier dialers are required")
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, carrierAttemptTimeout)
	defer cancel()
	stop := context.AfterFunc(c.lifetime, cancel)
	defer stop()
	if err := c.acquireCarrierChange(ctx); err != nil {
		return err
	}
	defer c.releaseCarrierChange()
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := attempt(ctx)
	if err != nil {
		return err
	}
	resume := c.resume
	welcome, control, err := handshake(ctx, conn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: c.clientID, Resume: &resume})
	if err != nil {
		_ = conn.Close()
		return err
	}
	if !c.sameSession(welcome) {
		_ = conn.Close()
		return errors.New("carrier switch did not resume the original session")
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		_ = conn.Close()
		return err
	}
	old := c.carrier
	c.dial, c.carrier, c.control = reconnect, conn, control
	select {
	case <-c.ready:
	default:
		close(c.ready)
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go c.watchControl(control)
	if old != nil && old != conn {
		_ = old.Close()
	}
	linkResumes.Inc()
	return nil
}

// UpgradeInBackground moves the session to another carrier without delaying
// its caller. prepare learns the replacement (for example over the current
// carrier) and returns the one-shot attempt and the later reconnect policy for
// SwitchCarrier. The work belongs to the session: it stops when the session
// ends, Close joins it, and any failure leaves the current carrier in use.
func (c *Client) UpgradeInBackground(prepare func(context.Context) (attempt, reconnect Dialer, err error)) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		ctx, cancel := context.WithTimeout(c.lifetime, carrierAttemptTimeout)
		attempt, reconnect, err := prepare(ctx)
		cancel()
		if err == nil {
			_ = c.SwitchCarrier(c.lifetime, attempt, reconnect)
		}
	}()
}
