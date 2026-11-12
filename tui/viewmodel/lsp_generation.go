package viewmodel

import (
	"context"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/tui/editor"
	"crude/workspace"
	"errors"
)

// knownLSPBusy accepts only the core's definite refusal. An uncertain
// transport outcome cannot be replayed even when it wraps a busy fault.
func knownLSPBusy(err error) bool {
	var uncertain *coreclient.UncertainError
	var fault *coreprotocol.Fault
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &uncertain) && errors.As(err, &fault) && fault.Code == "busy"
}

// A manual request can wait through startup. Automatic popup requests continue
// to require readiness so ordinary typing never starts a long-lived search.
func (m *App) manualLSPAvailable() bool {
	service := m.ws.LSPMgr()
	if service == nil {
		return false
	}
	if service.ServerReady() {
		return true
	}
	_, state, _ := service.Status()
	return state == workspace.LSPStarting
}

func (m *App) lspProducerConnected(instance string) bool {
	if instance == "" || m.ws.LSPMgr() == nil {
		return false
	}
	for _, health := range m.ws.LSPMgr().ServerHealthSnapshots() {
		if health.InstanceID == instance && health.Connected {
			return true
		}
	}
	return false
}

func (m *App) logLSPFailure(operation string, err error) {
	if err != nil && m.ws.Logger() != nil {
		m.ws.Logger().Warn("LSP", "request_failed", "operation", operation, "error", err.Error())
	}
}

// resetLSPRequestContext invalidates every request issued by the previous
// workspace generation. Result messages still carry a generation as a second
// ownership check because an LSP implementation may return after cancellation.
func (m *App) resetLSPRequestContext() {
	if m == nil {
		return
	}
	m.closeSignatureHelp()
	m.closeCompletionPopup()
	m.closeCodeActions()
	m.closeNavigation()
	m.resetInlayHints()
	m.resetSemanticTokens()
	if m.lspRequestCancel != nil {
		m.lspRequestCancel()
	}
	m.lspRequestCtx, m.lspRequestCancel = context.WithCancel(context.Background())
}

func (m *App) lspRequestContext() (context.Context, uint64) {
	if m.lspRequestCtx == nil {
		m.resetLSPRequestContext()
	}
	return m.lspRequestCtx, m.workspaceSwitchGen.Load()
}

func (m *App) acceptsLSPGeneration(generation uint64) bool {
	return m != nil && generation == m.workspaceSwitchGen.Load()
}

func (m *App) acceptsLSPDocument(generation uint64, path string, owner *editor.Editor, revision uint64) bool {
	return m.acceptsLSPGeneration(generation) && owner != nil && m.EditorState.Editor == owner && owner.IsOpen() && owner.FilePath() == path && owner.Revision() == revision
}
