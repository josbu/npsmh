package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

const (
	controlBurstGap   = 10 * time.Millisecond
	readyBurstGap     = 15 * time.Millisecond
	successBurstCount = 2
	readyBurstCount   = 4
)

func (s *runtimeSession) readLoopOnConn(ctx context.Context, readConn net.PacketConn) {
	if s == nil || !packetConnUsable(readConn) {
		return
	}
	s.addSocket(readConn)
	restoreDeadline := interruptPacketReadOnContext(ctx, readConn)
	if restoreDeadline != nil {
		defer restoreDeadline()
	}
	buf := common.BufPoolUdp.Get()
	defer common.BufPoolUdp.Put(buf)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = readConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := readConn.ReadFrom(buf)
		_ = readConn.SetReadDeadline(time.Time{})
		if err != nil {
			if conn.IsTimeout(err) || isIgnorableUDPIcmpError(err) {
				continue
			}
			return
		}
		packet, err := DecodeUDPPacket(buf[:n], s.start.Token)
		if err != nil {
			if errors.Is(err, ErrP2PTokenMismatch) {
				s.recordTokenMismatch()
			}
			continue
		}
		if packet.SessionID != s.start.SessionID || packet.Token != s.start.Token {
			s.recordTokenMismatch()
			continue
		}
		if routeID := p2pStartWireSpec(s.start).RouteID; routeID != "" && !SameWireRoute(packet.WireID, routeID) {
			s.recordTokenMismatch()
			continue
		}
		if !s.acceptReplay(packet) {
			continue
		}
		s.recordTokenVerified()
		remoteAddr := addr.String()
		localAddr := addrString(packetConnLocalAddr(readConn))
		s.dispatchValidatedPacket(ctx, readConn, addr, localAddr, remoteAddr, packet)
	}
}

func (s *runtimeSession) writeUDPToConn(writeConn net.PacketConn, packetType string, addr net.Addr) error {
	return s.writeUDPToConnWithEpoch(writeConn, packetType, addr, 0)
}

func (s *runtimeSession) writeUDPToConnWithEpoch(writeConn net.PacketConn, packetType string, addr net.Addr, epoch uint32) error {
	if !packetConnUsable(writeConn) {
		return net.ErrClosed
	}
	packet := newUDPPacketWithWire(s.start.SessionID, s.start.Token, s.start.Role, packetType, p2pStartWireSpec(s.start))
	packet.NominationEpoch = epoch
	raw, err := EncodeUDPPacket(packet)
	if err != nil {
		return err
	}
	_, err = writeConn.WriteTo(raw, addr)
	return err
}

func (s *runtimeSession) updateCandidateRemote(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endpoints.ConfirmedRemote != "" && s.endpoints.ConfirmedRemote != remote {
		return
	}
	if s.endpoints.CandidateRemote != "" && s.endpoints.CandidateRemote != remote {
		logs.Info("[P2P] remote corrected old=%s new=%s", s.endpoints.CandidateRemote, remote)
	}
	s.endpoints.CandidateRemote = remote
}

func (s *runtimeSession) updateConfirmedRemote(remote string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endpoints.ConfirmedRemote != "" && s.endpoints.ConfirmedRemote != remote {
		return
	}
	s.endpoints.CandidateRemote = remote
	s.endpoints.ConfirmedRemote = remote
}

func (s *runtimeSession) signalConfirmed(readConn net.PacketConn, localAddr, remote string) {
	if s == nil {
		return
	}
	if readConn != nil && !packetConnUsable(readConn) {
		return
	}
	s.markCandidateConfirmed()
	s.cancelRetryLoops()
	select {
	case s.confirmed <- confirmedPair{owner: s, conn: readConn, localAddr: localAddr, remoteAddr: remote}:
	default:
	}
}

func trySendProgress(c *conn.Conn, sessionID, role, stage, detail string) error {
	return trySendProgressWithStatus(c, sessionID, role, stage, "ok", detail, "", "", nil, nil)
}

func trySendProgressWithStatus(c *conn.Conn, sessionID, role, stage, status, detail, localAddr, remoteAddr string, meta map[string]string, counters map[string]int) error {
	if c == nil {
		return nil
	}
	return WritePunchProgress(c, P2PPunchProgress{
		SessionID:  sessionID,
		Role:       role,
		Stage:      stage,
		Status:     status,
		Detail:     detail,
		LocalAddr:  localAddr,
		RemoteAddr: remoteAddr,
		Meta:       meta,
		Counters:   counters,
	})
}

func (s *runtimeSession) sendProgress(stage, status, detail, localAddr, remoteAddr string, meta map[string]string) error {
	if s == nil {
		return nil
	}
	return trySendProgressWithStatus(s.control, s.start.SessionID, s.start.Role, stage, status, detail, localAddr, remoteAddr, s.enrichProgressMeta(localAddr, remoteAddr, meta), s.progressCounters())
}

func trySendAbort(c *conn.Conn, sessionID, role, reason string) error {
	if c == nil {
		return nil
	}
	return WriteBridgeMessage(c, common.P2P_PUNCH_ABORT, P2PPunchAbort{
		SessionID: sessionID,
		Role:      role,
		Reason:    reason,
	})
}

func (s *runtimeSession) socketForLocalAddr(localAddr string) net.PacketConn {
	for _, socket := range s.snapshotSockets() {
		if !packetConnUsable(socket) || addrString(packetConnLocalAddr(socket)) != localAddr {
			continue
		}
		return socket
	}
	return nil
}

func (s *runtimeSession) successCandidatePriority(localAddr, remoteAddr string) CandidatePriority {
	if s == nil {
		return CandidatePriority{}
	}
	ranker := s.ensureRanker()
	priority := ranker.ResponsivePriority(remoteAddr)
	if priority.Score > 0 {
		return priority
	}
	return ranker.Priority(localAddr, remoteAddr)
}

func candidateMeta(pair *CandidatePair) map[string]string {
	if pair == nil {
		return nil
	}
	meta := map[string]string{
		"candidate_score":   fmt.Sprintf("%d", pair.Score),
		"candidate_reason":  pair.ScoreReason,
		"candidate_success": fmt.Sprintf("%d", pair.SuccessCount),
	}
	if pair.SucceededAt.IsZero() {
		delete(meta, "candidate_success")
	}
	if meta["candidate_reason"] == "" {
		delete(meta, "candidate_reason")
	}
	if meta["candidate_score"] == "0" {
		delete(meta, "candidate_score")
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

func (s *runtimeSession) enrichProgressMeta(localAddr, remoteAddr string, meta map[string]string) map[string]string {
	if s == nil {
		return meta
	}
	out := cloneStringMap(meta)
	if out == nil {
		out = make(map[string]string)
	}
	if _, ok := out["family"]; !ok {
		if family := progressFamily(localAddr, remoteAddr, s.localConn); family != "" {
			out["family"] = family
		}
	}
	if _, ok := out["candidate_remote"]; !ok {
		if candidateRemote := s.cm.CandidateRemote(); candidateRemote != "" {
			out["candidate_remote"] = candidateRemote
		}
	}
	if _, ok := out["confirmed_remote"]; !ok {
		if confirmedRemote := s.cm.ConfirmedRemote(); confirmedRemote != "" {
			out["confirmed_remote"] = confirmedRemote
		}
	}
	if _, ok := out["rendezvous_remote"]; !ok && s.endpoints.RendezvousRemote != "" {
		out["rendezvous_remote"] = s.endpoints.RendezvousRemote
	}
	phase, event := s.phaseMeta()
	if _, ok := out["session_phase"]; !ok && phase != "" {
		out["session_phase"] = phase
	}
	if _, ok := out["session_event"]; !ok && event != "" {
		out["session_event"] = event
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func progressFamily(localAddr, remoteAddr string, localConn net.PacketConn) string {
	switch {
	case detectAddressFamily(localAddr) != udpFamilyAny:
		return detectAddressFamily(localAddr).String()
	case detectAddressFamily(remoteAddr) != udpFamilyAny:
		return detectAddressFamily(remoteAddr).String()
	case packetConnUsable(localConn):
		return detectAddrFamily(packetConnLocalAddr(localConn)).String()
	default:
		return ""
	}
}

func (s *runtimeSession) recordTokenMismatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.tokenMismatchDropped++
}

func (s *runtimeSession) recordReplayDrop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.replayDropped++
}

func (s *runtimeSession) recordTokenVerified() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stats.tokenVerified {
		return
	}
	s.stats.tokenVerified = true
	logs.Info("[P2P] token verified role=%s session=%s", s.start.Role, s.start.SessionID)
}

func (s *runtimeSession) logTokenStats() {
	stats := s.snapshotStats()
	if stats.tokenMismatchDropped > 0 {
		logs.Info("[P2P] token mismatch silently dropped role=%s count=%d", s.start.Role, stats.tokenMismatchDropped)
	}
	if stats.replayDropped > 0 {
		logs.Info("[P2P] replay silently dropped role=%s count=%d", s.start.Role, stats.replayDropped)
	}
	if stats.sessionEventDropped > 0 {
		logs.Info("[P2P] session event silently dropped role=%s count=%d last=%s", s.start.Role, stats.sessionEventDropped, stats.lastDroppedEvent)
	}
}

func (s *runtimeSession) progressCounters() map[string]int {
	if s == nil {
		return nil
	}
	var counts map[string]int
	if s.cm != nil {
		counts = s.cm.StateCounts()
	}
	stats := s.snapshotStats()
	if counts == nil {
		counts = make(map[string]int)
	}
	counts["token_mismatch_dropped"] = stats.tokenMismatchDropped
	counts["replay_dropped"] = stats.replayDropped
	counts["session_event_dropped"] = stats.sessionEventDropped
	if stats.tokenVerified {
		counts["token_verified"] = 1
	}
	return counts
}

func (s *runtimeSession) acceptReplay(packet *UDPPacket) bool {
	if packet == nil {
		return false
	}
	if s.replay == nil {
		return true
	}
	if s.replay.Accept(packet.Timestamp, packet.Nonce) {
		return true
	}
	s.recordReplayDrop()
	return false
}
