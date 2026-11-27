package coreclient

import (
	"context"
	"errors"
	"fmt"

	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/internal/buildinfo"
)

// WatchDataUpdate follows the core's data update until its final status, the
// end of ctx or the end of the carrier. It never starts a session. A core that
// already finished its update answers with one final status. Retry asks the
// core to run the update again, for example after the user freed disk space.
func WatchDataUpdate(ctx context.Context, dial Dialer, clientID string, retry bool, status func(coreprotocol.DataUpdate)) error {
	conn, err := dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	control, err := conn.OpenStream(ctx, corewire.ClassControl)
	if err != nil {
		return err
	}
	hello := coreprotocol.Hello{Schema: coreprotocol.Schema, Build: buildinfo.ProtocolBuild, ClientID: clientID, DataUpdate: &coreprotocol.DataUpdateRequest{Retry: retry}}
	if err := sendValues(control, &hello); err != nil {
		return err
	}
	reader := corewire.NewReader(control, 64<<10)
	for {
		body, err := reader.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("the core closed the data update connection: %w", err)
		}
		var welcome coreprotocol.Welcome
		if err := coreprotocol.Decode(body, &welcome); err != nil {
			if schema, build, idErr := coreprotocol.HandshakeIdentity(body); idErr == nil && (schema != hello.Schema || build != hello.Build) {
				return &coreprotocol.Fault{Code: "schema_mismatch", Message: fmt.Sprintf("core speaks protocol %s (build %s); client speaks %s (build %s)", schema, build, hello.Schema, hello.Build)}
			}
			return fmt.Errorf("read the data update: %w", err)
		}
		if welcome.Fault != nil {
			return welcome.Fault
		}
		if welcome.DataUpdate == nil {
			return errors.New("the core sent no data update status")
		}
		status(*welcome.DataUpdate)
		if welcome.DataUpdate.Final {
			_ = control.CloseWrite()
			return nil
		}
	}
}

// AwaitDataUpdate follows the core's data update to its end, for a client that
// cannot show it. It fails when the update needs the user: data from a newer
// Crude, or too little disk space for the backups. It returns nil when the
// update ended in any other way; the caller then connects again.
func AwaitDataUpdate(ctx context.Context, dial Dialer, clientID string) error {
	watch, cancel := context.WithCancel(ctx)
	defer cancel()
	var last coreprotocol.DataUpdate
	_ = WatchDataUpdate(watch, dial, clientID, false, func(u coreprotocol.DataUpdate) {
		last = u
		if u.Phase == coreprotocol.DataUpdateNoSpace {
			cancel()
		}
	})
	switch last.Phase {
	case coreprotocol.DataUpdateBlocked:
		return errors.New("the data update stopped; see core.log before restarting Crude")
	case coreprotocol.DataUpdateNewer:
		return errors.New("the saved data comes from a newer Crude; install that Crude to open it")
	case coreprotocol.DataUpdateNoSpace:
		return fmt.Errorf("not enough disk space to update the saved data: the backups need %d bytes and the disk has %d bytes free; open Crude to try again", last.Needed, last.Free)
	}
	return ctx.Err()
}

// IsCoreUpdating reports a session refused because the core updates saved data.
func IsCoreUpdating(err error) bool {
	var fault *coreprotocol.Fault
	return errors.As(err, &fault) && fault.Code == coreprotocol.FaultCoreUpdating
}
