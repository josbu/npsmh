package routers

import "testing"

func TestNormalizeFormalManagementAPIPathDoesNotDependOnGlobalConfig(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"/api/system/discovery":             "/api/system/discovery",
		"/ops/platform/admin/api/clients":   "/api/clients",
		"/tenant/root/alpha/api/ws":         "/api/ws",
		"/tenant/root/alpha/apiary/status":  "/tenant/root/alpha/apiary/status",
		"/tenant/root/alpha/not-api/status": "/tenant/root/alpha/not-api/status",
	}

	for input, want := range cases {
		if got := normalizeFormalManagementAPIPath(input); got != want {
			t.Fatalf("normalizeFormalManagementAPIPath(%q) = %q, want %q", input, got, want)
		}
	}
}
