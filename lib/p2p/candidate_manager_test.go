package p2p

import (
	"testing"
	"time"
)

func TestCandidateManagerConfirmedRemote(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Confirm("0.0.0.0:3000", "1.1.1.1:5002")
	if got := manager.ConfirmedRemote(); got != "1.1.1.1:5002" {
		t.Fatalf("ConfirmedRemote() = %q, want %q", got, "1.1.1.1:5002")
	}
}

func TestCandidateManagerSeedsCandidateRemoteFromRendezvous(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	if got := manager.CandidateRemote(); got != "1.1.1.1:5000" {
		t.Fatalf("CandidateRemote() = %q, want rendezvous remote", got)
	}
}

func TestCandidateManagerKeepsConfirmedRemoteStable(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Confirm("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5010")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5010")
	manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5010")
	if got := manager.ConfirmedRemote(); got != "1.1.1.1:5002" {
		t.Fatalf("ConfirmedRemote() changed to %q", got)
	}
	if got := manager.CandidateRemote(); got != "1.1.1.1:5002" {
		t.Fatalf("CandidateRemote() should stay on confirmed remote, got %q", got)
	}
}

func TestCandidateManagerSingleNomination(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Observe("0.0.0.0:3001", "1.1.1.1:5010")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3001", "1.1.1.1:5010")
	if _, ok := manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002"); !ok {
		t.Fatal("first nomination should succeed")
	}
	if _, ok := manager.TryNominate("0.0.0.0:3001", "1.1.1.1:5010"); ok {
		t.Fatal("second nomination should be blocked")
	}
	pair := manager.NominatedPair()
	if pair == nil || pair.LocalAddr != "0.0.0.0:3000" || pair.RemoteAddr != "1.1.1.1:5002" {
		t.Fatalf("unexpected nominated pair %#v", pair)
	}
}

func TestCandidateManagerHasConfirmedOrNominatedTracksSessionState(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"

	if manager.HasConfirmed() || manager.HasConfirmedOrNominated() {
		t.Fatal("fresh manager should not report confirmed or nominated state")
	}

	manager.MarkSucceeded(localAddr, remoteAddr)
	if manager.HasConfirmed() || manager.HasConfirmedOrNominated() {
		t.Fatal("succeeded-only candidate should not report confirmed or nominated state")
	}

	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed")
	}
	if manager.HasConfirmed() {
		t.Fatal("nominated candidate should not report confirmed state")
	}
	if !manager.HasConfirmedOrNominated() {
		t.Fatal("nominated candidate should report active nomination state")
	}

	if confirmed := manager.Confirm(localAddr, remoteAddr); confirmed == nil {
		t.Fatal("confirm should succeed")
	}
	if !manager.HasConfirmed() || !manager.HasConfirmedOrNominated() {
		t.Fatal("confirmed candidate should report terminal confirmed state")
	}
}

func TestCandidateManagerTryNominateBestPrefersHigherScore(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Observe("0.0.0.0:3001", "1.1.1.1:5010")
	manager.MarkSucceededWithPriority("0.0.0.0:3001", "1.1.1.1:5010", CandidatePriority{
		Score:  100,
		Reason: "target[1]",
	})
	manager.MarkSucceededWithPriority("0.0.0.0:3000", "1.1.1.1:5002", CandidatePriority{
		Score:  200,
		Reason: "direct[0]",
	})
	pair, ok := manager.TryNominateBest()
	if !ok {
		t.Fatal("best nomination should succeed")
	}
	if pair == nil || pair.LocalAddr != "0.0.0.0:3000" || pair.RemoteAddr != "1.1.1.1:5002" {
		t.Fatalf("unexpected nominated pair %#v", pair)
	}
	if pair.Score != 200 || pair.ScoreReason != "direct[0]" {
		t.Fatalf("unexpected candidate priority %#v", pair)
	}
}

func TestCandidateManagerReleaseNominationRestoresSucceededState(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceededWithPriority("0.0.0.0:3000", "1.1.1.1:5002", CandidatePriority{
		Score:  200,
		Reason: "direct[0]",
	})
	if _, ok := manager.TryNominateBest(); !ok {
		t.Fatal("best nomination should succeed")
	}
	pair := manager.ReleaseNomination("0.0.0.0:3000", "1.1.1.1:5002")
	if pair == nil {
		t.Fatal("ReleaseNomination should return nominated pair")
	}
	if pair.State != CandidateSucceeded || pair.Nominated {
		t.Fatalf("released pair should return to succeeded state, got %#v", pair)
	}
	if manager.HasNominated() {
		t.Fatal("nominated pair should be cleared after release")
	}
}

func TestCandidateManagerAdoptNominationReplacesOlderPair(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3001", "1.1.1.1:5010")
	if _, ok := manager.AdoptNomination("0.0.0.0:3000", "1.1.1.1:5002"); !ok {
		t.Fatal("first adopt nomination should succeed")
	}
	if _, ok := manager.AdoptNomination("0.0.0.0:3001", "1.1.1.1:5010"); !ok {
		t.Fatal("second adopt nomination should replace the first pair")
	}
	pair := manager.NominatedPair()
	if pair == nil || pair.LocalAddr != "0.0.0.0:3001" || pair.RemoteAddr != "1.1.1.1:5010" {
		t.Fatalf("unexpected nominated pair %#v", pair)
	}
	if prior := manager.candidates[candidateKey("0.0.0.0:3000", "1.1.1.1:5002")]; prior == nil || prior.Nominated || prior.State != CandidateSucceeded {
		t.Fatalf("old nominated pair should revert to succeeded, got %#v", prior)
	}
}

func TestCandidateManagerBackoffNominationSkipsCoolingPair(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.MarkSucceededWithPriority("0.0.0.0:3000", "1.1.1.1:5002", CandidatePriority{
		Score:  200,
		Reason: "direct[0]",
	})
	manager.MarkSucceededWithPriority("0.0.0.0:3001", "1.1.1.1:5010", CandidatePriority{
		Score:  150,
		Reason: "target[0]",
	})
	if _, ok := manager.TryNominateBest(); !ok {
		t.Fatal("best nomination should succeed")
	}
	if pair := manager.BackoffNomination("0.0.0.0:3000", "1.1.1.1:5002", 500*time.Millisecond); pair == nil {
		t.Fatal("BackoffNomination should return the previously nominated pair")
	}
	pair, ok := manager.TryNominateBest()
	if !ok {
		t.Fatal("cooling pair should allow the next candidate to be nominated")
	}
	if pair.LocalAddr != "0.0.0.0:3001" || pair.RemoteAddr != "1.1.1.1:5010" {
		t.Fatalf("unexpected nominated pair %#v", pair)
	}
}

func TestCandidateManagerRequiresNominationBeforeConfirm(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	if pair := manager.Confirm("0.0.0.0:3000", "1.1.1.1:5002"); pair != nil {
		t.Fatalf("confirm should fail without nomination, got %#v", pair)
	}
	if manager.HasConfirmed() {
		t.Fatal("confirmed pair should stay empty without nomination")
	}
}

func TestCandidateManagerRejectsCrossPairConfirm(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Observe("0.0.0.0:3001", "1.1.1.1:5010")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3001", "1.1.1.1:5010")
	if _, ok := manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002"); !ok {
		t.Fatal("nomination should succeed for first pair")
	}
	if pair := manager.Confirm("0.0.0.0:3001", "1.1.1.1:5010"); pair != nil {
		t.Fatalf("cross-pair confirm should be rejected, got %#v", pair)
	}
	if pair := manager.ConfirmedPair(); pair != nil {
		t.Fatalf("unexpected confirmed pair %#v", pair)
	}
	if pair := manager.Confirm("0.0.0.0:3000", "1.1.1.1:5002"); pair == nil {
		t.Fatal("nominated pair should confirm successfully")
	}
}

func TestCandidateManagerConfirmClearsNominationAndIgnoresDuplicates(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"

	manager.Observe(localAddr, remoteAddr)
	manager.MarkSucceeded(localAddr, remoteAddr)
	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed")
	}
	confirmed := manager.Confirm(localAddr, remoteAddr)
	if confirmed == nil {
		t.Fatal("confirm should succeed")
	}
	if confirmed.State != CandidateConfirmed || confirmed.Nominated || !confirmed.Confirmed {
		t.Fatalf("confirmed snapshot should represent a terminal confirmed pair, got %#v", confirmed)
	}
	if manager.HasNominated() {
		t.Fatal("nominated pair should clear after confirm")
	}
	if duplicate := manager.Confirm(localAddr, remoteAddr); duplicate != nil {
		t.Fatalf("duplicate confirm should be ignored, got %#v", duplicate)
	}
	if current := manager.ConfirmedPair(); current == nil || current.State != CandidateConfirmed || current.Nominated || !current.Confirmed {
		t.Fatalf("confirmed pair should stay stable after duplicate confirm, got %#v", current)
	}
}

func TestCandidateManagerClosesOtherPairsAfterConfirm(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.Observe("0.0.0.0:3001", "1.1.1.1:5010")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3001", "1.1.1.1:5010")
	manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002")
	if pair := manager.Confirm("0.0.0.0:3000", "1.1.1.1:5002"); pair == nil {
		t.Fatal("expected nominated pair to confirm")
	}
	if state := manager.candidates[candidateKey("0.0.0.0:3001", "1.1.1.1:5010")].State; state != CandidateClosed {
		t.Fatalf("other candidate state = %s, want %s", state, CandidateClosed)
	}
	manager.MarkSucceeded("0.0.0.0:3001", "1.1.1.1:5010")
	if state := manager.candidates[candidateKey("0.0.0.0:3001", "1.1.1.1:5010")].State; state != CandidateClosed {
		t.Fatalf("closed candidate should stay closed, got %s", state)
	}
}

func TestCandidateManagerMarkSucceededPreservesConfirmedState(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"

	manager.Observe(localAddr, remoteAddr)
	manager.MarkSucceeded(localAddr, remoteAddr)
	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed")
	}
	if confirmed := manager.Confirm(localAddr, remoteAddr); confirmed == nil {
		t.Fatal("confirm should succeed")
	}

	pair := manager.MarkSucceeded(localAddr, remoteAddr)
	if pair == nil || pair.State != CandidateConfirmed || !pair.Confirmed {
		t.Fatalf("confirmed pair should stay confirmed after success, got %#v", pair)
	}
	if current := manager.ConfirmedPair(); current == nil || current.State != CandidateConfirmed || !current.Confirmed {
		t.Fatalf("confirmed snapshot should stay confirmed, got %#v", current)
	}
}

func TestCandidateManagerAdoptNominationPreservesConfirmedState(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"

	manager.Observe(localAddr, remoteAddr)
	manager.MarkSucceeded(localAddr, remoteAddr)
	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed")
	}
	if confirmed := manager.Confirm(localAddr, remoteAddr); confirmed == nil {
		t.Fatal("confirm should succeed")
	}

	pair, adopted := manager.AdoptNomination(localAddr, remoteAddr)
	if adopted {
		t.Fatal("confirmed pair should not re-enter nomination")
	}
	if pair == nil || pair.State != CandidateConfirmed || !pair.Confirmed {
		t.Fatalf("confirmed pair should stay confirmed after duplicate adopt, got %#v", pair)
	}
	if current := manager.ConfirmedPair(); current == nil || current.State != CandidateConfirmed || !current.Confirmed {
		t.Fatalf("confirmed snapshot should stay confirmed, got %#v", current)
	}
}

func TestSplitBrainNominationConvergesToSinglePair(t *testing.T) {
	visitor := NewCandidateManager("1.1.1.1:5000")
	provider := NewCandidateManager("2.2.2.2:6000")

	visitor.Observe("v-local-1", "p-remote-1")
	visitor.Observe("v-local-2", "p-remote-2")
	visitor.MarkSucceeded("v-local-1", "p-remote-1")
	visitor.MarkSucceeded("v-local-2", "p-remote-2")

	provider.Observe("p-local-1", "v-remote-1")
	provider.Observe("p-local-2", "v-remote-2")
	provider.MarkSucceeded("p-local-1", "v-remote-1")
	provider.MarkSucceeded("p-local-2", "v-remote-2")

	if _, ok := visitor.TryNominate("v-local-2", "p-remote-2"); !ok {
		t.Fatal("visitor should nominate one winning pair")
	}
	if _, ok := provider.TryNominate("p-local-2", "v-remote-2"); !ok {
		t.Fatal("provider should accept the same nominated pair once END arrives")
	}
	if provider.Confirm("p-local-2", "v-remote-2") == nil {
		t.Fatal("provider should confirm nominated pair")
	}
	if visitor.Confirm("v-local-2", "p-remote-2") == nil {
		t.Fatal("visitor should confirm nominated pair after ACCEPT")
	}
	if pair := visitor.ConfirmedPair(); pair == nil || pair.LocalAddr != "v-local-2" || pair.RemoteAddr != "p-remote-2" {
		t.Fatalf("unexpected visitor confirmed pair %#v", pair)
	}
	if pair := visitor.candidates[candidateKey("v-local-1", "p-remote-1")]; pair == nil || pair.State != CandidateClosed {
		t.Fatalf("losing visitor pair should close, got %#v", pair)
	}
	if pair := provider.candidates[candidateKey("p-local-1", "v-remote-1")]; pair == nil || pair.State != CandidateClosed {
		t.Fatalf("losing provider pair should close, got %#v", pair)
	}
}

func TestCandidateManagerPruneAndCleanup(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	key := candidateKey("0.0.0.0:3000", "1.1.1.1:5002")
	manager.candidates[key].LastSeenAt = time.Now().Add(-10 * time.Second)
	if pruned := manager.PruneStale(2 * time.Second); pruned != 1 {
		t.Fatalf("PruneStale() = %d, want 1", pruned)
	}
	if pair := manager.candidates[key]; pair == nil || pair.State != CandidateClosed {
		t.Fatalf("pair state = %#v, want %s", pair, CandidateClosed)
	}
	manager.candidates[key].LastSeenAt = time.Now().Add(-10 * time.Second)
	if removed := manager.CleanupClosed(2 * time.Second); removed != 1 {
		t.Fatalf("CleanupClosed() = %d, want 1", removed)
	}
	if len(manager.candidates) != 0 {
		t.Fatalf("cleanup should remove closed candidate, still have %#v", manager.candidates)
	}
}

func TestCandidateManagerCanReopenPrunedCandidateBeforeConfirm(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	key := candidateKey("0.0.0.0:3000", "1.1.1.1:5002")
	manager.candidates[key].LastSeenAt = time.Now().Add(-10 * time.Second)
	manager.PruneStale(2 * time.Second)
	reopened := manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	if reopened.State != CandidateDiscovered {
		t.Fatalf("reopened candidate state = %s, want %s", reopened.State, CandidateDiscovered)
	}
	succeeded := manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	if succeeded == nil || succeeded.State != CandidateSucceeded {
		t.Fatalf("reopened candidate should succeed again, got %#v", succeeded)
	}
}

func TestCandidateManagerObserveReopenClearsNominationBackoff(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"
	key := candidateKey(localAddr, remoteAddr)

	manager.MarkSucceeded(localAddr, remoteAddr)
	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed before backoff")
	}
	backedOff := manager.BackoffNomination(localAddr, remoteAddr, time.Hour)
	if backedOff == nil || backedOff.NominationFailures != 1 || backedOff.NominationCooldownUntil.IsZero() {
		t.Fatalf("backoff should record cooldown state, got %#v", backedOff)
	}
	manager.candidates[key].LastSeenAt = time.Now().Add(-10 * time.Second)
	manager.PruneStale(2 * time.Second)

	reopened := manager.Observe(localAddr, remoteAddr)
	if reopened == nil {
		t.Fatal("Observe should reopen the pruned candidate")
	}
	if reopened.NominationFailures != 0 || !reopened.NominationCooldownUntil.IsZero() {
		t.Fatalf("reopened candidate should clear nomination backoff, got %#v", reopened)
	}
	manager.MarkSucceeded(localAddr, remoteAddr)
	if pair, ok := manager.TryNominateBest(); !ok || pair == nil {
		t.Fatal("reopened candidate should be immediately nominatable again")
	}
}

func TestCandidateManagerMarkSucceededReopenClearsNominationBackoff(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	localAddr := "0.0.0.0:3000"
	remoteAddr := "1.1.1.1:5002"
	key := candidateKey(localAddr, remoteAddr)

	manager.MarkSucceeded(localAddr, remoteAddr)
	if _, ok := manager.TryNominate(localAddr, remoteAddr); !ok {
		t.Fatal("nomination should succeed before backoff")
	}
	manager.BackoffNomination(localAddr, remoteAddr, time.Hour)
	manager.candidates[key].LastSeenAt = time.Now().Add(-10 * time.Second)
	manager.PruneStale(2 * time.Second)

	reopened := manager.MarkSucceeded(localAddr, remoteAddr)
	if reopened == nil {
		t.Fatal("MarkSucceeded should reopen the pruned candidate")
	}
	if reopened.NominationFailures != 0 || !reopened.NominationCooldownUntil.IsZero() {
		t.Fatalf("reopened succeeded candidate should clear nomination backoff, got %#v", reopened)
	}
	if pair, ok := manager.TryNominateBest(); !ok || pair == nil {
		t.Fatal("reopened succeeded candidate should be immediately nominatable again")
	}
}

func TestCandidateManagerReturnsSnapshotPairs(t *testing.T) {
	manager := NewCandidateManager("1.1.1.1:5000")
	pair := manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	if pair == nil {
		t.Fatal("Observe should return a candidate snapshot")
	}
	pair.Score = 999
	pair.State = CandidateClosed

	current := manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	if current == nil {
		t.Fatal("Observe should still return a snapshot")
	}
	if current.Score == 999 || current.State == CandidateClosed {
		t.Fatalf("returned candidate pair should be a snapshot, got %#v", current)
	}

	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	if _, nominated := manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002"); !nominated {
		t.Fatal("TryNominate should nominate the first succeeded pair")
	}
	existing, nominated := manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002")
	if nominated || existing == nil {
		t.Fatal("TryNominate should return the existing nominated snapshot")
	}
	existing.State = CandidateClosed

	if current := manager.NominatedPair(); current == nil || current.State == CandidateClosed {
		t.Fatalf("nominated pair should remain internal state, got %#v", current)
	}
}
