package settings

import (
	"encoding/json"
	"strconv"
	"strings"

	"crude/tui/ui"
	"crude/types"
)

// The Notifications page edits ui.desktop_notifications. The policy that
// uses these choices is in docs/reference/desktop-notifications.md.
const (
	fieldNotifyEnabled  = "notify-enabled"
	fieldNotifyInput    = "notify-input"
	fieldNotifyStopped  = "notify-stopped"
	fieldNotifyFinished = "notify-finished"
	fieldNotifySound    = "notify-sound"
	fieldNotifyAway     = "notify-away"
)

// notifyDependents are the choices that the master switch turns off.
var notifyDependents = []string{fieldNotifyInput, fieldNotifyStopped, fieldNotifyFinished, fieldNotifySound, fieldNotifyAway}

func (m *Model) buildNotificationsForm(current types.DesktopNotificationSettings) *ui.FormModel {
	away := ui.NumberFieldState(fieldNotifyAway, "Away after", current.AwayMinutesOrDefault(), "minutes",
		"With no input for this time, Crude counts you as away, also when its terminal has focus.")
	away.Validator = func(value ui.FieldValue) []string {
		minutes, err := strconv.Atoi(strings.TrimSpace(value.String()))
		if err != nil || minutes < types.DesktopNotificationAwayMin || minutes > types.DesktopNotificationAwayMax {
			return []string{"Enter a time from 1 to 60 minutes"}
		}
		return nil
	}
	form := formModel(
		ui.BoolFieldState(fieldNotifyEnabled, "Desktop notifications", current.EnabledOrDefault(),
			"Show a system notification when an agent needs you and you are away from Crude."),
		ui.SeparatorFieldState("notify-about", "Notify about"),
		ui.BoolFieldState(fieldNotifyInput, "Questions and approvals", current.InputOrDefault(),
			"An agent asks a question or needs your approval."),
		ui.BoolFieldState(fieldNotifyStopped, "Stopped runs", current.StoppedOrDefault(),
			"A run stops because of an error, a limit or an interruption."),
		ui.BoolFieldState(fieldNotifyFinished, "Finished work", current.FinishedOrDefault(),
			"An agent that you started finishes its turn."),
		ui.SeparatorFieldState("notify-delivery", "Delivery"),
		ui.BoolFieldState(fieldNotifySound, "Sound", current.SoundOrDefault(),
			"Play the system notification sound with each new message."),
		away,
	)
	syncNotificationsForm(form)
	return form
}

// syncNotificationsForm dims the choices that have no effect while desktop
// notifications are off. Their values stay as the user set them.
func syncNotificationsForm(form *ui.FormModel) {
	if form == nil || form.Field(fieldNotifyEnabled) == nil {
		return
	}
	off := form.Field(fieldNotifyEnabled).Value.String() != "on"
	for _, id := range notifyDependents {
		if field := form.Field(id); field != nil {
			field.Disabled = off
		}
	}
}

func (m *Model) collectDesktopNotifications(cfg *types.DesktopNotificationSettings) {
	choose := func(target **bool, fieldID string, current bool) {
		value := m.value(catNotifications, fieldID) == "on"
		if value != current {
			*target = &value
		}
	}
	choose(&cfg.Enabled, fieldNotifyEnabled, cfg.EnabledOrDefault())
	choose(&cfg.Input, fieldNotifyInput, cfg.InputOrDefault())
	choose(&cfg.Stopped, fieldNotifyStopped, cfg.StoppedOrDefault())
	choose(&cfg.Finished, fieldNotifyFinished, cfg.FinishedOrDefault())
	choose(&cfg.Sound, fieldNotifySound, cfg.SoundOrDefault())
	cfg.AwayMinutes = parseOpt(m.value(catNotifications, fieldNotifyAway), types.DesktopNotificationAwayDefault)
}

// uiOverrideKeys reports which ui keys a workspace stores; Interface and
// Notifications share the stored ui section.
func uiOverrideKeys(raw json.RawMessage) (notifications, other bool) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return false, len(raw) > 0
	}
	for key, value := range keys {
		if key == "desktop_notifications" {
			notifications = notifications || (len(value) > 0 && string(value) != "{}" && string(value) != "null")
		} else {
			other = true
		}
	}
	return notifications, other
}
