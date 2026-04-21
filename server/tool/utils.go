package tool

import (
	"errors"
	"math"
	stdnet "net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

var (
	portMu  sync.RWMutex
	ports   []int
	portSet map[int]struct{}

	portRandSeed uint64

	statusCap  = 1440
	ssMu       sync.RWMutex
	statBuf    = make([]map[string]interface{}, statusCap)
	statIdx    = 0
	statFilled = false

	startOnce sync.Once
)

type portUsageTestFn func(port int) bool

func init() {
	atomic.StoreUint64(&portRandSeed, uint64(time.Now().UnixNano())+1)
}

type Dialer interface {
	DialVirtual(remote string) (stdnet.Conn, error)
	ServeVirtual(c stdnet.Conn)
}

var lookup atomic.Value // holds: func(int) (Dialer, bool)

func SetLookup(fn func(int) (Dialer, bool)) {
	lookup.Store(fn)
}

func loadTunnelLookup() (func(int) (Dialer, bool), error) {
	v := lookup.Load()
	if v == nil {
		return nil, errors.New("tunnel lookup not set")
	}
	fn, ok := v.(func(int) (Dialer, bool))
	if !ok || fn == nil {
		return nil, errors.New("invalid tunnel lookup")
	}
	return fn, nil
}

func GetTunnelConn(id int, remote string) (stdnet.Conn, error) {
	fn, err := loadTunnelLookup()
	if err != nil {
		return nil, err
	}
	d, ok := fn(id)
	if !ok || d == nil {
		return nil, errors.New("tunnel not found")
	}
	return d.DialVirtual(remote)
}

var WebServerListener *conn.VirtualListener

func GetWebServerConn(remote string) (stdnet.Conn, error) {
	if WebServerListener == nil {
		return nil, errors.New("web server not set")
	}
	return WebServerListener.DialVirtual(remote)
}

func StartSystemInfo() {
	if servercfg.Current().Feature.SystemInfoDisplay {
		startOnce.Do(func() {
			go getServerStatus()
		})
	}
}

func InitAllowPort() {
	setAllowedPorts(common.GetPorts(servercfg.Current().Feature.AllowPorts))
}

func setAllowedPorts(p []int) {
	portMu.Lock()
	defer portMu.Unlock()
	ports = p
	portSet = buildAllowPortSet(p)
}

func buildAllowPortSet(p []int) map[int]struct{} {
	if len(p) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(p))
	for _, port := range p {
		set[port] = struct{}{}
	}
	return set
}

func allowPortSnapshot() ([]int, map[int]struct{}) {
	portMu.RLock()
	defer portMu.RUnlock()
	return ports, portSet
}

func TestServerPort(p int, m string) (b bool) {
	return testPortUsage(p, m, m == "udp")
}

func TestTunnelPort(t *file.Tunnel) bool {
	if t == nil {
		return false
	}
	return testPortUsage(t.Port, t.Mode, tunnelNeedsUDP(t))
}

func testPortUsage(p int, m string, needUDP bool) (b bool) {
	return testPortUsageWithFns(p, m, needUDP, common.TestTcpPort, common.TestUdpPort)
}

func testPortUsageWithFns(p int, m string, needUDP bool, tcpPortOK, udpPortOK portUsageTestFn) (b bool) {
	if m == "p2p" || m == "secret" {
		return true
	}
	if p > 65535 || p < 0 {
		return false
	}
	_, allowedSet := allowPortSnapshot()
	if len(allowedSet) != 0 {
		if _, ok := allowedSet[p]; !ok {
			return false
		}
	}
	needTCP := m != "udp"
	if needTCP && !tcpPortOK(p) {
		return false
	}
	if needUDP && !udpPortOK(p) {
		return false
	}
	return true
}

func tunnelNeedsUDP(t *file.Tunnel) bool {
	if t == nil {
		return false
	}
	switch t.Mode {
	case "udp", "socks5":
		return true
	case "mixProxy":
		return t.Socks5Proxy
	default:
		return false
	}
}

func GenerateServerPort(m string) int {
	return generateServerPortWithFns(m, common.TestTcpPort, common.TestUdpPort)
}

func generateServerPortWithFns(m string, tcpPortOK, udpPortOK portUsageTestFn) int {
	allowedPorts, _ := allowPortSnapshot()
	if len(allowedPorts) > 0 {
		return pickAllowedPort(allowedPorts, func(port int) bool {
			return testPortUsageWithFns(port, m, m == "udp", tcpPortOK, udpPortOK)
		})
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := randomDynamicPort()
			if testPortUsageWithFns(serverPort, m, m == "udp", tcpPortOK, udpPortOK) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			if testPortUsageWithFns(p, m, m == "udp", tcpPortOK, udpPortOK) {
				return p
			}
		}
	}
	return 0
}

func GenerateTunnelPort(t *file.Tunnel) int {
	return generateTunnelPortWithFns(t, common.TestTcpPort, common.TestUdpPort)
}

func generateTunnelPortWithFns(t *file.Tunnel, tcpPortOK, udpPortOK portUsageTestFn) int {
	if t == nil {
		return 0
	}
	probe := &file.Tunnel{
		Mode:        t.Mode,
		Socks5Proxy: t.Socks5Proxy,
	}
	allowedPorts, _ := allowPortSnapshot()
	if len(allowedPorts) > 0 {
		return pickAllowedPort(allowedPorts, func(port int) bool {
			probe.Port = port
			return testPortUsageWithFns(probe.Port, probe.Mode, tunnelNeedsUDP(probe), tcpPortOK, udpPortOK)
		})
	} else {
		for attempt := 0; attempt < 1000; attempt++ {
			serverPort := randomDynamicPort()
			probe.Port = serverPort
			if testPortUsageWithFns(probe.Port, probe.Mode, tunnelNeedsUDP(probe), tcpPortOK, udpPortOK) {
				return serverPort
			}
		}
		for p := 1024; p <= 65535; p++ {
			probe.Port = p
			if testPortUsageWithFns(probe.Port, probe.Mode, tunnelNeedsUDP(probe), tcpPortOK, udpPortOK) {
				return p
			}
		}
	}
	return 0
}

func pickAllowedPort(allowedPorts []int, usable func(port int) bool) int {
	if len(allowedPorts) == 0 {
		return 0
	}
	start := int(nextPortRand() % uint64(len(allowedPorts)))
	for offset := 0; offset < len(allowedPorts); offset++ {
		port := allowedPorts[(start+offset)%len(allowedPorts)]
		if port == 0 {
			continue
		}
		if usable(port) {
			return port
		}
	}
	return 0
}

func randomDynamicPort() int {
	const minPort = 1024
	const span = 65535 - minPort + 1
	return minPort + int(nextPortRand()%span)
}

func nextPortRand() uint64 {
	state := atomic.AddUint64(&portRandSeed, 0x9e3779b97f4a7c15)
	state ^= state >> 30
	state *= 0xbf58476d1ce4e5b9
	state ^= state >> 27
	state *= 0x94d049bb133111eb
	return state ^ (state >> 31)
}

func statusCount() int {
	ssMu.RLock()
	defer ssMu.RUnlock()
	if statFilled {
		return statusCap
	}
	return statIdx
}

func StatusSnapshot() []map[string]interface{} {
	ssMu.RLock()
	defer ssMu.RUnlock()

	if !statFilled {
		out := make([]map[string]interface{}, statIdx)
		for i := 0; i < statIdx; i++ {
			out[i] = cloneStatusEntry(statBuf[i])
		}
		return out
	}
	out := make([]map[string]interface{}, statusCap)
	for i := 0; i < statusCap; i++ {
		out[i] = cloneStatusEntry(statBuf[(statIdx+i)%statusCap])
	}
	return out
}

func ChartDeciles() []map[string]interface{} {
	ssMu.RLock()
	defer ssMu.RUnlock()

	var n, start int
	if statFilled {
		n, start = statusCap, statIdx
	} else {
		n, start = statIdx, 0
	}
	if n == 0 {
		return nil
	}
	if n <= 10 {
		out := make([]map[string]interface{}, n)
		for i := 0; i < n; i++ {
			out[i] = cloneStatusEntry(statBuf[(start+i)%statusCap])
		}
		return out
	}
	out := make([]map[string]interface{}, 10)
	for i := 0; i < 10; i++ {
		pos := (i * (n - 1)) / 9
		idx := (start + pos) % statusCap
		out[i] = cloneStatusEntry(statBuf[idx])
	}
	return out
}

func cloneStatusEntry(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneStatusValue(value)
	}
	return dst
}

func cloneStatusValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneStatusEntry(v)
	case []interface{}:
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = cloneStatusValue(item)
		}
		return items
	default:
		return value
	}
}

func getServerStatus() {
	for {
		if statusCount() < 10 {
			time.Sleep(1 * time.Second)
		} else {
			time.Sleep(1 * time.Minute)
		}

		m := make(map[string]interface{}, 12)

		// CPU
		if cpuPercent, err := cpu.Percent(0, true); err == nil && len(cpuPercent) > 0 {
			var sum float64
			for _, v := range cpuPercent {
				sum += v
			}
			m["cpu"] = math.Round(sum / float64(len(cpuPercent)))
		}

		// Load
		if loads, err := load.Avg(); err == nil {
			m["load1"] = loads.Load1
			m["load5"] = loads.Load5
			m["load15"] = loads.Load15
		}

		// Mem
		if swap, err := mem.SwapMemory(); err == nil {
			m["swap_mem"] = math.Round(swap.UsedPercent)
		}
		if vir, err := mem.VirtualMemory(); err == nil {
			m["virtual_mem"] = math.Round(vir.UsedPercent)
		}

		// Conn
		if pcounters, err := psnet.ProtoCounters(nil); err == nil {
			for _, v := range pcounters {
				if val, ok := v.Stats["CurrEstab"]; ok {
					m[v.Protocol] = val // int64
				}
			}
		}

		// IO
		if io1, err := psnet.IOCounters(false); err == nil {
			time.Sleep(500 * time.Millisecond)
			if io2, err2 := psnet.IOCounters(false); err2 == nil && len(io1) > 0 && len(io2) > 0 {
				m["io_send"] = (io2[0].BytesSent - io1[0].BytesSent) * 2
				m["io_recv"] = (io2[0].BytesRecv - io1[0].BytesRecv) * 2
			}
		}

		// Time
		t := time.Now()
		m["time"] = t.Format("15:04:05")

		ssMu.Lock()
		statBuf[statIdx] = m
		statIdx = (statIdx + 1) % statusCap
		if statIdx == 0 {
			statFilled = true
		}
		ssMu.Unlock()
	}
}
