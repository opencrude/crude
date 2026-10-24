package coreclient

import (
	"context"
	"errors"
	"time"

	"crude/buildinfo"
	"crude/coreprotocol"
)

// SwitchCarrier upgrades the existing session using its independent resume
// secret. Dial failure or rejection leaves the current carrier and dial policy
// unchanged; no command, attachment or input is replayed to establish the link.
func (c *Client) SwitchCarrier(ctx context.Context, dial Dialer) error {
	if dial == nil {
		return errors.New("carrier dialer is required")
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
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	conn, err := dial(ctx)
	if err != nil {
		return err
	}
	resume := c.resume
	welcome, control, err := handshake(ctx, conn, coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: c.clientID, Resume: &resume})
	if err != nil {
		_ = conn.Close()
		return err
	}
	if !welcome.Resumed || welcome.Owner != c.owner || welcome.LocalHost != c.localHost {
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
	c.dial, c.carrier, c.control = dial, conn, control
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
