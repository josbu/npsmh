package common

import (
	"bytes"
	"encoding/binary"
	"html/template"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/araddon/dateparse"
	"github.com/beevik/ntp"
	"github.com/djylb/nps/lib/logs"
)

const (
	CONN_DATA_SEQ        = "*#*" // Separator
	VERIFY_EER           = "vkey"
	VERIFY_SUCCESS       = "sucs"
	WORK_MAIN            = "main"
	WORK_CHAN            = "chan"
	WORK_VISITOR         = "vstr"
	WORK_CONFIG          = "conf"
	WORK_REGISTER        = "rgst"
	WORK_SECRET          = "sert"
	WORK_FILE            = "file"
	WORK_P2P             = "p2pm"
	WORK_P2P_RESOLVE     = "p2rv"
	WORK_P2P_SESSION     = "p2sj"
	WORK_P2P_VISITOR     = "p2pv"
	WORK_P2P_PROVIDER    = "p2pp"
	WORK_P2P_CONNECT     = "p2pc"
	WORK_P2P_SUCCESS     = "p2ps"
	WORK_P2P_END         = "p2pe"
	WORK_P2P_ACCEPT      = "p2pa"
	WORK_P2P_LAST        = "p2pl"
	WORK_P2P_NAT_PROBE   = "p2px"
	WORK_STATUS          = "stus"
	RES_MSG              = "msg0"
	RES_CLOSE            = "clse"
	NEW_UDP_CONN         = "udpc" // p2p udp conn
	P2P_PUNCH_START      = "p2st"
	P2P_ASSOCIATION_BIND = "p2bd"
	P2P_PROBE_REPORT     = "p2pr"
	P2P_PROBE_SUMMARY    = "p2sm"
	P2P_PUNCH_READY      = "p2rd"
	P2P_PUNCH_GO         = "p2go"
	P2P_PUNCH_PROGRESS   = "p2pg"
	P2P_PUNCH_ABORT      = "p2ab"
	NEW_TASK             = "task"
	NEW_CONF             = "conf"
	NEW_HOST             = "host"
	CONN_ALL             = "all"
	CONN_TCP             = "tcp"
	CONN_UDP             = "udp"
	CONN_KCP             = "kcp"
	CONN_TLS             = "tls"
	CONN_QUIC            = "quic"
	CONN_WEB             = "web"
	CONN_WS              = "ws"
	CONN_WSS             = "wss"
	CONN_TEST            = "TST"
	CONN_ACK             = "ACK"
	PING                 = "ping"
	PONG                 = "pong"
	TEST                 = "test"

	TOTP_SEQ = "totp:" // TOTP Separator

	UnauthorizedBytes = "HTTP/1.1 401 Unauthorized\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"WWW-Authenticate: Basic realm=\"easyProxy\"\r\n" +
		"\r\n" +
		"401 Unauthorized"

	ProxyAuthRequiredBytes = "HTTP/1.1 407 Proxy Authentication Required\r\n" +
		"Proxy-Authenticate: Basic realm=\"Proxy\"\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n" +
		"\r\n"

	ConnectionFailBytes = "HTTP/1.1 404 Not Found\r\n" +
		"\r\n"

	IPv4DNS = "8.8.8.8:53"
	IPv6DNS = "[2400:3200::1]:53"
)

var DefaultPort = map[string]string{
	"tcp":  "8024",
	"kcp":  "8024",
	"tls":  "8025",
	"quic": "8025",
	"ws":   "80",
	"wss":  "443",
}

const defaultNTPInterval = 5 * time.Minute

var (
	timeOffset   time.Duration
	ntpServer    string
	syncInterval = defaultNTPInterval
	lastSyncMono time.Time
	timeMutex    sync.RWMutex
	syncCh       = make(chan struct{}, 1)
	defaultTZ    = time.Local
)

func Max(values ...int) int {
	maxVal := math.MinInt
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}

func Min(values ...int) int {
	minVal := math.MaxInt
	for _, v := range values {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

// GetBoolByStr get bool by str
func GetBoolByStr(s string) bool {
	switch s {
	case "1", "true":
		return true
	}
	return false
}

// GetStrByBool get str by bool
func GetStrByBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// GetIntNoErrByStr int
func GetIntNoErrByStr(str string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(str))
	return i
}

// GetTimeNoErrByStr time
func GetTimeNoErrByStr(str string) time.Time {
	str = strings.TrimSpace(str)
	if str == "" {
		return time.Time{}
	}
	if timestamp, err := strconv.ParseInt(str, 10, 64); err == nil {
		if timestamp > 1_000_000_000_000 {
			return time.UnixMilli(timestamp)
		}
		return time.Unix(timestamp, 0)
	}
	t, err := dateparse.ParseLocal(str)
	if err == nil {
		return t
	}
	return time.Time{}
}

func ContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// BytesToNum convert bytes to num
func BytesToNum(b []byte) int {
	value := 0
	for _, item := range b {
		switch {
		case item >= 100:
			value = value*1000 + int(item)
		case item >= 10:
			value = value*100 + int(item)
		default:
			value = value*10 + int(item)
		}
	}
	return value
}

// TestTcpPort Judge whether the TCP port can open normally
func TestTcpPort(port int) bool {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// TestUdpPort Judge whether the UDP port can open normally
func TestUdpPort(port int) bool {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	defer func() {
		if l != nil {
			_ = l.Close()
		}
	}()
	return err == nil
}

// BinaryWrite Write length and individual byte data
// Length prevents sticking
// # Characters are used to separate data
func BinaryWrite(raw *bytes.Buffer, v ...string) {
	b := GetWriteStr(v...)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = raw.Write(lenBuf[:])
	_, _ = raw.Write(b)
}

// GetWriteStr get seq str
func GetWriteStr(v ...string) []byte {
	sep := CONN_DATA_SEQ
	sepLen := len(sep)
	total := 0
	for _, s := range v {
		total += len(s) + sepLen
	}
	buffer := make([]byte, 0, total)
	for _, s := range v {
		buffer = append(buffer, s...)
		buffer = append(buffer, sep...)
	}
	return buffer
}

// InStrArr inArray str interface
func InStrArr(arr []string, val string) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// InIntArr inArray int interface
func InIntArr(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

func in(target string, strArray []string) bool {
	if len(strArray) == 0 {
		return false
	}
	sorted := append([]string(nil), strArray...)
	sort.Strings(sorted)
	index := sort.SearchStrings(sorted, target)
	return index < len(sorted) && sorted[index] == target
}

func IsBlackIp(ipPort, vkey string, blackIpList []string) bool {
	ip := GetIpByAddr(ipPort)
	if in(ip, blackIpList) {
		logs.Warn("IP [%s] is in the blacklist for [%s]", ip, vkey)
		return true
	}
	return false
}

// ParseStr parse template
func ParseStr(str string) (string, error) {
	tmp := template.New("npc")
	w := new(bytes.Buffer)
	tmp, err := tmp.Parse(str)
	if err != nil {
		return "", err
	}
	if err = tmp.Execute(w, GetEnvMap()); err != nil {
		return "", err
	}
	return w.String(), nil
}

// GetEnvMap get env
func GetEnvMap() map[string]string {
	m := make(map[string]string)
	environ := os.Environ()
	for i := range environ {
		tmp := strings.SplitN(environ[i], "=", 2)
		if len(tmp) == 2 {
			m[tmp[0]] = tmp[1]
		}
	}
	return m
}

// TrimArr throw the empty element of the string array
func TrimArr(arr []string) []string {
	newArr := make([]string, 0)
	for _, v := range arr {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			newArr = append(newArr, trimmed)
		}
	}
	return newArr
}

func IsArrContains(arr []string, val string) bool {
	if arr == nil {
		return false
	}
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

// RemoveArrVal remove value from string array
func RemoveArrVal(arr []string, val string) []string {
	for k, v := range arr {
		if v == val {
			return append(arr[:k], arr[k+1:]...)
		}
	}
	return arr
}

func HandleArrEmptyVal(list []string) []string {
	for len(list) > 0 && (list[len(list)-1] == "" || strings.TrimSpace(list[len(list)-1]) == "") {
		list = list[:len(list)-1]
	}
	for i := 0; i < len(list); i++ {
		list[i] = strings.TrimSpace(list[i])
		if i > 0 && list[i] == "" {
			list[i] = list[i-1]
		}
	}
	return list
}

func ExtendArrs(arrays ...*[]string) int {
	maxLength := 0
	for _, arr := range arrays {
		if len(*arr) > maxLength {
			maxLength = len(*arr)
		}
	}
	if maxLength == 0 {
		return 0
	}
	for _, arr := range arrays {
		for len(*arr) < maxLength {
			if len(*arr) == 0 {
				*arr = append(*arr, "")
			} else {
				*arr = append(*arr, (*arr)[len(*arr)-1])
			}
		}
	}
	return maxLength
}

// GetSyncMapLen get the length of the sync map
func GetSyncMapLen(m *sync.Map) int {
	var c int
	m.Range(func(key, value interface{}) bool {
		c++
		return true
	})
	return c
}

func SetNtpServer(server string) {
	timeMutex.Lock()
	defer timeMutex.Unlock()
	ntpServer = server
}

func SetNtpInterval(d time.Duration) {
	if d <= 0 {
		d = defaultNTPInterval
	}
	timeMutex.Lock()
	defer timeMutex.Unlock()
	syncInterval = d
}

func CalibrateTimeOffset(server string) (time.Duration, error) {
	if server == "" {
		return 0, nil
	}
	ntpTime, err := ntp.Time(server)
	if err != nil {
		return 0, err
	}
	return time.Until(ntpTime), nil
}

func TimeOffset() time.Duration {
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return timeOffset
}

func TimeNow() time.Time {
	SyncTime()
	timeMutex.RLock()
	defer timeMutex.RUnlock()
	return time.Now().Add(timeOffset)
}

func SyncTime() {
	timeMutex.RLock()
	srv, last, interval := ntpServer, lastSyncMono, syncInterval
	timeMutex.RUnlock()
	if srv == "" || (!last.IsZero() && time.Since(last) < interval) {
		return
	}
	select {
	case syncCh <- struct{}{}:
		defer func() { <-syncCh }()
	default:
		return
	}
	now := time.Now()
	timeMutex.Lock()
	lastSyncMono = now
	timeMutex.Unlock()
	offset, err := CalibrateTimeOffset(srv)
	if err != nil {
		logs.Error("ntp[%s] sync failed: %v", srv, err)
	}
	timeMutex.Lock()
	timeOffset = offset
	timeMutex.Unlock()
	if offset != 0 {
		logs.Info("ntp[%s] offset=%v", srv, offset)
	}
}

func SetTimezone(tz string) error {
	if tz == "" {
		time.Local = defaultTZ
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return err
	}
	time.Local = loc
	return nil
}

// TimestampToBytes 8bit
func TimestampToBytes(ts int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(ts))
	return b
}

// BytesToTimestamp 8bit
func BytesToTimestamp(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}
