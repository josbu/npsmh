package routers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestResolveNodeChangesQueryRequest(t *testing.T) {
	request := resolveNodeChangesQueryRequest("-7", "999", "", "history")
	if request.After != 0 {
		t.Fatalf("resolveNodeChangesQueryRequest().After = %d, want 0", request.After)
	}
	if request.Limit != 500 {
		t.Fatalf("resolveNodeChangesQueryRequest().Limit = %d, want 500", request.Limit)
	}
	if !request.Durable {
		t.Fatal("resolveNodeChangesQueryRequest().Durable = false, want true")
	}

	request = resolveNodeChangesQueryRequest("42", "-3")
	if request.After != 42 {
		t.Fatalf("resolveNodeChangesQueryRequest(valid).After = %d, want 42", request.After)
	}
	if request.Limit != 100 {
		t.Fatalf("resolveNodeChangesQueryRequest(valid).Limit = %d, want 100", request.Limit)
	}
	if request.Durable {
		t.Fatal("resolveNodeChangesQueryRequest(valid).Durable = true, want false")
	}
}

func TestQueryNodeChangesRequestUsesDurableSource(t *testing.T) {
	log := newNodeEventLog(2, "")
	log.Record(webapi.Event{Name: "client.created", Resource: "client", Action: "create"})
	log.Record(webapi.Event{Name: "client.updated", Resource: "client", Action: "update"})
	log.Record(webapi.Event{Name: "client.status_changed", Resource: "client", Action: "update"})

	state := &State{
		App:          webapi.New(nil),
		NodeEventLog: log,
	}

	live := queryNodeChangesRequest(state, nil, nodeChangesQueryRequest{After: 0, Limit: 10})
	if live.Count != 2 || len(live.Items) != 2 || live.OldestCursor != 2 {
		t.Fatalf("unexpected live node changes snapshot: %+v", live)
	}

	durable := queryNodeChangesRequest(state, nil, nodeChangesQueryRequest{After: 0, Limit: 10, Durable: true})
	if durable.Count != 3 || len(durable.Items) != 3 || durable.OldestCursor != 1 {
		t.Fatalf("unexpected durable node changes snapshot: %+v", durable)
	}
}

func TestQueryNodeChangesForRequestRejectsAnonymousActor(t *testing.T) {
	state := &State{App: webapi.New(nil)}
	_, err := queryNodeChangesForRequest(state, nil, nodeChangesQueryRequest{After: 0, Limit: 10})
	if err == nil {
		t.Fatal("queryNodeChangesForRequest(nil actor) error = nil, want unauthorized")
	}
	status, detail := nodeChangesErrorDetail(err)
	if status != http.StatusUnauthorized || detail.Code != "unauthorized" || detail.Message != "unauthorized" {
		t.Fatalf("nodeChangesErrorDetail(anonymous) = (%d, %#v), want (401, unauthorized)", status, detail)
	}
}

func TestNodeChangesHTTPHandlerUsesFormalAccessErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("nil_state", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/system/changes", nil)

		nodeChangesHTTPHandler(nil)(ctx)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("nodeChangesHTTPHandler(nil state) status = %d, want 500 body=%s", recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if !strings.Contains(body, `"code":"node_state_unavailable"`) || !strings.Contains(body, `"message":"node state is unavailable"`) {
			t.Fatalf("nodeChangesHTTPHandler(nil state) body = %s, want node_state_unavailable", body)
		}
	})

	t.Run("anonymous_actor", func(t *testing.T) {
		state := &State{App: webapi.New(nil), NodeEventLog: newNodeEventLog(8, "")}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/system/changes", nil)

		nodeChangesHTTPHandler(state)(ctx)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("nodeChangesHTTPHandler(anonymous) status = %d, want 401 body=%s", recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if !strings.Contains(body, `"code":"unauthorized"`) || !strings.Contains(body, `"message":"unauthorized"`) {
			t.Fatalf("nodeChangesHTTPHandler(anonymous) body = %s, want unauthorized", body)
		}
	})

	t.Run("authorized", func(t *testing.T) {
		state := &State{App: webapi.New(nil), NodeEventLog: newNodeEventLog(8, "")}
		state.NodeEventLog.Record(webapi.Event{Name: "client.created", Resource: "client", Action: "create"})

		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/system/changes", nil)
		setActor(ctx, webapi.AdminActorWithFallback("admin", "admin"))

		nodeChangesHTTPHandler(state)(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("nodeChangesHTTPHandler(admin) status = %d, want 200 body=%s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"count":1`) {
			t.Fatalf("nodeChangesHTTPHandler(admin) body = %s, want one visible event", recorder.Body.String())
		}
	})
}

func TestNodeEventLogDeepClonesFieldValues(t *testing.T) {
	log := newNodeEventLog(8, "")
	managerUserIDs := []int{1, 2}
	tags := []string{"alpha", "beta"}
	nested := map[string]interface{}{
		"tags": tags,
	}

	log.Record(webapi.Event{
		Name:     "client.updated",
		Resource: "client",
		Action:   "update",
		Fields: map[string]interface{}{
			"id":               7,
			"manager_user_ids": managerUserIDs,
			"meta":             nested,
		},
	})

	managerUserIDs[0] = 99
	tags[0] = "changed"
	nested["extra"] = "value"

	snapshot := log.Query(0, 10, nil)
	if snapshot.Count != 1 || len(snapshot.Items) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	fields := snapshot.Items[0].Fields
	gotManagers, _ := fields["manager_user_ids"].([]int)
	if len(gotManagers) != 2 || gotManagers[0] != 1 || gotManagers[1] != 2 {
		t.Fatalf("manager_user_ids should stay immutable, got %#v", fields["manager_user_ids"])
	}
	meta, _ := fields["meta"].(map[string]interface{})
	gotTags, _ := meta["tags"].([]string)
	if len(gotTags) != 2 || gotTags[0] != "alpha" || gotTags[1] != "beta" {
		t.Fatalf("nested tags should stay immutable, got %#v", meta["tags"])
	}
	if _, exists := meta["extra"]; exists {
		t.Fatalf("nested map should not share later mutations, got %#v", meta)
	}
}

func TestNodeEventLogQueryNextAfterAdvancesAcrossFilteredEvents(t *testing.T) {
	log := newNodeEventLog(8, "")
	log.Record(webapi.Event{Name: "client.created", Resource: "client", Action: "create"})
	log.Record(webapi.Event{Name: "client.updated", Resource: "client", Action: "update"})

	allFiltered := log.Query(0, 10, func(webapi.Event) bool { return false })
	if allFiltered.Count != 0 || allFiltered.Cursor != 2 || allFiltered.NextAfter != 2 || allFiltered.HasMore {
		t.Fatalf("unexpected all-filtered snapshot: %+v", allFiltered)
	}

	visibleWithHiddenTail := log.Query(0, 10, func(event webapi.Event) bool { return event.Sequence == 1 })
	if visibleWithHiddenTail.Count != 1 || visibleWithHiddenTail.LastSequence != 1 || visibleWithHiddenTail.NextAfter != 2 || visibleWithHiddenTail.HasMore {
		t.Fatalf("unexpected visible-with-hidden-tail snapshot: %+v", visibleWithHiddenTail)
	}

	log.Record(webapi.Event{Name: "client.status_changed", Resource: "client", Action: "update"})
	pagedVisible := log.Query(0, 1, func(event webapi.Event) bool { return event.Sequence == 1 || event.Sequence == 3 })
	if pagedVisible.Count != 1 || !pagedVisible.HasMore || pagedVisible.LastSequence != 1 || pagedVisible.NextAfter != 1 {
		t.Fatalf("unexpected paged visible snapshot: %+v", pagedVisible)
	}
}

func TestNodeEventLogQueryDoesNotHoldReadLockAcrossFiltering(t *testing.T) {
	log := newNodeEventLog(8, "")
	log.Record(webapi.Event{Name: "client.created", Resource: "client", Action: "create"})

	filterStarted := make(chan struct{}, 1)
	releaseFilter := make(chan struct{})
	queryDone := make(chan struct{})
	go func() {
		_ = log.Query(0, 10, func(webapi.Event) bool {
			select {
			case filterStarted <- struct{}{}:
			default:
			}
			<-releaseFilter
			return true
		})
		close(queryDone)
	}()

	select {
	case <-filterStarted:
	case <-time.After(time.Second):
		t.Fatal("Query() filter did not start")
	}

	recordDone := make(chan struct{})
	go func() {
		log.Record(webapi.Event{Name: "client.updated", Resource: "client", Action: "update"})
		close(recordDone)
	}()

	select {
	case <-recordDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Record() should not block behind Query() filter work")
	}

	close(releaseFilter)

	select {
	case <-queryDone:
	case <-time.After(time.Second):
		t.Fatal("Query() did not finish after releasing filter")
	}

	snapshot := log.Query(0, 10, nil)
	if snapshot.Count != 2 || len(snapshot.Items) != 2 || snapshot.Cursor != 2 {
		t.Fatalf("unexpected snapshot after concurrent record/query: %+v", snapshot)
	}
}

func TestNodeEventLogRecordLiveOnlySkipsReplayWindow(t *testing.T) {
	log := newNodeEventLog(8, "")
	liveOnly := log.RecordLiveOnly(webapi.Event{
		Name:     "node.traffic.report",
		Resource: "node",
		Action:   "traffic",
	})
	if liveOnly.Sequence != 0 {
		t.Fatalf("RecordLiveOnly() sequence = %d, want 0 before first durable change", liveOnly.Sequence)
	}

	snapshot := log.Query(0, 10, nil)
	if snapshot.Count != 0 || snapshot.Cursor != 0 || snapshot.NextAfter != 0 {
		t.Fatalf("unexpected live-only snapshot: %+v", snapshot)
	}

	persisted := log.Record(webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
	})
	if persisted.Sequence != 1 {
		t.Fatalf("Record() sequence after live-only event = %d, want 1", persisted.Sequence)
	}

	snapshot = log.Query(0, 10, nil)
	if snapshot.Count != 1 || len(snapshot.Items) != 1 || snapshot.Items[0].Sequence != 1 || snapshot.Cursor != 1 || snapshot.NextAfter != 1 {
		t.Fatalf("unexpected persisted-after-live-only snapshot: %+v", snapshot)
	}
}

func TestNodeEventLogOverflowKeepsNewestEntriesInOrder(t *testing.T) {
	log := newNodeEventLog(3, "")
	for i := 1; i <= 5; i++ {
		log.Record(webapi.Event{
			Name:     "client.updated",
			Resource: "client",
			Action:   "update",
			Fields: map[string]interface{}{
				"id": i,
			},
		})
	}

	snapshot := log.Query(0, 10, nil)
	if snapshot.Count != 3 || len(snapshot.Items) != 3 {
		t.Fatalf("unexpected overflow snapshot size: %+v", snapshot)
	}
	for index, item := range snapshot.Items {
		want := index + 3
		if item.Sequence != int64(want) {
			t.Fatalf("snapshot.Items[%d].Sequence = %d, want %d", index, item.Sequence, want)
		}
		if gotID, _ := item.Fields["id"].(int); gotID != want {
			t.Fatalf("snapshot.Items[%d].Fields[id] = %#v, want %d", index, item.Fields["id"], want)
		}
	}
	if snapshot.Cursor != 5 || snapshot.NextAfter != 5 || snapshot.OldestCursor != 3 {
		t.Fatalf("unexpected overflow snapshot metadata: %+v", snapshot)
	}
}

func TestCanonicalNodeOperationPathWithParamsKeepsCanonicalAPIPaths(t *testing.T) {
	path := canonicalNodeOperationPathWithParams(nil, "/api/clients/7", nil)
	if path != "/api/clients/7" {
		t.Fatalf("canonicalNodeOperationPathWithParams(/api/clients/7) = %q, want /api/clients/7", path)
	}

	path = canonicalNodeOperationPathWithParams(nil, "/api/security/bans/10.0.0.1", nil)
	if path != "/api/security/bans/10.0.0.1" {
		t.Fatalf("canonicalNodeOperationPathWithParams(/api/security/bans/10.0.0.1) = %q, want /api/security/bans/10.0.0.1", path)
	}
}

func TestCanonicalNodeOperationPathWithParamsDoesNotPromoteUnknownPaths(t *testing.T) {
	if path := canonicalNodeOperationPathWithParams(nil, "/unknown/path", nil); path != "/unknown/path" {
		t.Fatalf("canonicalNodeOperationPathWithParams(unknown) = %q, want /unknown/path", path)
	}
	if path := canonicalNodeOperationPathWithParams(nil, "/api/callbacks/queue/actions/replay", nil); path != "/api/callbacks/queue/actions/replay" {
		t.Fatalf("canonicalNodeOperationPathWithParams(callback queue replay) = %q, want /api/callbacks/queue/actions/replay", path)
	}
	if path := canonicalNodeOperationPathWithParams(nil, "/api/system/import", nil); path != "/api/system/import" {
		t.Fatalf("canonicalNodeOperationPathWithParams(config import) = %q, want /api/system/import", path)
	}
}

func TestCanonicalNodeOperationPathWithParamsNormalizesLegacyQueryPaths(t *testing.T) {
	path := canonicalNodeOperationPathWithParams(nil, "/clients/get?id=7", nil)
	if path != "/clients/7" {
		t.Fatalf("canonicalNodeOperationPathWithParams(/clients/get?id=7) = %q, want /clients/7", path)
	}

	path = canonicalNodeOperationPathWithParams(nil, "/security/bans/delete?key=10.0.0.1", nil)
	if path != "/security/bans/10.0.0.1" {
		t.Fatalf("canonicalNodeOperationPathWithParams(/security/bans/delete?key=10.0.0.1) = %q, want /security/bans/10.0.0.1", path)
	}
}
