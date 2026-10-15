package provider

import "slices"

// Service tiers a user can choose for a role. Each maps to the protocol's own
// tier names, in preference order.
var serviceTierAliases = map[string][]string{
	"standard": {"default", "standard_only"},
	"fast":     {"fast", "priority"},
	"priority": {"priority"},
	"flex":     {"flex"},
}

// ServiceTierChoices are the role-level tiers settings and self control offer.
func ServiceTierChoices() []string { return []string{"standard", "fast", "priority", "flex"} }

// SetServiceTier applies a role's processing tier over the account's when the
// connection's protocol offers it. An empty, unknown or unsupported tier
// leaves the account setting in effect, so a role choice never breaks a
// request to a backend without tiers.
func (p *Provider) SetServiceTier(tier string) {
	if p == nil || tier == "" {
		return
	}
	protocol := p.Protocol()
	if protocol == nil {
		return
	}
	offered := protocol.Info().Options.ServiceTiers
	for _, candidate := range serviceTierAliases[tier] {
		if slices.Contains(offered, candidate) {
			settings := ProtocolSettings{}
			if p.protocolSettings != nil {
				settings = *cloneProtocolSettings(p.protocolSettings)
			}
			settings.ServiceTier = candidate
			p.protocolSettings = &settings
			return
		}
	}
}
