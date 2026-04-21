package config

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	viperini "github.com/go-viper/encoding/ini"
	"github.com/spf13/viper"
)

type CommonConfig struct {
	Server           string
	VKey             string
	Tp               string //bridgeType kcp or tcp
	AutoReconnection bool
	TlsEnable        bool
	ProxyUrl         string
	LocalIP          string
	DnsServer        string
	NtpServer        string
	NtpInterval      int
	Client           *file.Client
	DisconnectTime   int
}

type LocalServer struct {
	Type       string
	Port       int
	Ip         string
	Password   string
	Target     string
	TargetType string
	Fallback   bool
	LocalProxy bool
}

type Config struct {
	content      string
	title        []string
	CommonConfig *CommonConfig
	Hosts        []*file.Host
	Tasks        []*file.Tunnel
	Healths      []*file.Health
	LocalServer  []*LocalServer
}

func NewConfig(path string) (c *Config, err error) {
	var b []byte
	if b, err = common.ReadAllFromFile(path); err != nil {
		return
	}
	return NewConfigFromContent(string(b))
}

func NewConfigFromContent(raw string) (c *Config, err error) {
	c = new(Config)
	if c.content, err = common.ParseStr(raw); err != nil {
		return nil, err
	}
	return parseConfigContent(c)
}

func parseConfigContent(c *Config) (*Config, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	title, err := getAllTitle(c.content)
	if err != nil {
		return nil, err
	}
	c.title = title
	sections, err := parseConfigSections(c.content)
	if err != nil {
		return nil, err
	}
	var nowContent string
	for i := 0; i < len(c.title); i++ {
		sectionName := normalizeConfigSectionName(getTitleContent(c.title[i]))
		section := sections[sectionName]
		nowContent = renderConfigSection(section)
		if strings.HasPrefix(sectionName, "secret") && !sectionHasKey(section, "mode") {
			local := delLocalService(nowContent)
			local.Type = "secret"
			c.LocalServer = append(c.LocalServer, local)
			continue
		}
		if strings.HasPrefix(sectionName, "p2p") && !sectionHasKey(section, "mode") {
			local := delLocalService(nowContent)
			if local.Type == "" {
				local.Type = "p2p"
			}
			c.LocalServer = append(c.LocalServer, local)
			continue
		}
		//health set
		if strings.HasPrefix(sectionName, "health") {
			c.Healths = append(c.Healths, dealHealth(nowContent))
			continue
		}
		switch sectionName {
		case "common":
			c.CommonConfig = dealCommon(nowContent)
		default:
			if sectionHasKey(section, "host") {
				h, err := dealHost(nowContent)
				if err != nil {
					return nil, err
				}
				h.Remark = getTitleContent(c.title[i])
				c.Hosts = append(c.Hosts, h)
				continue
			}
			t, err := dealTunnel(nowContent)
			if err != nil {
				return nil, err
			}
			t.Remark = getTitleContent(c.title[i])
			c.Tasks = append(c.Tasks, t)
		}
	}
	return c, nil
}

func parseConfigSections(raw string) (map[string]map[string]string, error) {
	registry := viper.NewCodecRegistry()
	if err := registry.RegisterCodec("ini", viperini.Codec{KeyDelimiter: "::"}); err != nil {
		return nil, err
	}
	cfg := viper.NewWithOptions(
		viper.KeyDelimiter("::"),
		viper.WithCodecRegistry(registry),
	)
	cfg.SetConfigType("ini")
	if err := cfg.ReadConfig(bytes.NewBufferString(raw)); err != nil {
		return nil, err
	}
	settings := cfg.AllSettings()
	sections := make(map[string]map[string]string, len(settings))
	for sectionName, value := range settings {
		items, ok := configSectionValues(value)
		if !ok {
			continue
		}
		sections[normalizeConfigSectionName(sectionName)] = items
	}
	return sections, nil
}

func configSectionValues(value any) (map[string]string, bool) {
	switch section := value.(type) {
	case map[string]any:
		items := make(map[string]string, len(section))
		for key, item := range section {
			items[strings.TrimSpace(key)] = fmt.Sprint(item)
		}
		return items, true
	case map[any]any:
		items := make(map[string]string, len(section))
		for key, item := range section {
			items[strings.TrimSpace(fmt.Sprint(key))] = fmt.Sprint(item)
		}
		return items, true
	default:
		return nil, false
	}
}

func normalizeConfigSectionName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func sectionHasKey(section map[string]string, name string) bool {
	if len(section) == 0 {
		return false
	}
	name = normalizeConfigKey(name)
	for key := range section {
		if normalizeConfigKey(key) == name {
			return true
		}
	}
	return false
}

func renderConfigSection(section map[string]string) string {
	if len(section) == 0 {
		return ""
	}
	keys := make([]string, 0, len(section))
	for key := range section {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+section[key])
	}
	return strings.Join(lines, "\n")
}

var bracketRE = regexp.MustCompile(`[\[\]]`)

func getTitleContent(s string) string {
	return bracketRE.ReplaceAllString(s, "")
}

var commentLineRE = regexp.MustCompile(`(?m)^[ \t]*#.*(\r?\n|$)`)

func stripCommentLines(s string) string {
	return commentLineRE.ReplaceAllString(s, "")
}

func normalizeConfigKey(key string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(strings.ToLower(key)), func(r rune) bool {
		return r == '_' || r == '.' || r == '-' || unicode.IsSpace(r)
	})
	return strings.Join(parts, "_")
}

func splitConfigPrefixedField(rawKey, prefix string) (string, bool) {
	rawKey = strings.TrimSpace(rawKey)
	lower := strings.ToLower(rawKey)
	for _, sep := range []string{"_", "-", "."} {
		marker := prefix + sep
		if strings.HasPrefix(lower, marker) && len(rawKey) > len(marker) {
			return strings.TrimSpace(rawKey[len(marker):]), true
		}
	}
	return "", false
}

func dealCommon(s string) *CommonConfig {
	c := new(CommonConfig)
	c.Tp = "tcp"
	c.AutoReconnection = true
	c.Client = file.NewClient("", true, true)
	c.Client.Cnf = new(file.Config)
	for _, v := range splitStr(s) {
		key, value, ok := splitConfigLine(v)
		if !ok {
			continue
		}
		switch normalizeConfigKey(key) {
		case "server_addr":
			c.Server = value
		case "vkey":
			c.VKey = value
		case "conn_type":
			c.Tp = value
		case "auto_reconnection":
			c.AutoReconnection = common.GetBoolByStr(value)
		case "basic_username":
			c.Client.Cnf.U = value
		case "basic_password":
			c.Client.Cnf.P = value
		case "web_password":
			username, _, totpSecret := c.Client.LegacyWebLoginImport()
			c.Client.SetLegacyWebLoginImport(username, value, totpSecret)
		case "web_username":
			_, password, totpSecret := c.Client.LegacyWebLoginImport()
			c.Client.SetLegacyWebLoginImport(value, password, totpSecret)
		case "compress":
			c.Client.Cnf.Compress = common.GetBoolByStr(value)
		case "crypt":
			c.Client.Cnf.Crypt = common.GetBoolByStr(value)
		case "proxy_url":
			c.ProxyUrl = value
		case "local_ip":
			c.LocalIP = value
		case "dns_server":
			c.DnsServer = value
		case "ntp_server":
			c.NtpServer = value
		case "ntp_interval":
			c.NtpInterval = common.GetIntNoErrByStr(value)
		case "rate_limit":
			c.Client.RateLimit = common.GetIntNoErrByStr(value)
		case "flow_limit":
			c.Client.Flow.FlowLimit = int64(common.GetIntNoErrByStr(value))
		case "time_limit":
			c.Client.Flow.TimeLimit = common.GetTimeNoErrByStr(value)
		case "max_conn":
			c.Client.MaxConn = common.GetIntNoErrByStr(value)
		case "remark":
			c.Client.Remark = value
		case "pprof_addr":
			common.InitPProfByAddr(value)
		case "disconnect_timeout":
			c.DisconnectTime = common.GetIntNoErrByStr(value)
		case "tls_enable":
			c.TlsEnable = common.GetBoolByStr(value)
		}
	}
	return c
}

func dealHost(s string) (*file.Host, error) {
	h := new(file.Host)
	h.Target = new(file.Target)
	h.Scheme = "all"
	h.MultiAccount = new(file.MultiAccount)
	var headerChange, respHeaderChange string
	for _, v := range splitStr(s) {
		rawKey, value, ok := splitConfigLine(v)
		if !ok {
			continue
		}
		switch normalizeConfigKey(rawKey) {
		case "host":
			h.Host = value
		case "target_addr":
			h.Target.TargetStr = strings.ReplaceAll(value, ",", "\n")
		case "proxy_protocol":
			h.Target.ProxyProtocol = common.GetIntNoErrByStr(value)
		case "host_change":
			h.HostChange = value
		case "scheme":
			h.Scheme = value
		case "location":
			h.Location = value
		case "path_rewrite":
			h.PathRewrite = value
		case "cert_file":
			h.CertFile, _ = common.GetCertContent(value, "CERTIFICATE")
		case "key_file":
			h.KeyFile, _ = common.GetCertContent(value, "PRIVATE")
		case "https_just_proxy":
			h.HttpsJustProxy = common.GetBoolByStr(value)
		case "auto_ssl":
			h.AutoSSL = common.GetBoolByStr(value)
		case "auto_https":
			h.AutoHttps = common.GetBoolByStr(value)
		case "auto_cors":
			h.AutoCORS = common.GetBoolByStr(value)
		case "compat_mode":
			h.CompatMode = common.GetBoolByStr(value)
		case "redirect_url":
			h.RedirectURL = value
		case "target_is_https":
			h.TargetIsHttps = common.GetBoolByStr(value)
		case "local_proxy":
			h.Target.LocalProxy = common.GetBoolByStr(value)
		case "multi_account":
			multiAccount, err := loadMultiAccount(value)
			if err != nil {
				return nil, err
			}
			h.MultiAccount = multiAccount
		default:
			if name, ok := splitConfigPrefixedField(rawKey, "header"); ok {
				headerChange += name + ":" + value + "\n"
			}
			if name, ok := splitConfigPrefixedField(rawKey, "response"); ok {
				respHeaderChange += name + ":" + value + "\n"
			}
			h.HeaderChange = headerChange
			h.RespHeaderChange = respHeaderChange
		}
	}
	return h, nil
}

func dealHealth(s string) *file.Health {
	h := &file.Health{}
	for _, v := range splitStr(s) {
		key, value, ok := splitConfigLine(v)
		if !ok {
			continue
		}
		switch normalizeConfigKey(key) {
		case "health_check_timeout":
			h.HealthCheckTimeout = common.GetIntNoErrByStr(value)
		case "health_check_max_failed":
			h.HealthMaxFail = common.GetIntNoErrByStr(value)
		case "health_check_interval":
			h.HealthCheckInterval = common.GetIntNoErrByStr(value)
		case "health_http_url":
			h.HttpHealthUrl = value
		case "health_check_type":
			h.HealthCheckType = value
		case "health_check_target":
			h.HealthCheckTarget = value
		}
	}
	return h
}

func dealTunnel(s string) (*file.Tunnel, error) {
	t := new(file.Tunnel)
	t.Target = new(file.Target)
	t.MultiAccount = new(file.MultiAccount)
	for _, v := range splitStr(s) {
		key, value, ok := splitConfigLine(v)
		if !ok {
			continue
		}
		switch normalizeConfigKey(key) {
		case "server_port":
			t.Ports = value
		case "server_ip":
			t.ServerIp = value
		case "mode":
			t.Mode = value
		case "target_addr":
			t.Target.TargetStr = strings.ReplaceAll(value, ",", "\n")
		case "proxy_protocol":
			t.Target.ProxyProtocol = common.GetIntNoErrByStr(value)
		case "target_port":
			t.Target.TargetStr = value
		case "target_ip":
			t.TargetAddr = value
		case "password":
			t.Password = value
		case "socks5_proxy":
			t.Socks5Proxy = common.GetBoolByStr(value)
		case "http_proxy":
			t.HttpProxy = common.GetBoolByStr(value)
		case "dest_acl_mode":
			t.DestAclMode = common.GetIntNoErrByStr(value)
		case "dest_acl_rules":
			t.DestAclRules = strings.ReplaceAll(value, ",", "\n")
		case "local_path":
			t.LocalPath = value
		case "strip_pre":
			t.StripPre = value
		case "read_only":
			t.ReadOnly = common.GetBoolByStr(value)
		case "local_proxy":
			t.Target.LocalProxy = common.GetBoolByStr(value)
		case "multi_account":
			multiAccount, err := loadMultiAccount(value)
			if err != nil {
				return nil, err
			}
			t.MultiAccount = multiAccount
		}
	}
	return t, nil

}

func dealMultiUser(s string) map[string]string {
	multiUserMap := make(map[string]string)
	for _, line := range splitStr(s) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		var key, val string
		if idx >= 0 {
			key = strings.TrimSpace(line[:idx])
			val = strings.TrimSpace(line[idx+1:])
		} else {
			key = line
			val = ""
		}
		if key != "" {
			multiUserMap[key] = val
		}
	}
	return multiUserMap
}

func loadMultiAccount(path string) (*file.MultiAccount, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return new(file.MultiAccount), nil
	}

	content, err := common.ReadAllFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("read multi_account file %q: %w", path, err)
	}
	parsed, err := common.ParseStr(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse multi_account file %q: %w", path, err)
	}

	return &file.MultiAccount{
		Content:    parsed,
		AccountMap: dealMultiUser(parsed),
	}, nil
}

func delLocalService(s string) *LocalServer {
	l := new(LocalServer)
	for _, v := range splitStr(s) {
		key, value, ok := splitConfigLine(v)
		if !ok {
			continue
		}
		switch normalizeConfigKey(key) {
		case "local_port":
			l.Port = common.GetIntNoErrByStr(value)
		case "local_type":
			l.Type = value
		case "local_ip":
			l.Ip = value
		case "password":
			l.Password = value
		case "target_addr":
			l.Target = value
		case "target_type":
			l.TargetType = value
		case "local_proxy":
			l.LocalProxy = common.GetBoolByStr(value)
		case "fallback_secret":
			l.Fallback = common.GetBoolByStr(value)
		}
	}
	return l
}

func getAllTitle(content string) (arr []string, err error) {
	var re *regexp.Regexp
	re, err = regexp.Compile(`(?m)^\[[^\r\n\[\]]+]$`)
	if err != nil {
		return
	}
	arr = re.FindAllString(content, -1)
	m := make(map[string]bool)
	for _, v := range arr {
		key := normalizeConfigSectionName(getTitleContent(v))
		if _, ok := m[key]; ok {
			err = fmt.Errorf("item names %s are not allowed to be duplicated", v)
			return
		}
		m[key] = true
	}
	return
}

func splitConfigLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	parts := strings.SplitN(line, "=", 2)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

func splitStr(s string) (configDataArr []string) {
	if common.IsWindows() {
		configDataArr = strings.Split(s, "\r\n")
	}
	if len(configDataArr) < 3 {
		configDataArr = strings.Split(s, "\n")
	}
	return
}
