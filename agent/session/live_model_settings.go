package session

import (
	"reflect"
	"strings"

	"crude/agent/turnloop"
)

// bindLiveModelSettings is read by the run goroutine between requests. Never
// mutate an in-flight provider or rebuild the cached tools/system/history here.
func (s *Session) bindLiveModelSettings(a *turnloop.Agent, initial *Config, current func() (*Config, CategoryConfig)) {
	errand := initial.CategoryConfig(CategoryErrand)
	source := a.CompactionProvider
	a.LiveSettings = func() turnloop.LiveSettings {
		cfg, role := current()
		live := turnloop.LiveSettings{MaxOutputTokens: a.MaxOutputTokens, CompactionProvider: source, CompactionMaxOutput: errand.ModelMaxOutput}
		live.CompactionPercent, live.RetentionTokens = rootCompactionChoice(role)
		if role.Model == a.Model && role.AccountID == a.Provider.AccountID {
			live.MaxOutputTokens = role.ModelMaxOutput
		}
		if next := cfg.CategoryConfig(CategoryErrand); !reflect.DeepEqual(next, errand) {
			p, err := cfg.providerForCategory(next)
			if err != nil {
				s.logger.Warn("CTX", "compaction_connection_rejected", "error", err.Error())
			} else {
				source, errand = p, next
				live.CompactionProvider, live.CompactionMaxOutput = p, next.ModelMaxOutput
			}
		}
		return live
	}
}

func (r *SubAgentRunner) bindChildLiveSettings(a *turnloop.Agent, initial *Config, spec turnloop.SubAgentSpec) {
	if spec.SettingsRole == "" || initial == nil || r.session.runtimeProfile == nil {
		return
	}
	id := r.orchestratorID
	r.session.bindLiveModelSettings(a, initial, func() (*Config, CategoryConfig) {
		cfg := r.session.runtimeProfile.EffectiveConfig(id)
		role := cfg.CategoryConfig(spec.Category)
		if name, named := strings.CutPrefix(spec.SettingsRole, "profile/"); named {
			if p, ok := cfg.Agents.AgentProfiles[name]; ok {
				role = mergeCategoryConfig(role, CategoryConfig{AccountID: p.AccountID, Model: p.Model, ModelMaxOutput: p.ModelMaxOutput, ContextCompactionPercent: p.ContextCompactionPercent, ContextRetentionTokens: p.ContextRetentionTokens})
			}
		}
		return cfg, role
	})
}
