package p2p

import (
	"reflect"
	"testing"
)

func TestBuildP2PAccessPolicyUsesWhitelistForFixedTargets(t *testing.T) {
	policy := BuildP2PAccessPolicy("10.0.0.1:22\n db.internal.example:5432 \n10.0.0.1:22", false)
	if policy.Mode != P2PAccessModeWhitelist {
		t.Fatalf("Mode = %q, want whitelist", policy.Mode)
	}
	want := []string{"10.0.0.1:22", "db.internal.example:5432"}
	if !reflect.DeepEqual(policy.Targets, want) {
		t.Fatalf("Targets = %#v, want %#v", policy.Targets, want)
	}
}

func TestBuildAndMergeP2PAccessPolicyOpenDominatesWhitelist(t *testing.T) {
	base := BuildP2PAccessPolicy("10.0.0.1:22", false)
	open := BuildP2PAccessPolicy("", false)
	merged := MergeP2PAccessPolicy(base, open)
	if merged.Mode != P2PAccessModeOpen {
		t.Fatalf("merged mode = %q, want open", merged.Mode)
	}
	if merged.OpenReason != P2PAccessReasonDynamicTarget {
		t.Fatalf("OpenReason = %q, want %q", merged.OpenReason, P2PAccessReasonDynamicTarget)
	}

	proxyOpen := BuildP2PAccessPolicy("10.0.0.1:22", true)
	merged = MergeP2PAccessPolicy(base, proxyOpen)
	if merged.Mode != P2PAccessModeOpen {
		t.Fatalf("proxy merged mode = %q, want open", merged.Mode)
	}
	if merged.OpenReason != P2PAccessReasonProxyMode {
		t.Fatalf("proxy OpenReason = %q, want %q", merged.OpenReason, P2PAccessReasonProxyMode)
	}
}

func TestMergeP2PAccessPolicyCombinesWhitelistTargets(t *testing.T) {
	left := BuildP2PAccessPolicy("10.0.0.1:22\ndb.internal.example:5432", false)
	right := BuildP2PAccessPolicy("10.0.0.2:53\ndb.internal.example:5432", false)
	merged := MergeP2PAccessPolicy(left, right)
	if merged.Mode != P2PAccessModeWhitelist {
		t.Fatalf("merged mode = %q, want whitelist", merged.Mode)
	}
	want := []string{"10.0.0.1:22", "10.0.0.2:53", "db.internal.example:5432"}
	if !reflect.DeepEqual(merged.Targets, want) {
		t.Fatalf("Targets = %#v, want %#v", merged.Targets, want)
	}
}
