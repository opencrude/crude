package widgets

func (m *ChatModel) ScrollUp(n int) {
	m.offset += n
	m.scrolled = true
}

func (m *ChatModel) ScrollDown(n int) {
	m.offset -= n
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset == 0 {
		m.scrolled = false
	}
}

func (m *ChatModel) ScrollToBottom() {
	m.scrolled = false
	m.offset = 0
}

func (m *ChatModel) Scrolled() bool {
	return m.scrolled
}

func (m *ChatModel) ChatOffset() int {
	return m.offset
}

func (m *ChatModel) ClampOffset(maxOffset int) {
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *ChatModel) MouseToAllLine(mouseY int) (allIdx int, ok bool) {
	if len(m.cachedAllLines) == 0 {
		return -1, false
	}
	chatY := mouseY - m.chatAreaTop
	if chatY < 0 || chatY >= m.chatAreaHeight {
		return -1, false
	}
	total := len(m.cachedAllLines)
	maxOffset := total - m.chatAreaHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.offset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - m.chatAreaHeight
	if startIdx < 0 {
		startIdx = 0
	}
	visible := endIdx - startIdx
	emptyTop := m.chatAreaHeight - visible
	if emptyTop < 0 {
		emptyTop = 0
	}
	if chatY < emptyTop {
		return -1, false
	}
	allIdx = startIdx + (chatY - emptyTop)
	if allIdx < 0 || allIdx >= total {
		return -1, false
	}
	return allIdx, true
}

func (m *ChatModel) ChatAreaContentBounds() (firstLine, lastLine int) {
	if len(m.cachedAllLines) == 0 {
		return -1, -1
	}
	total := len(m.cachedAllLines)
	maxOffset := total - m.chatAreaHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	offset := m.offset
	if offset > maxOffset {
		offset = maxOffset
	}
	endIdx := total - offset
	startIdx := endIdx - m.chatAreaHeight
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	return startIdx, endIdx - 1
}

func (m *ChatModel) SetCachedAllLines(lines []string) { m.cachedAllLines = lines }

func (m *ChatModel) SetChatAreaBounds(top, height int) {
	m.chatAreaTop = top
	m.chatAreaHeight = height
}
