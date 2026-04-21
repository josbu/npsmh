package file

import (
	"reflect"
	"sync"
	"testing"
)

func TestGetMapKeysDropsInvalidEntries(t *testing.T) {
	var entries sync.Map
	entries.Store(3, &Tunnel{Id: 3})
	entries.Store(1, &Tunnel{Id: 1})
	entries.Store("bad-key", &Tunnel{Id: 9})

	keys := GetMapKeys(&entries, false, "", "")
	if !reflect.DeepEqual(keys, []int{1, 3}) {
		t.Fatalf("GetMapKeys() = %v, want [1 3]", keys)
	}
	if _, ok := entries.Load("bad-key"); ok {
		t.Fatal("GetMapKeys() should drop invalid key entry")
	}
}

func TestSortClientByKeyDropsInvalidEntries(t *testing.T) {
	var entries sync.Map
	entries.Store(1, &Client{Id: 1, Flow: &Flow{InletFlow: 10}})
	entries.Store(2, &Client{Id: 2, Flow: &Flow{InletFlow: 20}})
	entries.Store("bad-key", &Client{Id: 3, Flow: &Flow{InletFlow: 30}})
	entries.Store(4, "invalid")
	entries.Store(5, &Client{Id: 5})

	keys := sortClientByKey(&entries, "InletFlow", "asc")
	if !reflect.DeepEqual(keys, []int{2, 1, 5}) {
		t.Fatalf("sortClientByKey() = %v, want [2 1 5]", keys)
	}
	if _, ok := entries.Load("bad-key"); ok {
		t.Fatal("sortClientByKey() should drop invalid key entry")
	}
	if _, ok := entries.Load(4); ok {
		t.Fatal("sortClientByKey() should drop invalid value entry")
	}
}

func TestSortClientByKeyUsesLegacyDirectionAndStableTieBreak(t *testing.T) {
	var entries sync.Map
	entries.Store(3, &Client{Id: 3, Flow: &Flow{InletFlow: 10}})
	entries.Store(2, &Client{Id: 2, Flow: &Flow{InletFlow: 20}})
	entries.Store(1, &Client{Id: 1, Flow: &Flow{InletFlow: 10}})
	entries.Store(4, &Client{Id: 4, Flow: nil})

	ascKeys := sortClientByKey(&entries, "InletFlow", "asc")
	if !reflect.DeepEqual(ascKeys, []int{2, 1, 3, 4}) {
		t.Fatalf("sortClientByKey(asc) = %v, want [2 1 3 4]", ascKeys)
	}

	descKeys := sortClientByKey(&entries, "InletFlow", "desc")
	if !reflect.DeepEqual(descKeys, []int{4, 1, 3, 2}) {
		t.Fatalf("sortClientByKey(desc) = %v, want [4 1 3 2]", descKeys)
	}
}
