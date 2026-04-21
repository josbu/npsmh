package routers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func loadRecoveredNodeConfig(t *testing.T, content string) {
	t.Helper()
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	t.Cleanup(func() {
		common.ConfPath = oldConfPath
	})
	configPath := writeTestConfig(t, "nps.conf", content)
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func swapRecoveredNodeStore(t *testing.T, next file.Store) {
	t.Helper()
	oldStore := file.GlobalStore
	file.GlobalStore = next
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})
}

func recoveredNodeConfigEpoch(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Data struct {
			ConfigEpoch string `json:"config_epoch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(config epoch) error = %v body=%s", err, string(body))
	}
	return strings.TrimSpace(payload.Data.ConfigEpoch)
}

func recoveredNodeImportSnapshot(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(file.ConfigSnapshot{
		Users: []*file.User{{
			Id:        21,
			Username:  "import-user",
			Password:  "secret",
			Status:    1,
			TotalFlow: &file.Flow{},
		}},
		Clients: []*file.Client{{
			Id:        22,
			UserId:    21,
			VerifyKey: "import-client",
			Status:    true,
			Cnf:       &file.Config{},
			Flow:      &file.Flow{},
		}},
		Global: &file.Glob{},
	})
	if err != nil {
		t.Fatalf("json.Marshal(import snapshot) error = %v", err)
	}
	return body
}
