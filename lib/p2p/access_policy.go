package p2p

import (
	"sort"
	"strings"
)

const (
	P2PAccessModeOpen      = "open"
	P2PAccessModeWhitelist = "whitelist"

	P2PAccessReasonDynamicTarget = "dynamic_target"
	P2PAccessReasonProxyMode     = "proxy_mode"
)

type P2PAccessPolicy struct {
	Mode       string   `json:"mode,omitempty"`
	Targets    []string `json:"targets,omitempty"`
	OpenReason string   `json:"open_reason,omitempty"`
}

func NormalizeP2PAccessPolicy(policy P2PAccessPolicy) P2PAccessPolicy {
	mode := strings.TrimSpace(strings.ToLower(policy.Mode))
	switch mode {
	case P2PAccessModeWhitelist:
		seen := make(map[string]struct{}, len(policy.Targets))
		targets := make([]string, 0, len(policy.Targets))
		for _, target := range policy.Targets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			targets = append(targets, target)
		}
		sort.Strings(targets)
		if len(targets) == 0 {
			return P2PAccessPolicy{
				Mode:       P2PAccessModeOpen,
				OpenReason: P2PAccessReasonDynamicTarget,
			}
		}
		return P2PAccessPolicy{
			Mode:    P2PAccessModeWhitelist,
			Targets: targets,
		}
	default:
		reason := strings.TrimSpace(policy.OpenReason)
		if reason == "" {
			reason = P2PAccessReasonDynamicTarget
		}
		return P2PAccessPolicy{
			Mode:       P2PAccessModeOpen,
			OpenReason: reason,
		}
	}
}

func MergeP2PAccessPolicy(base, next P2PAccessPolicy) P2PAccessPolicy {
	base = NormalizeP2PAccessPolicy(base)
	next = NormalizeP2PAccessPolicy(next)
	if base.Mode == P2PAccessModeOpen {
		return base
	}
	if next.Mode == P2PAccessModeOpen {
		return next
	}
	merged := P2PAccessPolicy{
		Mode:    P2PAccessModeWhitelist,
		Targets: make([]string, 0, len(base.Targets)+len(next.Targets)),
	}
	merged.Targets = append(merged.Targets, base.Targets...)
	merged.Targets = append(merged.Targets, next.Targets...)
	return NormalizeP2PAccessPolicy(merged)
}

func BuildP2PAccessPolicy(targetRaw string, proxyLike bool) P2PAccessPolicy {
	if proxyLike {
		return NormalizeP2PAccessPolicy(P2PAccessPolicy{
			Mode:       P2PAccessModeOpen,
			OpenReason: P2PAccessReasonProxyMode,
		})
	}
	lines := make([]string, 0)
	targetRaw = strings.ReplaceAll(targetRaw, "\r\n", "\n")
	for _, part := range strings.Split(targetRaw, "\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines = append(lines, part)
	}
	return NormalizeP2PAccessPolicy(P2PAccessPolicy{
		Mode:    P2PAccessModeWhitelist,
		Targets: lines,
	})
}
