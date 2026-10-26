package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/metrics"
	"crude/perfdata"
	"crude/shellport"
	"crude/types/control"
	"crude/types/inspect"
)

type diagnosticControlDoneMsg struct {
	request    shellport.ControlRequestMsg
	generation uint64
	result     control.Result
}

// Admission is serialized by Update; collection uses the existing joined
// client-I/O lifecycle. No chart visibility, focus or dashboard state is changed.
func (m *App) handleDiagnosticControl(msg shellport.ControlRequestMsg) (tea.Cmd, bool) {
	r := msg.Envelope.Request
	if r.CommandID != "performance.shell" && r.CommandID != "connection.inspect" {
		return nil, false
	}
	fail := func(code control.FaultCode, text string) (tea.Cmd, bool) {
		return m.failShellControl(msg, code, text), true
	}
	d, known := control.Lookup(r.CommandID)
	port, ok := m.ws.(shellport.ShellControl)
	if !known || !ok || port != msg.Owner || m.asyncIOStopped.Load() {
		return fail(control.Unavailable, "exact shell diagnostic owner is detached")
	}
	if !reflect.DeepEqual(d, msg.Envelope.Descriptor) {
		return fail(control.Stale, "diagnostic schema or effect changed")
	}
	current := port.ControlIdentity()
	if r.Target.SessionID != "" {
		if m.ws.SessionsByID()[r.Target.SessionID] == nil {
			return fail(control.Unavailable, "bound diagnostic session unavailable")
		}
		current.SessionID = r.Target.SessionID
	}
	// An execution binding carries caller provenance, not another shell to read.
	current.ExecutionID = r.Target.ExecutionID
	if f := r.ValidateAgainst(d, control.Current{Target: current}); f != nil {
		return m.replyShellControl(msg, control.Result{State: control.Failed, Fault: f}), true
	}
	if r.Target.ResourceID != "" || r.Preconditions.Revision != nil || r.Preconditions.OpenEpoch != nil {
		return fail(control.Invalid, "diagnostic read uses snapshot_id/offset, not resource or UI edit preconditions")
	}
	if r.CommandID == "connection.inspect" {
		if err := decodeShellInput(r.Input, &struct{}{}); err != nil {
			return fail(control.Invalid, err.Error())
		}
		health := m.connectionHealth()
		value := struct {
			Shell     *control.ShellOwner `json:"shell"`
			Taken     time.Time           `json:"taken"`
			State     string              `json:"state"`
			Carrier   string              `json:"carrier"`
			RTTMicros int64               `json:"rtt_us"`
			RTTKnown  bool                `json:"rtt_known"`
			Notice    string              `json:"notice,omitempty"`
			Scope     string              `json:"scope"`
		}{current.Shell, time.Now(), health.status.State, health.status.Carrier, health.status.RTT.Microseconds(), health.status.RTT > 0, health.status.Notice, "Current carrier observation only; does not establish applied replica freshness or model-provider health."}
		raw, _ := json.Marshal(value)
		return m.replyShellControl(msg, control.Result{State: control.Applied, Output: raw}), true
	}
	var in coreprotocol.PerformanceInspectRequest
	if err := decodeShellInput(r.Input, &in); err != nil {
		return fail(control.Invalid, err.Error())
	}
	if _, err := in.Page.Normalize(); err != nil {
		return fail(control.Invalid, err.Error())
	}
	if m.shellControlPending >= 16 {
		return fail(control.Unavailable, "shell diagnostic capacity occupied")
	}
	m.shellControlPending++
	generation := m.workspaceSwitchGen.Load()
	key := fmt.Sprintf("%s/%d/%s/%s/%s", current.Shell.ClientID, current.Shell.Epoch, current.WorkspaceID, r.Caller.AgentID, r.Caller.RunID)
	if r.Caller.Kind == control.ExternalCaller {
		key += "/" + r.Caller.ClientID
	}
	// Register the command's owned work only on invocation, as other client I/O;
	// cancellation at switch/shutdown prevents a stale sample being installed.
	cmd := m.clientIOCommand(context.Background(), func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 3*time.Second)
		defer cancel()
		page, err := metrics.InspectWith(ctx, &m.diagnosticReads, key, metrics.InspectQuery{Page: in.Page, Seconds: in.Seconds, Points: in.Points}, func(s metrics.Snapshot) []inspect.Record { return perfdata.InspectionRecords(s, "shell") })
		result := control.Result{State: control.Applied}
		if err != nil {
			result.State = control.Failed
			result.Fault = &control.Fault{Code: control.Invalid, Message: err.Error()}
		} else {
			result.Output, _ = json.Marshal(page)
		}
		return diagnosticControlDoneMsg{request: msg, generation: generation, result: result}
	})
	return cmd, true
}

func (m *App) finishDiagnosticControl(msg diagnosticControlDoneMsg) tea.Cmd {
	if m.shellControlPending > 0 {
		m.shellControlPending--
	}
	if msg.generation != m.workspaceSwitchGen.Load() || m.asyncIOStopped.Load() {
		return m.failShellControl(msg.request, control.Stale, "original diagnostic shell attachment changed")
	}
	port, ok := m.ws.(shellport.ShellControl)
	if !ok || port != msg.request.Owner || !reflect.DeepEqual(port.ControlIdentity().Shell, msg.request.Envelope.Request.Target.Shell) {
		return m.failShellControl(msg.request, control.Stale, "original diagnostic shell epoch changed")
	}
	return m.replyShellControl(msg.request, msg.result)
}
