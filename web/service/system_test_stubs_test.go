package service

import "github.com/djylb/nps/lib/servercfg"

type stubSystemService struct {
	info                 SystemInfo
	display              BridgeDisplay
	bridgeDisplay        func(*servercfg.Snapshot, string) BridgeDisplay
	registerManagementIP func(string)
}

func (s stubSystemService) Info() SystemInfo {
	return s.info
}

func (s stubSystemService) BridgeDisplay(cfg *servercfg.Snapshot, host string) BridgeDisplay {
	if s.bridgeDisplay != nil {
		return s.bridgeDisplay(cfg, host)
	}
	return s.display
}

func (s stubSystemService) RegisterManagementAccess(remoteAddr string) {
	if s.registerManagementIP != nil {
		s.registerManagementIP(remoteAddr)
	}
}
