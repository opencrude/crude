# Virtual List / Windowing Architecture Plan

## 1. Current State Analysis

### What Exists Today
- **ChatModel** (`tui/widgets/chatmodel.go`) — flat list of `*ChatMessage`, integer offset scrolling, per-message `CachedLines`, dirty flag
- **RenderAllMessages** (`tui/widgets/chatmessage.go`) — renders ALL messages to a `[]string`, caches per-message lines
- **ChatScroll** (`tui/widgets/chatscroll.go`) — simple `offset` integer with ScrollUp/Down/ToBottom, ClampOffset, MouseToAllLine
- **ChatSelection** (`tui/widgets/chatselection.go`) — mouse selection with anchor/end line+col in global-line space, auto-scroll on edge drag
- **App.View** (`tui/app/view.go`) — slices `chatLines[startIdx:endIdx]` for visible window, applies selection highlight

### Problems Identified
| Problem | Impact |
|---|---|
| O(n) full render on every dirty cycle | Thousands of messages = frame drops |
| No scrollbar | No position awareness, hard to navigate long history |
| `CachedLines` stored per-message but all still re-split on View | Wasteful memory + compute |
| Markdown re-render on width change invalidates everything | Glamour is expensive |
| Offset is a bare int — no smooth/animated scroll | Janky feel |
| Hit testing (MouseToAllLine) uses global line index but doesn't verify message boundaries | Subtle bugs with cursor/message mapping |
| No overscan / render-ahead | Visible pop-in when scrolling fast |
| Height estimation is rendering itself | No cheap height pass |

---

## 2. Target Architecture

### Component Tree
```
App.View()
├── Header
├── Separator
├── MessageList (replaces chatLines + visible slicing)
│   ├── VirtualList (generic)
│   │   ├── HeightIndex (prefix-sum binary search)
│   │   ├── RenderSlots (visible + overscan items)
│   │   └── ScrollState (offset, velocity, animation)
│   ├── ScrollBar (vertical, right-aligned, on-demand)
│   │   ├── Track
│   │   └── Thumb (adaptive, draggable, auto-hide)
│   └── SelectionLayer (highlight over visible lines)
├── AsyncBar (if active)
├── Separator
├── Input
├── Separator
├── StatusBar
```

### New Packages (under `tui/`)
```
tui/
├── virtlist/           # Generic virtual list (no chat knowledge)
│   ├── virtlist.go     # Core: items, height index, viewport calc
│   ├── scroll.go       # Scroll state, animation, clamping
│   └── measure.go      # Height measurement helpers
├── scrollbar/          # Standalone scrollbar component
│   ├── scrollbar.go    # Model, Update, View, mouse handling
│   └── style.go        # Visual styling
├── widgets/
│   ├── messagelist.go  # NEW: ChatModel → MessageList refactor
│   ├── chatmodel.go    # KEPT: message mutations (AppendToken etc.)
│   ├── chatmessage.go  # KEPT: per-message renderers
│   ├── chatscroll.go   # DEPRECATED → replaced by virtlist scroll
│   ├── chatselection.go # REFACTORED: selection on virtual list
│   └── ...
```

---

## 3. VirtualList — Core Design

### 3.1 Item Representation
```go
type Item struct {
    ID      string      // stable unique key (e.g., msg hash or index)
    Height  int         // cached line count; -1 means "measure first"
    Dirty   bool        // true = content changed, re-render needed
    Rendered []string   // the rendered lines (populated lazily)
    Data    interface{} // the underlying ChatMessage or other payload
}
```

### 3.2 Height Index (prefix-sum array)
```go
type HeightIndex struct {
    prefixSums []int   // prefixSums[i] = sum of heights[0..i-1]
    total      int     // grand total = sum of all heights
}

func (hi *HeightIndex) Rebuild(items []Item) {
    // O(n) rebuild; called when any height changes
    // prefixSums[i] = prefixSums[i-1] + items[i-1].Height
}

func (hi *HeightIndex) ItemAtLine(globalLine int) (itemIdx int, localLine int) {
    // Binary search on prefixSums → O(log n)
}

func (hi *HeightIndex) LineOfItem(itemIdx int) int {
    // Returns the global line where this item starts
}
```

### 3.3 Viewport Calculation
```go
func (vl *VirtualList) VisibleRange() (firstItem, lastItem int, skipLines int) {
    // Given: scrollY, viewportH
    // 1. firstItem, skipLines = index.ItemAtLine(scrollY)
    //    skipLines = how many lines of firstItem are scrolled off top
    // 2. Walk forward accumulating heights until sum >= viewportH
    //    → lastItem determined
    // 3. Add OVERSCAN items (2 above, 2 below) for smooth scrolling
}
```

### 3.4 Lazy Rendering
```
1. On dirty cycle:
   a. Rebuild HeightIndex (only items whose heights changed)
   b. Compute VisibleRange()
   c. For each visible item that is Dirty:
      - Call RenderFn(item.Data, width) → []string
      - Cache in item.Rendered
      - Update item.Height
   d. Rebuild HeightIndex again if any heights changed
   e. Slice and concatenate rendered lines for viewport output
```

### 3.5 Key Operations
```go
ScrollBy(lines int)           // relative scroll
ScrollToItem(idx int)         // absolute scroll (ensure visible)
ScrollToBottom()              // tail-follow mode
ScrollToGlobalLine(line int)  // precise jump
SetViewport(h int)            // terminal resize
InvalidateItem(id string)     // mark single message dirty
InvalidateAll()               // full rebuild (width change)
```

---

## 4. ScrollBar — Design

### 4.1 Visual Design
- **Position**: Rightmost 2 columns of the chat area
- **Track**: Full chat height, subtle dim color
- **Thumb**: Block character `█` or `▓`, accent color, minimum 1 char tall
- **Behavior**: Hidden by default, appears on scroll, fades after 1.5s idle

### 4.2 Thumb Calculation
```go
contentH  := vl.TotalHeight()  // total lines of all messages
visibleH  := vl.ViewportHeight()
maxScroll := max(0, contentH - visibleH)

if maxScroll == 0 {
    thumbHeight = trackHeight  // full track
    thumbTop = 0
} else {
    // Proportion: thumb represents visible portion
    thumbHeight = max(1, (visibleH * trackHeight) / contentH)
    availableTrack = trackHeight - thumbHeight
    thumbTop = (scrollY * availableTrack) / maxScroll
}
```

### 4.3 Mouse Interaction
| Action | Behavior |
|---|---|
| Click on track (above thumb) | Page Up (scroll by viewportH) |
| Click on track (below thumb) | Page Down |
| MouseDown on thumb | Start drag |
| MouseMove while dragging | scrollY = (mouseY - dragOffset) / trackHeight * maxScroll |
| MouseUp | End drag, start fade timer |
| MouseWheel over chat area | Scroll by N lines (velocity-accelerated) |

### 4.4 Auto-Hide FSM
```
[HIDDEN] -- any scroll event --> [VISIBLE]
[VISIBLE] -- 1.5s no scroll --> [FADING] (dim style)
[FADING] -- 0.5s no scroll --> [HIDDEN]
[FADING] -- any scroll event --> [VISIBLE]
```

### 4.5 Rendering
```go
func (sb *ScrollBar) Render() string {
    // Returns a vertical column string of height = trackHeight
    // Each line is either: "  " (empty), " ░" (track), or " █" (thumb)
    // Uses lipgloss styles for colors
}
```

---

## 5. MessageList — Chat Integration

### 5.1 Refactored ChatModel → MessageList
`MessageList` wraps `VirtualList` and provides chat-specific operations:

```go
type MessageList struct {
    vl        virtlist.VirtualList
    scrollbar scrollbar.ScrollBar
    selection SelectionState
    // ... chat-specific fields
}
```

### 5.2 Height Estimation Strategy
Before rendering, we need approximate heights. Strategy:
1. **Non-markdown messages**: Height = len(WrapText(content, width)) — cheap
2. **Markdown messages**: Use cached rendered height; if dirty, render markdown (expensive) and cache
3. **Streaming messages**: Update incrementally — only re-wrap the last line
4. **Width changes**: Invalidate all heights, force full re-measure

### 5.3 Streaming Optimization
For the currently-streaming assistant message:
- Pre-compute completed portion (lines up to last `\n`) and cache those
- Only re-wrap the last incomplete line
- On each token, update only that message's height in the index (O(log n) insert, not O(n) rebuild)

### 5.4 Selection on Virtual List
Selection operates in **global line coordinates** (like today), which is correct for a virtual list:
```go
type SelectionState struct {
    AnchorLine, AnchorCol int   // global coordinates
    EndLine, EndCol       int
    Active                bool
    AutoScroll            bool
}
```
- `ApplySelection(line string, globalLineIdx int)` — checks if `globalLineIdx` falls in selection range
- Selection highlight applied only to visible lines (the ones actually rendered)

### 5.5 Message → Line Mapping
```
Message A (height 5): global lines 0-4
Message B (height 3): global lines 5-7
Message C (height 8): global lines 8-15
...
```
The HeightIndex binary search gives us `ItemAtLine(globalLine) → (msgIdx, localLine)`.

---

## 6. Rendering Pipeline (per frame)

```
1. App.View() called
2. MessageList.Render(width, height):
   a. If any message Dirty → invalidate in VirtualList
   b. vl.BuildHeightIndex()    (incremental: only changed items)
   c. vl.ClampScroll()
   d. visibleRange := vl.VisibleRange(overscan=4)
   e. For each item in visibleRange:
      - If item.Dirty → item.Rendered = renderMessage(item.Data, width)
      - Store item.Height = len(item.Rendered)
   f. If any height changed during (e) → rebuild index, reclamp, recalculate visible range
   g. Build output lines:
      - For firstItem: slice from skipLines to end
      - For middle items: full Rendered
      - For lastItem: slice 0 to remaining viewport space
   h. Apply selection highlight on output lines
   i. Render scrollbar overlay
3. App.View() pads and assembles with header/input/statusbar
```

---

## 7. Edge Cases & Robustness

### 7.1 Content Changes Above Viewport
When a message *before* the viewport changes height (e.g., streaming tool result expands), the scroll position must adjust to keep visible content stable:
```go
// If height of items before viewport changed:
scrollY += (newHeight - oldHeight)
// But if user is at bottom (auto-scroll), stay at bottom:
if autoScroll { scrollY = maxScroll }
```

### 7.2 Terminal Resize
1. Width change → all rendered lines invalid (re-wrap needed)
2. Height change → viewport grows/shrinks, clamp scroll
3. Both trigger full HeightIndex rebuild

### 7.3 Empty State
- Zero messages: viewport shows empty area, scrollbar hidden
- Content shorter than viewport: scrollbar shows full-track thumb, scroll disabled

### 7.4 Wide Characters & ANSI Sequences
- All width calculations use `runewidth.StringWidth()`
- ANSI sequences stripped before width measurement
- Selection application must skip ANSI bytes (already done in `ApplySelection`)

### 7.5 Streaming + Auto-Scroll
- When new tokens arrive and `autoScroll == true` (user at bottom), stay pinned
- If user scrolls up during streaming, `autoScroll = false`, don't jump
- When streaming ends, if near-bottom (within 3 lines), snap to bottom

### 7.6 Drag Selection Near Edges
- Mouse near top edge → scroll up by 1 line per tick, extend selection
- Mouse near bottom edge → scroll down by 1 line per tick, extend selection
- Already implemented — adapt to use `ScrollBy(1)` on VirtualList

---

## 8. Scroll Physics & Animation

### 8.1 Mouse Wheel Acceleration (Existing — Keep)
```go
if timeSinceLastScroll < 100ms {
    velocity = min(velocity + 0.5, 5.0)
} else {
    velocity = 1.0
}
scrollBy(int(velocity))
```

### 8.2 Smooth Scroll (Optional Enhancement)
Not strictly needed for initial implementation but nice-to-have:
- On PgUp/PgDown, animate scroll over ~100ms
- Use `tea.Tick` with easing function
- Can be added later without architecture changes

### 8.3 Snap-to-Message (Optional)
- When scrolling with keyboard, optionally snap to message boundaries
- Useful for browsing conversation turns
- Implement as post-processing on scroll target

---

## 9. Performance Budget

| Operation | Target Complexity | Notes |
|---|---|---|
| Height index rebuild | O(n) | Only when heights change; n = message count |
| ItemAtLine (binary search) | O(log n) | Per mouse event / frame |
| VisibleRange calculation | O(k + log n) | k = visible items (~50 max) |
| Render one message | O(text_len) | Markdown is the bottleneck |
| Selection apply | O(visible_lines) | Only on visible slice |
| ScrollBar render | O(track_height) | < terminal height |

With 10,000 messages averaging 5 lines each (50,000 total lines):
- Height index: 10,000 ints = 80KB — trivial
- Binary search: ~14 steps — instant
- Rendering: only ~50 messages in viewport (with overscan) — fast

---

## 10. Implementation Plan — Phased Steps

### Phase 1: VirtualList Core (`tui/virtlist/`)
- [x] `Item` struct and `HeightIndex`
- [x] `VirtualList` struct with Items, ScrollY, ViewportH
- [x] `BuildIndex()`, `ItemAtLine()`, `LineOfItem()`
- [x] `VisibleRange()` with overscan
- [x] `ScrollBy()`, `ScrollTo()`, `ScrollToBottom()`, `ClampScroll()`
- [x] `RenderVisible()` with lazy rendering and height-change reindex
- [x] `ScrollState` with wheel acceleration and auto-scroll detection
- [x] `measure.go` with height estimation and word-wrap utilities
- [x] Unit tests for binary search correctness (42 tests passing)

### Phase 2: ScrollBar (`tui/scrollbar/`)
- [x] `ScrollBar` model with Update/View
- [x] Thumb size/position calculation
- [x] Mouse handling: click-track, drag-thumb (Action/Button pattern)
- [x] Auto-hide FSM with fade timer (exported FadeTickMsg/HideTickMsg)
- [x] `style.go` with ANSI style constants

### Phase 3: MessageList (`tui/widgets/messagelist.go`)
- [x] Wrap VirtualList with chat-specific logic
- [x] Height estimation: cheap wrap for plain text, cached render for markdown
- [x] Lazy render: only visible+overscan messages rendered
- [x] Selection integration: global-line coordinates on virtual list
- [x] Streaming optimization: incremental last-line updates
- [x] Width change handling
- [x] ScrollBar integration (render + timer routing)

### Phase 4: Integration
- [x] Wire MessageList into App.View() replacing current chatLines slicing
- [x] Wire ScrollBar into viewport right side
- [x] Adapt existing mouse/keyboard handlers
- [x] Dual-write to both ChatModel and MessageList for backward compatibility
- [ ] Remove deprecated chatscroll.go and ChatModel dual-write (requires full regression testing)
- [ ] Full regression test: scrolling, selection, copy, resize, streaming

### Phase 5: Polish
- [ ] Animated smooth scrolling
- [x] Scrollbar auto-hide animation (FSM with fade)
- [ ] Message-level hit testing (click on message → show metadata)
- [ ] Performance profiling with 10k+ messages
- [ ] Edge case hardening

---

## 11. File Changes Summary

| File | Action |
|---|---|
| `tui/virtlist/virtlist.go` | **NEW** — VirtualList core |
| `tui/virtlist/scroll.go` | **NEW** — Scroll state & animation |
| `tui/virtlist/measure.go` | **NEW** — Height measurement utilities |
| `tui/scrollbar/scrollbar.go` | **NEW** — ScrollBar component |
| `tui/scrollbar/style.go` | **NEW** — ScrollBar styling |
| `tui/widgets/messagelist.go` | **NEW** — MessageList (replaces chat rendering orchestration) |
| `tui/widgets/chatmodel.go` | **MODIFY** — Keep message mutations, remove offset/scrolling |
| `tui/widgets/chatscroll.go` | **DELETE** — Replaced by virtlist scroll |
| `tui/widgets/chatselection.go` | **MODIFY** — Selection adapted for virtual list |
| `tui/widgets/chatmessage.go` | **MODIFY** — Extract per-message renderers to be called lazily |
| `tui/app/view.go` | **MODIFY** — Use MessageList.Render() instead of manual slicing |
| `tui/app/update.go` | **MODIFY** — Route scroll events through MessageList |
| `tui/app/app.go` | **MODIFY** — Replace ChatModel fields with MessageList + ScrollBar |
| `tui/types/types.go` | **MODIFY** — May add virtual-list-specific messages |

---

## 12. Design Principles

1. **Separation of concerns**: VirtualList knows nothing about chat; MessageList knows nothing about terminal rendering details; ScrollBar is a standalone component
2. **Lazy by default**: Nothing is rendered until it enters the viewport
3. **Cache aggressively but invalidate precisely**: Only dirty messages re-render; height changes trigger incremental index updates
4. **Coordinate space is global**: Selection, hit testing, scroll position all use global line numbers; conversion to local (message-relative) happens at the boundary
5. **Graceful degradation**: If markdown renderer fails, fall back to plaintext wrap
6. **Testable**: Each component has clear inputs/outputs, can be unit tested in isolation
