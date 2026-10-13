package viewmodel

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types/control"
)

type shellControlHost struct {
	host   *ui.Host
	prefix string
	target control.Target
	window *wm.FloatingWindow
	body   ui.Component
}

// Only the foreground input owner is exposed above a modal boundary. A
// permission surface is inspectable, but its actions still require authority.
func (m *App) controlHosts(target control.Target) []shellControlHost {
	m.configureShell()
	m.rootHost.MarkLayout()
	var floating *wm.FloatingWindow
	if m.windowManager != nil {
		floating = m.windowManager.Focused()
		for _, window := range m.windowManager.Windows() {
			if window.Visible() && window.Modal() {
				floating = window
			}
		}
	}
	if floating != nil {
		if body, ok := floating.Content.(interface{ Host() *ui.Host }); ok && body.Host() != nil {
			w, h := floating.Size()
			body.Host().Resize(w, h)
			body.Host().MarkLayout()
			return []shellControlHost{{host: body.Host(), prefix: fmt.Sprintf("floating/%s/%d::", floating.ID, body.Host().ControlLifetime()), target: target, window: floating}}
		}
		return nil
	}
	var hosts []shellControlHost
	if !m.sessionDeckBlocking() {
		hosts = append(hosts, shellControlHost{host: m.rootHost, target: target})
	}
	if projection := m.activeSessionDeck(); projection != nil && projection.deck.IsOpen() && m.ActiveSession != nil {
		deckTarget := target
		deckTarget.SessionID = m.ActiveSession.SessionID
		var body ui.Component
		if panel := projection.deck.Active(); panel != nil {
			body = panel.Content
		}
		hosts = append(hosts, shellControlHost{host: projection.deckHost, prefix: fmt.Sprintf("deck/%s/%d::", deckTarget.SessionID, projection.deckHost.ControlLifetime()), target: deckTarget, body: body})
	}
	return hosts
}

func (m *App) resolveControlHost(id string, target control.Target) (shellControlHost, ui.ComponentID, error) {
	for _, owner := range m.controlHosts(target) {
		if owner.prefix != "" && strings.HasPrefix(id, owner.prefix) {
			return owner, ui.ComponentID(strings.TrimPrefix(id, owner.prefix)), nil
		}
		if owner.prefix == "" && !strings.Contains(id, "::") {
			return owner, ui.ComponentID(id), nil
		}
	}
	return shellControlHost{}, "", errors.New("component host lifetime is unavailable or obscured")
}

func qualifyControlNode(node control.Node, owner shellControlHost) control.Node {
	localID := ui.ComponentID(node.ID)
	node.ID = owner.prefix + node.ID
	node.ParentID = owner.prefix + node.ParentID
	node.Target.ResourceID = node.ID
	// Draft text is private. Contextual owners may explicitly release safe values;
	// generic inventory never leaks passwords, composer or unrelated text.
	allowed := false
	if metadata, ok := owner.body.(interface{ ControlValueAllowed(ui.ComponentID) bool }); ok {
		allowed = metadata.ControlValueAllowed(localID)
	}
	if owner.window != nil {
		if frame, ok := owner.window.Content.(*ui.WindowFrameWidget); ok {
			if metadata, ok := frame.Body().(interface{ ControlValueAllowed(ui.ComponentID) bool }); ok {
				allowed = metadata.ControlValueAllowed(localID)
			}
		}
	}
	if !allowed {
		node.Value = nil
	}
	return node
}

func (m *App) inspectShellControls(target control.Target, req control.PageRequest) (control.Page, error) {
	if req.Limit < 1 || req.Limit > control.MaxPageLimit {
		return control.Page{}, errors.New("limit must be 1..128")
	}
	owners := m.controlHosts(target)
	// Host cursors bind their complete source, including offscreen items. Probe
	// one record per host to fence changes in hosts not reached on this page;
	// never materialize their entire collections in the shell adapter.
	hash := sha256.New()
	for _, owner := range owners {
		probe, err := owner.host.InspectControls(owner.target, control.PageRequest{Limit: 1})
		if err != nil {
			return control.Page{}, err
		}
		fmt.Fprintf(hash, "%s:%s;", owner.prefix, strings.Split(probe.NextCursor, ":")[0])
		for _, node := range probe.Nodes {
			fmt.Fprintf(hash, "%s:%d:%d;", node.ID, node.OpenEpoch, node.Revision)
		}
	}
	stamp := fmt.Sprintf("%x", hash.Sum(nil))
	type position struct {
		Stamp  string
		Host   int
		Cursor string
	}
	next := position{Stamp: stamp}
	if req.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil || json.Unmarshal(raw, &next) != nil || next.Host < 0 || next.Host >= len(owners) {
			return control.Page{}, errors.New("invalid control cursor")
		}
		if next.Stamp != stamp {
			return control.Page{}, errors.New("control inventory changed")
		}
	}
	page := control.Page{Nodes: []control.Node{}}
	bytes := 0
	for next.Host < len(owners) {
		owner := owners[next.Host]
		remaining := control.MaxValueBytes - bytes - len(page.Nodes)
		if remaining < 2048 {
			break
		}
		part, err := owner.host.InspectControlsProjected(owner.target, control.PageRequest{Limit: req.Limit - len(page.Nodes), Cursor: next.Cursor}, remaining, func(node control.Node) control.Node {
			return qualifyControlNode(node, owner)
		})
		if err != nil {
			return control.Page{}, err
		}
		for _, node := range part.Nodes {
			raw, err := json.Marshal(node)
			if err != nil {
				return control.Page{}, err
			}
			bytes += len(raw)
			page.Nodes = append(page.Nodes, node)
		}
		next.Cursor = part.NextCursor
		if next.Cursor == "" {
			next.Host++
		}
		if len(page.Nodes) == req.Limit || part.NextCursor != "" {
			break
		}
	}
	if next.Host < len(owners) {
		raw, err := json.Marshal(next)
		if err != nil {
			return control.Page{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
