package common

import (
	"testing"
	"time"
)

func TestSetNtpIntervalNormalizesNonPositiveDurations(t *testing.T) {
	timeMutex.Lock()
	previous := syncInterval
	timeMutex.Unlock()
	t.Cleanup(func() {
		timeMutex.Lock()
		syncInterval = previous
		timeMutex.Unlock()
	})

	SetNtpInterval(2 * time.Minute)
	timeMutex.RLock()
	if syncInterval != 2*time.Minute {
		t.Fatalf("syncInterval after positive update = %v, want 2m", syncInterval)
	}
	timeMutex.RUnlock()

	SetNtpInterval(0)
	timeMutex.RLock()
	if syncInterval != defaultNTPInterval {
		t.Fatalf("syncInterval after zero update = %v, want %v", syncInterval, defaultNTPInterval)
	}
	timeMutex.RUnlock()

	SetNtpInterval(-time.Minute)
	timeMutex.RLock()
	if syncInterval != defaultNTPInterval {
		t.Fatalf("syncInterval after negative update = %v, want %v", syncInterval, defaultNTPInterval)
	}
	timeMutex.RUnlock()
}
