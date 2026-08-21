package settings

import (
	"testing"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func chatgptPreset() types.ProviderPreset {
	return types.ProviderPreset{ID: "chatgpt", Name: "OpenAI (ChatGPT subscription)", StandardID: "codex"}
}

func TestChatGPTFormShowsSignInWhenNoOAuth(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]types.ProviderConfig{"chatgpt": {}},
	}, []types.ProviderPreset{chatgptPreset()})

	if f := m.categories[catProviders].Form.Field(fieldAPIKey); f != nil {
		t.Errorf("chatgpt form must not show an API-key field")
	}
	status := m.value(catProviders, "chatgpt-status")
	if status != "Not signed in" {
		t.Errorf("status = %q, want 'Not signed in'", status)
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signin"); f == nil || f.Target != "settings.chatgpt.signin" {
		t.Errorf("missing sign-in nav field")
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signout"); f != nil {
		t.Errorf("should not show sign-out when not signed in")
	}
}

func TestChatGPTFormShowsSignOutWhenSignedIn(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		ActiveProvider: "chatgpt",
		Providers: map[string]types.ProviderConfig{
			"chatgpt": {OAuth: &provider.OAuthCredentials{AccessToken: "AT", Email: "u@example.com"}},
		},
	}, []types.ProviderPreset{chatgptPreset()})

	if status := m.value(catProviders, "chatgpt-status"); status != "Signed in as u@example.com" {
		t.Errorf("status = %q", status)
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signout"); f == nil || f.Target != "settings.chatgpt.signout" {
		t.Errorf("missing sign-out nav field")
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signin"); f != nil {
		t.Errorf("should not show sign-in when already signed in")
	}
}

func TestSwitchToChatGPTRebuildsFormWithAuthRows(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		ActiveProvider: "openrouter",
		Providers: map[string]types.ProviderConfig{
			"openrouter": {},
			"chatgpt":    {OAuth: &provider.OAuthCredentials{AccessToken: "AT", AccountID: "acct_1"}},
		},
	}, []types.ProviderPreset{{ID: "openrouter", Name: "OpenRouter"}, chatgptPreset()})

	if f := m.categories[catProviders].Form.Field(fieldAPIKey); f == nil {
		t.Fatal("openrouter form should show an API-key field initially")
	}

	// Changing the provider select fires the form's OnChanged → rebuild.
	m.categories[catProviders].Form.SetValue(fieldProvider, ui.StringValue("chatgpt"))

	if m.providerID != "chatgpt" {
		t.Fatalf("providerID = %q, want chatgpt", m.providerID)
	}
	if f := m.categories[catProviders].Form.Field(fieldAPIKey); f != nil {
		t.Errorf("chatgpt form must not show an API-key field after switch")
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signout"); f == nil {
		t.Errorf("chatgpt form should show sign-out after switch (has OAuth)")
	}
	if status := m.value(catProviders, "chatgpt-status"); status != "Signed in (account acct_1)" {
		t.Errorf("status = %q", status)
	}
}

func TestSetChatGPTAuthSwapsRowsAndPreservesOAuthOnCollect(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]types.ProviderConfig{"chatgpt": {Model: "gpt-5.6"}},
	}, []types.ProviderPreset{chatgptPreset()})

	updated, _ := m.Update(ChatGPTSignInResultMsg{Creds: &provider.OAuthCredentials{AccessToken: "AT", Email: "a@b.com"}})
	m = &updated

	if status := m.value(catProviders, "chatgpt-status"); status != "Signed in as a@b.com" {
		t.Errorf("status = %q", status)
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signout"); f == nil {
		t.Errorf("sign-out nav should appear after sign-in")
	}

	// collectProviders copies the whole ProviderConfig, so OAuth survives while
	// the model field the user picked is also preserved.
	m.categories[catProviders].Form.SetValue(fieldModel, ui.StringValue("gpt-5.6"))
	got := m.AppSettings().Providers["chatgpt"]
	if got.OAuth == nil || got.OAuth.Email != "a@b.com" {
		t.Errorf("OAuth not preserved on collect: %+v", got.OAuth)
	}
	if got.Model != "gpt-5.6" {
		t.Errorf("model = %q", got.Model)
	}

	// Sign-out clears OAuth and swaps back to the sign-in button.
	updated, _ = m.Update(ChatGPTSignOutMsg{})
	m = &updated
	if got := m.providers["chatgpt"].OAuth; got != nil {
		t.Errorf("OAuth should be nil after sign-out: %+v", got)
	}
	if status := m.value(catProviders, "chatgpt-status"); status != "Not signed in" {
		t.Errorf("status = %q after sign-out", status)
	}
	if f := m.categories[catProviders].Form.Field("chatgpt-signin"); f == nil {
		t.Errorf("sign-in nav should reappear after sign-out")
	}
}

func TestChatGPTSignInErrorShownOnStatusRow(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]types.ProviderConfig{"chatgpt": {}},
	}, []types.ProviderPreset{chatgptPreset()})

	updated, _ := m.Update(ChatGPTSignInResultMsg{Err: &provider.ChatGPTSignInError{Fatal: true, Msg: "authorization denied"}})
	m = &updated
	f := m.categories[catProviders].Form.Field("chatgpt-status")
	if f == nil || len(f.Errors) == 0 || f.Errors[0] != "authorization denied" {
		t.Errorf("status errors = %#v", f)
	}
	if m.categories[catProviders].Form.Field("chatgpt-signin") == nil {
		t.Errorf("sign-in nav should remain after a failed sign-in")
	}
}

// TestChatGPTSignInButtonIsInsetBoxed pins the restyle: the sign-in affordance
// is an inset boxed button filling the right control column, with the ▸ chevron
// inset one gutter from the right border so it never sticks to the scrollbar —
// matching the dropdown/select affordance, not the old full-width drill-in row.
func TestChatGPTSignInButtonIsInsetBoxed(t *testing.T) {
	th := theme.DefaultDark()
	model := New(th, types.AppSettings{
		ActiveProvider: "chatgpt",
		Providers:      map[string]types.ProviderConfig{"chatgpt": {}},
	}, []types.ProviderPreset{chatgptPreset()})
	surfaceRoot := NewSurface(th, model)
	frame := ui.NewWindowFrameWidget(th, "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, 90, 24)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(90, 24)
	host.Layout()

	model.SelectCategory(catProviders)
	surfaceRoot.refreshForm()
	surfaceRoot.FocusForm()
	host.Layout()

	if ff := surfaceRoot.form.Field("chatgpt-signin"); ff == nil {
		t.Fatal("chatgpt-signin field not registered with surface form")
	}

	buf := newTestBuffer(90, 24)
	host.Paint(buf)

	borderRune := []rune(theme.DefaultGlyphs().BorderV)[0]
	chevronRune := []rune(theme.DefaultGlyphs().TreeCollapsed)[0]
	label := []rune("Sign in with ChatGPT")

	// Locate the painted button row by its label, then reason about chevron and
	// border positions on that row by scanning cells — avoids fragile nested
	// coordinate math (surface → frame → form → field).
	var rowY = -1
	var rowRunes []rune
	for y := 0; y < buf.Height(); y++ {
		var rr []rune
		for x := 0; x < buf.Width(); x++ {
			rr = append(rr, buf.Get(x, y).Char)
		}
		if containsRunes(rr, label) {
			rowY, rowRunes = y, rr
			break
		}
	}
	if rowY < 0 {
		t.Fatalf("no painted row contains %q\n%s", string(label), bufferPlain(buf))
	}

	// Find the chevron and the box borders on the button row.
	chevX, leftBorder, rightBorder := -1, -1, -1
	for x, c := range rowRunes {
		switch c {
		case chevronRune:
			chevX = x
		case borderRune:
			if leftBorder < 0 {
				leftBorder = x
			}
			rightBorder = x
		}
	}
	if chevX < 0 {
		t.Fatalf("chevron %q missing on sign-in row\nrow=%q", chevronRune, string(rowRunes))
	}
	if leftBorder < 0 || rightBorder < 0 || leftBorder == rightBorder {
		t.Fatalf("boxed button needs distinct left+right borders (left=%d right=%d)\nrow=%q",
			leftBorder, rightBorder, string(rowRunes))
	}

	// The chevron must sit strictly inside the box, inset from the right border —
	// not stuck to the right edge / scrollbar. Require a blank gutter between the
	// chevron and the right border.
	if chevX >= rightBorder-1 {
		t.Errorf("chevron at x=%d not inset from right border at x=%d (no right padding)\nrow=%q",
			chevX, rightBorder, string(rowRunes))
	}
	if c := rowRunes[rightBorder-1]; c == chevronRune || c == borderRune {
		t.Errorf("cell at x=%d (left of right border) = %q, want blank gutter padding\nrow=%q",
			rightBorder-1, c, string(rowRunes))
	}
	// The label must sit between the left and right borders (inside the box).
	labelStart := indexOfRunes(rowRunes, label)
	if labelStart <= leftBorder || labelStart+len(label) >= rightBorder {
		t.Errorf("label at x=%d not inside box borders [%d,%d]\nrow=%q",
			labelStart, leftBorder, rightBorder, string(rowRunes))
	}
}

func indexOfRunes(haystack, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func containsRunes(haystack, needle []rune) bool {
	return indexOfRunes(haystack, needle) >= 0
}
