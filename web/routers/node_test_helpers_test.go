package routers

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/gorilla/websocket"
)

type fakeNodeStore struct {
	clientsByID   map[int]*file.Client
	clientsByVKey map[string]*file.Client
	flushCalled   bool
	flushCalls    int
	flushErr      error
	syncCalled    bool
	syncCalls     int
	syncErr       error
}

func (s *fakeNodeStore) GetUser(int) (*file.User, error) { return nil, errors.New("not implemented") }
func (s *fakeNodeStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}
func (s *fakeNodeStore) CreateUser(*file.User) error { return errors.New("not implemented") }
func (s *fakeNodeStore) UpdateUser(*file.User) error { return errors.New("not implemented") }
func (s *fakeNodeStore) GetClient(vkey string) (*file.Client, error) {
	if client, ok := s.clientsByVKey[vkey]; ok {
		return client, nil
	}
	return nil, errors.New("not found")
}
func (s *fakeNodeStore) GetClientByID(id int) (*file.Client, error) {
	if client, ok := s.clientsByID[id]; ok {
		return client, nil
	}
	return nil, errors.New("not found")
}
func (s *fakeNodeStore) UpdateClient(client *file.Client) error {
	if client == nil {
		return errors.New("not found")
	}
	if s.clientsByID == nil {
		s.clientsByID = make(map[int]*file.Client)
	}
	if existing, ok := s.clientsByID[client.Id]; ok && existing != nil {
		if s.clientsByVKey != nil {
			delete(s.clientsByVKey, strings.TrimSpace(existing.VerifyKey))
		}
		copyFakeNodeClient(existing, client)
		client = existing
	} else {
		s.clientsByID[client.Id] = client
	}
	if strings.TrimSpace(client.VerifyKey) != "" {
		if s.clientsByVKey == nil {
			s.clientsByVKey = make(map[string]*file.Client)
		}
		s.clientsByVKey[client.VerifyKey] = client
	}
	return nil
}
func (s *fakeNodeStore) GetAllClients() []*file.Client         { return nil }
func (s *fakeNodeStore) GetClientsByUserId(int) []*file.Client { return nil }
func (s *fakeNodeStore) GetTunnelsByUserId(int) int            { return 0 }
func (s *fakeNodeStore) GetHostsByUserId(int) int              { return 0 }
func (s *fakeNodeStore) Flush() error {
	s.flushCalled = true
	s.flushCalls++
	return s.flushErr
}
func (s *fakeNodeStore) ExportConfigSnapshot() (*file.ConfigSnapshot, error) {
	return &file.ConfigSnapshot{}, nil
}
func (s *fakeNodeStore) SyncNow() error {
	s.syncCalled = true
	s.syncCalls++
	return s.syncErr
}

func copyFakeNodeClient(dst, src *file.Client) {
	if dst == nil || src == nil {
		return
	}
	dst.Cnf = src.Cnf
	dst.Id = src.Id
	dst.UserId = src.UserId
	dst.OwnerUserID = src.OwnerUserID
	dst.ManagerUserIDs = append(dst.ManagerUserIDs[:0], src.ManagerUserIDs...)
	dst.SourceType = src.SourceType
	dst.SourcePlatformID = src.SourcePlatformID
	dst.SourceActorID = src.SourceActorID
	dst.Revision = src.Revision
	dst.UpdatedAt = src.UpdatedAt
	dst.VerifyKey = src.VerifyKey
	dst.Mode = src.Mode
	dst.Addr = src.Addr
	dst.LocalAddr = src.LocalAddr
	dst.Remark = src.Remark
	dst.Status = src.Status
	dst.IsConnect = src.IsConnect
	dst.ExpireAt = src.ExpireAt
	dst.FlowLimit = src.FlowLimit
	dst.RateLimit = src.RateLimit
	dst.Flow = src.Flow
	dst.ExportFlow = src.ExportFlow
	dst.InletFlow = src.InletFlow
	dst.Rate = src.Rate
	dst.BridgeTraffic = src.BridgeTraffic
	dst.ServiceTraffic = src.ServiceTraffic
	dst.BridgeMeter = src.BridgeMeter
	dst.ServiceMeter = src.ServiceMeter
	dst.TotalMeter = src.TotalMeter
	dst.NoStore = src.NoStore
	dst.NoDisplay = src.NoDisplay
	dst.MaxConn = src.MaxConn
	dst.NowConn = src.NowConn
	dst.ConfigConnAllow = src.ConfigConnAllow
	dst.MaxTunnelNum = src.MaxTunnelNum
	dst.Version = src.Version
	dst.SetLegacyBlackIPImport(src.LegacyBlackIPImport())
	dst.EntryAclMode = src.EntryAclMode
	dst.EntryAclRules = src.EntryAclRules
	dst.CreateTime = src.CreateTime
	dst.LastOnlineTime = src.LastOnlineTime
}

func readNodeWSFrameUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, match func(nodeWSFrame) bool) nodeWSFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("SetReadDeadline() error = %v", err)
		}
		var frame nodeWSFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON() error = %v", err)
		}
		if frame.Type == "error" {
			t.Fatalf("unexpected node ws error frame: id=%q status=%d error=%q body=%s", frame.ID, frame.Status, frame.Error, string(frame.Body))
		}
		if match(frame) {
			return frame
		}
	}
}
