package pmux

import (
	"bufio"
	"bytes"
	"net"
	"net/url"
	"strings"

	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
)

type httpRequestMeta struct {
	host       string
	path       string
	connection string
	upgrade    string
}

func parseHeader(line []byte, name string) (string, bool) {
	prefix := []byte(name + ":")
	if len(line) < len(prefix) || !bytes.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	return string(bytes.TrimSpace(line[len(prefix):])), true
}

func parseHostHeader(line []byte) (string, bool) {
	return parseHeader(line, "Host")
}

func parseRequestPath(line []byte) string {
	fields := bytes.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	target := string(fields[1])
	if parsed, err := url.ParseRequestURI(target); err == nil && parsed != nil {
		if parsed.Path != "" {
			return parsed.Path
		}
	}
	if idx := strings.IndexAny(target, "?#"); idx >= 0 {
		target = target[:idx]
	}
	if strings.HasPrefix(target, "/") {
		return target
	}
	return ""
}

func headerContainsToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func (pMux *PortMux) normalizedBridgePath() string {
	if pMux == nil || strings.TrimSpace(pMux.bridgePath) == "" {
		return "/"
	}
	path := strings.TrimSpace(pMux.bridgePath)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (pMux *PortMux) classifyHTTP(conn net.Conn, first []byte) (*portRoute, []byte, bool) {
	httpRoutes := pMux.routeEntries(routeFamilyHTTP)
	if len(httpRoutes) == 0 {
		if route := pMux.getRoute(routeHTTPS); route != nil {
			return route, nil, true
		}
		return nil, nil, false
	}
	if len(httpRoutes) == 1 && pMux.getRoute(routeHTTPS) == nil {
		return httpRoutes[0].route, nil, true
	}

	var buffer bytes.Buffer
	r := bufio.NewReader(conn)
	buffer.Write(first)
	requestLine, _, err := r.ReadLine()
	if err != nil {
		logs.Warn("read http request line error %v", err)
		return nil, nil, false
	}
	fullRequestLine := append(append([]byte(nil), first...), requestLine...)
	buffer.Write(requestLine)
	buffer.WriteString("\r\n")
	meta := httpRequestMeta{
		path: parseRequestPath(fullRequestLine),
	}
	for {
		line, _, err := r.ReadLine()
		if err != nil {
			logs.Warn("read line error %v", err)
			return nil, nil, false
		}
		buffer.Write(line)
		buffer.WriteString("\r\n")
		if len(line) == 0 {
			if remaining, _ := r.Peek(r.Buffered()); len(remaining) > 0 {
				buffer.Write(remaining)
			}
			return pMux.classifyHTTPRequest(meta), buffer.Bytes(), false
		}
		if host, ok := parseHeader(line, "Host"); ok {
			meta.host = host
			continue
		}
		if connection, ok := parseHeader(line, "Connection"); ok {
			meta.connection = connection
			continue
		}
		if upgrade, ok := parseHeader(line, "Upgrade"); ok {
			meta.upgrade = upgrade
		}
	}
}

func (pMux *PortMux) httpMatch(entry *registeredRoute, meta httpRequestMeta) bool {
	if entry == nil || entry.route == nil {
		return false
	}
	normalizedHost := normalizeRouteHost(meta.host)
	match := entry.matcher
	if match.websocketOnly {
		return strings.EqualFold(normalizedHost, match.host) &&
			meta.path == match.path &&
			headerContainsToken(meta.connection, "upgrade") &&
			headerContainsToken(meta.upgrade, "websocket")
	}
	if match.host != "" {
		return strings.EqualFold(normalizedHost, match.host)
	}
	return match.fallback
}

func (pMux *PortMux) classifyHTTPRequest(meta httpRequestMeta) *portRoute {
	normalizedHost := normalizeRouteHost(meta.host)
	for _, entry := range pMux.routeEntries(routeFamilyHTTP) {
		if entry.matcher.fallback {
			continue
		}
		if pMux.httpMatch(entry, meta) {
			return entry.route
		}
	}
	// Keep HTTPS redirect behavior available on shared TLS ports. When the
	// web manager is HTTPS-only, reserve web_host for the TLS listener rather
	// than letting HTTP proxy traffic consume it.
	if managerTLS := pMux.getRouteEntry(routeManagerTLS); managerTLS != nil &&
		strings.EqualFold(normalizedHost, managerTLS.matcher.host) {
		if httpsRoute := pMux.getRoute(routeHTTPS); httpsRoute != nil {
			return httpsRoute
		}
		return nil
	}
	for _, entry := range pMux.routeEntries(routeFamilyHTTP) {
		if entry.matcher.fallback {
			return entry.route
		}
	}
	if route := pMux.getRoute(routeHTTPS); route != nil {
		return route
	}
	return nil
}

func (pMux *PortMux) tlsMatch(entry *registeredRoute, serverName string) bool {
	if entry == nil || entry.route == nil {
		return false
	}
	match := entry.matcher
	if match.fallback {
		return true
	}
	if serverName == "" {
		return match.allowEmptySNI
	}
	if match.host == "" {
		return match.allowEmptySNI
	}
	return strings.EqualFold(serverName, match.host)
}

func (pMux *PortMux) classifyTLS(conn net.Conn, first []byte) (*portRoute, []byte, bool) {
	tlsRoutes := pMux.routeEntries(routeFamilyTLS)
	if len(tlsRoutes) == 0 {
		return nil, nil, false
	}
	if len(tlsRoutes) == 1 && !tlsRoutes[0].matcher.fallback {
		return tlsRoutes[0].route, nil, true
	}

	helloInfo, rawData, err := crypt.ReadClientHello(conn, first)
	if err != nil {
		for _, entry := range tlsRoutes {
			if entry.matcher.fallback {
				return entry.route, rawData, true
			}
		}
		return nil, rawData, false
	}
	serverName := ""
	if helloInfo != nil {
		serverName = normalizeRouteHost(helloInfo.ServerName)
	}
	if serverName == "" && len(tlsRoutes) > 1 {
		return nil, rawData, false
	}
	for _, entry := range tlsRoutes {
		if entry.matcher.fallback {
			continue
		}
		if pMux.tlsMatch(entry, serverName) {
			return entry.route, rawData, true
		}
	}
	for _, entry := range tlsRoutes {
		if entry.matcher.fallback {
			return entry.route, rawData, true
		}
	}
	return nil, rawData, false
}
