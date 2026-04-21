package mux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

func (s *Mux) sendInfo(flag uint8, id int32, priority bool, data interface{}) error {
	if s.IsClosed() {
		return s.closedErr("send mux frame failed: the mux has closed")
	}

	s.applyWriteBackpressure(flag, priority)

	if s.IsClosed() {
		return s.closedErr("send mux frame failed: the mux has closed")
	}

	pack := muxPack.Get()
	pack.priority = priority
	if err := pack.Set(flag, id, data); err != nil {
		muxPack.Put(pack)
		_ = s.closeWithReason(fmt.Sprintf("build mux frame failed: %v", err))
		return fmt.Errorf("send mux frame failed: %w", err)
	}
	if s.IsClosed() {
		if pack.content != nil {
			windowBuff.Put(pack.content)
		}
		muxPack.Put(pack)
		return s.closedErr("send mux frame failed: the mux has closed")
	}
	if s.writeQueue.Push(pack) {
		return nil
	}
	if pack.content != nil {
		windowBuff.Put(pack.content)
	}
	muxPack.Put(pack)
	if s.IsClosed() {
		return s.closedErr("send mux frame failed: the mux has closed")
	}
	return errors.New("send mux frame failed: write queue stopped")
}

func (s *Mux) applyWriteBackpressure(flag uint8, priority bool) {
	if priority {
		return
	}
	if !isMuxStreamDataFlag(flag) {
		return
	}

	if s.writeQueue.Len() < s.writeQueue.HighWater() {
		return
	}

	q := &s.writeQueue
	q.cond.L.Lock()
	defer q.cond.L.Unlock()

	for !s.IsClosed() && atomic.LoadUint32(&q.stop) == 0 && q.Len() >= q.LowWater() {
		q.cond.Wait()
	}
}

func (s *Mux) writeSession() {
	fw := NewFlushWriterWithTimeout(s.conn, s.config.WriteTimeout)
	go func() {
		defer func() {
			_ = fw.Flush()
			_ = fw.Close()
		}()
		for {
			if s.IsClosed() {
				return
			}
			pack := s.writeQueue.TryPop()
			if pack == nil {
				_ = fw.Flush()
				pack = s.writeQueue.Pop()
			}
			if pack == nil {
				break
			}

			flag := pack.flag

			//if pack.flag == muxNewMsg || pack.flag == muxNewMsgPart {
			//	if pack.length >= 100 {
			//		logs.Println("write session id", pack.id, "\n", string(pack.content[:100]))
			//	} else {
			//		logs.Println("write session id", pack.id, "\n", string(pack.content[:pack.length]))
			//	}
			//}

			err := pack.Pack(fw)
			muxPack.Put(pack)
			if err != nil {
				reason := fmt.Sprintf("write session pack failed: %s", describeNetError(err, s.conn))
				_ = s.closeWithReason(reason)
				break
			}

			if isMuxImmediateFlushFlag(flag) {
				_ = fw.Flush()
			}
		}
	}()
}

func (s *Mux) ping() {
	go func() {
		buf := make([]byte, 8+s.config.PingMaxPad)
		timer := time.NewTimer(s.nextPingDelay())
		defer timer.Stop()
		for {
			select {
			case <-s.closeChan:
				return
			case <-timer.C:
				now := time.Now()
				timeout := s.effectivePingTimeout()
				if s.isAliveTimeout(now) {
					reason := fmt.Sprintf("ping timeout last_alive=%s timeout=%s local=%v remote=%v",
						time.Unix(0, atomic.LoadInt64(&s.lastAliveTime)).Format(time.RFC3339Nano),
						timeout,
						s.conn.LocalAddr(),
						s.conn.RemoteAddr(),
					)
					_ = s.closeWithReason(reason)
					return
				}
				if !s.shouldSendPing(now) {
					timer.Reset(s.nextPingDelay())
					continue
				}

				s.sendInfo(muxPingFlag, muxPing, false, s.preparePingPayload(buf, now))

				timer.Reset(s.nextPingDelay())
			}
		}
	}()

	go func() {
		for {
			select {
			case pack := <-s.pingCh:
				data, _ := pack.GetContent()
				if len(data) >= 8 {
					sent := int64(binary.BigEndian.Uint64(data[:8]))
					rtt := time.Now().UnixNano() - sent
					if rtt > 0 {
						s.observeLatency(time.Duration(rtt))
					}
				}
				windowBuff.Put(pack.content)
				muxPack.Put(pack)
			case <-s.closeChan:
				for {
					select {
					case pack := <-s.pingCh:
						windowBuff.Put(pack.content)
						muxPack.Put(pack)
					default:
						return
					}
				}
			}
		}
	}()
}

func (s *Mux) shouldSendPing(now time.Time) bool {
	if s == nil {
		return false
	}
	if s.config.DisableTrafficAwarePing {
		return true
	}
	last := atomic.LoadInt64(&s.lastAliveTime)
	if last <= 0 {
		return true
	}
	threshold := pingIdleThreshold(s.config)
	if threshold <= 0 {
		return true
	}
	return now.Sub(time.Unix(0, last)) >= threshold
}

func (s *Mux) readSession() {
	go func() {
		var pack *muxPackager
		var l uint16
		var err error
		for {
			if s.IsClosed() {
				return
			}
			pack = muxPack.Get()
			if s.config.ReadTimeout > 0 {
				_ = s.conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
			}
			s.bw.StartRead()
			if l, err = pack.UnPack(s.conn); err != nil {
				if s.IsClosed() {
					muxPack.Put(pack)
					return
				}
				reason := fmt.Sprintf("read session unpack failed: %s", describeNetError(err, s.conn))
				_ = s.closeWithReason(reason)
				muxPack.Put(pack)
				return
			}
			s.markAlive()
			s.bw.SetCopySize(l)
			//if pack.flag == muxNewMsg || pack.flag == muxNewMsgPart {
			//	if pack.length >= 100 {
			//		logs.Printf("read session id %d pointer %p\n%v", pack.id, pack.content, string(pack.content[:100]))
			//	} else {
			//		logs.Printf("read session id %d pointer %p\n%v", pack.id, pack.content, string(pack.content[:pack.length]))
			//	}
			//}
			switch pack.flag {
			case muxNewConn: //New connection
				connection := NewConn(pack.id, s)
				if s.enqueueAcceptedConn(connection) {
					s.sendInfo(muxNewConnOk, connection.connId, false, nil)
				} else {
					connection.closeLocal()
					s.sendInfo(muxNewConnFail, pack.id, false, nil)
				}
				muxPack.Put(pack)
				continue
			case muxPingFlag: //ping
				buf := s.preparePingReply(pack.content[:pack.length])
				s.sendInfo(muxPingReturn, muxPing, false, buf)
				windowBuff.Put(pack.content)
				muxPack.Put(pack)
				continue
			case muxPingReturn:
				select {
				case <-s.closeChan:
					windowBuff.Put(pack.content)
					muxPack.Put(pack)
				case s.pingCh <- pack:
				}
				continue
			case muxPeerHello:
				s.setRemoteCapabilities(uint32(pack.id))
				muxPack.Put(pack)
				continue
			default:
			}

			if connection, ok := s.connMap.Get(pack.id); ok && !connection.IsClosed() {
				switch pack.flag {
				case muxNewMsg, muxNewMsgPart: //New msg from remote connection
					err = s.newMsg(connection, pack)
					if err != nil {
						_ = connection.Close()
					}
					muxPack.Put(pack)
					continue
				case muxNewConnOk: //connection ok
					select {
					case connection.connStatusOkCh <- struct{}{}:
					default:
					}
					muxPack.Put(pack)
					continue
				case muxNewConnFail:
					select {
					case connection.connStatusFailCh <- struct{}{}:
					default:
					}
					muxPack.Put(pack)
					continue
				case muxMsgSendOk:
					if connection.IsClosed() {
						muxPack.Put(pack)
						continue
					}
					connection.sendWindow.SetSize(pack.window)
					muxPack.Put(pack)
					continue
				case muxConnCloseWrite:
					connection.markRemoteWriteClosed()
					connection.receiveWindow.Stop()
					muxPack.Put(pack)
					continue
				case muxConnClose: //close the connection
					connection.markRemoteWriteClosed()
					connection.SetClosingFlag()
					connection.receiveWindow.Stop() // close signal to receive window
					muxPack.Put(pack)
					continue
				default:
				}
			} else if pack.flag == muxConnClose || pack.flag == muxConnCloseWrite {
				muxPack.Put(pack)
				continue
			}
			muxPack.Put(pack)
		}
	}()
}

func isZero(buf []byte) bool {
	for _, b := range buf {
		if b != 0 {
			return false
		}
	}
	return true
}

func (s *Mux) enqueueAcceptedConn(connection *Conn) bool {
	if s.IsClosed() {
		return false
	}
	select {
	case <-s.closeChan:
		return false
	case s.newConnCh <- connection:
		s.connMap.Set(connection.connId, connection)
		return true
	default:
		return false
	}
}

func (s *Mux) preparePingPayload(buf []byte, now time.Time) []byte {
	binary.BigEndian.PutUint64(buf[:8], uint64(now.UnixNano()))
	pad := 0
	if s.config.PingMaxPad > 0 {
		pad = randIntn(s.config.PingMaxPad + 1)
	}
	if pad == 0 {
		return buf[:8]
	}
	s.fillPingPad(buf[8 : 8+pad])
	return buf[:8+pad]
}

func (s *Mux) preparePingReply(buf []byte) []byte {
	if len(buf) <= 8 {
		return buf[:minInt(len(buf), 8)]
	}
	if isZero(buf[8:]) {
		pad := 0
		if s.config.PingMaxPad > 0 {
			pad = randIntn(s.config.PingMaxPad + 1)
		}
		reply := buf[:8+pad]
		if pad > 0 {
			s.fillPingPad(reply[8:])
		}
		return reply
	}
	return buf
}

func (s *Mux) fillPingPad(buf []byte) {
	if len(buf) == 0 {
		return
	}
	if s.config.DisablePingPadRandom {
		for i := range buf {
			buf[i] = 0
		}
		return
	}
	fillRandomBytes(buf)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Mux) newMsg(connection *Conn, pack *muxPackager) (err error) {
	if connection.IsClosed() {
		err = io.ErrClosedPipe
		return
	}
	//insert into queue
	if pack.flag == muxNewMsgPart {
		err = connection.receiveWindow.Write(pack.content, pack.length, true, pack.id)
	}
	if pack.flag == muxNewMsg {
		err = connection.receiveWindow.Write(pack.content, pack.length, false, pack.id)
	}
	return
}
