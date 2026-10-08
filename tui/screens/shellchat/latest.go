package shellchat

import "crude/tui/ui"

// latestButton consumes its native action locally: another selected chat can
// never receive the jump through the viewmodel's focused-screen alias.
type latestButton struct {
	*ui.Button
	screen *Screen
}

func (b *latestButton) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !b.screen.latestVisible() {
		return ui.EventResult{}
	}
	res := b.Button.Handle(ctx, evt)
	if res.Action != nil {
		b.screen.StickToBottom()
		res.Action = nil
		res.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return res
}
