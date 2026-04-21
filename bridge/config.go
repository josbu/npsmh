package bridge

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/tool"
)

var bridgeConfigEventEnqueueTimeout = 2 * time.Second

type bridgeConfigSession struct {
	isPub  bool
	client *file.Client
	ver    int
	vs     string
	uuid   string
}

func enqueueBridgeEvent[T any](ch chan T, value T) bool {
	if ch == nil {
		return false
	}
	timeout := bridgeConfigEventEnqueueTimeout
	if timeout <= 0 {
		select {
		case ch <- value:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case ch <- value:
		return true
	case <-timer.C:
		return false
	}
}

func (s *Bridge) enqueueOpenHost(host *file.Host) bool {
	return enqueueBridgeEvent(s.OpenHost, host)
}

func (s *Bridge) enqueueOpenTask(task *file.Tunnel) bool {
	return enqueueBridgeEvent(s.OpenTask, task)
}

func normalizeNewConfigClient(client *file.Client) *file.Client {
	if client == nil {
		return nil
	}
	// NEW_CONF is a registration path; runtime/db identity and login key stay server-assigned.
	client.Id = 0
	client.VerifyKey = ""
	return client
}

func writeConfigAddFail(c *conn.Conn) error {
	return binary.Write(c, binary.LittleEndian, false)
}

func rollbackNewConfigClient(client *file.Client) {
	if client == nil || client.Id == 0 {
		return
	}
	if err := currentBridgeDB().DelClient(client.Id); err != nil {
		logs.Warn("Rollback config client %d error: %v", client.Id, err)
	}
}

func drainUnknownConfigPayload(c *conn.Conn) error {
	if c == nil {
		return nil
	}
	_, err := c.GetShortLenContent()
	return err
}

func (s *Bridge) handleConfigNewClient(c *conn.Conn, ver int, vs, uuid string) (*file.Client, bool) {
	client, err := c.GetConfigInfo()
	if err != nil {
		_ = writeConfigAddFail(c)
		return nil, false
	}
	client = normalizeNewConfigClient(client)
	if err := currentBridgeDB().NewClient(client); err != nil {
		_ = writeConfigAddFail(c)
		return nil, false
	}
	if err := c.WriteAddOk(); err != nil {
		rollbackNewConfigClient(client)
		return nil, false
	}
	if _, err := c.Write([]byte(client.VerifyKey)); err != nil {
		rollbackNewConfigClient(client)
		return nil, false
	}
	runtimeClient := NewClient(client.Id, NewNode(uuid, vs, ver))
	runtimeClient.SetCloseNodeHook(s.notifyCloseNode)
	s.Client.Store(client.Id, runtimeClient)
	return client, true
}

func buildConfigWorkStatusPayload(clientID int, currentClient *file.Client, isPub bool, runList interface {
	Load(key interface{}) (value interface{}, ok bool)
}) string {
	var strBuilder strings.Builder
	if currentClient.IsConnect && !isPub {
		appendConfigWorkStatusHosts(&strBuilder, clientID)
		appendConfigWorkStatusTasks(&strBuilder, clientID, runList)
	}
	return strBuilder.String()
}

func appendConfigWorkStatusHosts(dst *strings.Builder, clientID int) {
	if dst == nil {
		return
	}
	currentBridgeDB().RangeHosts(func(v *file.Host) bool {
		if v.Client != nil && v.Client.Id == clientID {
			dst.WriteString(v.Remark + common.CONN_DATA_SEQ)
		}
		return true
	})
}

func appendConfigWorkStatusTasks(dst *strings.Builder, clientID int, runList interface {
	Load(key interface{}) (value interface{}, ok bool)
}) {
	if dst == nil {
		return
	}
	currentBridgeDB().RangeTasks(func(v *file.Tunnel) bool {
		if runList != nil {
			if _, ok := runList.Load(v.Id); !ok {
				return true
			}
		}
		if v.Client != nil && v.Client.Id == clientID {
			dst.WriteString(v.Remark + common.CONN_DATA_SEQ)
		}
		return true
	})
}

func writeConfigWorkStatusPayload(c *conn.Conn, status string) {
	_ = binary.Write(c, binary.LittleEndian, int32(len([]byte(status))))
	_ = binary.Write(c, binary.LittleEndian, []byte(status))
}

func (s *Bridge) handleConfigWorkStatus(c *conn.Conn, client *file.Client, isPub bool) {
	verifyKey, err := c.GetShortContent(64)
	if err != nil {
		return
	}
	clientID, err := currentBridgeDB().GetClientIdByBlake2bVkey(string(verifyKey))
	if err != nil {
		return
	}
	writeConfigWorkStatusPayload(c, buildConfigWorkStatusPayload(clientID, client, isPub, s.runList))
}

func (s *Bridge) publishNoStoreHostUpdate(host *file.Host, updated *file.Host, uuid string) bool {
	if s == nil || host == nil || updated == nil {
		return false
	}
	snapshot := host.SnapshotForUpdate()
	binding := host.SnapshotRuntimeBinding()
	host.BindRuntimeOwner(uuid, updated)
	if s.enqueueOpenHost(host) {
		return true
	}
	host.Update(snapshot)
	host.RestoreRuntimeBinding(binding)
	return false
}

func (s *Bridge) handleConfigNewHost(c *conn.Conn, client *file.Client, uuid string) bool {
	host, err := c.GetHostInfo()
	if err != nil {
		_ = writeConfigAddFail(c)
		return false
	}
	host.Client = client
	if host.Location == "" {
		host.Location = "/"
	}
	rollbackHost, ok := s.publishConfigHost(host, uuid)
	if !ok {
		return writeConfigAddFail(c) == nil
	}

	if err := c.WriteAddOk(); err != nil {
		if rollbackHost != nil {
			rollbackHost()
		}
		return false
	}
	return true
}

func (s *Bridge) publishConfigHost(host *file.Host, uuid string) (func(), bool) {
	if host == nil || host.Client == nil {
		return nil, false
	}
	existing, ok := host.Client.HasHost(host)
	if !ok {
		return publishNewConfigHost(host, uuid)
	}
	if !existing.NoStore {
		return nil, true
	}
	return s.publishNoStoreConfigHost(existing, host, uuid)
}

func publishNewConfigHost(host *file.Host, uuid string) (func(), bool) {
	if host == nil {
		return nil, false
	}
	host.BindRuntimeOwner(uuid, host)
	if currentBridgeDB().IsHostExist(host) {
		return nil, false
	}
	if err := currentBridgeDB().NewHost(host); err != nil {
		logs.Warn("Add host error: %v", err)
		return nil, false
	}
	return func() {
		if err := currentBridgeDB().DelHost(host.Id); err != nil {
			logs.Warn("Rollback host %d after config response failure error: %v", host.Id, err)
		}
	}, true
}

func (s *Bridge) publishNoStoreConfigHost(existing *file.Host, updated *file.Host, uuid string) (func(), bool) {
	if existing == nil || updated == nil {
		return nil, false
	}
	snapshot := existing.SnapshotForUpdate()
	binding := existing.SnapshotRuntimeBinding()
	if !s.publishNoStoreHostUpdate(existing, updated, uuid) {
		return nil, false
	}
	return func() {
		existing.Update(snapshot)
		existing.RestoreRuntimeBinding(binding)
	}, true
}

type bridgeConfigTaskVariantStatus int

const (
	bridgeConfigTaskVariantContinue bridgeConfigTaskVariantStatus = iota
	bridgeConfigTaskVariantContinueAfterFailure
	bridgeConfigTaskVariantStop
	bridgeConfigTaskVariantStopRemoveClient
)

type bridgeConfigPreparedTaskVariant struct {
	task            *file.Tunnel
	fileRuntime     *Client
	fileRuntimeKey  string
	fileRuntimeUUID string
	fileRuntimeAdd  bool
}

func (s *Bridge) publishNoStoreTaskUpdate(task *file.Tunnel, updated *file.Tunnel, uuid string) bool {
	if s == nil || task == nil || updated == nil {
		return false
	}
	snapshot := task.SnapshotForUpdate()
	binding := task.SnapshotRuntimeBinding()
	task.BindRuntimeOwner(uuid, updated)
	if s.enqueueOpenTask(task) {
		return true
	}
	task.Update(snapshot)
	task.RestoreRuntimeBinding(binding)
	return false
}

func (s *Bridge) publishNewTask(task *file.Tunnel) bool {
	if s == nil || task == nil {
		return false
	}
	if s.enqueueOpenTask(task) {
		return true
	}
	_ = currentBridgeDB().DelTask(task.Id)
	return false
}

func copyConfigTaskHealth(dst *file.Tunnel, src *file.Tunnel) {
	if dst == nil || src == nil {
		return
	}
	dst.HealthCheckTimeout = src.HealthCheckTimeout
	dst.HealthMaxFail = src.HealthMaxFail
	dst.HealthCheckInterval = src.HealthCheckInterval
	dst.HttpHealthUrl = src.HttpHealthUrl
	dst.HealthCheckType = src.HealthCheckType
	dst.HealthCheckTarget = src.HealthCheckTarget
}

func cleanupConfigTaskCreate(task *file.Tunnel, runtimeClient *Client, fileKey, fileOwnerUUID string, removeFileOwner bool) {
	if removeFileOwner && runtimeClient != nil && fileKey != "" && fileOwnerUUID != "" {
		runtimeClient.RemoveFileOwner(fileKey, fileOwnerUUID)
	}
	if task == nil || task.Id == 0 {
		return
	}
	if err := currentBridgeDB().DelTask(task.Id); err != nil {
		logs.Warn("Rollback task %d after config failure error: %v", task.Id, err)
	}
}

func (s *Bridge) rollbackPublishedConfigTaskCreate(task *file.Tunnel, runtimeClient *Client, fileKey, fileOwnerUUID string, removeFileOwner bool) {
	cleanupConfigTaskCreate(task, runtimeClient, fileKey, fileOwnerUUID, removeFileOwner)
	if s == nil || task == nil {
		return
	}
	if !s.enqueueOpenTask(task) {
		logs.Warn("Enqueue rollback for task %d after config response failure timed out", task.Id)
	}
}

func writeRemainingTaskAddFails(c *conn.Conn, count int) error {
	for i := 0; i < count; i++ {
		if err := writeConfigAddFail(c); err != nil {
			return err
		}
	}
	return nil
}

func splitConfigTaskTargets(task *file.Tunnel) []string {
	if task == nil || task.Target == nil {
		return nil
	}
	raw := strings.TrimSpace(task.Target.TargetStr)
	if raw == "" {
		return nil
	}
	items := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func expandConfigTaskPorts(task *file.Tunnel) (ports []int, targets []string, ok bool) {
	ports = common.GetPorts(task.Ports)
	targets = splitConfigTaskTargets(task)
	if len(ports) > 1 && (task.Mode == "tcp" || task.Mode == "udp") && len(ports) != len(targets) {
		return nil, nil, false
	}
	if task.Mode == "secret" || task.Mode == "p2p" {
		ports = append(ports, 0)
	}
	if task.Mode == "file" && len(ports) == 0 {
		ports = append(ports, 0)
	}
	if len(ports) == 0 {
		return nil, nil, false
	}
	return ports, targets, true
}

func newConfigTaskVariant(task *file.Tunnel, client *file.Client, ports []int, targets []string, index int) *file.Tunnel {
	tl := &file.Tunnel{
		Mode:          task.Mode,
		Port:          ports[index],
		ServerIp:      task.ServerIp,
		Client:        client,
		Password:      task.Password,
		Remark:        task.Remark,
		TargetAddr:    task.TargetAddr,
		TargetType:    task.TargetType,
		EntryAclMode:  task.EntryAclMode,
		EntryAclRules: task.EntryAclRules,
		DestAclMode:   task.DestAclMode,
		DestAclRules:  task.DestAclRules,
		LocalPath:     task.LocalPath,
		StripPre:      task.StripPre,
		ReadOnly:      task.ReadOnly,
		Socks5Proxy:   task.Socks5Proxy,
		HttpProxy:     task.HttpProxy,
		UserAuth:      task.UserAuth,
		MultiAccount:  task.MultiAccount,
		ExpireAt:      task.ExpireAt,
		FlowLimit:     task.FlowLimit,
		RateLimit:     task.RateLimit,
		MaxConn:       task.MaxConn,
		Id:            currentBridgeDB().NextTaskID(),
		Status:        true,
		Flow:          new(file.Flow),
		NoStore:       true,
	}
	copyConfigTaskHealth(tl, task)

	if len(ports) == 1 {
		tl.Target = task.Target
		tl.Remark = task.Remark
	} else {
		tl.Remark = fmt.Sprintf("%s_%d", task.Remark, tl.Port)
		targetStr := strings.TrimSpace(targets[index])
		if task.TargetAddr != "" && !strings.Contains(targetStr, ":") {
			targetStr = fmt.Sprintf("%s:%s", task.TargetAddr, targetStr)
		}
		tl.Target = &file.Target{TargetStr: targetStr}
		if task.Target != nil {
			tl.Target.LocalProxy = task.Target.LocalProxy
			tl.Target.ProxyProtocol = task.Target.ProxyProtocol
		}
	}
	if tl.MultiAccount == nil {
		tl.MultiAccount = new(file.MultiAccount)
	}
	return tl
}

func (s *Bridge) attachConfigTaskFileRuntime(task *file.Tunnel, client *file.Client, ver int, vs, uuid string) (*Client, string, bool, bool) {
	if task.Mode != "file" {
		return nil, "", false, true
	}
	runtimeClient := NewClient(client.Id, NewNode(uuid, vs, ver))
	runtimeClient.SetCloseNodeHook(s.notifyCloseNode)
	if existing, loaded := s.loadOrStoreRuntimeClient(client.Id, runtimeClient); loaded {
		runtimeClient = existing
	}
	fileKey := file.FileTunnelRuntimeKey(client.VerifyKey, task)
	added, err := runtimeClient.AddFileOwner(fileKey, uuid)
	if err != nil {
		logs.Error("Add file failed, error %v", err)
		return nil, "", false, false
	}
	fileTarget := fmt.Sprintf("file://%s", fileKey)
	task.Target.TargetStr = fileTarget
	return runtimeClient, fileKey, added, true
}

func (s *Bridge) handleConfigNewTask(c *conn.Conn, client *file.Client, ver int, vs, uuid string) bool {
	task, err := c.GetTaskInfo()
	if err != nil {
		_ = writeConfigAddFail(c)
		return false
	}
	ports, targets, ok := expandConfigTaskPorts(task)
	if !ok {
		return writeConfigAddFail(c) == nil
	}

	for i := 0; i < len(ports); i++ {
		switch s.handleConfigTaskVariant(c, client, ver, vs, uuid, task, ports, targets, i) {
		case bridgeConfigTaskVariantContinue:
			continue
		case bridgeConfigTaskVariantContinueAfterFailure:
			return writeRemainingTaskAddFails(c, len(ports)-i) == nil
		case bridgeConfigTaskVariantStopRemoveClient:
			_ = writeRemainingTaskAddFails(c, len(ports)-i)
			s.DelClient(client.Id)
			return false
		default:
			return false
		}
	}
	return true
}

func (s *Bridge) handleConfigTaskVariant(c *conn.Conn, client *file.Client, ver int, vs, uuid string, task *file.Tunnel, ports []int, targets []string, index int) bridgeConfigTaskVariantStatus {
	variant, status := s.prepareConfigTaskVariant(client, ver, vs, uuid, task, ports, targets, index)
	if status != bridgeConfigTaskVariantContinue {
		return status
	}
	rollbackTask, ok := s.publishConfigTaskVariant(variant)
	if !ok {
		rollbackPreparedConfigTaskVariant(variant)
		return bridgeConfigTaskVariantContinueAfterFailure
	}
	if err := c.WriteAddOk(); err != nil {
		if rollbackTask != nil {
			rollbackTask()
		}
		return bridgeConfigTaskVariantStop
	}
	return bridgeConfigTaskVariantContinue
}

func (s *Bridge) prepareConfigTaskVariant(client *file.Client, ver int, vs, uuid string, task *file.Tunnel, ports []int, targets []string, index int) (*bridgeConfigPreparedTaskVariant, bridgeConfigTaskVariantStatus) {
	tl := newConfigTaskVariant(task, client, ports, targets, index)
	fileRuntimeClient, fileRuntimeKey, fileRuntimeAdded, ok := s.attachConfigTaskFileRuntime(tl, client, ver, vs, uuid)
	if !ok {
		return nil, bridgeConfigTaskVariantStopRemoveClient
	}
	return &bridgeConfigPreparedTaskVariant{
		task:            tl,
		fileRuntime:     fileRuntimeClient,
		fileRuntimeKey:  fileRuntimeKey,
		fileRuntimeUUID: uuid,
		fileRuntimeAdd:  fileRuntimeAdded,
	}, bridgeConfigTaskVariantContinue
}

func rollbackPreparedConfigTaskVariant(variant *bridgeConfigPreparedTaskVariant) {
	if variant == nil || !variant.fileRuntimeAdd || variant.fileRuntime == nil || variant.fileRuntimeKey == "" || variant.fileRuntimeUUID == "" {
		return
	}
	variant.fileRuntime.RemoveFileOwner(variant.fileRuntimeKey, variant.fileRuntimeUUID)
}

func (s *Bridge) publishConfigTaskVariant(variant *bridgeConfigPreparedTaskVariant) (func(), bool) {
	if variant == nil || variant.task == nil {
		return nil, false
	}
	tl := variant.task

	existing, exists := tl.Client.HasTunnel(tl)
	if !exists {
		return s.publishNewConfigTaskVariant(variant)
	}
	if !existing.NoStore {
		return nil, true
	}
	return s.publishNoStoreConfigTaskVariant(existing, variant)
}

func (s *Bridge) publishNewConfigTaskVariant(variant *bridgeConfigPreparedTaskVariant) (func(), bool) {
	if variant == nil || variant.task == nil {
		return nil, false
	}
	tl := variant.task
	tl.BindRuntimeOwner(variant.fileRuntimeUUID, tl)
	if err := currentBridgeDB().NewTask(tl); err != nil {
		cleanupConfigTaskCreate(nil, variant.fileRuntime, variant.fileRuntimeKey, variant.fileRuntimeUUID, variant.fileRuntimeAdd)
		logs.Warn("Add task error: %v", err)
		return nil, false
	}
	rollbackTask := func() {
		s.rollbackPublishedConfigTaskCreate(tl, variant.fileRuntime, variant.fileRuntimeKey, variant.fileRuntimeUUID, variant.fileRuntimeAdd)
	}
	if ok := tool.TestTunnelPort(tl); !ok && tl.Mode != "secret" && tl.Mode != "p2p" && tl.Port > 0 {
		cleanupConfigTaskCreate(tl, variant.fileRuntime, variant.fileRuntimeKey, variant.fileRuntimeUUID, variant.fileRuntimeAdd)
		return nil, false
	}
	if !s.publishNewTask(tl) {
		cleanupConfigTaskCreate(nil, variant.fileRuntime, variant.fileRuntimeKey, variant.fileRuntimeUUID, variant.fileRuntimeAdd)
		return nil, false
	}
	return rollbackTask, true
}

func (s *Bridge) publishNoStoreConfigTaskVariant(existing *file.Tunnel, variant *bridgeConfigPreparedTaskVariant) (func(), bool) {
	if existing == nil || variant == nil || variant.task == nil {
		return nil, false
	}
	snapshot := existing.SnapshotForUpdate()
	binding := existing.SnapshotRuntimeBinding()
	if !s.publishNoStoreTaskUpdate(existing, variant.task, variant.fileRuntimeUUID) {
		return nil, false
	}
	return func() {
		rollbackPreparedConfigTaskVariant(variant)
		existing.Update(snapshot)
		existing.RestoreRuntimeBinding(binding)
		if !s.enqueueOpenTask(existing) {
			logs.Warn("Enqueue rollback for task %d after config response failure timed out", existing.Id)
		}
	}, true
}

func (s *Bridge) getConfig(c *conn.Conn, isPub bool, client *file.Client, ver int, vs, uuid string) {
	defer func() { _ = c.Close() }()
	session := bridgeConfigSession{
		isPub:  isPub,
		client: client,
		ver:    ver,
		vs:     vs,
		uuid:   uuid,
	}
	for {
		flag, err := readBridgeConfigFlag(c)
		if err != nil {
			return
		}
		if !s.handleBridgeConfigFlag(c, &session, flag) {
			return
		}
	}
}

func readBridgeConfigFlag(c *conn.Conn) (string, error) {
	return c.ReadFlag()
}

func (s *Bridge) handleBridgeConfigFlag(c *conn.Conn, session *bridgeConfigSession, flag string) bool {
	switch flag {
	case common.WORK_STATUS:
		s.handleConfigWorkStatus(c, session.client, session.isPub)
		return false
	case common.NEW_CONF:
		nextClient, ok := s.handleConfigNewClient(c, session.ver, session.vs, session.uuid)
		if ok {
			session.client = nextClient
			return true
		}
		return false
	case common.NEW_HOST:
		return s.handleConfigNewHost(c, session.client, session.uuid)
	case common.NEW_TASK:
		return s.handleConfigNewTask(c, session.client, session.ver, session.vs, session.uuid)
	default:
		return drainUnknownConfigPayload(c) == nil
	}
}
