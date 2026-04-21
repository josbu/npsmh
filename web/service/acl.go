package service

import (
	"strings"

	"github.com/djylb/nps/lib/file"
)

func countNormalizedRuleLines(rules string) int {
	rules = normalizeRules(rules)
	if rules == "" {
		return 0
	}
	seen := make(map[string]struct{})
	count := 0
	for len(rules) > 0 {
		line, rest, found := strings.Cut(rules, "\n")
		rules = rest
		line = strings.TrimSpace(line)
		if line != "" {
			if _, ok := seen[line]; !ok {
				seen[line] = struct{}{}
				count++
			}
		}
		if !found {
			break
		}
	}
	return count
}

func normalizeEntryACLInput(mode int, rules string) (int, string) {
	normalizedMode := normalizeACLMode(mode)
	normalizedRules := normalizeRules(rules)
	if normalizedMode == file.AclOff {
		return file.AclOff, ""
	}
	return normalizedMode, normalizedRules
}

func normalizeDestinationACLInput(mode int, rules string) (int, string) {
	normalizedMode := normalizeACLMode(mode)
	normalizedRules := normalizeRules(rules)
	if normalizedMode == file.AclOff {
		return file.AclOff, ""
	}
	return normalizedMode, normalizedRules
}

func normalizeACLMode(mode int) int {
	switch mode {
	case file.AclOff, file.AclWhitelist, file.AclBlacklist:
		return mode
	default:
		return file.AclOff
	}
}

func normalizeRules(rules string) string {
	return strings.TrimSpace(strings.ReplaceAll(rules, "\r\n", "\n"))
}
