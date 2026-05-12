package settings

import (
	tea "github.com/charmbracelet/bubbletea"

	"crude/tui/widget"
)

func (m *Model) openModelSelect(target string) {
	m.selectTarget = target
	m.mode = modeModelSelect
	m.models.SetFetching(true)
}

func (m *Model) handleModelSelectResult(selected widget.ListItem, target string) {
	if m.selectTarget == "model" {
		m.form.SetFieldValue(3, selected.ID)
	} else {
		m.form.SetFieldValue(4, selected.ID)
	}

	ctx := 0
	inPrice := 0.0
	outPrice := 0.0
	cachePrice := 0.0
	for _, item := range m.models.Items() {
		if item.ID == selected.ID || item.Label == selected.Label {
			break
		}
	}

	if m.selectTarget == "model" {
		m.modelCtx = ctx
		m.modelInPrice = inPrice
		m.modelOutPrice = outPrice
		m.modelCachePrice = cachePrice
	} else {
		m.oracleCtx = ctx
		m.oracleInPrice = inPrice
		m.oracleOutPrice = outPrice
		m.oracleCachePrice = cachePrice
	}
}

func FetchModelsCmd(fn func(string, string, string) tea.Cmd, providerID, baseURL, apiKey string, isOracle bool) tea.Cmd {
	if fn == nil {
		return nil
	}
	return fn(providerID, baseURL, apiKey)
}
