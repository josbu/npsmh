package policy

import (
	"os"
	"testing"
)

func TestRealGeoDataFiles(t *testing.T) {
	geoIPPath := os.Getenv("NPS_REAL_GEOIP_PATH")
	geoSitePath := os.Getenv("NPS_REAL_GEOSITE_PATH")
	if geoIPPath == "" || geoSitePath == "" {
		t.Skip("set NPS_REAL_GEOIP_PATH and NPS_REAL_GEOSITE_PATH to run real geodata verification")
	}

	ipPolicy := CompileSourceIPPolicy(ModeBlacklist, []string{"geoip:cn"}, Options{GeoIPPath: geoIPPath})
	if ipPolicy.AllowsAddr("223.5.5.5:53") {
		t.Fatal("expected geoip:cn to match 223.5.5.5 from real geoip.dat")
	}
	if !ipPolicy.AllowsAddr("8.8.8.8:53") {
		t.Fatal("expected geoip:cn not to match 8.8.8.8 from real geoip.dat")
	}

	sitePolicy := CompileDestinationPolicy(ModeWhitelist, "geosite:cn", Options{GeoSitePath: geoSitePath})
	if !sitePolicy.AllowsAddr("qq.com:443") {
		t.Fatal("expected geosite:cn to match qq.com from real geosite.dat")
	}
	if sitePolicy.AllowsAddr("google.com:443") {
		t.Fatal("expected geosite:cn not to match google.com from real geosite.dat")
	}
}
