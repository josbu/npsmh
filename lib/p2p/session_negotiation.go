package p2p

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

func (s *runtimeSession) startNominationLoopWithEpoch(ctx context.Context, writeConn net.PacketConn, remoteAddr net.Addr, epoch uint32) {
	if writeConn == nil || remoteAddr == nil {
		return
	}
	candidateID := candidateKey(writeConn.LocalAddr().String(), remoteAddr.String())
	key := nominationLoopKey(candidateID, epoch)
	loopCtx, loopCancel := context.WithCancel(normalizeRuntimeSessionContext(ctx))
	alreadyActive, staleCancels, loopToken := s.beginRetryLoop(&s.nominate, &s.nominateTokens, key, loopCancel)
	if alreadyActive {
		loopCancel()
		return
	}
	for _, cancel := range staleCancels {
		cancel()
	}

	go func() {
		defer s.finishRetryLoop(&s.nominate, &s.nominateTokens, key, loopToken)
		attempt := 0
		for {
			if !sleepContext(loopCtx, s.nominationRetryDelay(epoch, attempt)) {
				return
			}
			attempt++
			if s.cm.HasConfirmed() {
				return
			}
			if !s.matchesOutboundNomination(writeConn.LocalAddr().String(), remoteAddr.String(), epoch) {
				return
			}
			if s.shouldRenominate(writeConn.LocalAddr().String(), remoteAddr.String(), epoch) {
				s.tryRenominate(loopCtx, writeConn.LocalAddr().String(), remoteAddr.String(), epoch)
				return
			}
			if !s.sendNominationBurst(loopCtx, writeConn, remoteAddr, epoch) {
				return
			}
		}
	}()
}

func (s *runtimeSession) startAcceptLoop(ctx context.Context, writeConn net.PacketConn, remoteAddr net.Addr, epoch uint32) {
	if writeConn == nil || remoteAddr == nil {
		return
	}
	key := nominationLoopKey(candidateKey(writeConn.LocalAddr().String(), remoteAddr.String()), epoch)
	loopCtx, loopCancel := context.WithCancel(normalizeRuntimeSessionContext(ctx))
	alreadyActive, staleCancels, loopToken := s.beginRetryLoop(&s.accept, &s.acceptTokens, key, loopCancel)
	if alreadyActive {
		loopCancel()
		return
	}
	for _, cancel := range staleCancels {
		cancel()
	}

	go func() {
		defer s.finishRetryLoop(&s.accept, &s.acceptTokens, key, loopToken)
		attempt := 0
		for {
			if !sleepContext(loopCtx, s.nominationRetryDelay(epoch, attempt)) {
				return
			}
			attempt++
			if s.cm.HasConfirmed() {
				return
			}
			if !s.matchesInboundProposal(writeConn.LocalAddr().String(), remoteAddr.String(), epoch) {
				return
			}
			if !s.sendAcceptBurst(loopCtx, writeConn, remoteAddr, epoch) {
				return
			}
		}
	}()
}

func (s *runtimeSession) scheduleBestNomination(ctx context.Context) {
	if s == nil || s.start.Role != common.WORK_P2P_VISITOR {
		return
	}
	if s.cm.HasConfirmedOrNominated() {
		return
	}
	s.markNominationQueued()
	delay := s.nominationDelay()
	if delay <= 0 {
		s.nominateBestCandidate(ctx)
		return
	}
	s.mu.Lock()
	if s.nominationScheduled {
		s.mu.Unlock()
		return
	}
	s.nominationScheduled = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.nominationScheduled = false
			s.mu.Unlock()
		}()
		if !sleepContext(ctx, delay) {
			return
		}
		s.nominateBestCandidate(ctx)
	}()
}

func (s *runtimeSession) nominateBestCandidate(ctx context.Context) {
	if s == nil || s.start.Role != common.WORK_P2P_VISITOR {
		return
	}
	pair, nominated := s.cm.TryNominateBest()
	if !nominated || pair == nil {
		return
	}
	writeConn := s.socketForLocalAddr(pair.LocalAddr)
	if writeConn == nil {
		_ = s.cm.ReleaseNomination(pair.LocalAddr, pair.RemoteAddr)
		return
	}
	remoteAddr, err := net.ResolveUDPAddr(detectAddrFamily(writeConn.LocalAddr()).network(), pair.RemoteAddr)
	if err != nil {
		_ = s.cm.ReleaseNomination(pair.LocalAddr, pair.RemoteAddr)
		return
	}
	epoch := s.nextOutboundNominationEpoch()
	s.setOutboundNomination(pair.LocalAddr, pair.RemoteAddr, epoch)
	s.markCandidateNominated()
	logs.Info("[P2P] candidate nominated local=%s remote=%s score=%d reason=%s successes=%d", pair.LocalAddr, pair.RemoteAddr, pair.Score, pair.ScoreReason, pair.SuccessCount)
	if !s.sendNominationBurst(ctx, writeConn, remoteAddr, epoch) {
		s.clearOutboundNomination(pair.LocalAddr, pair.RemoteAddr, epoch)
		_ = s.cm.ReleaseNomination(pair.LocalAddr, pair.RemoteAddr)
		return
	}
	s.updateCandidateRemote(pair.RemoteAddr)
	logs.Info("[P2P] END sent local=%s remote=%s", pair.LocalAddr, pair.RemoteAddr)
	meta := nominationProgressMeta(epoch, candidateMeta(pair))
	_ = s.sendProgress("candidate_nominated", "ok", pair.RemoteAddr, pair.LocalAddr, pair.RemoteAddr, meta)
	s.startNominationLoopWithEpoch(ctx, writeConn, remoteAddr, epoch)
}

func (s *runtimeSession) sendControlBurst(ctx context.Context, writeConn net.PacketConn, packetType string, remoteAddr net.Addr, count int, gap time.Duration, epoch uint32) {
	if s == nil || writeConn == nil || remoteAddr == nil || count <= 0 {
		return
	}
	pacer := s.ensurePacer()
	for i := 0; i < count; i++ {
		_ = s.writeUDPToConnWithEpoch(writeConn, packetType, remoteAddr, epoch)
		if i >= count-1 || gap <= 0 {
			continue
		}
		if !sleepContext(ctx, pacer.controlGap(packetType, i, gap)) {
			return
		}
	}
}

func (s *runtimeSession) sendNominationBurst(ctx context.Context, writeConn net.PacketConn, remoteAddr net.Addr, epoch uint32) bool {
	if s == nil || writeConn == nil || remoteAddr == nil {
		return false
	}
	if err := s.writeUDPToConnWithEpoch(writeConn, packetTypePunch, remoteAddr, epoch); err != nil {
		return false
	}
	if !sleepContext(ctx, s.ensurePacer().controlGap(packetTypePunch, int(epoch), controlBurstGap)) {
		return false
	}
	return s.writeUDPToConnWithEpoch(writeConn, packetTypeEnd, remoteAddr, epoch) == nil
}

func (s *runtimeSession) sendAcceptBurst(ctx context.Context, writeConn net.PacketConn, remoteAddr net.Addr, epoch uint32) bool {
	if s == nil || writeConn == nil || remoteAddr == nil {
		return false
	}
	if err := s.writeUDPToConnWithEpoch(writeConn, packetTypeSucc, remoteAddr, epoch); err != nil {
		return false
	}
	if !sleepContext(ctx, s.ensurePacer().controlGap(packetTypeSucc, int(epoch), controlBurstGap)) {
		return false
	}
	return s.writeUDPToConnWithEpoch(writeConn, packetTypeAccept, remoteAddr, epoch) == nil
}

func (s *runtimeSession) sendSuccessBurst(ctx context.Context, writeConn net.PacketConn, remoteAddr net.Addr) bool {
	if s == nil || writeConn == nil || remoteAddr == nil {
		return false
	}
	s.sendControlBurst(ctx, writeConn, packetTypeSucc, remoteAddr, successBurstCount, controlBurstGap, 0)
	return true
}

func effectiveNominationEpoch(packet *UDPPacket) uint32 {
	if packet == nil {
		return 0
	}
	if packet.NominationEpoch != 0 {
		return packet.NominationEpoch
	}
	switch packet.Type {
	case packetTypeEnd, packetTypeAccept, packetTypeReady:
		return 1
	default:
		return 0
	}
}

func nominationLoopKey(candidateID string, epoch uint32) string {
	return fmt.Sprintf("%s|%d", candidateID, epoch)
}

func (s *runtimeSession) nextOutboundNominationEpoch() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextNominationEpoch++
	if s.nextNominationEpoch == 0 {
		s.nextNominationEpoch = 1
	}
	return s.nextNominationEpoch
}

func (s *runtimeSession) setOutboundNomination(localAddr, remoteAddr string, epoch uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outboundNomination = outboundNominationState{
		key:       candidateKey(localAddr, remoteAddr),
		epoch:     epoch,
		startedAt: time.Now(),
	}
}

func (s *runtimeSession) clearOutboundNomination(localAddr, remoteAddr string, epoch uint32) {
	key := candidateKey(localAddr, remoteAddr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outboundNomination.key != key || s.outboundNomination.epoch != epoch {
		return
	}
	s.outboundNomination = outboundNominationState{}
}

func (s *runtimeSession) matchesOutboundNomination(localAddr, remoteAddr string, epoch uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch == 0 {
		return false
	}
	return s.outboundNomination.key == candidateKey(localAddr, remoteAddr) && s.outboundNomination.epoch == epoch
}

func (s *runtimeSession) shouldRenominate(localAddr, remoteAddr string, epoch uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outboundNomination.key != candidateKey(localAddr, remoteAddr) || s.outboundNomination.epoch != epoch {
		return false
	}
	if s.outboundNomination.startedAt.IsZero() {
		return false
	}
	return time.Since(s.outboundNomination.startedAt) >= s.plan.renominationTimeout()
}

func (s *runtimeSession) scheduleNominationRetry(ctx context.Context, delay time.Duration) {
	go func() {
		if !sleepContext(ctx, s.nominationDelayValue(delay)) {
			return
		}
		s.nominateBestCandidate(ctx)
	}()
}

func (s *runtimeSession) tryRenominate(ctx context.Context, localAddr, remoteAddr string, epoch uint32) {
	if !s.matchesOutboundNomination(localAddr, remoteAddr, epoch) {
		return
	}
	_ = s.cm.BackoffNomination(localAddr, remoteAddr, s.plan.renominationCooldown())
	s.clearOutboundNomination(localAddr, remoteAddr, epoch)
	_ = s.sendProgress("candidate_renominate", "ok", remoteAddr, localAddr, remoteAddr, map[string]string{
		"nomination_epoch": fmt.Sprintf("%d", epoch),
	})
	s.nominateBestCandidate(ctx)
	if !s.cm.HasNominated() {
		s.scheduleNominationRetry(ctx, s.plan.renominationCooldown())
	}
}

func (s *runtimeSession) adoptInboundProposal(localAddr, remoteAddr string, epoch uint32) (*CandidatePair, bool) {
	if epoch == 0 {
		return nil, false
	}
	key := candidateKey(localAddr, remoteAddr)
	s.mu.Lock()
	current := s.inboundProposal
	switch {
	case current.epoch > epoch:
		s.mu.Unlock()
		return nil, false
	case current.epoch == epoch && current.key != "" && current.key != key:
		s.mu.Unlock()
		return nil, false
	default:
		s.inboundProposal = inboundProposalState{key: key, epoch: epoch}
		s.mu.Unlock()
	}
	return s.cm.AdoptNomination(localAddr, remoteAddr)
}

func (s *runtimeSession) matchesInboundProposal(localAddr, remoteAddr string, epoch uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch == 0 {
		return false
	}
	return s.inboundProposal.key == candidateKey(localAddr, remoteAddr) && s.inboundProposal.epoch == epoch
}

func (s *runtimeSession) nominationDelay() time.Duration {
	return s.nominationDelayValue(s.plan.nominationDelay())
}

func (s *runtimeSession) nominationDelayValue(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if pacer := s.ensurePacer(); pacer != nil {
		return pacer.nominationDelay(base)
	}
	return base
}

func (s *runtimeSession) nominationRetryDelay(epoch uint32, attempt int) time.Duration {
	base := s.plan.nominationRetryInterval()
	if pacer := s.ensurePacer(); pacer != nil {
		return pacer.nominationRetryDelay(epoch, attempt, base)
	}
	return base
}
