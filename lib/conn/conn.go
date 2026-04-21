package conn

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/pmux"
	"github.com/golang/snappy"
	"github.com/xtaci/kcp-go/v5"
)

var LocalTCPAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}
var requestEndBytes = []byte("\r\n\r\n")

type Conn struct {
	Conn       net.Conn
	rbs        [][]byte
	wBuf       *bytes.Buffer
	mu         sync.Mutex
	rbMu       sync.Mutex
	hooksMu    sync.Mutex
	closed     uint32
	closeHooks []func(*Conn)
}

// NewConn new conn
func NewConn(conn net.Conn) *Conn {
	return &Conn{
		Conn: conn,
	}
}

func (s *Conn) SetRb(rbs ...[]byte) *Conn {
	s.rbMu.Lock()
	defer s.rbMu.Unlock()
	for _, rb := range rbs {
		if len(rb) > 0 {
			s.rbs = append(s.rbs, rb)
		}
	}
	return s
}

func (s *Conn) OnClose(fn func(*Conn)) *Conn {
	if fn == nil {
		return s
	}
	s.hooksMu.Lock()
	defer s.hooksMu.Unlock()
	if s.IsClosed() {
		return s
	}
	s.closeHooks = append(s.closeHooks, fn)
	return s
}

func (s *Conn) readRequest(buf []byte) (n int, err error) {
	var rd int
	for {
		rd, err = s.Read(buf[n:])
		if err != nil {
			return
		}
		if rd == 0 {
			err = io.ErrNoProgress
			return
		}
		n += rd
		if n < 4 {
			continue
		}
		if bytes.Equal(buf[n-4:n], requestEndBytes) {
			return
		}
		// buf is full, can't contain the request
		if n == cap(buf) {
			err = io.ErrUnexpectedEOF
			return
		}
	}
}

// GetHost get host 、connection type、method...from connection
func (s *Conn) GetHost() (method, address string, rb []byte, r *http.Request, err error) {
	var b [32 * 1024]byte
	var n int
	if n, err = s.readRequest(b[:]); err != nil {
		return
	}
	rb = b[:n]
	r, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(rb)))
	if err != nil {
		return
	}
	method = r.Method
	address = normalizeRequestHostAddress(r)
	return
}

func (s *Conn) GetShortLenContent() (b []byte, err error) {
	var l int
	if l, err = s.GetLen(); err != nil {
		return
	}
	if l < 0 || l > 32<<10 {
		err = errors.New("read length error")
		return
	}
	return s.GetShortContent(l)
}

func (s *Conn) GetShortContent(l int) (b []byte, err error) {
	buf := make([]byte, l)
	_, err = io.ReadFull(s, buf)
	return buf, err
}

func (s *Conn) ReadLen(cLen int, buf []byte) (int, error) {
	if cLen < 0 || cLen > len(buf) {
		return 0, errors.New("invalid length: " + strconv.Itoa(cLen))
	}
	if cLen == 0 {
		return 0, nil
	}
	n, err := io.ReadFull(s, buf[:cLen])
	if err != nil || n != cLen {
		return n, fmt.Errorf("error reading %d bytes: %w", cLen, err)
	}
	return cLen, nil
}

func (s *Conn) GetLen() (int, error) {
	var l int32
	err := binary.Read(s, binary.LittleEndian, &l)
	return int(l), err
}

func (s *Conn) WriteLenContent(buf []byte) (err error) {
	var b []byte
	if b, err = GetLenBytes(buf); err != nil {
		return
	}
	//return binary.Write(s.Conn, binary.LittleEndian, b)
	_, err = s.BufferWrite(b)
	return
}

// ReadFlag read flag
func (s *Conn) ReadFlag() (string, error) {
	var buf [4]byte
	_, err := io.ReadFull(s, buf[:])
	return string(buf[:]), err
}

// SetAlive set alive
func (s *Conn) SetAlive() {
	if s == nil {
		return
	}
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Time{})
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Time{})
	case *pmux.PortConn:
		_ = s.Conn.(*pmux.PortConn).SetReadDeadline(time.Time{})
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Time{})
	case *TlsConn:
		_ = s.Conn.(*TlsConn).SetReadDeadline(time.Time{})
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Time{})
		}
	}
}

// SetReadDeadlineBySecond set read deadline
func (s *Conn) SetReadDeadlineBySecond(t time.Duration) {
	if s == nil {
		return
	}
	switch s.Conn.(type) {
	case *kcp.UDPSession:
		_ = s.Conn.(*kcp.UDPSession).SetReadDeadline(time.Now().Add(t * time.Second))
	case *net.TCPConn:
		_ = s.Conn.(*net.TCPConn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *pmux.PortConn:
		_ = s.Conn.(*pmux.PortConn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *tls.Conn:
		_ = s.Conn.(*tls.Conn).SetReadDeadline(time.Now().Add(t * time.Second))
	case *TlsConn:
		_ = s.Conn.(*TlsConn).SetReadDeadline(time.Now().Add(t * time.Second))
	default:
		if conn, ok := s.Conn.(interface{ SetReadDeadline(time.Time) error }); ok {
			_ = conn.SetReadDeadline(time.Now().Add(t * time.Second))
		}
	}
}

// GetLinkInfo get link info from conn
func (s *Conn) GetLinkInfo() (lk *Link, err error) {
	err = s.getInfo(&lk)
	if err == nil {
		lk = normalizeLink(lk)
	}
	return
}

// SendHealthInfo send info for link
func (s *Conn) SendHealthInfo(info, status string) (int, error) {
	var raw bytes.Buffer
	common.BinaryWrite(&raw, info, status)
	return s.Write(raw.Bytes())
}

// GetHealthInfo get health info from conn
func (s *Conn) GetHealthInfo() (info string, status bool, err error) {
	//_ = s.SetReadDeadline(time.Now().Add(timeout))
	//defer s.SetReadDeadline(time.Time{})
	var l int
	l, err = s.GetLen()
	if err != nil {
		return
	}
	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)
	_, err = s.ReadLen(l, buf)
	if err != nil {
		return
	}
	raw := string(buf[:l])
	info, statusRaw, ok := strings.Cut(raw, common.CONN_DATA_SEQ)
	if !ok {
		return "", false, errors.New("receive health info error")
	}
	return info, common.GetBoolByStr(statusRaw), nil
}

// GetHostInfo get task info
func (s *Conn) GetHostInfo() (h *file.Host, err error) {
	err = s.getInfo(&h)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("receive host info error")
	}
	h.Id = file.GetDb().NextHostID()
	h.Flow = new(file.Flow)
	h.NoStore = true
	return
}

// GetConfigInfo get task info
func (s *Conn) GetConfigInfo() (c *file.Client, err error) {
	err = s.getInfo(&c)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errors.New("receive client info error")
	}
	c.NoStore = true
	c.Status = true
	if c.Flow == nil {
		c.Flow = new(file.Flow)
	}
	c.NoDisplay = false
	return
}

// GetTaskInfo get task info
func (s *Conn) GetTaskInfo() (t *file.Tunnel, err error) {
	err = s.getInfo(&t)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("receive task info error")
	}
	t.Id = file.GetDb().NextTaskID()
	t.NoStore = true
	t.Flow = new(file.Flow)
	return
}

// SendInfo send  info
func (s *Conn) SendInfo(t interface{}, flag string) (int, error) {
	/*
		The task info is formed as follows:
		+----+-----+---------+
		|type| len | content |
		+----+---------------+
		| 4  |  4  |   ...   |
		+----+---------------+
	*/
	b, err := json.Marshal(t)
	if err != nil {
		return 0, err
	}
	raw := make([]byte, 0, len(flag)+4+len(b))
	if flag != "" {
		raw = append(raw, flag...)
	}
	raw, err = appendLenBytes(raw, b)
	if err != nil {
		return 0, err
	}
	return s.Write(raw)
}

// get task info
func (s *Conn) getInfo(t interface{}) (err error) {
	var l int
	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)
	if l, err = s.GetLen(); err != nil {
		return
	}
	if _, err = s.ReadLen(l, buf); err != nil {
		return
	}
	return json.Unmarshal(buf[:l], t)
}

func (s *Conn) IsClosed() bool {
	return atomic.LoadUint32(&s.closed) == 1
}

func (s *Conn) Close() error {
	if s == nil || s.Conn == nil {
		return net.ErrClosed
	}
	if atomic.CompareAndSwapUint32(&s.closed, 0, 1) {
		s.hooksMu.Lock()
		hooks := s.closeHooks
		s.closeHooks = nil
		s.hooksMu.Unlock()
		for _, h := range hooks {
			func() {
				defer func() { _ = recover() }()
				h(s)
			}()
		}
		s.rbMu.Lock()
		for i := range s.rbs {
			s.rbs[i] = nil
		}
		s.rbs = nil
		s.rbMu.Unlock()
		s.mu.Lock()
		if s.wBuf != nil {
			s.wBuf.Reset()
		}
		s.mu.Unlock()
		return s.Conn.Close()
	}
	return errors.New("connection already closed")
}

// write
func (s *Conn) Write(b []byte) (n int, err error) {
	if s == nil || s.Conn == nil {
		return 0, net.ErrClosed
	}
	if s.IsClosed() {
		return 0, errors.New("connection error")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil || s.wBuf.Len() == 0 {
		return writeAllCount(s.Conn, b)
	}
	n, err = s.wBuf.Write(b)
	if err != nil {
		return n, err
	}
	err = s.flushBufferLocked()
	return n, err
}

func (s *Conn) flushBufferLocked() error {
	if s.wBuf == nil || s.wBuf.Len() == 0 {
		return nil
	}
	pending := s.wBuf.Bytes()
	written, err := writeAllCount(s.Conn, pending)
	if written >= len(pending) {
		s.wBuf.Reset()
		return err
	}

	// Preserve the unsent tail so a later FlushBuf/Read can retry it.
	remaining := append([]byte(nil), pending[written:]...)
	s.wBuf.Reset()
	_, _ = s.wBuf.Write(remaining)
	return err
}

func writeAllCount(w io.Writer, p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			total += n
			p = p[n:]
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (s *Conn) BufferWrite(b []byte) (int, error) {
	if s == nil || s.Conn == nil {
		return 0, net.ErrClosed
	}
	if s.IsClosed() {
		return 0, errors.New("connection error")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wBuf == nil {
		s.wBuf = new(bytes.Buffer)
	}
	return s.wBuf.Write(b)
}

func (s *Conn) FlushBuf() error {
	if s == nil || s.Conn == nil {
		return net.ErrClosed
	}
	if s.IsClosed() {
		return errors.New("connection closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushBufferLocked()
}

// read
func (s *Conn) Read(b []byte) (n int, err error) {
	if s == nil || s.Conn == nil {
		return 0, net.ErrClosed
	}
	if err = s.FlushBuf(); err != nil {
		return 0, err
	}

	s.rbMu.Lock()
	for len(s.rbs) > 0 {
		cur := s.rbs[0]
		if len(cur) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			continue
		}
		n = copy(b, cur)
		s.rbs[0] = cur[n:]
		if len(s.rbs[0]) == 0 {
			s.rbs[0] = nil
			s.rbs = s.rbs[1:]
			if len(s.rbs) == 0 {
				s.rbs = nil
			}
		}
		s.rbMu.Unlock()
		return n, nil
	}
	s.rbMu.Unlock()

	return s.Conn.Read(b)
}

// WriteClose write sign flag
func (s *Conn) WriteClose() (int, error) {
	return s.Write([]byte(common.RES_CLOSE))
}

// WriteMain write main
func (s *Conn) WriteMain() (int, error) {
	return s.Write([]byte(common.WORK_MAIN))
}

// WriteConfig write config
func (s *Conn) WriteConfig() (int, error) {
	return s.Write([]byte(common.WORK_CONFIG))
}

// WriteChan write chan
func (s *Conn) WriteChan() (int, error) {
	return s.Write([]byte(common.WORK_CHAN))
}

// GetAddStatus get task or host result of add
func (s *Conn) GetAddStatus() (b bool) {
	_ = binary.Read(s, binary.LittleEndian, &b)
	return
}

func (s *Conn) WriteAddOk() error {
	return binary.Write(s, binary.LittleEndian, true)
}

func (s *Conn) WriteAddFail() error {
	defer func() { _ = s.Close() }()
	return binary.Write(s, binary.LittleEndian, false)
}

func (s *Conn) LocalAddr() net.Addr {
	if s == nil || s.Conn == nil {
		return nil
	}
	return s.Conn.LocalAddr()
}

func (s *Conn) RemoteAddr() net.Addr {
	if s == nil || s.Conn == nil {
		return nil
	}
	return s.Conn.RemoteAddr()
}

func (s *Conn) SetDeadline(t time.Time) error {
	if s == nil || s.Conn == nil {
		return net.ErrClosed
	}
	return s.Conn.SetDeadline(t)
}

func (s *Conn) SetWriteDeadline(t time.Time) error {
	if s == nil || s.Conn == nil {
		return net.ErrClosed
	}
	return s.Conn.SetWriteDeadline(t)
}

func (s *Conn) SetReadDeadline(t time.Time) error {
	if s == nil || s.Conn == nil {
		return net.ErrClosed
	}
	return s.Conn.SetReadDeadline(t)
}

func normalizeRequestHostAddress(r *http.Request) string {
	host := r.Host
	if host == "" && r.URL != nil {
		host = r.URL.Host
	}
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Host != "" {
		host = parsed.Host
		if _, _, err := net.SplitHostPort(host); err == nil {
			return host
		}
	}
	defaultPort := "80"
	if r.Method == http.MethodConnect {
		defaultPort = "443"
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return net.JoinHostPort(host, defaultPort)
}

type wrappedConn struct {
	rwc         io.ReadWriteCloser
	parent      net.Conn
	closeParent bool
}

func WrapConn(rwc io.ReadWriteCloser, parent net.Conn) net.Conn {
	return wrapConnWithCloseMode(rwc, parent, true)
}

func wrapConnWithoutParentClose(rwc io.ReadWriteCloser, parent net.Conn) net.Conn {
	return wrapConnWithCloseMode(rwc, parent, false)
}

func wrapConnWithCloseMode(rwc io.ReadWriteCloser, parent net.Conn, closeParent bool) net.Conn {
	return &wrappedConn{rwc: rwc, parent: parent, closeParent: closeParent}
}

func (w *wrappedConn) Read(b []byte) (int, error) {
	if w == nil || w.rwc == nil {
		return 0, net.ErrClosed
	}
	return w.rwc.Read(b)
}

func (w *wrappedConn) Write(b []byte) (int, error) {
	if w == nil || w.rwc == nil {
		return 0, net.ErrClosed
	}
	return w.rwc.Write(b)
}

func (w *wrappedConn) Close() error {
	if w == nil {
		return nil
	}
	var err1, err2 error
	if w.rwc != nil {
		err1 = w.rwc.Close()
	}
	if w.closeParent && w.parent != nil && rawConnOf(w.rwc) != w.parent {
		err2 = w.parent.Close()
	}
	return errors.Join(err1, err2)
}

func (w *wrappedConn) LocalAddr() net.Addr {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.LocalAddr()
}

func (w *wrappedConn) RemoteAddr() net.Addr {
	if w == nil || w.parent == nil {
		return nil
	}
	return w.parent.RemoteAddr()
}

func (w *wrappedConn) SetDeadline(t time.Time) error {
	if w == nil || w.parent == nil {
		return net.ErrClosed
	}
	return w.parent.SetDeadline(t)
}

func (w *wrappedConn) SetReadDeadline(t time.Time) error {
	if w == nil || w.parent == nil {
		return net.ErrClosed
	}
	return w.parent.SetReadDeadline(t)
}

func (w *wrappedConn) SetWriteDeadline(t time.Time) error {
	if w == nil || w.parent == nil {
		return net.ErrClosed
	}
	return w.parent.SetWriteDeadline(t)
}

func (w *wrappedConn) GetRawConn() net.Conn {
	if w == nil {
		return nil
	}
	return w.parent
}

type AddrOverrideConn struct {
	net.Conn
	lAddr net.Addr
	rAddr net.Addr
}

func NewAddrOverrideConn(base net.Conn, remote, local string) (*AddrOverrideConn, error) {
	if base == nil {
		return nil, fmt.Errorf("base conn is nil")
	}
	rAddr, err := parseTCPAddrMaybe(remote)
	if err != nil {
		return nil, fmt.Errorf("invalid remote addr %q: %w", remote, err)
	}
	lAddr, _ := parseTCPAddrMaybe(local)
	return &AddrOverrideConn{
		Conn:  base,
		lAddr: lAddr,
		rAddr: rAddr,
	}, nil
}

func NewAddrOverrideFromAddr(base net.Conn, remote, local net.Addr) *AddrOverrideConn {
	return &AddrOverrideConn{
		Conn:  base,
		lAddr: local,
		rAddr: remote,
	}
}

func (c *AddrOverrideConn) Read(b []byte) (int, error) {
	if c == nil || c.Conn == nil {
		return 0, net.ErrClosed
	}
	return c.Conn.Read(b)
}

func (c *AddrOverrideConn) Write(b []byte) (int, error) {
	if c == nil || c.Conn == nil {
		return 0, net.ErrClosed
	}
	return c.Conn.Write(b)
}

func (c *AddrOverrideConn) Close() error {
	if c == nil || c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

func (c *AddrOverrideConn) LocalAddr() net.Addr {
	if c == nil {
		return nil
	}
	if c.lAddr != nil {
		return c.lAddr
	}
	if c.Conn == nil {
		return nil
	}
	return c.Conn.LocalAddr()
}

func (c *AddrOverrideConn) RemoteAddr() net.Addr {
	if c == nil {
		return nil
	}
	if c.rAddr != nil {
		return c.rAddr
	}
	if c.Conn == nil {
		return nil
	}
	return c.Conn.RemoteAddr()
}

func (c *AddrOverrideConn) SetDeadline(t time.Time) error {
	if c == nil || c.Conn == nil {
		return net.ErrClosed
	}
	return c.Conn.SetDeadline(t)
}

func (c *AddrOverrideConn) SetReadDeadline(t time.Time) error {
	if c == nil || c.Conn == nil {
		return net.ErrClosed
	}
	return c.Conn.SetReadDeadline(t)
}

func (c *AddrOverrideConn) SetWriteDeadline(t time.Time) error {
	if c == nil || c.Conn == nil {
		return net.ErrClosed
	}
	return c.Conn.SetWriteDeadline(t)
}

func (c *AddrOverrideConn) GetRawConn() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}

func rawConnOf(v any) net.Conn {
	if v == nil {
		return nil
	}
	if getter, ok := v.(interface{ GetRawConn() net.Conn }); ok {
		return getter.GetRawConn()
	}
	if conn, ok := v.(net.Conn); ok {
		return conn
	}
	return nil
}

type LenConn struct {
	conn io.Writer
	Len  int
}

func NewLenConn(conn io.Writer) *LenConn {
	return &LenConn{conn: conn}
}

func (c *LenConn) Write(p []byte) (n int, err error) {
	if c == nil || c.conn == nil {
		return 0, net.ErrClosed
	}
	n, err = c.conn.Write(p)
	c.Len += n
	return
}

type RWConn struct {
	io.ReadWriteCloser
	FakeAddr net.Addr
}

func NewRWConn(conn io.ReadWriteCloser) *RWConn {
	return &RWConn{
		ReadWriteCloser: conn,
		FakeAddr:        LocalTCPAddr,
	}
}

func (c *RWConn) Read(p []byte) (int, error) {
	if c == nil || c.ReadWriteCloser == nil {
		return 0, net.ErrClosed
	}
	return c.ReadWriteCloser.Read(p)
}

func (c *RWConn) Write(p []byte) (int, error) {
	if c == nil || c.ReadWriteCloser == nil {
		return 0, net.ErrClosed
	}
	return c.ReadWriteCloser.Write(p)
}

func (c *RWConn) Close() error {
	if c == nil || c.ReadWriteCloser == nil {
		return nil
	}
	return c.ReadWriteCloser.Close()
}

func (c *RWConn) LocalAddr() net.Addr {
	if c == nil {
		return nil
	}
	return c.FakeAddr
}

func (c *RWConn) RemoteAddr() net.Addr {
	if c == nil {
		return nil
	}
	return c.FakeAddr
}

func (c *RWConn) SetDeadline(_ time.Time) error      { return nil }
func (c *RWConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *RWConn) SetWriteDeadline(_ time.Time) error { return nil }

type FlowConn struct {
	*RWConn
	taskFlow   *file.Flow
	clientFlow *file.Flow
}

func NewFlowConn(conn io.ReadWriteCloser, task, client *file.Flow) *FlowConn {
	return &FlowConn{
		RWConn:     NewRWConn(conn),
		taskFlow:   task,
		clientFlow: client,
	}
}

func (c *FlowConn) Read(p []byte) (int, error) {
	if c == nil || c.RWConn == nil {
		return 0, net.ErrClosed
	}
	n, err := c.RWConn.Read(p)
	n64 := int64(n)
	if c.taskFlow != nil {
		c.taskFlow.Add(0, n64)
	}
	if c.clientFlow != nil {
		c.clientFlow.Add(n64, n64)
	}
	return n, err
}

func (c *FlowConn) Write(p []byte) (int, error) {
	if c == nil || c.RWConn == nil {
		return 0, net.ErrClosed
	}
	n, err := c.RWConn.Write(p)
	n64 := int64(n)
	if c.taskFlow != nil {
		c.taskFlow.Add(n64, 0)
	}
	if c.clientFlow != nil {
		c.clientFlow.Add(n64, n64)
	}
	return n, err
}

type Secret struct {
	Password string
	Conn     *Conn
}

func NewSecret(p string, conn *Conn) *Secret {
	return &Secret{
		Password: p,
		Conn:     conn,
	}
}

type Link struct {
	ConnType   string
	Host       string
	Crypt      bool
	Compress   bool
	LocalProxy bool
	RemoteAddr string
	Option     Options
}

type Option func(*Options)

type Options struct {
	Timeout           time.Duration
	NeedAck           bool
	WaitConnectResult bool
	RouteUUID         string
}

var defaultTimeOut = 5 * time.Second

func NewLink(connType string, host string, crypt bool, compress bool, remoteAddr string, localProxy bool, opts ...Option) *Link {
	options := newOptions(opts...)

	return &Link{
		RemoteAddr: remoteAddr,
		ConnType:   connType,
		Host:       host,
		Crypt:      crypt,
		Compress:   compress,
		LocalProxy: localProxy,
		Option:     options,
	}
}

func newOptions(opts ...Option) Options {
	opt := Options{
		Timeout:           defaultTimeOut,
		NeedAck:           false,
		WaitConnectResult: false,
	}
	for _, o := range opts {
		o(&opt)
	}
	return opt
}

func LinkTimeout(t time.Duration) Option {
	return func(opt *Options) {
		opt.Timeout = normalizeLinkTimeout(t)
	}
}

func WithAck(enabled bool) Option {
	return func(opt *Options) {
		opt.NeedAck = enabled
	}
}

func WithConnectResult(enabled bool) Option {
	return func(opt *Options) {
		opt.WaitConnectResult = enabled
	}
}

func WithRouteUUID(uuid string) Option {
	return func(opt *Options) {
		opt.RouteUUID = uuid
	}
}

func normalizeLink(lk *Link) *Link {
	if lk == nil {
		return nil
	}
	lk.Option.Timeout = normalizeLinkTimeout(lk.Option.Timeout)
	return lk
}

func normalizeLinkTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultTimeOut
	}
	return timeout
}

type SnappyConn struct {
	w   *snappy.Writer
	r   *snappy.Reader
	c   io.Closer
	raw net.Conn
}

func NewSnappyConn(conn io.ReadWriteCloser) *SnappyConn {
	c := new(SnappyConn)
	c.w = snappy.NewBufferedWriter(conn)
	c.r = snappy.NewReader(conn)
	c.c = conn
	c.raw = rawConnOf(conn)
	return c
}

func (s *SnappyConn) Write(b []byte) (n int, err error) {
	if s == nil || s.w == nil {
		return 0, net.ErrClosed
	}
	if n, err = s.w.Write(b); err != nil {
		return
	}
	if err = s.w.Flush(); err != nil {
		return
	}
	return
}

func (s *SnappyConn) Read(b []byte) (n int, err error) {
	if s == nil || s.r == nil {
		return 0, net.ErrClosed
	}
	return s.r.Read(b)
}

func (s *SnappyConn) Close() error {
	if s == nil {
		return nil
	}
	var err1, err2 error
	if s.w != nil {
		err1 = s.w.Close()
	}
	if s.c != nil {
		err2 = s.c.Close()
	}
	return errors.Join(err1, err2)
}

func (s *SnappyConn) GetRawConn() net.Conn {
	if s == nil {
		return nil
	}
	return s.raw
}

type ConnectResultStatus byte

const (
	connectResultFrameVersion byte = 1

	ConnectResultOK ConnectResultStatus = iota
	ConnectResultConnectionRefused
	ConnectResultHostUnreachable
	ConnectResultNetworkUnreachable
	ConnectResultNotAllowed
	_
	_
	_
	_
	_
	_
	_
	_
	_
	_
	ConnectResultServerFailure ConnectResultStatus = 255
)

func WriteConnectResult(c net.Conn, status ConnectResultStatus, timeout time.Duration) error {
	if c == nil {
		return net.ErrClosed
	}
	timeout = normalizeLinkTimeout(timeout)
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := c.Write([]byte{connectResultFrameVersion, byte(status)})
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

func ReadConnectResult(c net.Conn, timeout time.Duration) (ConnectResultStatus, error) {
	if c == nil {
		return ConnectResultServerFailure, net.ErrClosed
	}
	timeout = normalizeLinkTimeout(timeout)
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	var buf [2]byte
	_, err := io.ReadFull(c, buf[:])
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return ConnectResultServerFailure, err
	}
	if buf[0] != connectResultFrameVersion {
		return ConnectResultServerFailure, io.ErrUnexpectedEOF
	}
	return ConnectResultStatus(buf[1]), nil
}

func DialConnectResult(err error) ConnectResultStatus {
	if err == nil {
		return ConnectResultOK
	}
	if IsTimeout(err) {
		return ConnectResultNetworkUnreachable
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ConnectResultHostUnreachable
	}

	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return ConnectResultConnectionRefused
	case errors.Is(err, syscall.EHOSTUNREACH):
		return ConnectResultHostUnreachable
	case errors.Is(err, syscall.ENETUNREACH):
		return ConnectResultNetworkUnreachable
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return ConnectResultNotAllowed
	}

	msg := normalizeNetErrorText(err)
	switch {
	case strings.Contains(msg, "connectionrefused"),
		strings.Contains(msg, "activelyrefusedit"):
		return ConnectResultConnectionRefused
	case strings.Contains(msg, "nosuchhost"),
		strings.Contains(msg, "nameorservicenotknown"),
		strings.Contains(msg, "temporaryfailureinnameresolution"),
		strings.Contains(msg, "servermisbehaving"),
		strings.Contains(msg, "hostunreachable"),
		strings.Contains(msg, "noroutetohost"):
		return ConnectResultHostUnreachable
	case strings.Contains(msg, "networkisunreachable"):
		return ConnectResultNetworkUnreachable
	case strings.Contains(msg, "permissiondenied"),
		strings.Contains(msg, "accessisdenied"):
		return ConnectResultNotAllowed
	default:
		return ConnectResultServerFailure
	}
}

type ByteObserver func(int64) error

type TrafficObserver struct {
	OnRead  ByteObserver
	OnWrite ByteObserver
}

type observedReadWriteCloser struct {
	rwc     io.ReadWriteCloser
	onRead  ByteObserver
	onWrite ByteObserver
}

func WrapReadWriteCloserWithTrafficObserver(rwc io.ReadWriteCloser, observer TrafficObserver) io.ReadWriteCloser {
	if rwc == nil || (observer.OnRead == nil && observer.OnWrite == nil) {
		return rwc
	}
	return &observedReadWriteCloser{
		rwc:     rwc,
		onRead:  observer.OnRead,
		onWrite: observer.OnWrite,
	}
}

func WrapNetConnWithTrafficObserver(conn net.Conn, observer TrafficObserver) net.Conn {
	if conn == nil || (observer.OnRead == nil && observer.OnWrite == nil) {
		return conn
	}
	return WrapConn(WrapReadWriteCloserWithTrafficObserver(conn, observer), conn)
}

func (c *observedReadWriteCloser) Read(p []byte) (int, error) {
	if c == nil || c.rwc == nil {
		return 0, net.ErrClosed
	}
	n, err := c.rwc.Read(p)
	if c.onRead != nil && n > 0 {
		if observeErr := c.onRead(int64(n)); observeErr != nil {
			return n, observeErr
		}
	}
	return n, err
}

func (c *observedReadWriteCloser) Write(p []byte) (int, error) {
	if c == nil || c.rwc == nil {
		return 0, net.ErrClosed
	}
	n, err := c.rwc.Write(p)
	if c.onWrite != nil && n > 0 {
		if observeErr := c.onWrite(int64(n)); observeErr != nil {
			return n, observeErr
		}
	}
	return n, err
}

func (c *observedReadWriteCloser) Close() error {
	if c == nil || c.rwc == nil {
		return nil
	}
	return c.rwc.Close()
}

func (c *observedReadWriteCloser) GetRawConn() net.Conn {
	if c == nil {
		return nil
	}
	return rawConnOf(c.rwc)
}
