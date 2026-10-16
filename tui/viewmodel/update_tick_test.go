package viewmodel

import "testing"

// The idle launcher previously scheduled the 16 ms streaming tick forever
// (AgentMgr.Count()==0 && ActiveView==""), repainting full frames at ~60 Hz
// even with nothing animating: the "floating terminal burns 26% CPU" storm.
// The fast tick is reserved for real streaming/reveal activity.
func TestWantsFastTick(t *testing.T) {
	app := newTestApp(t)
	if app.wantsFastTick() {
		t.Fatal("idle launcher must use the normal 400ms tick, not the 16ms streaming tick")
	}

	app.reveals = map[string]revealAnim{"popup": {}}
	if !app.wantsFastTick() {
		t.Fatal("mid-flight popup reveal must keep the 16ms streaming tick")
	}
	delete(app.reveals, "popup")
	if app.wantsFastTick() {
		t.Fatal("after the reveal completes the fast tick must stop")
	}
}
