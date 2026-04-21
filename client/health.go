package client

import (
	"container/heap"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

const minDelay = 10 * time.Millisecond

var healthCheckDialContext = func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	return dialer.DialContext(ctx, network, address)
}

type HealthChecker struct {
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	healths    []*file.Health
	serverConn *conn.Conn
	mu         sync.Mutex
	heap       *healthScheduleHeap
	timer      *time.Timer
	client     *http.Client
	idleCloser healthCheckIdleCloser
}

type healthSchedule struct {
	health *file.Health
	next   time.Time
}

type healthCheckIdleCloser interface {
	CloseIdleConnections()
}

type healthScheduleHeap []healthSchedule

func (h *healthScheduleHeap) Len() int { return len(*h) }

func (h *healthScheduleHeap) Less(i, j int) bool {
	left := (*h)[i].next
	right := (*h)[j].next
	if !left.Equal(right) {
		return left.Before(right)
	}
	return (*h)[i].health.HealthCheckTarget < (*h)[j].health.HealthCheckTarget
}

func (h *healthScheduleHeap) Swap(i, j int) {
	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
}

func (h *healthScheduleHeap) Push(x interface{}) {
	*h = append(*h, x.(healthSchedule))
}

func (h *healthScheduleHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func (h *healthScheduleHeap) Peek() (healthSchedule, bool) {
	if h == nil || len(*h) == 0 {
		return healthSchedule{}, false
	}
	return (*h)[0], true
}

func NewHealthChecker(parentCtx context.Context, healths []*file.Health, c *conn.Conn) *HealthChecker {
	parentCtx = normalizeClientParentContext(parentCtx)
	ctx, cancel := context.WithCancel(parentCtx)
	hq := newHealthScheduleHeap()
	initializeHealthScheduleHeap(hq, healths, time.Now(), false)
	client, idleCloser := newHealthCheckHTTPClient()

	tmr := time.NewTimer(0)
	if !tmr.Stop() {
		<-tmr.C
	}

	return &HealthChecker{
		ctx:        ctx,
		cancel:     cancel,
		healths:    healths,
		serverConn: c,
		heap:       hq,
		timer:      tmr,
		client:     client,
		idleCloser: idleCloser,
	}
}

func (hc *HealthChecker) Start() {
	hc.wg.Add(1)
	go func() {
		defer hc.wg.Done()
		hc.loop()
	}()
}

func (hc *HealthChecker) Stop() {
	if hc == nil {
		return
	}
	if hc.cancel != nil {
		hc.cancel()
	}
	hc.mu.Lock()
	if hc.timer != nil {
		stopAndDrain(hc.timer)
	}
	hc.mu.Unlock()
	hc.wg.Wait()
	closeHealthCheckIdleConnections(hc.idleCloser)
}

func (hc *HealthChecker) Reset() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.heap = newHealthScheduleHeap()
	initializeHealthScheduleHeap(hc.heap, hc.healths, time.Now(), true)
	stopAndDrain(hc.timer)
}

func (hc *HealthChecker) loop() {
	for {
		hc.mu.Lock()
		if hc.heap.Len() == 0 {
			hc.mu.Unlock()
			logs.Warn("health check list empty, exiting")
			return
		}
		next, _ := hc.heap.Peek()
		delay := time.Until(next.next)
		if delay < minDelay {
			delay = minDelay
		}
		stopAndDrain(hc.timer)
		hc.timer.Reset(delay)
		hc.mu.Unlock()
		select {
		case <-hc.ctx.Done():
			return
		case <-hc.timer.C:
			hc.runChecks()
		}
	}
}

func (hc *HealthChecker) runChecks() {
	now := time.Now()
	due := hc.popDueHealthSchedules(now)
	for _, item := range due {
		hc.doCheck(item.health)
	}
	hc.rescheduleHealths(due, now)
}

func newHealthScheduleHeap() *healthScheduleHeap {
	h := &healthScheduleHeap{}
	heap.Init(h)
	return h
}

func initializeHealthScheduleHeap(h *healthScheduleHeap, healths []*file.Health, now time.Time, afterInterval bool) {
	if h == nil {
		return
	}
	for _, health := range healths {
		if !isHealthConfigValid(health) {
			continue
		}
		next := now
		if afterInterval {
			next = nextHealthCheckTime(health, now, now)
		}
		health.HealthNextTime = next
		health.HealthMap = make(map[string]int)
		heap.Push(h, healthSchedule{health: health, next: next})
	}
}

func (hc *HealthChecker) popDueHealthSchedules(now time.Time) []healthSchedule {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.heap == nil || hc.heap.Len() == 0 {
		return nil
	}
	due := make([]healthSchedule, 0, hc.heap.Len())
	for hc.heap.Len() > 0 {
		next, ok := hc.heap.Peek()
		if !ok || next.next.After(now) {
			break
		}
		due = append(due, heap.Pop(hc.heap).(healthSchedule))
	}
	return due
}

func (hc *HealthChecker) rescheduleHealths(due []healthSchedule, now time.Time) {
	if len(due) == 0 {
		return
	}
	hc.mu.Lock()
	defer hc.mu.Unlock()
	for _, item := range due {
		if !isHealthConfigValid(item.health) {
			continue
		}
		next := nextHealthCheckTime(item.health, item.next, now)
		item.health.HealthNextTime = next
		heap.Push(hc.heap, healthSchedule{health: item.health, next: next})
	}
}

func nextHealthCheckTime(h *file.Health, previous, now time.Time) time.Time {
	interval := time.Duration(h.HealthCheckInterval) * time.Second
	if interval <= 0 {
		return now
	}
	next := previous.Add(interval)
	if next.After(now) {
		return next
	}
	missed := now.Sub(previous)/interval + 1
	return previous.Add(missed * interval)
}

func (hc *HealthChecker) doCheck(h *file.Health) {
	timeout := time.Duration(h.HealthCheckTimeout) * time.Second
	checkCtx := normalizeHealthCheckContext(hc.ctx)
	for _, target := range strings.Split(h.HealthCheckTarget, ",") {
		var err error
		switch h.HealthCheckType {
		case "tcp":
			dialCtx, cancel := context.WithTimeout(checkCtx, timeout)
			dialer := &net.Dialer{Timeout: timeout}
			c, errDial := healthCheckDialContext(dialCtx, dialer, "tcp", target)
			cancel()
			if errDial == nil {
				_ = c.Close()
			} else {
				err = errDial
			}
		case "http", "https":
			scheme := h.HealthCheckType
			url := fmt.Sprintf("%s://%s%s", scheme, target, h.HttpHealthUrl)
			ctx, cancel := context.WithTimeout(checkCtx, timeout)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, getErr := hc.client.Do(req)
			cancel()
			if getErr != nil {
				err = getErr
			} else {
				_, _ = io.Copy(io.Discard, resp.Body)
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("unexpected status %d", resp.StatusCode)
				}
				_ = resp.Body.Close()
			}
		default:
			err = fmt.Errorf("unsupported health check type: %s", h.HealthCheckType)
		}
		h.Lock()
		if err != nil {
			h.HealthMap[target]++
			if h.HealthMap[target]%h.HealthMaxFail == 0 {
				hc.sendHealthStatus(target, "0")
			}
		} else {
			if h.HealthMap[target] >= h.HealthMaxFail {
				hc.sendHealthStatus(target, "1")
			}
			h.HealthMap[target] = 0
		}
		h.Unlock()
	}
}

func newHealthCheckHTTPClient() (*http.Client, healthCheckIdleCloser) {
	transport := cloneHealthCheckTransport(http.DefaultTransport)
	return &http.Client{Transport: transport}, transport
}

func cloneHealthCheckTransport(base http.RoundTripper) *http.Transport {
	if transport, ok := base.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return &http.Transport{}
}

func normalizeHealthCheckContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func closeHealthCheckIdleConnections(closer healthCheckIdleCloser) {
	if closer != nil {
		closer.CloseIdleConnections()
	}
}

func stopAndDrain(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func isHealthConfigValid(h *file.Health) bool {
	return h != nil && h.HealthMaxFail > 0 && h.HealthCheckInterval > 0 && h.HealthCheckTimeout > 0
}

func (hc *HealthChecker) sendHealthStatus(target, status string) {
	if hc == nil || hc.serverConn == nil {
		return
	}
	_, _ = hc.serverConn.SendHealthInfo(target, status)
}
