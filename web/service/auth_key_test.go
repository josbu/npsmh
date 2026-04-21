package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
)

func TestValidAuthKey(t *testing.T) {
	now := time.Now().Unix()
	timestamp := int(now)
	configKey := "test-config-key"
	valid := crypt.Md5(configKey + strconv.Itoa(timestamp))

	if !ValidAuthKey(configKey, valid, timestamp, now) {
		t.Fatal("ValidAuthKey() = false, want true for current matching token")
	}
}

func TestValidAuthKeyRejectsInvalidInput(t *testing.T) {
	now := time.Now().Unix()
	timestamp := int(now)
	configKey := "test-config-key"
	valid := crypt.Md5(configKey + strconv.Itoa(timestamp))

	tests := []struct {
		name      string
		configKey string
		md5Key    string
		timestamp int
		nowUnix   int64
	}{
		{name: "empty token", configKey: configKey, md5Key: "", timestamp: timestamp, nowUnix: now},
		{name: "expired timestamp", configKey: configKey, md5Key: valid, timestamp: timestamp, nowUnix: now + 21},
		{name: "length mismatch", configKey: configKey, md5Key: valid[:10], timestamp: timestamp, nowUnix: now},
		{name: "wrong key", configKey: configKey, md5Key: crypt.Md5("wrong" + strconv.Itoa(timestamp)), timestamp: timestamp, nowUnix: now},
		{name: "empty config key", configKey: "", md5Key: valid, timestamp: timestamp, nowUnix: now},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ValidAuthKey(tt.configKey, tt.md5Key, tt.timestamp, tt.nowUnix) {
				t.Fatalf("ValidAuthKey(%q, %q, %d, %d) = true, want false", tt.configKey, tt.md5Key, tt.timestamp, tt.nowUnix)
			}
		})
	}
}
