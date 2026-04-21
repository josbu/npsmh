package routers

import (
	"sync"
	"testing"

	webapi "github.com/djylb/nps/web/api"
)

func TestNodeWSActorStateConcurrentAccess(t *testing.T) {
	state := newNodeWSActorState(webapi.AdminActorWithFallback("admin", "admin"))
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				next := &webapi.Actor{
					Kind:      "user",
					SubjectID: "user:test",
					Username:  "user",
					ClientIDs: []int{index, j},
				}
				state.Update(next)
				current := state.Current()
				if current == nil {
					t.Error("Current() returned nil actor")
					return
				}
				current.ClientIDs[0] = -1
			}
		}(i)
	}

	wg.Wait()
	current := state.Current()
	if current == nil {
		t.Fatal("Current() returned nil actor after concurrent updates")
	}
	if len(current.ClientIDs) == 0 {
		t.Fatalf("Current().ClientIDs = %v, want non-empty", current.ClientIDs)
	}
	if current.ClientIDs[0] == -1 {
		t.Fatalf("Current() returned shared slice = %v, want cloned state", current.ClientIDs)
	}
}
