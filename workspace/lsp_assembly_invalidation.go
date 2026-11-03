package workspace

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	ttypes "crude/types"
)

const (
	assemblyWatchIdle = 150 * time.Millisecond
	assemblyWatchHold = time.Second
)

// invalidateAssemblyForWatch is the passive watch input to discovery. Source
// contents do not change name-based detection, but topology and ignore rules do.
// No filesystem operations run on the caller or under the manager lock.
func (m *LSPManager) invalidateAssemblyForWatch(ctx context.Context, path string, changeType int) {
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
	}
	if changeType != 1 && changeType != 3 && name != ".gitignore" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil || m.closed || !m.assemblyActive || !pathWithin(m.workspaceRoot, path) {
		return
	}
	m.invalidateAssemblyDiscoveryLocked()
	if !m.projectWatchUnreliable {
		m.scheduleAssemblyInvalidationLocked(ctx)
	}
}

// Fence the old scan immediately, not when the debounce timer fires. Otherwise
// it could reinstall pre-event evidence in the quiet interval.
func (m *LSPManager) invalidateAssemblyDiscoveryLocked() {
	m.discovery = nil
	if m.assemblyCancel != nil {
		m.assemblyCancel()
	}
	m.assemblyRequested++
	if m.assemblyActive && m.assembly.State != ttypes.AssemblyScanning {
		m.assembly.State, m.assembly.Message = ttypes.AssemblyScanning, "Crude is looking at the project files."
		m.bumpVersionLocked()
	}
}

func (m *LSPManager) scheduleAssemblyInvalidationLocked(ctx context.Context) {
	if m.closed || !m.assemblyActive {
		return
	}
	if m.assemblyInvalidationStarted.IsZero() {
		m.assemblyInvalidationStarted = time.Now()
	}
	delay := min(assemblyWatchIdle, time.Until(m.assemblyInvalidationStarted.Add(assemblyWatchHold)))
	if m.assemblyInvalidationTimer != nil {
		m.assemblyInvalidationTimer.Stop()
	}
	m.assemblyInvalidationEpoch++
	epoch := m.assemblyInvalidationEpoch
	root, settingsGeneration := m.workspaceRoot, m.settingsGeneration
	m.assemblyInvalidationTimer = time.AfterFunc(max(delay, 0), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed || epoch != m.assemblyInvalidationEpoch {
			return
		}
		m.assemblyInvalidationTimer = nil
		m.assemblyInvalidationStarted = time.Time{}
		if ctx.Err() == nil && !m.projectWatchUnreliable && root == m.workspaceRoot && settingsGeneration == m.settingsGeneration {
			m.requestAssemblyLocked()
		}
	})
}

func (m *LSPManager) cancelAssemblyInvalidationLocked() {
	m.assemblyInvalidationEpoch++
	if m.assemblyInvalidationTimer != nil {
		m.assemblyInvalidationTimer.Stop()
		m.assemblyInvalidationTimer = nil
	}
	m.assemblyInvalidationStarted = time.Time{}
}
