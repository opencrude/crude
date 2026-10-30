package core

import (
	"context"
	"crude/coreprotocol"
	"crude/provider"
	"crude/types"
	captypes "crude/types/capability"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
)

// prepareApplicationSettings retains private committed snapshots only in this
// closure. Responses never transport credentials or authorize publication.
func (a *actor) prepareApplicationSettings(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
	bad := func(code, message string) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, code, message) }, nil, true
	}
	var operation func(context.Context) (types.AppSettings, coreprotocol.Value, error)
	var publishApp bool
	var capabilityFailure func(error) string
	switch request.Method {
	case coreprotocol.AccountChange:
		var in coreprotocol.AccountChangeRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		before, exists := a.ws.AllAccounts()[in.ID]
		if in.ID == "" {
			return bad("invalid_request", "account identity is required")
		}
		change := types.AccountChange{ID: in.ID, ProviderID: before.ProviderID}
		if exists {
			copy := provider.CloneAccount(before)
			change.Before = &copy
		}
		if in.After != nil {
			if in.After.ID != in.ID {
				return bad("invalid_request", "account identity differs")
			}
			after, err := resolveAccount(*in.After, before)
			if err != nil {
				return bad("invalid_request", err.Error())
			}
			change.After = &after
			change.ProviderID = after.ProviderID
		}
		commit := a.ws.OnAppAccountCommit
		if commit == nil {
			return bad("settings_unavailable", "application account persistence unavailable")
		}
		operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
			s, err := commit(change)
			if change.After == nil {
				return s, wire(coreprotocol.AccountView{ID: in.ID}), err
			}
			return s, wire(publicAccount(s.Accounts[in.ID])), err
		}
		publishApp = true
	case coreprotocol.ApplicationPreferencesSave:
		var in coreprotocol.ApplicationPreferencesRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		before := a.ws.AppDefaults()
		if err := types.ValidateSettingEdits(types.AppSettings{}, in.Edits); err != nil {
			return bad("invalid_request", err.Error())
		}
		if err := types.ValidateAnalysisSettingEdits(types.SettingsScopeApplication, in.Edits); err != nil {
			return bad("invalid_request", err.Error())
		}
		for _, edit := range in.Edits {
			if len(edit.Path) == 0 || edit.Path[0] == "accounts" || edit.Path[0] == "capabilities" && (len(edit.Path) < 2 || edit.Path[1] != "acquisition") {
				return bad("invalid_request", "accounts and capabilities require entity operations")
			}
		}
		// Secrets never enter the ordinary projection. Keep leaves the stored
		// proxy untouched, even if another preference form was opened earlier.
		privateEdits := slices.Clone(in.Edits)
		privateEdits = slices.DeleteFunc(privateEdits, func(e types.SettingEdit) bool { return slices.Equal(e.Path, []string{"network", "proxy_url"}) })
		proxy, err := resolveSecret(in.ProxyURL, before.Network.ProxyURL)
		if err != nil {
			return bad("invalid_request", err.Error())
		}
		if !in.ProxyURL.Keep {
			raw, _ := json.Marshal(proxy)
			privateEdits = append(privateEdits, types.SettingEdit{Path: []string{"network", "proxy_url"}, After: raw})
		}
		commit := a.ws.OnAppPreferencesCommit
		if commit == nil {
			return bad("settings_unavailable", "application persistence unavailable")
		}
		operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
			s, err := commit(privateEdits)
			return s, wire(coreprotocol.ApplicationPreferencesResult{Value: publicApp(s), Revision: entityRevision(s)}), err
		}
		publishApp = true
	case coreprotocol.CapabilitySnapshot, coreprotocol.CapabilityAcquisitionPreview, coreprotocol.CapabilityAction:
		appBefore := a.ws.AppDefaults()
		appCommit := a.ws.OnAppPreferencesCommit
		var in coreprotocol.CapabilityActionRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		overlayCommit := a.ws.PrepareCapabilityOverlayCommit(in.Action)
		effectiveBefore := a.ws.LoadAppSettings()
		capabilityFailure = func(err error) string {
			return publicCapabilityFailure(err, effectiveBefore, in.Action)
		}
		secretBefore := effectiveBefore.Capabilities
		if in.Action.Scope == captypes.ScopeApplication {
			secretBefore = appBefore.Capabilities
		}
		if in.Action.Operation == "save-connection" || in.Action.Operation == "test" {
			if err := resolveConnectionSecrets(&in, secretBefore); err != nil {
				return bad("invalid_request", err.Error())
			}
		}
		if in.Action.Operation == "save-source" {
			if in.Action.Source == nil || in.Action.Source.ID != in.Action.ID {
				return bad("invalid_request", "source identity differs")
			}
			location, err := resolveSecret(in.SourceLocation, secretBefore.Sources[in.Action.ID].Location)
			if err != nil {
				return bad("invalid_request", err.Error())
			}
			in.Action.Source.Location = location
		}

		prepared, err := a.ws.PrepareCapabilities(func(ctx context.Context, before, after types.AppSettings, scope captypes.Scope) (types.AppSettings, error) {
			if scope == captypes.ScopeApplication {
				if appCommit == nil {
					return types.AppSettings{}, errors.New("application persistence unavailable")
				}
				return appCommit(types.SettingsEdits(before, after))
			}
			return overlayCommit(ctx, before, after, scope)
		})
		if err != nil {
			return bad("capability_unavailable", err.Error())
		}
		publishApp = in.Action.Scope == captypes.ScopeApplication
		switch request.Method {
		case coreprotocol.CapabilitySnapshot:
			operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
				s, e := prepared.Snapshot(ctx)
				return types.AppSettings{}, wire(publicCapabilityResult(s, nil, "")), e
			}
		case coreprotocol.CapabilityAcquisitionPreview:
			operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
				p, e := prepared.Preview(ctx, in.Action)
				return types.AppSettings{}, wire(coreprotocol.CapabilityPreviewResult{Preview: publicCapabilityPreview(p)}), e
			}
		default:
			run := prepared.PrepareAction(in.Action)
			operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
				s, entries, content, published, e := run(ctx)
				if in.Action.Operation == "test" {
					content = ""
				}
				return published, wire(publicCapabilityResult(s, entries, content)), e
			}
		}
	case coreprotocol.SkillInput:
		var in coreprotocol.SkillInputRequest
		if err := decodeRequest(request, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		run, err := a.ws.PrepareSkillActivation(in.AgentID, in.ID, in.Arguments)
		if err != nil {
			return bad("agent_missing", err.Error())
		}
		operation = func(ctx context.Context) (types.AppSettings, coreprotocol.Value, error) {
			input, e := run(ctx)
			return types.AppSettings{}, wire(coreprotocol.SkillInputResult{Input: input}), e
		}
	default:
		return nil, nil, false
	}
	var mu sync.Mutex
	var once sync.Once
	var out coreprotocol.Response
	var retained types.AppSettings
	run := func(ctx context.Context) coreprotocol.Response {
		mu.Lock()
		defer mu.Unlock()
		once.Do(func() {
			if ctx.Err() != nil {
				out = fault(request, "cancelled", "settings operation cancelled")
				return
			}
			var value coreprotocol.Value
			var err error
			retained, value, err = operation(ctx)
			if err != nil {
				if capabilityFailure != nil {
					out = fault(request, "capability_failed", capabilityFailure(err))
				} else {
					out = fault(request, "settings_failed", err.Error())
				}
				return
			}
			out = responseValue(request, value)
		})
		return out
	}
	apply := func(result coreprotocol.Response) {
		mu.Lock()
		defer mu.Unlock()
		if result.Error != nil || out.Error != nil || len(out.Payload) == 0 {
			return
		}
		// Preview, discovery and runtime observation do not commit preferences.
		// Publishing their zero return used to reset application defaults after a
		// preview, so its exact source could never pass the subsequent approval.
		if reflect.DeepEqual(retained, types.AppSettings{}) {
			return
		}
		if publishApp {
			a.ws.SetAppDefaults(retained)
		} else if request.Method == coreprotocol.CapabilityAction {
			a.ws.SetAppDefaults(a.ws.AppDefaults())
		}
	}
	return run, apply, true
}

func publicCapabilityResult(s captypes.Snapshot, entries []captypes.Entry, content string) coreprotocol.CapabilityResult {
	revision := entityRevision(s.Settings)
	s.Settings = publicCapabilities(s.Settings)
	s.Entries = publicCapabilityEntries(s.Entries)
	entries = publicCapabilityEntries(entries)
	s.Error = ""
	for i := range s.Connections {
		s.Connections[i].Error = ""
		s.Connections[i].Logs = nil
		s.Connections[i].AuthURL = ""
	}
	// Detached connection tests and auth state can contain credential diagnostics.
	// Structured content is restricted to non-secret data reads by the caller.
	return coreprotocol.CapabilityResult{Revision: revision, Snapshot: s, Entries: entries, Content: content}
}

func publicCapabilityEntries(entries []captypes.Entry) []captypes.Entry {
	out := append([]captypes.Entry(nil), entries...)
	for i := range out {
		out[i].Reason = ""
		out[i].Repository = publicURL(out[i].Repository)
		out[i].Website = publicURL(out[i].Website)
	}
	return out
}

func publicCapabilityPreview(p captypes.AcquisitionPreview) captypes.AcquisitionPreview {
	p.Source.Location = publicURL(p.Source.Location)
	p.Candidate = publicCapabilityEntries([]captypes.Entry{p.Candidate})[0]
	if p.Existing != nil {
		value := publicCapabilityEntries([]captypes.Entry{*p.Existing})[0]
		p.Existing = &value
	}
	return p
}

func resolveConnectionSecrets(in *coreprotocol.CapabilityActionRequest, before captypes.Settings) error {
	if in.Action.Connection == nil || in.Action.Connection.ID != in.Action.ID {
		return errors.New("connection identity differs")
	}
	value := in.Action.Connection
	if len(value.Environment)+len(value.Headers)+len(value.SecretRefs)+len(value.Args) != 0 || value.CredentialRef != "" {
		return errors.New("use write-only connection secret updates")
	}
	old := before.Connections[in.Action.ID]
	updates := in.ConnectionSecrets
	var err error
	if value.URL, err = resolveSecret(updates.Endpoint, old.URL); err != nil {
		return err
	}
	if value.Args, err = resolveSecret(updates.Args, old.Args); err != nil {
		return err
	}
	if value.Environment, err = resolveSecret(updates.Environment, old.Environment); err != nil {
		return err
	}
	if value.Headers, err = resolveSecret(updates.Headers, old.Headers); err != nil {
		return err
	}
	if value.SecretRefs, err = resolveSecret(updates.SecretRefs, old.SecretRefs); err != nil {
		return err
	}
	if value.CredentialRef, err = resolveSecret(updates.CredentialRef, old.CredentialRef); err != nil {
		return err
	}
	return nil
}
