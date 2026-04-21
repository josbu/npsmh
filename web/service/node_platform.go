package service

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type ManagementPlatformStore interface {
	EnsureServiceUser(servercfg.ManagementPlatformConfig) (*file.User, error)
	ServiceUsername(string, string) string
	OwnedClientIDs(int) ([]int, error)
}

type DefaultManagementPlatformStore struct {
	Repo    ManagementPlatformRepository
	Backend Backend
}

type managementPlatformServiceUserLookupRepository interface {
	SupportsGetUserByExternalPlatformID() bool
	GetUserByExternalPlatformID(string) (*file.User, error)
}

type ManagementPlatformReverseRuntimeStatus struct {
	ConnectMode                 string
	ReverseWSURL                string
	ReverseEnabled              bool
	CallbackURL                 string
	CallbackEnabled             bool
	CallbackTimeoutSeconds      int
	CallbackRetryMax            int
	CallbackRetryBackoffSec     int
	CallbackQueueMax            int
	CallbackQueueSize           int
	CallbackDropped             int64
	ReverseConnected            bool
	LastConnectedAt             int64
	LastDisconnectedAt          int64
	LastHelloAt                 int64
	LastPingAt                  int64
	LastPongAt                  int64
	LastEventAt                 int64
	LastCallbackAt              int64
	LastCallbackSuccessAt       int64
	LastCallbackQueuedAt        int64
	LastCallbackReplayAt        int64
	LastCallbackStatusCode      int
	CallbackDeliveries          int64
	CallbackFailures            int64
	CallbackConsecutiveFailures int64
	LastReverseError            string
	LastReverseErrorAt          int64
	LastCallbackError           string
	LastCallbackErrorAt         int64
}

type ManagementPlatformRuntimeStatusStore interface {
	Reset()
	Status(string) ManagementPlatformReverseRuntimeStatus
	NoteConfigured(string, string, string, bool)
	NoteCallbackConfigured(string, string, bool, int, int, int, int, int)
	NoteReverseConnected(string)
	NoteReverseDisconnected(string, error)
	NoteReverseHello(string)
	NoteReversePing(string)
	NoteReversePong(string)
	NoteReverseEvent(string)
	NoteCallbackDelivered(string, int)
	NoteCallbackQueued(string, int, bool)
	NoteCallbackReplayDelivered(string, int, int)
	NoteCallbackQueueSize(string, int)
	NoteCallbackFailed(string, error, int)
}

type InMemoryManagementPlatformRuntimeStatusStore struct {
	mu     sync.RWMutex
	byPlat map[string]ManagementPlatformReverseRuntimeStatus
}

func (s DefaultManagementPlatformStore) EnsureServiceUser(platform servercfg.ManagementPlatformConfig) (*file.User, error) {
	if user, err := s.lookupServiceUser(platform.PlatformID); err != nil {
		return nil, err
	} else if user != nil {
		return user, nil
	}
	serviceUsername := strings.TrimSpace(platform.ServiceUsername)
	if serviceUsername == "" {
		serviceUsername = servercfg.DefaultPlatformServiceUsername(platform.PlatformID)
	}
	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{
			PlatformID:      platform.PlatformID,
			ServiceUsername: serviceUsername,
			Enabled:         platform.Enabled,
		},
	})
	return s.lookupServiceUser(platform.PlatformID)
}

func (s DefaultManagementPlatformStore) ServiceUsername(platformID, configured string) string {
	if user, err := s.lookupServiceUser(platformID); err == nil && user != nil {
		if username := strings.TrimSpace(user.Username); username != "" {
			return username
		}
	}
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	return servercfg.DefaultPlatformServiceUsername(platformID)
}

func (s DefaultManagementPlatformStore) OwnedClientIDs(serviceUserID int) ([]int, error) {
	if serviceUserID <= 0 {
		return nil, nil
	}
	clientIDs := make([]int, 0)
	if err := rangeOwnedClientIDsByUser(s.repo(), serviceUserID, func(clientID int) bool {
		clientIDs = append(clientIDs, clientID)
		return true
	}); err != nil {
		return nil, err
	}
	sort.Ints(clientIDs)
	return clientIDs, nil
}

func (s DefaultManagementPlatformStore) lookupServiceUser(platformID string) (*file.User, error) {
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return nil, nil
	}
	repo := s.repo()
	if indexedRepo, ok := repo.(managementPlatformServiceUserLookupRepository); ok && indexedRepo.SupportsGetUserByExternalPlatformID() {
		user, err := indexedRepo.GetUserByExternalPlatformID(platformID)
		switch {
		case err == nil && user != nil:
			if user.Kind == "platform_service" && strings.TrimSpace(user.ExternalPlatformID) == platformID {
				return user, nil
			}
		case errors.Is(err, file.ErrUserNotFound):
			return nil, nil
		case err != nil:
			return nil, err
		}
	}
	var resolved *file.User
	repo.RangeUsers(func(user *file.User) bool {
		if user != nil && user.Kind == "platform_service" && strings.TrimSpace(user.ExternalPlatformID) == platformID {
			resolved = user
			return false
		}
		return true
	})
	return resolved, nil
}

func (s DefaultManagementPlatformStore) repo() ManagementPlatformRepository {
	if !isNilServiceValue(s.Repo) {
		return s.Repo
	}
	if !isNilServiceValue(s.Backend.Repository) {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}

func NewInMemoryManagementPlatformRuntimeStatusStore() *InMemoryManagementPlatformRuntimeStatusStore {
	return &InMemoryManagementPlatformRuntimeStatusStore{
		byPlat: make(map[string]ManagementPlatformReverseRuntimeStatus),
	}
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPlat = make(map[string]ManagementPlatformReverseRuntimeStatus)
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) Status(platformID string) ManagementPlatformReverseRuntimeStatus {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return ManagementPlatformReverseRuntimeStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byPlat[platformID]
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteConfigured(platformID, connectMode, reverseWSURL string, reverseEnabled bool) {
	s.update(platformID, func(status *ManagementPlatformReverseRuntimeStatus) {
		status.ConnectMode = strings.TrimSpace(connectMode)
		status.ReverseWSURL = strings.TrimSpace(reverseWSURL)
		status.ReverseEnabled = reverseEnabled
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackConfigured(platformID, callbackURL string, callbackEnabled bool, callbackTimeoutSeconds int, callbackRetryMax int, callbackRetryBackoffSec int, callbackQueueMax int, callbackQueueSize int) {
	s.update(platformID, func(status *ManagementPlatformReverseRuntimeStatus) {
		status.CallbackURL = strings.TrimSpace(callbackURL)
		status.CallbackEnabled = callbackEnabled
		if callbackTimeoutSeconds > 0 {
			status.CallbackTimeoutSeconds = callbackTimeoutSeconds
		}
		if callbackRetryMax >= 0 {
			status.CallbackRetryMax = callbackRetryMax
		}
		if callbackRetryBackoffSec > 0 {
			status.CallbackRetryBackoffSec = callbackRetryBackoffSec
		}
		if callbackQueueMax >= 0 {
			status.CallbackQueueMax = callbackQueueMax
		}
		if callbackQueueSize >= 0 {
			status.CallbackQueueSize = callbackQueueSize
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReverseConnected(platformID string) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.ReverseConnected = true
		status.LastConnectedAt = now
		status.LastReverseError = ""
		status.LastReverseErrorAt = 0
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReverseDisconnected(platformID string, err error) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.ReverseConnected = false
		status.LastDisconnectedAt = now
		if err != nil {
			status.LastReverseError = err.Error()
			status.LastReverseErrorAt = now
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReverseHello(platformID string) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastHelloAt = now
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReversePing(platformID string) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastPingAt = now
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReversePong(platformID string) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastPongAt = now
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteReverseEvent(platformID string) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastEventAt = now
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackDelivered(platformID string, statusCode int) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastCallbackAt = now
		status.LastCallbackSuccessAt = now
		status.LastCallbackStatusCode = statusCode
		status.CallbackDeliveries++
		status.CallbackConsecutiveFailures = 0
		status.LastCallbackError = ""
		status.LastCallbackErrorAt = 0
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackQueued(platformID string, queueSize int, dropped bool) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastCallbackQueuedAt = now
		if queueSize >= 0 {
			status.CallbackQueueSize = queueSize
		}
		if dropped {
			status.CallbackDropped++
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackReplayDelivered(platformID string, statusCode int, queueSize int) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastCallbackAt = now
		status.LastCallbackSuccessAt = now
		status.LastCallbackReplayAt = now
		status.LastCallbackStatusCode = statusCode
		status.CallbackDeliveries++
		status.CallbackConsecutiveFailures = 0
		status.LastCallbackError = ""
		status.LastCallbackErrorAt = 0
		if queueSize >= 0 {
			status.CallbackQueueSize = queueSize
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackQueueSize(platformID string, queueSize int) {
	s.update(platformID, func(status *ManagementPlatformReverseRuntimeStatus) {
		if queueSize >= 0 {
			status.CallbackQueueSize = queueSize
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) NoteCallbackFailed(platformID string, err error, statusCode int) {
	s.updateAt(platformID, func(status *ManagementPlatformReverseRuntimeStatus, now int64) {
		status.LastCallbackAt = now
		status.LastCallbackStatusCode = statusCode
		status.CallbackFailures++
		status.CallbackConsecutiveFailures++
		if err != nil {
			status.LastCallbackError = err.Error()
			status.LastCallbackErrorAt = now
		}
	})
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) update(platformID string, update func(*ManagementPlatformReverseRuntimeStatus)) {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" || update == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.byPlat[platformID]
	update(&status)
	s.byPlat[platformID] = status
}

func (s *InMemoryManagementPlatformRuntimeStatusStore) updateAt(platformID string, update func(*ManagementPlatformReverseRuntimeStatus, int64)) {
	if update == nil {
		return
	}
	s.update(platformID, func(status *ManagementPlatformReverseRuntimeStatus) {
		update(status, time.Now().Unix())
	})
}
