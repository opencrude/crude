package settings

import (
	"strconv"
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestDialSecondsRoundTrip(t *testing.T) {
	for _, seconds := range []int{0, 30, 60, 120, 300, 600} {
		id := dialChoiceID(seconds, 0)
		if got := dialSecondsFromChoice(id, 0); got != seconds {
			t.Errorf("round trip %d -> %q -> %d", seconds, id, got)
		}
	}
	// Unknown IDs degrade to unset rather than fabricating a value.
	if got := dialSecondsFromChoice("bogus", 0); got != 0 {
		t.Errorf("bogus ID parsed to %d, want 0", got)
	}
}

func TestDialAttemptsRoundTrip(t *testing.T) {
	for _, attempts := range []int{1, 2, 3, 5, 10} {
		id := dialChoiceID(attempts, provider.DefaultMaxAttempts)
		if got := dialAttemptsFromChoice(id); got != attempts {
			t.Errorf("round trip %d -> %q -> %d", attempts, id, got)
		}
	}
	// The built-in default persists as unset, by design.
	if got := dialAttemptsFromChoice(dialChoiceID(provider.DefaultMaxAttempts, provider.DefaultMaxAttempts)); got != 0 {
		t.Errorf("built-in default attempts parsed to %d, want 0 (unset)", got)
	}
}

func TestDialerFromFormAllDefaultYieldsNil(t *testing.T) {
	// The form's initial selection is the built-in default of each row.
	defaults := dialerFromForm(
		dialChoiceID(0, 0),
		"0",
		dialChoiceID(0, provider.DefaultMaxAttempts),
		dialChoiceID(0, int(provider.DefaultMaxBackoff.Seconds())),
		dialChoiceID(0, int(provider.DefaultProgressStartupTimeout.Seconds())),
		dialChoiceID(0, int(provider.DefaultProgressTimeoutFloor.Seconds())),
		dialChoiceID(0, int(provider.DefaultProgressTimeoutCeiling.Seconds())),
	)
	if defaults != nil {
		t.Fatalf("all-default form = %+v, want nil (inherit built-in defaults)", defaults)
	}
}

func TestDialerFromFormCapturesNonDefaults(t *testing.T) {
	dialer := dialerFromForm("60", "10", "3", "60", "120", "90", "600")
	want := provider.DialSettings{
		RequestTimeoutSeconds:         60,
		ConnectTimeoutSeconds:         10,
		MaxAttempts:                   3,
		MaxBackoffSeconds:             60,
		ProgressStartupTimeoutSeconds: 120,
		ProgressTimeoutFloorSeconds:   90,
		ProgressTimeoutCeilingSeconds: 600,
	}
	if dialer == nil || *dialer != want {
		t.Fatalf("dialer = %+v, want %+v", dialer, want)
	}
}

func TestDialerFromFormPersistsBuiltInDefaultAsUnset(t *testing.T) {
	// A row explicitly moved to the built-in default must not freeze that
	// number into the config — it persists as unset so a future change to the
	// built-in default is honored.
	dialer := dialerFromForm("0", "0", strconv.Itoa(provider.DefaultMaxAttempts), strconv.Itoa(int(provider.DefaultMaxBackoff.Seconds())), "60", "45", "300")
	if dialer != nil {
		t.Fatalf("built-in-default selections = %+v, want nil", dialer)
	}
}

func TestDialerFieldsRenderCurrentValues(t *testing.T) {
	dialer := &provider.DialSettings{
		RequestTimeoutSeconds: 120,
		MaxAttempts:           5,
	}
	fields := dialerFields(dialer)
	// Fields: separator + seven connection-tuning rows.
	if len(fields) != 8 {
		t.Fatalf("fields = %d, want 8", len(fields))
	}
	byID := map[string]string{}
	for _, field := range fields {
		byID[field.ID] = field.Value.String()
	}
	if byID[fieldDialTimeout] != "120" {
		t.Errorf("request timeout field = %q, want 120", byID[fieldDialTimeout])
	}
	if byID[fieldDialAttempts] != "5" {
		t.Errorf("max attempts field = %q, want 5", byID[fieldDialAttempts])
	}
	// Unset rows select the built-in default option ID.
	if got := byID[fieldDialConnect]; got != "0" {
		t.Errorf("connect timeout field = %q, want Network inheritance", got)
	}
	if got := byID[fieldDialBackoff]; got != strconv.Itoa(int(provider.DefaultMaxBackoff.Seconds())) {
		t.Errorf("max backoff field = %q, want %q (built-in default)", got,
			dialChoiceID(0, int(provider.DefaultMaxBackoff.Seconds())))
	}
	for id, want := range map[string]string{
		fieldDialProgressStartup: "60", fieldDialProgressFloor: "45", fieldDialProgressCeiling: "300",
	} {
		if got := byID[id]; got != want {
			t.Errorf("%s = %q, want default %s", id, got, want)
		}
	}
}

func TestExplicitConnectBudgetIsDistinctFromNetworkInheritance(t *testing.T) {
	if field := dialConnectField(0); field.Value.String() != "0" || field.Options[0].Label != "Network defaults" {
		t.Fatalf("inheritance is falsely labelled as a hard-coded timeout: %+v", field)
	}
	explicit := dialerFromForm("0", "30", "6", "30", "60", "45", "300")
	if explicit == nil || explicit.ConnectTimeoutSeconds != 30 {
		t.Fatal("explicit 30s must override both budgets, not silently become Network inheritance")
	}
}

func TestDialerFieldsSelectBuiltInDefaultsForNil(t *testing.T) {
	fields := dialerFields(nil)
	for _, field := range fields[1:] {
		if field.Value.String() == "" {
			t.Errorf("nil dialer field %s = empty, want a built-in default ID", field.ID)
		}
	}
}

func TestDialFieldOptionsLabelBuiltInDefault(t *testing.T) {
	options := dialFieldOptions(0, 30, dialConnectLadder, "s")
	if len(options) == 0 || options[0].ID != "30" {
		t.Fatalf("first option = %+v, want the built-in default 30", options[0])
	}
	if options[0].Label != "30s (default)" {
		t.Errorf("default label = %q, want %q", options[0].Label, "30s (default)")
	}
	for _, option := range options[1:] {
		if option.Label == options[0].Label {
			t.Errorf("non-default option shares the default label: %+v", option)
		}
	}
}

func TestDialFieldOptionsUnlimitedLabel(t *testing.T) {
	options := dialFieldOptions(0, 0, dialTimeoutLadder, "s")
	if options[0].ID != "0" || options[0].Label != "Unlimited (default)" {
		t.Fatalf("first option = %+v, want Unlimited (default)", options[0])
	}
}

func TestDialFieldOptionsInsertOffLadderValue(t *testing.T) {
	options := dialFieldOptions(45, 0, dialTimeoutLadder, "s")
	ids := map[string]bool{}
	for _, option := range options {
		ids[option.ID] = true
	}
	if !ids["45"] {
		t.Fatalf("off-ladder stored value 45 missing from options: %+v", options)
	}
	// The built-in default stays first; the ladder remains sorted.
	if options[0].ID != "0" {
		t.Errorf("first option = %q, want built-in default", options[0].ID)
	}
	for i := 1; i+1 < len(options); i++ {
		prev, next := options[i].ID, options[i+1].ID
		if prevInt, err1 := strconv.Atoi(prev); err1 == nil {
			if nextInt, err2 := strconv.Atoi(next); err2 == nil && prevInt > nextInt {
				t.Errorf("ladder not sorted at %d: %q > %q", i, prev, next)
			}
		}
	}
}

func TestDialFieldOptionsDoNotDuplicateDefaults(t *testing.T) {
	for _, builtin := range []int{30, 45, 60, 300} {
		ids := map[string]bool{}
		for _, option := range dialFieldOptions(0, builtin, dialProgressLadder, "s") {
			if ids[option.ID] {
				t.Fatalf("default %d duplicated option %q", builtin, option.ID)
			}
			ids[option.ID] = true
		}
	}
}

func TestConnectionTuningSettingsEditors(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	m.SelectAccounts()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	category := catAccounts
	backoff := m.categories[category].Form.Field(fieldDialBackoff)
	if backoff == nil || !strings.Contains(backoff.Description, "Retry-After") || !strings.Contains(backoff.Description, "1-300") {
		t.Fatal("missing retry cap guidance")
	}
	m.SetValueForTest(category, fieldDialBackoff, "301")
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}); result.Cmd != nil || !backoff.HasErrors() {
		t.Fatal("five-minute cap was not enforced")
	}
	for id, value := range map[string]string{fieldDialTimeout: "300", fieldDialConnect: "10", fieldDialAttempts: "3", fieldDialBackoff: "60", fieldDialProgressStartup: "120", fieldDialProgressFloor: "60", fieldDialProgressCeiling: "600"} {
		m.SetValueForTest(category, id, value)
	}
	m.SetValueForTest(category, fieldDialProgressStartup, "900")
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"}); result.Cmd != nil || !m.categories[category].Form.Field(fieldDialProgressStartup).HasErrors() {
		t.Fatal("conflicting progress timeouts accepted")
	}
	m.SetValueForTest(category, fieldDialProgressStartup, "120")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.commit"})
	if result.Cmd == nil {
		t.Fatalf("valid transport rejected: %s", m.accountError)
	}
	got := result.Cmd().(AccountChangeMsg).Change.After.Connection.Dialer
	want := provider.DialSettings{RequestTimeoutSeconds: 300, ConnectTimeoutSeconds: 10, MaxAttempts: 3, MaxBackoffSeconds: 60, ProgressStartupTimeoutSeconds: 120, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 600}
	if got == nil || *got != want {
		t.Fatalf("account transport=%+v want %+v", got, want)
	}
}

func TestAccountEditorHasSingleConnectionTuningHeader(t *testing.T) {
	// The transport rows own their section header (dialerFields); the account
	// editor must not prepend a second "Connection tuning" separator.
	m := New(theme.DefaultDark(), accountSettingsFixture(), nil)
	m.SelectAccounts()
	m.selectAccount("work")
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	headers := 0
	for _, field := range m.categories[catAccounts].Form.Fields() {
		if field.Type == ui.FieldSeparator && field.Label == "Connection tuning" {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("account editor renders %d Connection tuning headers, want 1", headers)
	}
}

func TestAccountProgressTimeoutsAreEditableAndIndependent(t *testing.T) {
	settings := accountSettingsFixture()
	account := settings.Accounts["work"]
	account.Connection.Dialer = &provider.DialSettings{ProgressStartupTimeoutSeconds: 120, ProgressTimeoutFloorSeconds: 60, ProgressTimeoutCeilingSeconds: 600}
	settings.Accounts["work"] = account
	m := New(theme.DefaultDark(), settings, nil)
	m.SelectAccounts()
	m.selectAccount("work")
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	if field := m.categories[catAccounts].Form.Field(fieldDialProgressStartup); field.ReadOnly || field.Value.String() != "120" {
		t.Fatal("owned transport remained inherited/read-only")
	}
	m.SetValueForTest(catAccounts, fieldDialProgressCeiling, "900")
	m.storeAccountEditorForm()
	if m.accountEditor.draft.Connection.Dialer.ProgressTimeoutCeilingSeconds != 900 || m.accounts["home"].Connection.Dialer != nil {
		t.Fatal("draft transport changed another account")
	}
}

func TestProgressTimeoutControlsPaintAtDesktopAndNarrowSizes(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, accountSettingsFixture(), nil)
	s := NewSurface(th, m)
	m.SelectAccounts()
	m.selectAccount("work")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.account.edit"})
	host := ui.NewHost(th, s)
	for _, size := range []ui.Size{{W: 124, H: 28}, {W: 70, H: 18}} {
		host.Resize(size.W, size.H)
		host.Layout()
		for id, label := range map[string]string{fieldDialProgressStartup: "Initial progress", fieldDialProgressFloor: "Min progress", fieldDialProgressCeiling: "Max progress"} {
			if !s.form.FocusField(id) {
				t.Fatalf("cannot focus %s", id)
			}
			host.MarkLayout()
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if out := bufferPlain(buf); !strings.Contains(out, label) || !strings.Contains(out, "Save") {
				t.Fatalf("size=%+v missing transport/action:\n%s", size, out)
			}
		}
	}
}
