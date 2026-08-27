package settings

import (
	"testing"

	"crude/provider"
)

func TestDialSecondsChoiceRoundTrip(t *testing.T) {
	for _, seconds := range []int{0, 30, 60, 120, 300, 600} {
		label := dialSecondsChoice(seconds)
		if got := dialSecondsFromChoice(label); got != seconds {
			t.Errorf("round trip %d -> %q -> %d", seconds, label, got)
		}
	}
	if got := dialSecondsChoice(0); got != "Default" {
		t.Errorf("zero seconds label = %q, want Default", got)
	}
	// Unknown values degrade to Default rather than fabricating a label.
	if got := dialSecondsFromChoice("bogus"); got != 0 {
		t.Errorf("bogus label parsed to %d, want 0", got)
	}
}

func TestDialAttemptsChoiceRoundTrip(t *testing.T) {
	for _, attempts := range []int{0, 1, 2, 3, 5, 10} {
		label := dialAttemptsChoice(attempts)
		if got := dialAttemptsFromChoice(label); got != attempts {
			t.Errorf("round trip %d -> %q -> %d", attempts, label, got)
		}
	}
}

func TestDialerFromFormAllDefaultYieldsNil(t *testing.T) {
	if dialer := dialerFromForm("Default", "Default", "Default", "Default"); dialer != nil {
		t.Fatalf("all-Default form = %+v, want nil (inherit built-in defaults)", dialer)
	}
}

func TestDialerFromFormCapturesNonDefaults(t *testing.T) {
	dialer := dialerFromForm("60s", "10s", "3", "30s")
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
	if byID[fieldDialTimeout] != "120s" {
		t.Errorf("request timeout field = %q, want 120s", byID[fieldDialTimeout])
	}
	if byID[fieldDialAttempts] != "5" {
		t.Errorf("max attempts field = %q, want 5", byID[fieldDialAttempts])
	}
	if byID[fieldDialConnect] != "Default" || byID[fieldDialBackoff] != "Default" {
		t.Errorf("unset fields = %q %q, want Default", byID[fieldDialConnect], byID[fieldDialBackoff])
	}
}

func TestDialerFieldsTolerateNilSettings(t *testing.T) {
	fields := dialerFields(nil)
	for _, field := range fields[1:] {
		if field.Value.String() != "Default" {
			t.Errorf("nil dialer field %s = %q, want Default", field.ID, field.Value.String())
		}
	}
}
