package p2pstate

import (
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/p2p"
)

func resetRegistryState(t *testing.T) {
	t.Helper()
	probeSessionsByID = sync.Map{}
	probeSessionsByRoute = sync.Map{}
	currentProbeTime = time.Now
	t.Cleanup(func() {
		probeSessionsByID = sync.Map{}
		probeSessionsByRoute = sync.Map{}
		currentProbeTime = time.Now
	})
}

func TestRegistryLifecycle(t *testing.T) {
	resetRegistryState(t)
	const sessionID = "session-1"
	const token = "token-1"
	wire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: "route-1"}, sessionID)
	routeKey := p2p.WireRouteKey(wire.RouteID)
	Register(sessionID, wire, token, time.Second)
	if !ValidateToken(sessionID, token) {
		t.Fatal("expected token to validate")
	}
	lookup, ok := LookupSession(routeKey)
	if !ok || lookup.SessionID != sessionID || lookup.Token != token {
		t.Fatalf("LookupSession() = %#v, %v", lookup, ok)
	}
	RecordObservation(routeKey, common.WORK_P2P_VISITOR, p2p.ProbeSample{
		ProbePort:       10206,
		ObservedAddr:    "1.1.1.1:4000",
		ServerReplyAddr: "1.1.1.1:4000",
	})
	RecordObservation(routeKey, common.WORK_P2P_VISITOR, p2p.ProbeSample{
		ProbePort:       10208,
		ObservedAddr:    "1.1.1.1:4004",
		ServerReplyAddr: "1.1.1.1:4004",
	})
	if !AcceptPacket(routeKey, time.Now().UnixMilli(), "nonce-1") {
		t.Fatal("expected AcceptPacket to accept the first nonce")
	}
	samples := GetObservations(sessionID, common.WORK_P2P_VISITOR)
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if samples[0].ProbePort != 10206 || samples[1].ProbePort != 10208 {
		t.Fatalf("unexpected sample ordering %#v", samples)
	}
	Unregister(sessionID)
	if ValidateToken(sessionID, token) {
		t.Fatal("token should be invalid after unregister")
	}
	if _, ok := LookupSession(routeKey); ok {
		t.Fatal("route lookup should be invalid after unregister")
	}
	if samples := GetObservations(sessionID, common.WORK_P2P_VISITOR); len(samples) != 0 {
		t.Fatalf("observations should be empty after unregister, got %#v", samples)
	}
}

func TestRegistryKeepsMultipleObservationsForSameProbePort(t *testing.T) {
	resetRegistryState(t)
	const sessionID = "session-2"
	const token = "token-2"
	wire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: "route-2"}, sessionID)
	routeKey := p2p.WireRouteKey(wire.RouteID)
	Register(sessionID, wire, token, time.Second)
	t.Cleanup(func() { Unregister(sessionID) })

	RecordObservation(routeKey, common.WORK_P2P_VISITOR, p2p.ProbeSample{
		ProbePort:       10206,
		ObservedAddr:    "198.51.100.10:4000",
		ServerReplyAddr: "203.0.113.10:10206",
	})
	RecordObservation(routeKey, common.WORK_P2P_VISITOR, p2p.ProbeSample{
		ProbePort:       10206,
		ObservedAddr:    "[2001:db8::10]:4000",
		ServerReplyAddr: "[2001:db8::20]:10206",
	})

	samples := GetObservations(sessionID, common.WORK_P2P_VISITOR)
	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if samples[0].ServerReplyAddr == samples[1].ServerReplyAddr {
		t.Fatalf("same probe port observations should be stored independently, got %#v", samples)
	}
}

func TestRegisterReplacesPreviousRouteForSameSessionID(t *testing.T) {
	resetRegistryState(t)
	const sessionID = "session-rebind"
	const token = "token-rebind"

	firstWire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: "route-a"}, sessionID)
	secondWire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: "route-b"}, sessionID)
	firstRoute := p2p.WireRouteKey(firstWire.RouteID)
	secondRoute := p2p.WireRouteKey(secondWire.RouteID)

	Register(sessionID, firstWire, token, time.Second)
	Register(sessionID, secondWire, token, time.Second)
	t.Cleanup(func() { Unregister(sessionID) })

	if _, ok := LookupSession(firstRoute); ok {
		t.Fatal("old route lookup should be removed when the same session id is re-registered")
	}
	lookup, ok := LookupSession(secondRoute)
	if !ok || lookup.SessionID != sessionID {
		t.Fatalf("LookupSession(secondRoute) = %#v, %v", lookup, ok)
	}
}

func TestRegisterReplacesPreviousSessionForSameRoute(t *testing.T) {
	resetRegistryState(t)
	const routeID = "shared-route"
	const firstSessionID = "session-old"
	const secondSessionID = "session-new"
	const firstToken = "token-old"
	const secondToken = "token-new"

	firstWire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: routeID}, firstSessionID)
	secondWire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: routeID}, secondSessionID)
	routeKey := p2p.WireRouteKey(firstWire.RouteID)

	Register(firstSessionID, firstWire, firstToken, time.Second)
	Register(secondSessionID, secondWire, secondToken, time.Second)
	t.Cleanup(func() { Unregister(secondSessionID) })

	if ValidateToken(firstSessionID, firstToken) {
		t.Fatal("old session id should be removed when a new session claims the same route")
	}
	lookup, ok := LookupSession(routeKey)
	if !ok || lookup.SessionID != secondSessionID || lookup.Token != secondToken {
		t.Fatalf("LookupSession(routeKey) = %#v, %v", lookup, ok)
	}
}

func TestUnregisterDoesNotRemoveReplacementRouteMapping(t *testing.T) {
	resetRegistryState(t)
	expiresAt := time.Now().Add(time.Minute)
	oldSession := &probeSession{
		sessionID: "session-old",
		routeKey:  "route-shared",
		token:     "token-old",
		expiresAt: expiresAt,
	}
	newSession := &probeSession{
		sessionID: "session-new",
		routeKey:  "route-shared",
		token:     "token-new",
		expiresAt: expiresAt,
	}
	probeSessionsByID.Store(oldSession.sessionID, oldSession)
	probeSessionsByID.Store(newSession.sessionID, newSession)
	probeSessionsByRoute.Store(oldSession.routeKey, newSession)

	Unregister(oldSession.sessionID)

	lookup, ok := LookupSession(oldSession.routeKey)
	if !ok || lookup.SessionID != newSession.sessionID || lookup.Token != newSession.token {
		t.Fatalf("LookupSession(routeKey) = %#v, %v", lookup, ok)
	}
	if getByID(oldSession.sessionID) != nil {
		t.Fatal("old session should be removed from id index")
	}
}

func TestDeleteSessionDoesNotRemoveReplacementRouteMapping(t *testing.T) {
	resetRegistryState(t)
	expiresAt := time.Now().Add(time.Minute)
	oldSession := &probeSession{
		sessionID: "session-old",
		routeKey:  "route-shared",
		token:     "token-old",
		expiresAt: expiresAt,
	}
	newSession := &probeSession{
		sessionID: "session-new",
		routeKey:  "route-shared",
		token:     "token-new",
		expiresAt: expiresAt,
	}
	probeSessionsByID.Store(oldSession.sessionID, oldSession)
	probeSessionsByID.Store(newSession.sessionID, newSession)
	probeSessionsByRoute.Store(oldSession.routeKey, newSession)

	deleteSession(oldSession)

	lookup, ok := LookupSession(oldSession.routeKey)
	if !ok || lookup.SessionID != newSession.sessionID || lookup.Token != newSession.token {
		t.Fatalf("LookupSession(routeKey) = %#v, %v", lookup, ok)
	}
	if getByID(oldSession.sessionID) != nil {
		t.Fatal("old session should be removed from id index")
	}
}

func TestDeleteSessionIDValueIfCurrentIgnoresReplacement(t *testing.T) {
	resetRegistryState(t)
	replacement := &probeSession{
		sessionID: "session-new",
		routeKey:  "route-shared",
		token:     "token-new",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByID.Store(replacement.sessionID, replacement)

	if deleteSessionIDValueIfCurrent(replacement.sessionID, "stale-invalid-entry") {
		t.Fatal("deleteSessionIDValueIfCurrent should not remove a replacement session")
	}
	if current := getByID(replacement.sessionID); current != replacement {
		t.Fatalf("current session = %#v, want replacement session", current)
	}
}

func TestDeleteSessionRouteValueIfCurrentIgnoresReplacement(t *testing.T) {
	resetRegistryState(t)
	replacement := &probeSession{
		sessionID: "session-new",
		routeKey:  "route-shared",
		token:     "token-new",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByRoute.Store(replacement.routeKey, replacement)

	if deleteSessionRouteValueIfCurrent(replacement.routeKey, "stale-invalid-entry") {
		t.Fatal("deleteSessionRouteValueIfCurrent should not remove a replacement session")
	}
	if current := getByRoute(replacement.routeKey); current != replacement {
		t.Fatalf("current route session = %#v, want replacement session", current)
	}
}

func TestGetByIDAndRouteDropInvalidEntries(t *testing.T) {
	resetRegistryState(t)
	probeSessionsByID.Store("invalid-session", "bad-id-entry")
	probeSessionsByRoute.Store("invalid-route", "bad-route-entry")

	if session := getByID("invalid-session"); session != nil {
		t.Fatalf("getByID(invalid-session) = %#v, want nil", session)
	}
	if _, ok := probeSessionsByID.Load("invalid-session"); ok {
		t.Fatal("getByID should remove invalid id entry")
	}

	if session := getByRoute("invalid-route"); session != nil {
		t.Fatalf("getByRoute(invalid-route) = %#v, want nil", session)
	}
	if _, ok := probeSessionsByRoute.Load("invalid-route"); ok {
		t.Fatal("getByRoute should remove invalid route entry")
	}
}

func TestUnregisterDropsInvalidEntry(t *testing.T) {
	resetRegistryState(t)
	probeSessionsByID.Store("invalid-session", "bad-id-entry")

	Unregister("invalid-session")

	if _, ok := probeSessionsByID.Load("invalid-session"); ok {
		t.Fatal("Unregister should remove invalid id entry")
	}
}

func TestCleanupProbeSessionIDEntriesDropsInvalidKeyWithoutAffectingValidSession(t *testing.T) {
	resetRegistryState(t)
	valid := &probeSession{
		sessionID: "session-valid",
		routeKey:  "route-valid",
		token:     "token-valid",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByID.Store(valid.sessionID, valid)
	probeSessionsByID.Store(12345, "bad-id-entry")

	cleanupProbeSessionIDEntries(time.Now())

	if _, ok := probeSessionsByID.Load(12345); ok {
		t.Fatal("cleanupProbeSessionIDEntries should remove invalid id key entry")
	}
	if current := getByID(valid.sessionID); current != valid {
		t.Fatalf("getByID(valid.sessionID) = %#v, want valid session", current)
	}
}

func TestCleanupProbeSessionIDEntriesDropsMismatchedSessionIDEntry(t *testing.T) {
	resetRegistryState(t)
	valid := &probeSession{
		sessionID: "session-valid",
		routeKey:  "route-valid",
		token:     "token-valid",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByID.Store(valid.sessionID, valid)
	probeSessionsByID.Store("session-stale", valid)

	cleanupProbeSessionIDEntries(time.Now())

	if _, ok := probeSessionsByID.Load("session-stale"); ok {
		t.Fatal("cleanupProbeSessionIDEntries should remove mismatched session id entry")
	}
	if current := getByID(valid.sessionID); current != valid {
		t.Fatalf("getByID(valid.sessionID) = %#v, want valid session", current)
	}
}

func TestCleanupProbeSessionRouteEntriesDropsInvalidKeyWithoutAffectingValidSession(t *testing.T) {
	resetRegistryState(t)
	valid := &probeSession{
		sessionID: "session-valid",
		routeKey:  "route-valid",
		token:     "token-valid",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByRoute.Store(valid.routeKey, valid)
	probeSessionsByRoute.Store(12345, "bad-route-entry")

	cleanupProbeSessionRouteEntries(time.Now())

	if _, ok := probeSessionsByRoute.Load(12345); ok {
		t.Fatal("cleanupProbeSessionRouteEntries should remove invalid route key entry")
	}
	if current := getByRoute(valid.routeKey); current != valid {
		t.Fatalf("getByRoute(valid.routeKey) = %#v, want valid session", current)
	}
}

func TestCleanupProbeSessionRouteEntriesDropsMismatchedRouteEntry(t *testing.T) {
	resetRegistryState(t)
	valid := &probeSession{
		sessionID: "session-valid",
		routeKey:  "route-valid",
		token:     "token-valid",
		expiresAt: time.Now().Add(time.Minute),
	}
	probeSessionsByRoute.Store(valid.routeKey, valid)
	probeSessionsByRoute.Store("route-stale", valid)

	cleanupProbeSessionRouteEntries(time.Now())

	if _, ok := probeSessionsByRoute.Load("route-stale"); ok {
		t.Fatal("cleanupProbeSessionRouteEntries should remove mismatched route entry")
	}
	if current := getByRoute(valid.routeKey); current != valid {
		t.Fatalf("getByRoute(valid.routeKey) = %#v, want valid session", current)
	}
}

func TestValidateTokenRemovesExpiredSession(t *testing.T) {
	resetRegistryState(t)
	expired := &probeSession{
		sessionID: "session-expired",
		routeKey:  "route-expired",
		token:     "token-expired",
		expiresAt: time.Now().Add(-time.Second),
	}
	probeSessionsByID.Store(expired.sessionID, expired)
	probeSessionsByRoute.Store(expired.routeKey, expired)

	if ValidateToken(expired.sessionID, expired.token) {
		t.Fatal("ValidateToken should reject expired session")
	}
	if current := getByID(expired.sessionID); current != nil {
		t.Fatalf("getByID(expired.sessionID) = %#v, want nil after expiry cleanup", current)
	}
	if _, ok := LookupSession(expired.routeKey); ok {
		t.Fatal("LookupSession should be invalid after expiry cleanup")
	}
}

func TestValidateTokenExpiresAtDeadline(t *testing.T) {
	resetRegistryState(t)
	base := time.Unix(1700000000, 0)
	currentProbeTime = func() time.Time { return base }

	const sessionID = "session-deadline"
	const token = "token-deadline"
	wire := p2p.NormalizeP2PWireSpec(p2p.P2PWireSpec{RouteID: "route-deadline"}, sessionID)
	routeKey := p2p.WireRouteKey(wire.RouteID)
	Register(sessionID, wire, token, time.Second)

	currentProbeTime = func() time.Time { return base.Add(time.Second) }

	if ValidateToken(sessionID, token) {
		t.Fatal("ValidateToken should reject a session exactly at its expiry deadline")
	}
	if _, ok := LookupSession(routeKey); ok {
		t.Fatal("LookupSession should be invalid at the expiry deadline")
	}
}

func TestRecordObservationRemovesExpiredSession(t *testing.T) {
	resetRegistryState(t)
	expired := &probeSession{
		sessionID: "session-expired-observation",
		routeKey:  "route-expired-observation",
		token:     "token-expired-observation",
		expiresAt: time.Now().Add(-time.Second),
	}
	probeSessionsByID.Store(expired.sessionID, expired)
	probeSessionsByRoute.Store(expired.routeKey, expired)

	RecordObservation(expired.routeKey, common.WORK_P2P_VISITOR, p2p.ProbeSample{
		ProbePort:       10206,
		ObservedAddr:    "198.51.100.10:4000",
		ServerReplyAddr: "203.0.113.10:10206",
	})

	if current := getByID(expired.sessionID); current != nil {
		t.Fatalf("getByID(expired.sessionID) = %#v, want nil after expired observation cleanup", current)
	}
	if _, ok := LookupSession(expired.routeKey); ok {
		t.Fatal("LookupSession should be invalid after expired observation cleanup")
	}
}

func TestAcceptPacketRemovesExpiredSession(t *testing.T) {
	resetRegistryState(t)
	expired := &probeSession{
		sessionID: "session-expired-accept",
		routeKey:  "route-expired-accept",
		token:     "token-expired-accept",
		expiresAt: time.Now().Add(-time.Second),
		replay:    p2p.NewReplayWindow(30 * time.Second),
	}
	probeSessionsByID.Store(expired.sessionID, expired)
	probeSessionsByRoute.Store(expired.routeKey, expired)

	if AcceptPacket(expired.routeKey, time.Now().UnixMilli(), "nonce-expired") {
		t.Fatal("AcceptPacket should reject expired session")
	}
	if current := getByID(expired.sessionID); current != nil {
		t.Fatalf("getByID(expired.sessionID) = %#v, want nil after expired accept cleanup", current)
	}
	if _, ok := LookupSession(expired.routeKey); ok {
		t.Fatal("LookupSession should be invalid after expired accept cleanup")
	}
}

func TestGetObservationsReturnsDetachedSortedCopy(t *testing.T) {
	resetRegistryState(t)
	session := &probeSession{
		sessionID: "session-copy",
		routeKey:  "route-copy",
		token:     "token-copy",
		expiresAt: time.Now().Add(time.Minute),
		observations: map[string]map[string]p2p.ProbeSample{
			common.WORK_P2P_VISITOR: {
				"10208|203.0.113.8:10208": {
					ProbePort:       10208,
					ObservedAddr:    "198.51.100.8:4000",
					ServerReplyAddr: "203.0.113.8:10208",
				},
				"10206|203.0.113.6:10206": {
					ProbePort:       10206,
					ObservedAddr:    "198.51.100.6:4000",
					ServerReplyAddr: "203.0.113.6:10206",
				},
			},
		},
	}
	probeSessionsByID.Store(session.sessionID, session)
	probeSessionsByRoute.Store(session.routeKey, session)

	first := GetObservations(session.sessionID, common.WORK_P2P_VISITOR)
	if len(first) != 2 {
		t.Fatalf("len(first) = %d, want 2", len(first))
	}
	if first[0].ProbePort != 10206 || first[1].ProbePort != 10208 {
		t.Fatalf("first observations not sorted by probe port: %#v", first)
	}

	first[0].ObservedAddr = "mutated"

	second := GetObservations(session.sessionID, common.WORK_P2P_VISITOR)
	if len(second) != 2 {
		t.Fatalf("len(second) = %d, want 2", len(second))
	}
	if second[0].ObservedAddr != "198.51.100.6:4000" {
		t.Fatalf("second[0].ObservedAddr = %q, want original stored value", second[0].ObservedAddr)
	}
}
