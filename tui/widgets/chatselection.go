package widgets

import (
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/rendering"
	"crude/tui/types"
)

func (m ChatModel) handleMouse(msg tea.MouseMsg) (ChatModel, tea.Cmd) {
	chatBottom := m.chatAreaTop + m.chatAreaHeight - 1

	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		if msg.Y >= m.chatAreaTop && msg.Y <= chatBottom {
			lineIdx, ok := m.MouseToAllLine(msg.Y)
			if ok {
				m.selecting = true
				m.selAnchorLine = lineIdx
				m.selAnchorCol = rendering.Max(0, msg.X-m.theme.Spacing.PadLR)
				m.selEndLine = lineIdx
				m.selEndCol = m.selAnchorCol
				m.selMouseY = msg.Y
				m.selAutoScroll = false
			}
		} else {
			m.selecting = false
			m.selAutoScroll = false
		}

	case msg.Action == tea.MouseActionMotion && m.selecting:
		lineIdx, ok := m.MouseToAllLine(msg.Y)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = rendering.Max(0, msg.X-m.theme.Spacing.PadLR)
		} else if msg.Y < m.chatAreaTop {
			firstLine, _ := m.ChatAreaContentBounds()
			if firstLine >= 0 {
				m.selEndLine = firstLine
				m.selEndCol = 0
			}
		} else if msg.Y > chatBottom {
			_, lastLine := m.ChatAreaContentBounds()
			if lastLine >= 0 {
				m.selEndLine = lastLine
				m.selEndCol = 1 << 30
			}
		}
		m.selMouseY = msg.Y

		if !m.selAutoScroll && (m.selMouseY <= m.chatAreaTop-1 || m.selMouseY >= chatBottom+1) {
			m.selAutoScroll = true
			return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
				return types.SelectionTickMsg(t)
			})
		}

	case msg.Action == tea.MouseActionRelease && m.selecting:
		m.selecting = false
		m.selAutoScroll = false
		sLine, sCol, eLine, eCol := m.SelectionRange()
		text := rendering.ExtractSelectedText(m.cachedAllLines, sLine, sCol, eLine, eCol)
		text = strings.TrimRight(text, " \t\n\r")
		m.selAnchorLine = 0
		m.selAnchorCol = 0
		m.selEndLine = 0
		m.selEndCol = 0
		if text != "" {
			return m, rendering.CopyToClipboardCmd(text)
		}

	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		now := time.Now()
		elapsed := now.Sub(m.lastScrollTime).Seconds()
		if elapsed < 0.1 {
			m.scrollVelocity = math.Min(m.scrollVelocity+0.5, 5)
		} else {
			m.scrollVelocity = 1
		}
		m.lastScrollTime = now
		n := int(m.scrollVelocity)
		if n < 1 {
			n = 1
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.ScrollUp(n)
		} else {
			m.ScrollDown(n)
		}
	}

	return m, nil
}

func (m ChatModel) handleSelectionTick() (ChatModel, tea.Cmd) {
	if !m.selecting || !m.selAutoScroll {
		return m, nil
	}

	chatBottom := m.chatAreaTop + m.chatAreaHeight - 1

	if m.selMouseY <= m.chatAreaTop-1 {
		m.ScrollUp(1)
		lineIdx, ok := m.MouseToAllLine(m.chatAreaTop)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = 0
		}
	} else if m.selMouseY >= chatBottom+1 {
		m.ScrollDown(1)
		lineIdx, ok := m.MouseToAllLine(chatBottom)
		if ok {
			m.selEndLine = lineIdx
			m.selEndCol = 1 << 30
		}
	}

	if m.selMouseY <= m.chatAreaTop-1 || m.selMouseY >= chatBottom+1 {
		return m, tea.Tick(selScrollInterval, func(t time.Time) tea.Msg {
			return types.SelectionTickMsg(t)
		})
	}

	m.selAutoScroll = false
	return m, nil
}

func (m ChatModel) SelectionRange() (startLine, startCol, endLine, endCol int) {
	if m.selAnchorLine < m.selEndLine ||
		(m.selAnchorLine == m.selEndLine && m.selAnchorCol <= m.selEndCol) {
		return m.selAnchorLine, m.selAnchorCol, m.selEndLine, m.selEndCol
	}
	return m.selEndLine, m.selEndCol, m.selAnchorLine, m.selAnchorCol
}

func (m *ChatModel) HasActiveSelection() bool {
	if !m.selecting && m.selAnchorLine == m.selEndLine && m.selAnchorCol == m.selEndCol {
		return false
	}
	return m.selAnchorLine != m.selEndLine || m.selAnchorCol != m.selEndCol
}

func (m *ChatModel) ClearSelection() {
	m.selecting = false
	m.selAutoScroll = false
	m.selAnchorLine = 0
	m.selAnchorCol = 0
	m.selEndLine = 0
	m.selEndCol = 0
}
