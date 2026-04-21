package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/file"
	"gopkg.in/yaml.v3"
)

const maxLaunchResolveDepth = 6

const maxLaunchConfigSourceDepth = 4

type launchStringList []string

type LaunchMultiAccount struct {
	Path     string
	Accounts map[string]string
}

type LaunchRuntime struct {
	Debug             *bool   `json:"debug,omitempty"`
	Log               *string `json:"log,omitempty"`
	LogLevel          *string `json:"log_level,omitempty"`
	LogPath           *string `json:"log_path,omitempty"`
	LogMaxSize        *int    `json:"log_max_size,omitempty"`
	LogMaxDays        *int    `json:"log_max_days,omitempty"`
	LogMaxFiles       *int    `json:"log_max_files,omitempty"`
	LogCompress       *bool   `json:"log_compress,omitempty"`
	LogColor          *bool   `json:"log_color,omitempty"`
	PProf             *string `json:"pprof,omitempty"`
	ProtoVersion      *int    `json:"proto_version,omitempty"`
	SkipVerify        *bool   `json:"skip_verify,omitempty"`
	KeepAlive         *int    `json:"keepalive,omitempty"`
	DNSServer         *string `json:"dns_server,omitempty"`
	NTPServer         *string `json:"ntp_server,omitempty"`
	NTPInterval       *int    `json:"ntp_interval,omitempty"`
	Timezone          *string `json:"timezone,omitempty"`
	DisableP2P        *bool   `json:"disable_p2p,omitempty"`
	P2PType           *string `json:"p2p_type,omitempty"`
	LocalIPForward    *bool   `json:"local_ip_forward,omitempty"`
	AutoReconnect     *bool   `json:"auto_reconnect,omitempty"`
	DisconnectTimeout *int    `json:"disconnect_timeout,omitempty"`
	P2PTimeout        *int    `json:"p2p_timeout,omitempty"`
}

type LaunchDirect struct {
	Server  launchStringList `json:"server,omitempty"`
	VKey    launchStringList `json:"vkey,omitempty"`
	Type    launchStringList `json:"type,omitempty"`
	Proxy   string           `json:"proxy,omitempty"`
	LocalIP launchStringList `json:"local_ip,omitempty"`
}

type LaunchLocal struct {
	Server         string `json:"server,omitempty"`
	VKey           string `json:"vkey,omitempty"`
	Type           string `json:"type,omitempty"`
	Proxy          string `json:"proxy,omitempty"`
	LocalIP        string `json:"local_ip,omitempty"`
	LocalType      string `json:"local_type,omitempty"`
	LocalPort      *int   `json:"local_port,omitempty"`
	Password       string `json:"password,omitempty"`
	Target         string `json:"target,omitempty"`
	TargetAddr     string `json:"target_addr,omitempty"`
	TargetType     string `json:"target_type,omitempty"`
	FallbackSecret *bool  `json:"fallback_secret,omitempty"`
	LocalProxy     *bool  `json:"local_proxy,omitempty"`
}

type LaunchCommon struct {
	ServerAddr        string           `json:"server_addr,omitempty"`
	Server            string           `json:"server,omitempty"`
	VKey              string           `json:"vkey,omitempty"`
	ConnType          string           `json:"conn_type,omitempty"`
	Type              string           `json:"type,omitempty"`
	AutoReconnection  *bool            `json:"auto_reconnection,omitempty"`
	TLSEnable         *bool            `json:"tls_enable,omitempty"`
	ProxyURL          string           `json:"proxy_url,omitempty"`
	Proxy             string           `json:"proxy,omitempty"`
	LocalIP           string           `json:"local_ip,omitempty"`
	DNSServer         string           `json:"dns_server,omitempty"`
	NTPServer         string           `json:"ntp_server,omitempty"`
	NTPInterval       *int             `json:"ntp_interval,omitempty"`
	BasicUsername     string           `json:"basic_username,omitempty"`
	BasicPassword     string           `json:"basic_password,omitempty"`
	WebUsername       string           `json:"web_username,omitempty"`
	WebPassword       string           `json:"web_password,omitempty"`
	Compress          *bool            `json:"compress,omitempty"`
	Crypt             *bool            `json:"crypt,omitempty"`
	RateLimit         *int             `json:"rate_limit,omitempty"`
	FlowLimit         *int             `json:"flow_limit,omitempty"`
	TimeLimit         string           `json:"time_limit,omitempty"`
	MaxConn           *int             `json:"max_conn,omitempty"`
	Remark            string           `json:"remark,omitempty"`
	EntryACLMode      *int             `json:"entry_acl_mode,omitempty"`
	EntryACLRules     launchStringList `json:"entry_acl_rules,omitempty"`
	PProfAddr         string           `json:"pprof_addr,omitempty"`
	DisconnectTimeout *int             `json:"disconnect_timeout,omitempty"`
}

type LaunchHealth struct {
	Timeout   *int             `json:"health_check_timeout,omitempty"`
	MaxFailed *int             `json:"health_check_max_failed,omitempty"`
	Interval  *int             `json:"health_check_interval,omitempty"`
	HTTPURL   string           `json:"health_http_url,omitempty"`
	Type      string           `json:"health_check_type,omitempty"`
	Target    launchStringList `json:"health_check_target,omitempty"`
}

type LaunchHost struct {
	Remark           string              `json:"remark,omitempty"`
	Host             string              `json:"host,omitempty"`
	TargetAddr       launchStringList    `json:"target_addr,omitempty"`
	ProxyProtocol    *int                `json:"proxy_protocol,omitempty"`
	HostChange       string              `json:"host_change,omitempty"`
	HeaderChange     string              `json:"header_change,omitempty"`
	Headers          map[string]string   `json:"headers,omitempty"`
	RequestHeaders   map[string]string   `json:"request_headers,omitempty"`
	RespHeaderChange string              `json:"response_header_change,omitempty"`
	ResponseHeaders  map[string]string   `json:"response_headers,omitempty"`
	Scheme           string              `json:"scheme,omitempty"`
	Location         string              `json:"location,omitempty"`
	PathRewrite      string              `json:"path_rewrite,omitempty"`
	CertFile         string              `json:"cert_file,omitempty"`
	KeyFile          string              `json:"key_file,omitempty"`
	HTTPSJustProxy   *bool               `json:"https_just_proxy,omitempty"`
	TLSOffload       *bool               `json:"tls_offload,omitempty"`
	AutoSSL          *bool               `json:"auto_ssl,omitempty"`
	AutoHTTPS        *bool               `json:"auto_https,omitempty"`
	AutoCORS         *bool               `json:"auto_cors,omitempty"`
	CompatMode       *bool               `json:"compat_mode,omitempty"`
	RedirectURL      string              `json:"redirect_url,omitempty"`
	TargetIsHTTPS    *bool               `json:"target_is_https,omitempty"`
	LocalProxy       *bool               `json:"local_proxy,omitempty"`
	EntryACLMode     *int                `json:"entry_acl_mode,omitempty"`
	EntryACLRules    launchStringList    `json:"entry_acl_rules,omitempty"`
	UserAuth         *LaunchMultiAccount `json:"user_auth,omitempty"`
	MultiAccount     *LaunchMultiAccount `json:"multi_account,omitempty"`
}

type LaunchTask struct {
	Remark        string              `json:"remark,omitempty"`
	ServerPort    string              `json:"server_port,omitempty"`
	ServerIP      string              `json:"server_ip,omitempty"`
	Mode          string              `json:"mode,omitempty"`
	TargetAddr    launchStringList    `json:"target_addr,omitempty"`
	ProxyProtocol *int                `json:"proxy_protocol,omitempty"`
	TargetPort    string              `json:"target_port,omitempty"`
	TargetIP      string              `json:"target_ip,omitempty"`
	TargetType    string              `json:"target_type,omitempty"`
	Password      string              `json:"password,omitempty"`
	Socks5Proxy   *bool               `json:"socks5_proxy,omitempty"`
	HTTPProxy     *bool               `json:"http_proxy,omitempty"`
	LocalProxy    *bool               `json:"local_proxy,omitempty"`
	EntryACLMode  *int                `json:"entry_acl_mode,omitempty"`
	EntryACLRules launchStringList    `json:"entry_acl_rules,omitempty"`
	DestACLMode   *int                `json:"dest_acl_mode,omitempty"`
	DestACLRules  launchStringList    `json:"dest_acl_rules,omitempty"`
	LocalPath     string              `json:"local_path,omitempty"`
	StripPre      string              `json:"strip_pre,omitempty"`
	ReadOnly      *bool               `json:"read_only,omitempty"`
	UserAuth      *LaunchMultiAccount `json:"user_auth,omitempty"`
	MultiAccount  *LaunchMultiAccount `json:"multi_account,omitempty"`
}

type LaunchLocalServer struct {
	LocalPort      *int   `json:"local_port,omitempty"`
	LocalType      string `json:"local_type,omitempty"`
	LocalIP        string `json:"local_ip,omitempty"`
	Password       string `json:"password,omitempty"`
	TargetAddr     string `json:"target_addr,omitempty"`
	TargetType     string `json:"target_type,omitempty"`
	LocalProxy     *bool  `json:"local_proxy,omitempty"`
	FallbackSecret *bool  `json:"fallback_secret,omitempty"`
}

type LaunchConfig struct {
	Source       string              `json:"source,omitempty"`
	Common       *LaunchCommon       `json:"common,omitempty"`
	Hosts        []LaunchHost        `json:"hosts,omitempty"`
	Tasks        []LaunchTask        `json:"tasks,omitempty"`
	Tunnels      []LaunchTask        `json:"tunnels,omitempty"`
	Healths      []LaunchHealth      `json:"healths,omitempty"`
	LocalServers []LaunchLocalServer `json:"local_servers,omitempty"`
	Locals       []LaunchLocalServer `json:"locals,omitempty"`
}

type LaunchProfile struct {
	Name   string        `json:"name,omitempty"`
	Direct *LaunchDirect `json:"direct,omitempty"`
	Config *LaunchConfig `json:"config,omitempty"`
	Local  *LaunchLocal  `json:"local,omitempty"`
}

type LaunchSpec struct {
	Version  int             `json:"version,omitempty"`
	Runtime  *LaunchRuntime  `json:"runtime,omitempty"`
	Direct   *LaunchDirect   `json:"direct,omitempty"`
	Config   *LaunchConfig   `json:"config,omitempty"`
	Local    *LaunchLocal    `json:"local,omitempty"`
	Profiles []LaunchProfile `json:"profiles,omitempty"`
	Nodes    []LaunchProfile `json:"nodes,omitempty"`
}

type launchConfigSourceDepthKey struct{}

type LaunchSourceError struct {
	Source     string
	StatusCode int
	RetryAfter time.Duration
	Temporary  bool
	Revoked    bool
	Invalid    bool
	Err        error
}

func (e *LaunchSourceError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Source != "" && e.Err != nil:
		return fmt.Sprintf("launch source %s: %v", e.Source, e.Err)
	case e.Err != nil:
		return e.Err.Error()
	case e.Source != "":
		return fmt.Sprintf("launch source %s error", e.Source)
	default:
		return "launch source error"
	}
}

func (e *LaunchSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (r *LaunchRuntime) HasValue() bool {
	return r != nil && (r.Debug != nil ||
		r.Log != nil ||
		r.LogLevel != nil ||
		r.LogPath != nil ||
		r.LogMaxSize != nil ||
		r.LogMaxDays != nil ||
		r.LogMaxFiles != nil ||
		r.LogCompress != nil ||
		r.LogColor != nil ||
		r.PProf != nil ||
		r.ProtoVersion != nil ||
		r.SkipVerify != nil ||
		r.KeepAlive != nil ||
		r.DNSServer != nil ||
		r.NTPServer != nil ||
		r.NTPInterval != nil ||
		r.Timezone != nil ||
		r.DisableP2P != nil ||
		r.P2PType != nil ||
		r.LocalIPForward != nil ||
		r.AutoReconnect != nil ||
		r.DisconnectTimeout != nil ||
		r.P2PTimeout != nil)
}

func (d *LaunchDirect) HasValue() bool {
	return d != nil && (d.Server.HasValue() || d.VKey.HasValue() || d.Type.HasValue() || d.Proxy != "" || d.LocalIP.HasValue())
}

func (d *LaunchDirect) Validate() error {
	if d == nil {
		return fmt.Errorf("direct launch is empty")
	}
	if !d.Server.HasValue() {
		return fmt.Errorf("direct launch requires server")
	}
	if !d.VKey.HasValue() {
		return fmt.Errorf("direct launch requires vkey")
	}
	return nil
}

func (l *LaunchLocal) HasValue() bool {
	return l != nil && (l.Server != "" || l.VKey != "" || l.Type != "" || l.Proxy != "" || l.LocalIP != "" || l.LocalType != "" || l.LocalPort != nil || l.Password != "" || l.Target != "" || l.TargetAddr != "" || l.TargetType != "" || l.FallbackSecret != nil || l.LocalProxy != nil)
}

func (l *LaunchLocal) Validate() error {
	if l == nil {
		return fmt.Errorf("local launch is empty")
	}
	if strings.TrimSpace(l.Server) == "" {
		return fmt.Errorf("local launch requires server")
	}
	if strings.TrimSpace(l.VKey) == "" {
		return fmt.Errorf("local launch requires vkey")
	}
	if strings.TrimSpace(l.Password) == "" {
		return fmt.Errorf("local launch requires password")
	}
	return nil
}

func (l *LaunchLocal) NormalizedTarget() string {
	if l == nil {
		return ""
	}
	if strings.TrimSpace(l.Target) != "" {
		return strings.TrimSpace(l.Target)
	}
	return strings.TrimSpace(l.TargetAddr)
}

func (l *launchStringList) UnmarshalJSON(data []byte) error {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case '"':
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*l = splitLaunchValues(raw)
		return nil
	case '[':
		var raw []string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		values := make([]string, 0, len(raw))
		for _, item := range raw {
			values = append(values, splitLaunchValues(item)...)
		}
		*l = values
		return nil
	default:
		return fmt.Errorf("unsupported list payload: %s", string(data))
	}
}

func (l launchStringList) HasValue() bool {
	return len(l) > 0
}

func (l launchStringList) JoinComma() string {
	return strings.Join(l, ",")
}

func (l launchStringList) JoinLines() string {
	return strings.Join(l, "\n")
}

func (m *LaunchMultiAccount) UnmarshalJSON(data []byte) error {
	data = bytesTrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &m.Path)
	}
	if data[0] == '{' {
		var accounts map[string]string
		if err := json.Unmarshal(data, &accounts); err == nil {
			m.Accounts = accounts
			return nil
		}
		var raw struct {
			Path     string            `json:"path"`
			Accounts map[string]string `json:"accounts"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		m.Path = raw.Path
		m.Accounts = raw.Accounts
		return nil
	}
	return fmt.Errorf("unsupported multi_account payload: %s", string(data))
}

func (m *LaunchMultiAccount) HasValue() bool {
	return m != nil && (strings.TrimSpace(m.Path) != "" || len(m.Accounts) > 0)
}

func (m *LaunchMultiAccount) Build() (*file.MultiAccount, error) {
	if m == nil || !m.HasValue() {
		return nil, nil
	}
	if strings.TrimSpace(m.Path) != "" {
		content, err := common.ReadAllFromFile(m.Path)
		if err != nil {
			return nil, err
		}
		parsed, err := common.ParseStr(string(content))
		if err != nil {
			return nil, err
		}
		return &file.MultiAccount{
			Content:    parsed,
			AccountMap: parseMultiAccountContent(parsed),
		}, nil
	}
	keys := make([]string, 0, len(m.Accounts))
	for key := range m.Accounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	accountMap := make(map[string]string, len(keys))
	for _, key := range keys {
		value := m.Accounts[key]
		accountMap[key] = value
		lines = append(lines, key+"="+value)
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return &file.MultiAccount{
		Content:    content,
		AccountMap: accountMap,
	}, nil
}

func isSpecialNPCRoute(value string) bool {
	switch normalizeNPCRoute(value) {
	case "direct", "connect", "local":
		return true
	default:
		return false
	}
}

func firstNonEmptyQuery(query url.Values, keys ...string) string {
	for _, key := range keys {
		values := query[key]
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func splitLaunchValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "，", ",")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.ReplaceAll(raw, "\n", ",")
	parts := strings.Split(raw, ",")
	return common.TrimArr(parts)
}

func parseMultiAccountContent(content string) map[string]string {
	accountMap := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		index := strings.Index(line, "=")
		if index < 0 {
			accountMap[line] = ""
			continue
		}
		key := strings.TrimSpace(line[:index])
		if key == "" {
			continue
		}
		accountMap[key] = strings.TrimSpace(line[index+1:])
	}
	return accountMap
}

func normalizeLaunchStructuredBytes(data []byte, format string) ([]byte, error) {
	var value any
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "yaml", "yml":
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, err
		}
	default:
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
	}
	normalized := normalizeLaunchStructuredValue(value, nil)
	return json.Marshal(normalized)
}

func normalizeLaunchStructuredValue(value any, path []string) any {
	if preserveLaunchStructuredMapKeys(path) {
		return value
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalizedKey := normalizeLaunchStructuredKey(key)
			if normalizedKey == "" {
				continue
			}
			out[normalizedKey] = normalizeLaunchStructuredValue(item, appendPath(path, normalizedKey))
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			normalizedKey := normalizeLaunchStructuredKey(fmt.Sprint(key))
			if normalizedKey == "" {
				continue
			}
			out[normalizedKey] = normalizeLaunchStructuredValue(item, appendPath(path, normalizedKey))
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, normalizeLaunchStructuredValue(item, path))
		}
		return out
	default:
		return value
	}
}

func appendPath(path []string, key string) []string {
	out := make([]string, 0, len(path)+1)
	out = append(out, path...)
	out = append(out, key)
	return out
}

func preserveLaunchStructuredMapKeys(path []string) bool {
	if len(path) == 0 {
		return false
	}
	switch path[len(path)-1] {
	case "headers", "request_headers", "response_headers", "accounts":
		return true
	case "user_auth", "multi_account":
		return true
	default:
		return false
	}
}

func normalizeLaunchStructuredKey(key string) string {
	parts := strings.FieldsFunc(strings.TrimSpace(strings.ToLower(key)), func(r rune) bool {
		return r == '_' || r == '.' || r == '-' || unicode.IsSpace(r)
	})
	return strings.Join(parts, "_")
}

func extractLaunchConfigValue(root any) any {
	if object, ok := root.(map[string]any); ok {
		if cfg, exists := object["config"]; exists {
			return cfg
		}
	}
	return root
}

func looksLikeINIConfig(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "[") {
		return false
	}
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return false
	}
	first := strings.TrimSpace(lines[0])
	return strings.HasPrefix(first, "[") && strings.HasSuffix(first, "]") && len(first) > 2 && !strings.Contains(first, `"`)
}

func normalizeBase64(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, raw)
	raw = strings.ReplaceAll(raw, "-", "+")
	raw = strings.ReplaceAll(raw, "_", "/")
	if mod := len(raw) % 4; mod != 0 {
		raw += strings.Repeat("=", 4-mod)
	}
	return raw
}

func looksLikeJSON(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")
}

func bytesTrimSpace(data []byte) []byte {
	return []byte(strings.TrimSpace(string(data)))
}

func (c *LaunchCommon) HasValue() bool {
	return c != nil && (c.ServerAddr != "" ||
		c.Server != "" ||
		c.VKey != "" ||
		c.ConnType != "" ||
		c.Type != "" ||
		c.AutoReconnection != nil ||
		c.TLSEnable != nil ||
		c.ProxyURL != "" ||
		c.Proxy != "" ||
		c.LocalIP != "" ||
		c.DNSServer != "" ||
		c.NTPServer != "" ||
		c.NTPInterval != nil ||
		c.BasicUsername != "" ||
		c.BasicPassword != "" ||
		c.WebUsername != "" ||
		c.WebPassword != "" ||
		c.Compress != nil ||
		c.Crypt != nil ||
		c.RateLimit != nil ||
		c.FlowLimit != nil ||
		c.TimeLimit != "" ||
		c.MaxConn != nil ||
		c.Remark != "" ||
		c.EntryACLMode != nil ||
		c.EntryACLRules.HasValue() ||
		c.PProfAddr != "" ||
		c.DisconnectTimeout != nil)
}

func (c *LaunchCommon) normalizeAliases() {
	if c == nil {
		return
	}
	if strings.TrimSpace(c.ServerAddr) == "" {
		c.ServerAddr = strings.TrimSpace(c.Server)
	}
	if strings.TrimSpace(c.ConnType) == "" {
		c.ConnType = strings.TrimSpace(c.Type)
	}
	if strings.TrimSpace(c.ProxyURL) == "" {
		c.ProxyURL = strings.TrimSpace(c.Proxy)
	}
}

func (c *LaunchCommon) Build() (*config.CommonConfig, error) {
	if c == nil {
		return nil, fmt.Errorf("common section is empty")
	}
	c.normalizeAliases()
	commonConfig := &config.CommonConfig{
		Tp:               "tcp",
		AutoReconnection: true,
		Client:           file.NewClient("", true, true),
	}
	if commonConfig.Client.Cnf == nil {
		commonConfig.Client.Cnf = new(file.Config)
	}
	commonConfig.Server = strings.TrimSpace(c.ServerAddr)
	commonConfig.VKey = strings.TrimSpace(c.VKey)
	if strings.TrimSpace(c.ConnType) != "" {
		commonConfig.Tp = strings.TrimSpace(c.ConnType)
	}
	if c.AutoReconnection != nil {
		commonConfig.AutoReconnection = *c.AutoReconnection
	}
	if c.TLSEnable != nil {
		commonConfig.TlsEnable = *c.TLSEnable
	}
	commonConfig.ProxyUrl = strings.TrimSpace(c.ProxyURL)
	commonConfig.LocalIP = strings.TrimSpace(c.LocalIP)
	commonConfig.DnsServer = strings.TrimSpace(c.DNSServer)
	commonConfig.NtpServer = strings.TrimSpace(c.NTPServer)
	if c.NTPInterval != nil {
		commonConfig.NtpInterval = *c.NTPInterval
	}
	commonConfig.Client.Cnf.U = c.BasicUsername
	commonConfig.Client.Cnf.P = c.BasicPassword
	applyLaunchLegacyWebLogin(commonConfig.Client, c.WebUsername, c.WebPassword)
	if c.Compress != nil {
		commonConfig.Client.Cnf.Compress = *c.Compress
	}
	if c.Crypt != nil {
		commonConfig.Client.Cnf.Crypt = *c.Crypt
	}
	if c.RateLimit != nil {
		commonConfig.Client.RateLimit = *c.RateLimit
	}
	if c.FlowLimit != nil {
		commonConfig.Client.Flow.FlowLimit = int64(*c.FlowLimit)
	}
	if strings.TrimSpace(c.TimeLimit) != "" {
		commonConfig.Client.Flow.TimeLimit = common.GetTimeNoErrByStr(c.TimeLimit)
	}
	if c.MaxConn != nil {
		commonConfig.Client.MaxConn = *c.MaxConn
	}
	commonConfig.Client.Remark = c.Remark
	if c.EntryACLMode != nil {
		commonConfig.Client.EntryAclMode = *c.EntryACLMode
	}
	commonConfig.Client.EntryAclRules = c.EntryACLRules.JoinLines()
	if strings.TrimSpace(c.PProfAddr) != "" {
		common.InitPProfByAddr(strings.TrimSpace(c.PProfAddr))
	}
	if c.DisconnectTimeout != nil {
		commonConfig.DisconnectTime = *c.DisconnectTimeout
	}
	return commonConfig, nil
}

func (c *LaunchCommon) overlayConfigCommon(dst *config.CommonConfig) {
	if c == nil || dst == nil {
		return
	}
	c.normalizeAliases()
	if strings.TrimSpace(c.ServerAddr) != "" {
		dst.Server = strings.TrimSpace(c.ServerAddr)
	}
	if strings.TrimSpace(c.VKey) != "" {
		dst.VKey = strings.TrimSpace(c.VKey)
	}
	if strings.TrimSpace(c.ConnType) != "" {
		dst.Tp = strings.ToLower(strings.TrimSpace(c.ConnType))
	}
	if c.AutoReconnection != nil {
		dst.AutoReconnection = *c.AutoReconnection
	}
	if c.TLSEnable != nil {
		dst.TlsEnable = *c.TLSEnable
	}
	if strings.TrimSpace(c.ProxyURL) != "" {
		dst.ProxyUrl = strings.TrimSpace(c.ProxyURL)
	}
	if strings.TrimSpace(c.LocalIP) != "" {
		dst.LocalIP = strings.TrimSpace(c.LocalIP)
	}
	if strings.TrimSpace(c.DNSServer) != "" {
		dst.DnsServer = strings.TrimSpace(c.DNSServer)
	}
	if strings.TrimSpace(c.NTPServer) != "" {
		dst.NtpServer = strings.TrimSpace(c.NTPServer)
	}
	if c.NTPInterval != nil {
		dst.NtpInterval = *c.NTPInterval
	}
	if c.DisconnectTimeout != nil {
		dst.DisconnectTime = *c.DisconnectTimeout
	}
	if dst.Client == nil {
		dst.Client = file.NewClient("", true, true)
	}
	if dst.Client.Cnf == nil {
		dst.Client.Cnf = new(file.Config)
	}
	if strings.TrimSpace(c.BasicUsername) != "" {
		dst.Client.Cnf.U = strings.TrimSpace(c.BasicUsername)
	}
	if strings.TrimSpace(c.BasicPassword) != "" {
		dst.Client.Cnf.P = strings.TrimSpace(c.BasicPassword)
	}
	applyLaunchLegacyWebLogin(dst.Client, c.WebUsername, c.WebPassword)
	if c.Compress != nil {
		dst.Client.Cnf.Compress = *c.Compress
	}
	if c.Crypt != nil {
		dst.Client.Cnf.Crypt = *c.Crypt
	}
	if c.RateLimit != nil {
		dst.Client.RateLimit = *c.RateLimit
	}
	if c.FlowLimit != nil {
		dst.Client.Flow.FlowLimit = int64(*c.FlowLimit)
	}
	if strings.TrimSpace(c.TimeLimit) != "" {
		dst.Client.Flow.TimeLimit = common.GetTimeNoErrByStr(strings.TrimSpace(c.TimeLimit))
	}
	if c.MaxConn != nil {
		dst.Client.MaxConn = *c.MaxConn
	}
	if strings.TrimSpace(c.Remark) != "" {
		dst.Client.Remark = strings.TrimSpace(c.Remark)
	}
	if c.EntryACLMode != nil {
		dst.Client.EntryAclMode = *c.EntryACLMode
	}
	if c.EntryACLRules.HasValue() {
		dst.Client.EntryAclRules = c.EntryACLRules.JoinLines()
	}
	if strings.TrimSpace(c.PProfAddr) != "" {
		common.InitPProfByAddr(strings.TrimSpace(c.PProfAddr))
	}
}

func applyLaunchLegacyWebLogin(dst *file.Client, username, password string) {
	if dst == nil {
		return
	}
	currentUsername, currentPassword, currentTOTP := dst.LegacyWebLoginImport()
	if strings.TrimSpace(username) != "" {
		currentUsername = strings.TrimSpace(username)
	}
	if strings.TrimSpace(password) != "" {
		currentPassword = strings.TrimSpace(password)
	}
	dst.SetLegacyWebLoginImport(currentUsername, currentPassword, currentTOTP)
}

func (h *LaunchHealth) HasValue() bool {
	return h != nil && (h.Timeout != nil || h.MaxFailed != nil || h.Interval != nil || h.HTTPURL != "" || h.Type != "" || h.Target.HasValue())
}

func (h *LaunchHealth) Build() *file.Health {
	if h == nil {
		return nil
	}
	out := &file.Health{}
	if h.Timeout != nil {
		out.HealthCheckTimeout = *h.Timeout
	}
	if h.MaxFailed != nil {
		out.HealthMaxFail = *h.MaxFailed
	}
	if h.Interval != nil {
		out.HealthCheckInterval = *h.Interval
	}
	out.HttpHealthUrl = h.HTTPURL
	out.HealthCheckType = h.Type
	out.HealthCheckTarget = h.Target.JoinLines()
	return out
}

func buildLaunchHeaderLines(raw string, groups ...map[string]string) string {
	lines := make([]string, 0)
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw != "" {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	for _, group := range groups {
		if len(group) == 0 {
			continue
		}
		keys := make([]string, 0, len(group))
		for key := range group {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, strings.TrimSpace(key)+":"+group[key])
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (h *LaunchHost) Build() (*file.Host, error) {
	host := &file.Host{
		Scheme:       "all",
		Target:       &file.Target{},
		MultiAccount: &file.MultiAccount{},
	}
	host.Remark = h.Remark
	host.Host = h.Host
	host.Target.TargetStr = h.TargetAddr.JoinLines()
	if h.ProxyProtocol != nil {
		host.Target.ProxyProtocol = *h.ProxyProtocol
	}
	host.HostChange = h.HostChange
	host.HeaderChange = buildLaunchHeaderLines(h.HeaderChange, h.Headers, h.RequestHeaders)
	host.RespHeaderChange = buildLaunchHeaderLines(h.RespHeaderChange, h.ResponseHeaders)
	if strings.TrimSpace(h.Scheme) != "" {
		host.Scheme = h.Scheme
	}
	host.Location = h.Location
	host.PathRewrite = h.PathRewrite
	if strings.TrimSpace(h.CertFile) != "" {
		certContent, err := common.GetCertContent(h.CertFile, "CERTIFICATE")
		if err != nil {
			return nil, err
		}
		host.CertFile = certContent
	}
	if strings.TrimSpace(h.KeyFile) != "" {
		keyContent, err := common.GetCertContent(h.KeyFile, "PRIVATE")
		if err != nil {
			return nil, err
		}
		host.KeyFile = keyContent
	}
	if h.HTTPSJustProxy != nil {
		host.HttpsJustProxy = *h.HTTPSJustProxy
	}
	if h.TLSOffload != nil {
		host.TlsOffload = *h.TLSOffload
	}
	if h.AutoSSL != nil {
		host.AutoSSL = *h.AutoSSL
	}
	if h.AutoHTTPS != nil {
		host.AutoHttps = *h.AutoHTTPS
	}
	if h.AutoCORS != nil {
		host.AutoCORS = *h.AutoCORS
	}
	if h.CompatMode != nil {
		host.CompatMode = *h.CompatMode
	}
	host.RedirectURL = h.RedirectURL
	if h.TargetIsHTTPS != nil {
		host.TargetIsHttps = *h.TargetIsHTTPS
	}
	if h.LocalProxy != nil {
		host.Target.LocalProxy = *h.LocalProxy
	}
	if h.EntryACLMode != nil {
		host.EntryAclMode = *h.EntryACLMode
	}
	host.EntryAclRules = h.EntryACLRules.JoinLines()
	if h.UserAuth != nil && h.UserAuth.HasValue() {
		userAuth, err := h.UserAuth.Build()
		if err != nil {
			return nil, err
		}
		if userAuth != nil {
			host.UserAuth = userAuth
		}
	}
	if h.MultiAccount != nil && h.MultiAccount.HasValue() {
		multiAccount, err := h.MultiAccount.Build()
		if err != nil {
			return nil, err
		}
		if multiAccount != nil {
			host.MultiAccount = multiAccount
		}
	}
	return host, nil
}

func (t *LaunchTask) Build() (*file.Tunnel, error) {
	tunnel := &file.Tunnel{
		Target:       &file.Target{},
		MultiAccount: &file.MultiAccount{},
	}
	tunnel.Remark = t.Remark
	tunnel.Ports = t.ServerPort
	tunnel.ServerIp = t.ServerIP
	tunnel.Mode = t.Mode
	tunnel.Target.TargetStr = t.TargetAddr.JoinLines()
	if t.ProxyProtocol != nil {
		tunnel.Target.ProxyProtocol = *t.ProxyProtocol
	}
	if strings.TrimSpace(t.TargetPort) != "" && tunnel.Target.TargetStr == "" {
		tunnel.Target.TargetStr = strings.TrimSpace(t.TargetPort)
	}
	tunnel.TargetAddr = t.TargetIP
	tunnel.TargetType = strings.TrimSpace(t.TargetType)
	tunnel.Password = t.Password
	if t.Socks5Proxy != nil {
		tunnel.Socks5Proxy = *t.Socks5Proxy
	}
	if t.HTTPProxy != nil {
		tunnel.HttpProxy = *t.HTTPProxy
	}
	if t.LocalProxy != nil {
		tunnel.Target.LocalProxy = *t.LocalProxy
	}
	if t.EntryACLMode != nil {
		tunnel.EntryAclMode = *t.EntryACLMode
	}
	tunnel.EntryAclRules = t.EntryACLRules.JoinLines()
	if t.DestACLMode != nil {
		tunnel.DestAclMode = *t.DestACLMode
	}
	tunnel.DestAclRules = t.DestACLRules.JoinLines()
	tunnel.LocalPath = t.LocalPath
	tunnel.StripPre = t.StripPre
	if t.ReadOnly != nil {
		tunnel.ReadOnly = *t.ReadOnly
	}
	if t.UserAuth != nil && t.UserAuth.HasValue() {
		userAuth, err := t.UserAuth.Build()
		if err != nil {
			return nil, err
		}
		if userAuth != nil {
			tunnel.UserAuth = userAuth
		}
	}
	if t.MultiAccount != nil && t.MultiAccount.HasValue() {
		multiAccount, err := t.MultiAccount.Build()
		if err != nil {
			return nil, err
		}
		if multiAccount != nil {
			tunnel.MultiAccount = multiAccount
		}
	}
	return tunnel, nil
}

func (l *LaunchLocalServer) HasValue() bool {
	return l != nil && (l.LocalPort != nil || l.LocalType != "" || l.LocalIP != "" || l.Password != "" || l.TargetAddr != "" || l.TargetType != "" || l.LocalProxy != nil || l.FallbackSecret != nil)
}

func (l *LaunchLocalServer) Build() config.LocalServer {
	out := config.LocalServer{
		Type:       strings.TrimSpace(l.LocalType),
		Ip:         strings.TrimSpace(l.LocalIP),
		Password:   l.Password,
		Target:     strings.TrimSpace(l.TargetAddr),
		TargetType: strings.TrimSpace(l.TargetType),
	}
	if l.LocalPort != nil {
		out.Port = *l.LocalPort
	}
	if out.Type == "" {
		out.Type = "p2p"
	}
	if out.TargetType == "" {
		out.TargetType = "all"
	}
	if l.LocalProxy != nil {
		out.LocalProxy = *l.LocalProxy
	}
	if l.FallbackSecret != nil {
		out.Fallback = *l.FallbackSecret
	}
	return out
}

func (c *LaunchConfig) taskItems() []LaunchTask {
	if c == nil {
		return nil
	}
	if len(c.Tunnels) == 0 {
		return c.Tasks
	}
	out := make([]LaunchTask, 0, len(c.Tasks)+len(c.Tunnels))
	out = append(out, c.Tasks...)
	out = append(out, c.Tunnels...)
	return out
}

func (c *LaunchConfig) localServerItems() []LaunchLocalServer {
	if c == nil {
		return nil
	}
	if len(c.Locals) == 0 {
		return c.LocalServers
	}
	out := make([]LaunchLocalServer, 0, len(c.LocalServers)+len(c.Locals))
	out = append(out, c.LocalServers...)
	out = append(out, c.Locals...)
	return out
}

func (c *LaunchConfig) hasSource() bool {
	return c != nil && strings.TrimSpace(c.Source) != ""
}

func (c *LaunchConfig) hasInlineValue() bool {
	return c != nil && ((c.Common != nil && c.Common.HasValue()) || len(c.Hosts) > 0 || len(c.taskItems()) > 0 || len(c.Healths) > 0 || len(c.localServerItems()) > 0)
}

func (c *LaunchConfig) HasValue() bool {
	return c != nil && (c.hasSource() || c.hasInlineValue())
}

func (c *LaunchConfig) Build() (*config.Config, error) {
	return c.BuildWithContext(context.Background())
}

func (c *LaunchConfig) BuildWithContext(ctx context.Context) (*config.Config, error) {
	if c == nil {
		return nil, fmt.Errorf("config launch is empty")
	}
	ctx = normalizeLaunchContext(ctx)
	var out *config.Config
	if c.hasSource() {
		base, err := c.loadSourceConfig(ctx)
		if err != nil {
			return nil, err
		}
		out = base
	}
	if out == nil {
		out = &config.Config{}
	}
	if c.Common != nil && c.Common.HasValue() {
		if out.CommonConfig == nil {
			commonConfig, err := c.Common.Build()
			if err != nil {
				return nil, err
			}
			out.CommonConfig = commonConfig
		} else {
			c.Common.overlayConfigCommon(out.CommonConfig)
		}
	}
	for i := range c.Hosts {
		host, err := c.Hosts[i].Build()
		if err != nil {
			return nil, err
		}
		out.Hosts = append(out.Hosts, host)
	}
	for _, item := range c.taskItems() {
		task, err := item.Build()
		if err != nil {
			return nil, err
		}
		out.Tasks = append(out.Tasks, task)
	}
	for i := range c.Healths {
		if c.Healths[i].HasValue() {
			out.Healths = append(out.Healths, c.Healths[i].Build())
		}
	}
	for _, item := range c.localServerItems() {
		if !item.HasValue() {
			continue
		}
		server := item.Build()
		out.LocalServer = append(out.LocalServer, &server)
	}
	if out.CommonConfig == nil {
		return nil, fmt.Errorf("config launch requires common section or config source")
	}
	return out, nil
}

func (c *LaunchConfig) loadSourceConfig(ctx context.Context) (*config.Config, error) {
	source := strings.TrimSpace(c.Source)
	if source == "" {
		return nil, fmt.Errorf("config source is empty")
	}
	ctx, err := nextLaunchConfigSourceContext(ctx)
	if err != nil {
		return nil, err
	}
	body := ""
	if u, ok := parseLaunchURL(source); ok {
		switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
		case "http", "https":
			body, err = fetchLaunchPayload(ctx, source)
			if err != nil {
				return nil, err
			}
			return loadLaunchConfigSourceContent(ctx, source, body)
		}
	}
	bytes, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	if len(bytes) > 1<<20 {
		return nil, fmt.Errorf("config source is too large")
	}
	body = string(bytes)
	return loadLaunchConfigSourceContent(ctx, source, body)
}

func nextLaunchConfigSourceContext(ctx context.Context) (context.Context, error) {
	ctx = normalizeLaunchContext(ctx)
	depth, _ := ctx.Value(launchConfigSourceDepthKey{}).(int)
	if depth >= maxLaunchConfigSourceDepth {
		return nil, fmt.Errorf("config source nesting is too deep")
	}
	return context.WithValue(ctx, launchConfigSourceDepthKey{}, depth+1), nil
}

func loadLaunchConfigSourceContent(ctx context.Context, source, body string) (*config.Config, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("config source %q is empty", source)
	}
	switch detectLaunchConfigSourceFormat(source, body) {
	case "json", "yaml":
		launchCfg, err := parseStructuredLaunchConfig(body)
		if err != nil {
			return nil, &LaunchSourceError{Source: source, Invalid: true, Err: err}
		}
		cfg, err := launchCfg.BuildWithContext(ctx)
		if err != nil {
			return nil, &LaunchSourceError{Source: source, Invalid: true, Err: err}
		}
		return cfg, nil
	default:
		cfg, err := config.NewConfigFromContent(body)
		if err != nil {
			return nil, &LaunchSourceError{Source: source, Invalid: true, Err: err}
		}
		return cfg, nil
	}
}

func detectLaunchConfigSourceFormat(source, body string) string {
	body = strings.TrimSpace(body)
	ext := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(source)), ".")))
	switch ext {
	case "json":
		return "json"
	case "yaml", "yml":
		return "yaml"
	case "conf", "ini":
		return "ini"
	}
	if looksLikeINIConfig(body) {
		return "ini"
	}
	if looksLikeJSON(body) {
		return "json"
	}
	return "yaml"
}

func parseStructuredLaunchConfig(raw string) (*LaunchConfig, error) {
	format := detectLaunchConfigSourceFormat("", raw)
	normalized, err := normalizeLaunchStructuredBytes([]byte(raw), format)
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, err
	}
	root = extractLaunchConfigValue(root)
	data, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	var cfg LaunchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if !cfg.HasValue() {
		return nil, fmt.Errorf("structured config source does not contain config settings")
	}
	return &cfg, nil
}

func (p *LaunchProfile) HasValue() bool {
	return p != nil && ((p.Direct != nil && p.Direct.HasValue()) || (p.Config != nil && p.Config.HasValue()) || (p.Local != nil && p.Local.HasValue()))
}

func (p *LaunchProfile) Validate() error {
	if p == nil {
		return fmt.Errorf("launch profile is empty")
	}
	modeCount := 0
	if p.Direct != nil && p.Direct.HasValue() {
		modeCount++
	}
	if p.Config != nil && p.Config.HasValue() {
		modeCount++
	}
	if p.Local != nil && p.Local.HasValue() {
		modeCount++
	}
	if modeCount == 0 {
		return fmt.Errorf("launch profile does not contain direct/config/local settings")
	}
	if modeCount > 1 {
		return fmt.Errorf("launch profile must contain exactly one of direct/config/local")
	}
	if p.Direct != nil && p.Direct.HasValue() {
		return p.Direct.Validate()
	}
	if p.Config != nil && p.Config.HasValue() {
		if !p.Config.hasSource() {
			if p.Config.Common == nil {
				return fmt.Errorf("config launch requires common section")
			}
			p.Config.Common.normalizeAliases()
			if strings.TrimSpace(p.Config.Common.ServerAddr) == "" {
				return fmt.Errorf("config common.server_addr cannot be empty")
			}
			if strings.TrimSpace(p.Config.Common.VKey) == "" {
				return fmt.Errorf("config common.vkey cannot be empty")
			}
		}
		return nil
	}
	return p.Local.Validate()
}

func (p *LaunchProfile) Mode() string {
	if p == nil {
		return ""
	}
	if p.Direct != nil && p.Direct.HasValue() {
		return "direct"
	}
	if p.Config != nil && p.Config.HasValue() {
		return "config"
	}
	if p.Local != nil && p.Local.HasValue() {
		return "local"
	}
	return ""
}

func (p *LaunchProfile) BuildConfig() (*config.Config, error) {
	return p.BuildConfigWithContext(context.Background())
}

func (p *LaunchProfile) BuildConfigWithContext(ctx context.Context) (*config.Config, error) {
	if p == nil || p.Config == nil {
		return nil, fmt.Errorf("launch profile does not contain config section")
	}
	return p.Config.BuildWithContext(ctx)
}

func (p *LaunchProfile) BuildConfigWithRuntime(runtime *LaunchRuntime) (*config.Config, error) {
	return p.BuildConfigWithRuntimeContext(context.Background(), runtime)
}

func (p *LaunchProfile) BuildConfigWithRuntimeContext(ctx context.Context, runtime *LaunchRuntime) (*config.Config, error) {
	cfg, err := p.BuildConfigWithContext(ctx)
	if err != nil {
		return nil, err
	}
	overlayRuntimeOnConfig(runtime, cfg)
	return cfg, nil
}

func (s *LaunchSpec) Normalize() {
	if s.Version <= 0 {
		if s.HasProfiles() {
			s.Version = 2
		} else {
			s.Version = 1
		}
	}
}

func (s *LaunchSpec) ProfileItems() []LaunchProfile {
	if s == nil {
		return nil
	}
	if len(s.Nodes) == 0 {
		return s.Profiles
	}
	out := make([]LaunchProfile, 0, len(s.Profiles)+len(s.Nodes))
	out = append(out, s.Profiles...)
	out = append(out, s.Nodes...)
	return out
}

func (s *LaunchSpec) HasProfiles() bool {
	return len(s.ProfileItems()) > 0
}

func (s *LaunchSpec) ExpandProfiles() []LaunchProfile {
	if s == nil {
		return nil
	}
	if profiles := s.ProfileItems(); len(profiles) > 0 {
		return profiles
	}
	switch s.Mode() {
	case "direct":
		return []LaunchProfile{{Name: "default", Direct: s.Direct}}
	case "config":
		return []LaunchProfile{{Name: "default", Config: s.Config}}
	case "local":
		return []LaunchProfile{{Name: "default", Local: s.Local}}
	default:
		return nil
	}
}

func (s *LaunchSpec) Validate() error {
	if s == nil {
		return fmt.Errorf("launch spec is empty")
	}
	s.Normalize()
	modeCount := 0
	if s.Direct != nil && s.Direct.HasValue() {
		modeCount++
	}
	if s.Config != nil && s.Config.HasValue() {
		modeCount++
	}
	if s.Local != nil && s.Local.HasValue() {
		modeCount++
	}
	if s.HasProfiles() {
		modeCount++
	}
	if modeCount == 0 {
		return fmt.Errorf("launch spec does not contain direct/config/local/profiles settings")
	}
	if modeCount > 1 {
		return fmt.Errorf("launch spec must contain exactly one of direct/config/local/profiles")
	}
	if s.HasProfiles() {
		profiles := s.ProfileItems()
		for i := range profiles {
			if err := profiles[i].Validate(); err != nil {
				if strings.TrimSpace(profiles[i].Name) != "" {
					return fmt.Errorf("launch profile %q: %w", profiles[i].Name, err)
				}
				return fmt.Errorf("launch profile #%d: %w", i+1, err)
			}
		}
		return nil
	}
	if s.Direct != nil && s.Direct.HasValue() {
		return s.Direct.Validate()
	}
	if s.Config != nil && s.Config.HasValue() {
		if !s.Config.hasSource() {
			if s.Config.Common == nil {
				return fmt.Errorf("config launch requires common section")
			}
			s.Config.Common.normalizeAliases()
			if strings.TrimSpace(s.Config.Common.ServerAddr) == "" {
				return fmt.Errorf("config common.server_addr cannot be empty")
			}
			if strings.TrimSpace(s.Config.Common.VKey) == "" {
				return fmt.Errorf("config common.vkey cannot be empty")
			}
		}
		return nil
	}
	return s.Local.Validate()
}

func (s *LaunchSpec) Mode() string {
	if s == nil {
		return ""
	}
	if s.Direct != nil && s.Direct.HasValue() {
		return "direct"
	}
	if s.Config != nil && s.Config.HasValue() {
		return "config"
	}
	if s.Local != nil && s.Local.HasValue() {
		return "local"
	}
	if s.HasProfiles() {
		return "profiles"
	}
	return ""
}

func (s *LaunchSpec) BuildConfig() (*config.Config, error) {
	return s.BuildConfigWithContext(context.Background())
}

func (s *LaunchSpec) BuildConfigWithContext(ctx context.Context) (*config.Config, error) {
	if s == nil || s.Config == nil {
		return nil, fmt.Errorf("launch spec does not contain config section")
	}
	cfg, err := s.Config.BuildWithContext(ctx)
	if err != nil {
		return nil, err
	}
	if s.Runtime != nil {
		overlayRuntimeOnConfig(s.Runtime, cfg)
	}
	return cfg, nil
}

func ResolveLaunchSpec(ctx context.Context, raw string) (*LaunchSpec, error) {
	return resolveLaunchSpec(ctx, raw, 0)
}

func ResolveLaunchInputs(ctx context.Context, inputs []string) (*LaunchSpec, error) {
	items := make([]string, 0, len(inputs))
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		items = append(items, input)
	}
	switch len(items) {
	case 0:
		return nil, fmt.Errorf("launch payload is empty")
	case 1:
		return resolveLaunchSpec(ctx, items[0], 0)
	default:
		return resolveLaunchSpecList(ctx, items, 1)
	}
}

func resolveLaunchSpec(ctx context.Context, raw string, depth int) (*LaunchSpec, error) {
	if depth > maxLaunchResolveDepth {
		return nil, fmt.Errorf("launch payload nesting is too deep")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("launch payload is empty")
	}
	if strings.HasPrefix(raw, "@") && len(raw) > 1 {
		body, err := readLaunchPayloadFile(strings.TrimSpace(raw[1:]))
		if err != nil {
			return nil, err
		}
		return resolveLaunchSpec(ctx, body, depth+1)
	}
	if looksLikeJSON(raw) {
		return parseLaunchJSON(raw)
	}
	if u, ok := parseLaunchURL(raw); ok {
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			body, err := fetchLaunchPayload(ctx, raw)
			if err != nil {
				return nil, err
			}
			spec, err := resolveLaunchSpec(ctx, body, depth+1)
			if err != nil {
				return nil, &LaunchSourceError{Source: raw, Invalid: true, Err: err}
			}
			return spec, nil
		case "npc":
			return parseNPCLaunchURL(ctx, u, depth)
		}
	}
	if decoded, ok := decodeLaunchPayload(raw); ok {
		return resolveLaunchSpec(ctx, decoded, depth+1)
	}
	return nil, fmt.Errorf("unsupported launch payload format")
}

func resolveLaunchSpecList(ctx context.Context, items []string, depth int) (*LaunchSpec, error) {
	specs := make([]*LaunchSpec, 0, len(items))
	for i, item := range items {
		spec, err := resolveLaunchSpec(ctx, item, depth)
		if err != nil {
			return nil, fmt.Errorf("launch payload #%d: %w", i+1, err)
		}
		specs = append(specs, spec)
	}
	return mergeLaunchSpecList(specs)
}

func parseLaunchJSON(raw string) (*LaunchSpec, error) {
	normalizedRaw, err := normalizeLaunchStructuredBytes([]byte(raw), "json")
	if err == nil && len(normalizedRaw) > 0 {
		raw = string(normalizedRaw)
	}
	var spec LaunchSpec
	if err := json.Unmarshal([]byte(raw), &spec); err == nil {
		if spec.Direct != nil || spec.Config != nil || spec.Local != nil || len(spec.Profiles) > 0 || len(spec.Nodes) > 0 {
			if err := spec.Validate(); err != nil {
				return nil, err
			}
			return &spec, nil
		}
	}
	var profiles []LaunchProfile
	if err := json.Unmarshal([]byte(raw), &profiles); err == nil && len(profiles) > 0 {
		spec := &LaunchSpec{Version: 2, Profiles: profiles}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		return spec, nil
	}
	var cfg LaunchConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err == nil && cfg.HasValue() {
		spec := &LaunchSpec{Version: 1, Config: &cfg}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		return spec, nil
	}
	var direct LaunchDirect
	if err := json.Unmarshal([]byte(raw), &direct); err == nil && direct.HasValue() {
		spec := &LaunchSpec{Version: 1, Direct: &direct}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		return spec, nil
	}
	var local LaunchLocal
	if err := json.Unmarshal([]byte(raw), &local); err == nil && local.HasValue() {
		spec := &LaunchSpec{Version: 1, Local: &local}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		return spec, nil
	}
	return nil, fmt.Errorf("invalid launch json payload")
}

func parseNPCLaunchURL(ctx context.Context, u *url.URL, depth int) (*LaunchSpec, error) {
	query := u.Query()
	runtime := runtimeFromQuery(query)
	hostRoute := normalizeNPCRoute(u.Hostname())

	if shouldTreatNPCAsWrappedPayload(u, query) {
		if candidate := npcWrappedPayloadCandidate(u, true); candidate != "" {
			if spec, err := resolveNPCWrappedPayload(ctx, candidate, depth, runtime); err == nil {
				return spec, nil
			}
		}
		if candidate := npcWrappedPayloadCandidate(u, false); candidate != "" {
			if spec, err := resolveNPCWrappedPayload(ctx, candidate, depth, runtime); err == nil {
				return spec, nil
			}
		}
	}
	server := strings.TrimSpace(firstNonEmptyQuery(query, "server", "server_addr"))
	route := ""
	if isSpecialNPCRoute(hostRoute) {
		route = hostRoute
	}
	if route == "" {
		candidate := normalizeNPCRoute(firstNonEmptyQuery(query, "route", "mode", "action"))
		if isSpecialNPCRoute(candidate) {
			route = candidate
		}
	}
	if server == "" && hostRoute != "" && !isSpecialNPCRoute(hostRoute) {
		server = strings.TrimSpace(u.Host)
		if strings.TrimSpace(u.Path) != "" {
			server += strings.TrimSpace(u.Path)
		}
	}
	vkey := strings.TrimSpace(firstNonEmptyQuery(query, "vkey"))
	if vkey == "" && u.User != nil {
		vkey = u.User.Username()
	}

	if route == "local" || firstNonEmptyQuery(query, "password") != "" || firstNonEmptyQuery(query, "local_type") != "" || firstNonEmptyQuery(query, "local_port") != "" || firstNonEmptyQuery(query, "target", "target_addr") != "" || firstNonEmptyQuery(query, "target_type") != "" {
		spec := &LaunchSpec{
			Version: 1,
			Runtime: runtime,
			Local: &LaunchLocal{
				Server:         server,
				VKey:           vkey,
				Type:           strings.TrimSpace(firstNonEmptyQuery(query, "type", "conn_type")),
				Proxy:          strings.TrimSpace(firstNonEmptyQuery(query, "proxy", "proxy_url")),
				LocalIP:        strings.TrimSpace(firstNonEmptyQuery(query, "local_ip")),
				LocalType:      strings.TrimSpace(firstNonEmptyQuery(query, "local_type")),
				LocalPort:      parseOptionalInt(firstNonEmptyQuery(query, "local_port")),
				Password:       strings.TrimSpace(firstNonEmptyQuery(query, "password")),
				Target:         strings.TrimSpace(firstNonEmptyQuery(query, "target")),
				TargetAddr:     strings.TrimSpace(firstNonEmptyQuery(query, "target_addr")),
				TargetType:     strings.TrimSpace(firstNonEmptyQuery(query, "target_type")),
				FallbackSecret: parseOptionalBool(firstNonEmptyQuery(query, "fallback_secret")),
				LocalProxy:     parseOptionalBool(firstNonEmptyQuery(query, "local_proxy")),
			},
		}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		return spec, nil
	}

	direct := &LaunchDirect{
		Server:  queryStringListKeys(query, "server", "server_addr"),
		VKey:    queryStringListKeys(query, "vkey"),
		Type:    queryStringListKeys(query, "type", "conn_type"),
		Proxy:   strings.TrimSpace(firstNonEmptyQuery(query, "proxy", "proxy_url")),
		LocalIP: queryStringList(query, "local_ip"),
	}
	if !direct.Server.HasValue() && server != "" {
		direct.Server = splitLaunchValues(server)
	}
	if !direct.VKey.HasValue() && vkey != "" {
		direct.VKey = splitLaunchValues(vkey)
	}
	spec := &LaunchSpec{
		Version: 1,
		Runtime: runtime,
		Direct:  direct,
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

func resolveNPCWrappedPayload(ctx context.Context, payload string, depth int, runtime *LaunchRuntime) (*LaunchSpec, error) {
	payload = strings.TrimSpace(payload)
	if !isWrappedPayloadCandidate(payload) {
		return nil, fmt.Errorf("npc nested payload must use base64 or encrypted text")
	}
	spec, err := resolveLaunchSpec(ctx, payload, depth+1)
	if err != nil {
		return nil, err
	}
	spec.Runtime = mergeLaunchRuntime(spec.Runtime, runtime)
	return spec, nil
}

func mergeLaunchSpecList(specs []*LaunchSpec) (*LaunchSpec, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("launch payload list is empty")
	}
	if len(specs) == 1 {
		return specs[0], nil
	}
	merged := &LaunchSpec{}
	for i, spec := range specs {
		if spec == nil {
			return nil, fmt.Errorf("launch payload #%d is empty", i+1)
		}
		runtime, err := mergeCompatibleLaunchRuntime(merged.Runtime, spec.Runtime)
		if err != nil {
			return nil, fmt.Errorf("launch payload #%d runtime conflict: %w", i+1, err)
		}
		merged.Runtime = runtime
		merged.Profiles = append(merged.Profiles, spec.ExpandProfiles()...)
	}
	if err := merged.Validate(); err != nil {
		return nil, err
	}
	return merged, nil
}
