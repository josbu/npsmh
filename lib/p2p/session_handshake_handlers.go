package p2p

import (
	"context"
	"fmt"
	"net"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

func (s *runtimeSession) handleProbePacket(ctx context.Context, readConn net.PacketConn, addr net.Addr, localAddr, remoteAddr string) {
	s.cm.Observe(localAddr, remoteAddr)
	s.updateCandidateRemote(remoteAddr)
	s.sendSuccessBurst(ctx, readConn, addr)
}

func (s *runtimeSession) handleSuccessPacket(ctx context.Context, readConn net.PacketConn, addr net.Addr, localAddr, remoteAddr string) {
	logs.Info("[P2P] SUCCESS received local=%s remote=%s", localAddr, remoteAddr)
	s.cm.Observe(localAddr, remoteAddr)
	s.markCandidateHit()
	priority := s.successCandidatePriority(localAddr, remoteAddr)
	pair := s.cm.MarkSucceededWithPriority(localAddr, remoteAddr, priority)
	s.updateCandidateRemote(remoteAddr)
	if pair != nil {
		logs.Info("[P2P] candidate hit local=%s remote=%s score=%d reason=%s successes=%d", localAddr, remoteAddr, pair.Score, pair.ScoreReason, pair.SuccessCount)
	} else {
		logs.Info("[P2P] candidate hit local=%s remote=%s", localAddr, remoteAddr)
	}
	_ = s.sendProgress("success_received", "ok", remoteAddr, localAddr, remoteAddr, candidateMeta(pair))
	if s.start.Role == common.WORK_P2P_VISITOR {
		s.scheduleBestNomination(ctx)
		return
	}
	s.sendSuccessBurst(ctx, readConn, addr)
}

func (s *runtimeSession) handleProposalPacket(ctx context.Context, readConn net.PacketConn, addr net.Addr, localAddr, remoteAddr string, epoch uint32) {
	logs.Info("[P2P] END received local=%s remote=%s", localAddr, remoteAddr)
	s.cm.Observe(localAddr, remoteAddr)
	if s.start.Role != common.WORK_P2P_PROVIDER || epoch == 0 {
		return
	}
	if pair, adopted := s.adoptInboundProposal(localAddr, remoteAddr, epoch); adopted || (pair != nil && pair.Nominated) {
		s.markProposalAdopted()
		logs.Info("[P2P] candidate proposed local=%s remote=%s epoch=%d", localAddr, remoteAddr, epoch)
		s.sendAcceptBurst(ctx, readConn, addr, epoch)
		_ = s.sendProgress("candidate_proposed", "ok", remoteAddr, localAddr, remoteAddr, nominationProgressMeta(epoch, nil))
		s.startAcceptLoop(ctx, readConn, addr, epoch)
	}
}

func (s *runtimeSession) handleAcceptPacket(ctx context.Context, readConn net.PacketConn, addr net.Addr, localAddr, remoteAddr string, epoch uint32) {
	logs.Info("[P2P] ACCEPT received local=%s remote=%s", localAddr, remoteAddr)
	s.cm.Observe(localAddr, remoteAddr)
	if s.start.Role != common.WORK_P2P_VISITOR {
		return
	}
	if !s.matchesOutboundNomination(localAddr, remoteAddr, epoch) {
		return
	}
	s.markAcceptReceived()
	if confirmed := s.cm.Confirm(localAddr, remoteAddr); confirmed != nil {
		logs.Info("[P2P] candidate confirmed local=%s remote=%s", localAddr, remoteAddr)
		s.updateConfirmedRemote(remoteAddr)
		s.clearOutboundNomination(localAddr, remoteAddr, epoch)
		s.sendControlBurst(ctx, readConn, packetTypeReady, addr, readyBurstCount, readyBurstGap, epoch)
		_ = s.sendProgress("accept_received", "ok", remoteAddr, localAddr, remoteAddr, nominationProgressMeta(epoch, nil))
		s.signalConfirmed(readConn, localAddr, remoteAddr)
	}
}

func (s *runtimeSession) handleReadyPacket(readConn net.PacketConn, localAddr, remoteAddr string, epoch uint32) {
	logs.Info("[P2P] READY received local=%s remote=%s", localAddr, remoteAddr)
	s.cm.Observe(localAddr, remoteAddr)
	if s.start.Role != common.WORK_P2P_PROVIDER {
		return
	}
	if !s.matchesInboundProposal(localAddr, remoteAddr, epoch) {
		return
	}
	s.markReadyReceived()
	if confirmed := s.cm.Confirm(localAddr, remoteAddr); confirmed != nil {
		logs.Info("[P2P] candidate confirmed local=%s remote=%s epoch=%d", localAddr, remoteAddr, epoch)
		s.updateConfirmedRemote(remoteAddr)
		_ = s.sendProgress("ready_received", "ok", remoteAddr, localAddr, remoteAddr, nominationProgressMeta(epoch, nil))
		s.signalConfirmed(readConn, localAddr, remoteAddr)
	}
}

func nominationProgressMeta(epoch uint32, base map[string]string) map[string]string {
	meta := cloneStringMap(base)
	if meta == nil {
		meta = make(map[string]string, 1)
	}
	if epoch != 0 {
		meta["nomination_epoch"] = fmt.Sprintf("%d", epoch)
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
