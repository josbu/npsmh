package common

import (
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func parsePortString(s string) (int, bool) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return 0, false
	}
	return p, true
}

// ExtractHost
// return "[2001:db8::1]:80"
func ExtractHost(input string) string {
	if strings.Contains(input, "://") {
		if u, err := url.Parse(input); err == nil && u.Host != "" {
			return u.Host
		}
	}
	if idx := strings.IndexByte(input, '/'); idx != -1 {
		input = input[:idx]
	}
	return input
}

// RemovePortFromHost
// return "[2001:db8::1]"
func RemovePortFromHost(host string) string {
	if len(host) == 0 {
		return host
	}
	var idx int
	// IPv6
	if host[0] == '[' {
		if idx = strings.IndexByte(host, ']'); idx != -1 {
			return host[:idx+1]
		}
		return ""
	}
	// IPv4 or Domain
	if idx = strings.LastIndexByte(host, ':'); idx != -1 && idx == strings.IndexByte(host, ':') {
		return host[:idx]
	}
	return host
}

// GetIpByAddr
// return "2001:db8::1"
func GetIpByAddr(host string) string {
	if len(host) == 0 {
		return host
	}
	var idx int
	// IPv6
	if host[0] == '[' {
		if idx = strings.IndexByte(host, ']'); idx != -1 {
			return host[1:idx]
		}
		return ""
	}
	// IPv4 or Domain
	if idx = strings.LastIndexByte(host, ':'); idx != -1 && idx == strings.IndexByte(host, ':') {
		return host[:idx]
	}
	return host
}

func IsDomain(s string) bool {
	return net.ParseIP(s) == nil
}

// GetPortByAddr
// return int or 0
func GetPortByAddr(addr string) int {
	if len(addr) == 0 {
		return 0
	}
	// IPv6
	if addr[0] == '[' {
		if end := strings.IndexByte(addr, ']'); end != -1 && end+1 < len(addr) && addr[end+1] == ':' {
			if port, ok := parsePortString(addr[end+2:]); ok {
				return port
			}
		}
		return 0
	}
	// Other
	if idx := strings.LastIndexByte(addr, ':'); idx != -1 {
		if port, ok := parsePortString(addr[idx+1:]); ok {
			return port
		}
	}
	return 0
}

func GetPortStrByAddr(addr string) string {
	port := GetPortByAddr(addr)
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func ValidateAddr(s string) string {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil {
		return ""
	}
	if _, ok := parsePortString(port); !ok {
		return ""
	}
	return s
}

func SplitServerAndPath(s string) (server, path string) {
	index := strings.IndexByte(s, '/')
	if index == -1 {
		return s, ""
	}
	return s[:index], s[index:]
}

func SplitAddrAndHost(s string) (addr, host, sni string) {
	s = strings.TrimSpace(s)
	index := strings.IndexByte(s, '@')
	if index == -1 {
		return s, s, GetSni(s)
	}
	addr = strings.TrimSpace(s[:index])
	host = strings.TrimSpace(s[index+1:])
	if host == "" {
		return addr, addr, ""
	}
	return addr, host, GetSni(host)
}

func GetSni(host string) string {
	sni := GetIpByAddr(host)
	if !IsDomain(sni) {
		return ""
	}
	return sni
}

// GetHostByName Get the corresponding IP address through domain name
func GetHostByName(hostname string) string {
	if !DomainCheck(hostname) {
		return hostname
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return ""
	}
	for _, v := range ips {
		if v.To4() != nil || v.To16() != nil {
			return v.String()
		}
	}
	return ""
}

// DomainCheck Check the legality of domain
func DomainCheck(domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false
	}
	if strings.Contains(domain, "://") {
		parsed, err := url.Parse(domain)
		if err != nil || parsed.Host == "" || parsed.Port() != "" {
			return false
		}
		domain = parsed.Hostname()
	} else {
		if idx := strings.IndexByte(domain, '/'); idx != -1 {
			domain = domain[:idx]
		}
		if strings.ContainsAny(domain, "?#") {
			return false
		}
	}

	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" || len(domain) > 253 || net.ParseIP(domain) != nil {
		return false
	}

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || len(tld) > 63 {
		return false
	}
	for _, label := range labels {
		if !isDomainLabel(label) {
			return false
		}
	}
	return true
}

func isDomainLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		ch := label[i]
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	return true
}

func GetPort(value int) int {
	if value >= 0 {
		return value % 65536
	}
	return (65536 + value%65536) % 65536
}

// GetPorts format ports str to an int array
func GetPorts(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	seen := make(map[int]struct{})
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if fw := strings.SplitN(item, "-", 2); len(fw) == 2 {
			a, b := strings.TrimSpace(fw[0]), strings.TrimSpace(fw[1])
			start, ok1 := parsePortString(a)
			end, ok2 := parsePortString(b)
			if ok1 && ok2 {
				if end < start {
					start, end = end, start
				}
				for i := start; i <= end; i++ {
					seen[i] = struct{}{}
				}
			}
			continue
		}
		if port, ok := parsePortString(item); ok {
			seen[port] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	ps := make([]int, 0, len(seen))
	for p := range seen {
		ps = append(ps, p)
	}
	sort.Ints(ps)
	return ps
}

// IsPort is the string a port
func IsPort(p string) bool {
	_, ok := parsePortString(p)
	return ok
}

// FormatAddress if the s is just a port,return 127.0.0.1:s
func FormatAddress(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return net.JoinHostPort("127.0.0.1", s)
}
