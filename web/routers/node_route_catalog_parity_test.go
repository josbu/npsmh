package routers

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestNodeHTTPRouteCatalogParity(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	handler := Init()
	engine, ok := handler.(*gin.Engine)
	if !ok || engine == nil {
		t.Fatalf("Init() handler type = %T, want *gin.Engine", handler)
	}

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		if route.Method != http.MethodGet && route.Method != http.MethodPost {
			continue
		}
		if !strings.HasPrefix(route.Path, "/api/") {
			continue
		}
		routes[route.Method+" "+normalizeNodeRouteTemplate(route.Path)] = struct{}{}
	}

	expected := make(map[string]struct{})
	for _, spec := range append(
		webapi.SessionActionCatalog(nil),
		webapi.ProtectedActionCatalog(nil)...,
	) {
		if spec.Method != http.MethodGet && spec.Method != http.MethodPost {
			continue
		}
		if !strings.HasPrefix(spec.Path, "/api/") {
			continue
		}
		expected[spec.Method+" "+normalizeNodeRouteTemplate(spec.Path)] = struct{}{}
	}
	expected[http.MethodGet+" /api/ws"] = struct{}{}
	expected[http.MethodPost+" /api/batch"] = struct{}{}
	expected[http.MethodGet+" /api/system/discovery"] = struct{}{}

	extra := diffStringSet(routes, expected)
	missing := diffStringSet(expected, routes)
	if len(extra) != 0 || len(missing) != 0 {
		t.Fatalf("route/catalog mismatch\nextra routes: %v\nmissing routes: %v", extra, missing)
	}
}

func normalizeNodeRouteTemplate(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") && len(part) > 1 {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
		}
	}
	normalized := strings.Join(parts, "/")
	if !strings.HasPrefix(normalized, "/") {
		return "/" + normalized
	}
	return normalized
}

func diffStringSet(left map[string]struct{}, right map[string]struct{}) []string {
	result := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; ok {
			continue
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
