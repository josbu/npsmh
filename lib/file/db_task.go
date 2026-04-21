package file

import (
	"errors"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
)

func normalizeTunnelProtocolFields(tunnel *Tunnel) {
	if tunnel == nil {
		return
	}
	switch tunnel.Mode {
	case "socks5":
		tunnel.Mode = "mixProxy"
		tunnel.HttpProxy = false
		tunnel.Socks5Proxy = true
	case "httpProxy":
		tunnel.Mode = "mixProxy"
		tunnel.HttpProxy = true
		tunnel.Socks5Proxy = false
	}
	if tunnel.TargetType != common.CONN_TCP && tunnel.TargetType != common.CONN_UDP {
		tunnel.TargetType = common.CONN_ALL
	}
}

func (s *DbUtils) NewTask(t *Tunnel) (err error) {
	if t == nil {
		return errors.New("task is nil")
	}
	if t.Client, err = resolveStoredClientRef(s, t.Client); err != nil {
		return err
	}
	if (t.Mode == "secret" || t.Mode == "p2p") && t.Password == "" {
		t.Password = crypt.GetRandomString(16, t.Id)
	}

	if t.Flow == nil {
		t.Flow = new(Flow)
	}
	if t.Id == 0 {
		t.Id = int(s.JsonDb.GetTaskId())
	} else if t.Id > int(s.JsonDb.TaskIncreaseId) {
		s.JsonDb.TaskIncreaseId = int32(t.Id)
	}

	if t.Password != "" {
		for {
			hash := crypt.Md5(t.Password)
			if idxId, ok := runtimeTaskPasswordIndex().Get(hash); !ok || idxId == t.Id {
				runtimeTaskPasswordIndex().Add(hash, t.Id)
				break
			}
			t.Password = crypt.GetRandomString(16, t.Id)
		}
	}

	normalizeTunnelProtocolFields(t)
	InitializeTunnelRuntime(t)
	t.CompileEntryACL()
	t.CompileDestACL()
	s.JsonDb.Tasks.Store(t.Id, t)
	s.JsonDb.taskClientIndex.add(t.Client.Id, t.Id)
	s.JsonDb.taskClientIndex.markReady()
	s.JsonDb.StoreTasks()
	return
}

func (s *DbUtils) UpdateTask(t *Tunnel) error {
	if t == nil {
		return errors.New("task is nil")
	}
	var err error
	if t.Client, err = resolveStoredClientRef(s, t.Client); err != nil {
		return err
	}
	current, ok := loadTaskEntry(&s.JsonDb.Tasks, t.Id)
	if !ok {
		return ErrTaskNotFound
	}
	if t.ExpectedRevision > 0 {
		if current.Revision != t.ExpectedRevision {
			return ErrRevisionConflict
		}
	}
	if (t.Mode == "secret" || t.Mode == "p2p") && t.Password == "" {
		t.Password = crypt.GetRandomString(16, t.Id)
	}

	if oldPwd := current.Password; oldPwd != "" {
		if idxId, ok := runtimeTaskPasswordIndex().Get(crypt.Md5(oldPwd)); ok && idxId == t.Id {
			runtimeTaskPasswordIndex().Remove(crypt.Md5(oldPwd))
		}
	}

	if t.Password != "" {
		for {
			hash := crypt.Md5(t.Password)
			if idxId, ok := runtimeTaskPasswordIndex().Get(hash); !ok || idxId == t.Id {
				runtimeTaskPasswordIndex().Add(hash, t.Id)
				break
			}
			t.Password = crypt.GetRandomString(16, t.Id)
		}
	}
	normalizeTunnelProtocolFields(t)
	t.ExpectedRevision = 0
	InitializeTunnelRuntime(t)
	t.CompileEntryACL()
	t.CompileDestACL()
	s.JsonDb.Tasks.Store(t.Id, t)
	if current.Client != nil {
		s.JsonDb.taskClientIndex.remove(current.Client.Id, current.Id)
	}
	s.JsonDb.taskClientIndex.add(t.Client.Id, t.Id)
	s.JsonDb.taskClientIndex.markReady()
	s.JsonDb.StoreTasks()
	return nil
}

func (s *DbUtils) UpdateTaskStatus(id int, status bool) error {
	if s == nil || s.JsonDb == nil {
		return ErrTaskNotFound
	}
	task, ok := loadTaskEntry(&s.JsonDb.Tasks, id)
	if !ok {
		return ErrTaskNotFound
	}
	task.Lock()
	task.Status = status
	task.Unlock()
	s.JsonDb.StoreTasks()
	return nil
}

func (s *DbUtils) DelTask(id int) error {
	t, ok := loadTaskEntry(&s.JsonDb.Tasks, id)
	if !ok {
		return ErrTaskNotFound
	}
	runtimeTaskPasswordIndex().Remove(crypt.Md5(t.Password))
	if t.Rate != nil {
		t.Rate.Stop()
	}
	if t.Client != nil {
		s.JsonDb.taskClientIndex.remove(t.Client.Id, t.Id)
	}
	s.JsonDb.Tasks.Delete(id)
	s.JsonDb.taskClientIndex.markReady()
	s.JsonDb.StoreTasks()
	return nil
}

func (s *DbUtils) DeleteNoStoreTaskIfCurrentOwnerless(task *Tunnel) bool {
	if s == nil || s.JsonDb == nil || task == nil {
		return false
	}

	task.Lock()
	defer task.Unlock()

	current, ok := s.JsonDb.Tasks.Load(task.Id)
	if !ok || current != task || !task.NoStore || task.Client == nil {
		return false
	}
	if task.runtimeOwners != nil && task.runtimeOwners.count() > 0 {
		return false
	}

	runtimeTaskPasswordIndex().Remove(crypt.Md5(task.Password))
	if task.Rate != nil {
		task.Rate.Stop()
	}
	if task.Client != nil {
		s.JsonDb.taskClientIndex.remove(task.Client.Id, task.Id)
	}
	s.JsonDb.Tasks.Delete(task.Id)
	s.JsonDb.taskClientIndex.markReady()
	s.JsonDb.StoreTasks()
	return true
}

// GetTaskByMd5Password md5 password
func (s *DbUtils) GetTaskByMd5Password(p string) (t *Tunnel) {
	id, ok := runtimeTaskPasswordIndex().Get(p)
	if ok {
		if v, ok := loadTaskEntry(&s.JsonDb.Tasks, id); ok {
			t = v
			return
		}
		runtimeTaskPasswordIndex().Remove(p)
	}
	return
}

func (s *DbUtils) GetTaskByMd5PasswordOld(p string) (t *Tunnel) {
	s.RangeTasks(func(current *Tunnel) bool {
		if crypt.Md5(current.Password) == p {
			t = current
			return false
		}
		return true
	})
	return
}

func (s *DbUtils) GetTask(id int) (t *Tunnel, err error) {
	if t, ok := loadTaskEntry(&s.JsonDb.Tasks, id); ok {
		return t, nil
	}
	err = ErrTaskNotFound
	return
}
