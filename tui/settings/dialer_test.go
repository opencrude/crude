package settings

import (
	"strconv"
	"testing"

	"crude/provider"
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
	for _, attempts := range []int{1, 2, 3, 5} {
		id := dialChoiceID(attempts, provider.DefaultMaxAttempts)
		if got := dialAttemptsFromChoice(id); got != attempts {
			t.Errorf("round trip %d -> %q -> %d", attempts, id, got)
		}
	}
	// The built-in default persists as unset, by design.
	if got := dialAttemptsFromChoice(dialChoiceID(10, provider.DefaultMaxAttempts)); got != 0 {
		t.Errorf("built-in default attempts parsed to %d, want 0 (unset)", got)
	}
}

func TestDialerFromFormAllDefaultYieldsNil(t *testing.T) {
	// The form's initial selection is the built-in default of each row.
	defaults := dialerFromForm(
		dialChoiceID(0, 0),
		dialChoiceID(0, int(provider.DefaultConnectTimeout.Seconds())),
		dialChoiceID(0, provider.DefaultMaxAttempts),
		dialChoiceID(0, int(provider.DefaultMaxBackoff.Seconds())),
	)
	if defaults != nil {
		t.Fatalf("all-default form = %+v, want nil (inherit built-in defaults)", defaults)
	}
}

func TestDialerFromFormCapturesNonDefaults(t *testing.T) {
	dialer := dialerFromForm("60", "10", "3", "30")
	want := provider.DialSettings{
		RequestTimeoutSeconds: 60,
		ConnectTimeoutSeconds: 10,
		MaxAttempts:           3,
		MaxBackoffSeconds:     30,
	}
	if dialer == nil || *dialer != want {
		t.Fatalf("dialer = %+v, want %+v", dialer, want)
	}
}

func TestDialerFromFormPersistsBuiltInDefaultAsUnset(t *testing.T) {
	// A row explicitly moved to the built-in default must not freeze that
	// number into the config — it persists as unset so a future change to the
	// built-in default is honored.
	dialer := dialerFromForm("0", "30", "10", "120")
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
	// Fields: separator + four rows.
	if len(fields) != 5 {
		t.Fatalf("fields = %d, want 5", len(fields))
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
	if got := byID[fieldDialConnect]; got != "30" {
		t.Errorf("connect timeout field = %q, want %q (built-in default)", got,
			dialChoiceID(0, int(provider.DefaultConnectTimeout.Seconds())))
	}
	if got := byID[fieldDialBackoff]; got != "120" {
		t.Errorf("max backoff field = %q, want %q (built-in default)", got,
			dialChoiceID(0, int(provider.DefaultMaxBackoff.Seconds())))
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
