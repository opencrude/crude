package settingspages

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/setting"
)

func TestNotificationsPageFollowsTerminalInIDEGroup(t *testing.T) {
	model := New(theme.DefaultDark(), setting.App{}, nil)
	visible := model.visibleCategoryIndices()
	terminal, notifications := slices.Index(visible, catTerminal), slices.Index(visible, catNotifications)
	if terminal < 0 || notifications != terminal+1 || model.categories[catNotifications].Group != "IDE" || model.categories[catNotifications].Name != "Notifications" {
		t.Fatalf("Notifications page position %d after Terminal %d in %v", notifications, terminal, visible)
	}
}

func TestNotificationsDefaultsAreOnAndStoreNothing(t *testing.T) {
	model := New(theme.DefaultDark(), setting.App{}, nil)
	for _, id := range []string{fieldNotifyEnabled, fieldNotifyInput, fieldNotifyStopped, fieldNotifyFinished, fieldNotifySound} {
		if got := model.value(catNotifications, id); got != "on" {
			t.Fatalf("%s = %q, want on by default", id, got)
		}
	}
	if got := model.value(catNotifications, fieldNotifyAway); got != "5" {
		t.Fatalf("away = %q, want 5 minutes", got)
	}
	got := model.AppSettings().UI.DesktopNotifications
	if got.Enabled != nil || got.Input != nil || got.Stopped != nil || got.Finished != nil || got.Sound != nil {
		t.Fatalf("defaults were stored as explicit choices: %+v", got)
	}
	if model.hasUnsavedPreferences() {
		t.Fatal("an untouched Notifications page reports unsaved changes")
	}
}

func TestNotificationsChoicesRoundTrip(t *testing.T) {
	off, on := false, true
	want := setting.DesktopNotifications{Enabled: &on, Input: &on, Stopped: &off, Finished: &off, Sound: &off, AwayMinutes: 15}
	model := New(theme.DefaultDark(), setting.App{UI: setting.UI{DesktopNotifications: want}}, nil)
	got := model.AppSettings().UI.DesktopNotifications
	if got.StoppedOrDefault() || got.FinishedOrDefault() || got.SoundOrDefault() || !got.InputOrDefault() || got.AwayMinutes != 15 {
		t.Fatalf("round trip = %+v", got)
	}

	model.SetValueForTest(catNotifications, fieldNotifyFinished, "on")
	model.SetValueForTest(catNotifications, fieldNotifyAway, "2")
	got = model.AppSettings().UI.DesktopNotifications
	if !got.FinishedOrDefault() || got.AwayMinutes != 2 {
		t.Fatalf("edited = %+v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	_ = json.Unmarshal(data, &stored)
	if !reflect.DeepEqual(stored["finished"], true) || !reflect.DeepEqual(stored["stopped"], false) {
		t.Fatalf("stored JSON %s", data)
	}
}

func TestNotificationsMasterSwitchDimsTheOtherChoices(t *testing.T) {
	model := New(theme.DefaultDark(), setting.App{}, nil)
	form := model.categories[catNotifications].Form
	for _, id := range notifyDependents {
		if form.Field(id).Disabled {
			t.Fatalf("%s is dimmed while notifications are on", id)
		}
	}
	form.SetValue(fieldNotifyEnabled, ui.StringValue("off"))
	model.handleFieldChanged(catNotifications, fieldNotifyEnabled)
	for _, id := range notifyDependents {
		if !form.Field(id).Disabled {
			t.Fatalf("%s stays active while notifications are off", id)
		}
	}
	if got := model.AppSettings().UI.DesktopNotifications; got.EnabledOrDefault() || !got.InputOrDefault() {
		t.Fatalf("off keeps the other choices: %+v", got)
	}
	form.SetValue(fieldNotifyEnabled, ui.StringValue("on"))
	model.handleFieldChanged(catNotifications, fieldNotifyEnabled)
	if form.Field(fieldNotifyInput).Disabled {
		t.Fatal("turning notifications on again must enable the other choices")
	}
}

func TestNotificationsAwayRejectsOutOfRange(t *testing.T) {
	model := New(theme.DefaultDark(), setting.App{}, nil)
	field := model.categories[catNotifications].Form.Field(fieldNotifyAway)
	for value, valid := range map[string]bool{"0": false, "1": true, "60": true, "61": false, "x": false} {
		field.Value = ui.StringValue(value)
		if got := len(field.Validator(field.Value)) == 0; got != valid {
			t.Fatalf("away %q valid = %v, want %v", value, got, valid)
		}
	}
}

func TestNotificationsAndInterfaceShareUIWithoutOverlap(t *testing.T) {
	iface, _ := pageOwnershipFor(catUI, setting.ScopeWorkspace)
	notifications, _ := pageOwnershipFor(catNotifications, setting.ScopeWorkspace)
	enabled := []string{"ui", "desktop_notifications", "enabled"}
	if iface.owns(enabled) || !notifications.owns(enabled) {
		t.Fatal("Reset on Interface must not reset notifications, and Notifications must own them")
	}
	if !iface.owns([]string{"ui", "attachment_open"}) || notifications.owns([]string{"ui", "attachment_open"}) {
		t.Fatal("Interface choices belong to Interface only")
	}
	notified, other := uiOverrideKeys(json.RawMessage(`{"desktop_notifications":{"sound":false}}`))
	if !notified || other {
		t.Fatalf("workspace marker: notifications=%v other=%v", notified, other)
	}
	notified, other = uiOverrideKeys(json.RawMessage(`{"attachment_open":"crude","desktop_notifications":{}}`))
	if notified || !other {
		t.Fatalf("workspace marker: notifications=%v other=%v", notified, other)
	}
	for id, path := range controlFieldPaths[catNotifications] {
		if len(path) < len("ui.desktop_notifications.") || path[:len("ui.desktop_notifications.")] != "ui.desktop_notifications." {
			t.Fatalf("%s control path %q", id, path)
		}
	}
}
