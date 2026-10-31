package lsp

// CodeActionSupport observes only initialized static capabilities, never a dynamic grant.
func (c *Client) CodeActionSupport() (request, resolve bool) {
	if !c.initialized.Load() {
		return false, false
	}
	return c.staticCodeActionSupport()
}
