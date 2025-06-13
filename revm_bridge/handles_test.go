package revmbridge

import (
    "sync"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    statedb "github.com/ethereum/go-ethereum/core/state"
)

// TestHandleRegistry verifies that NewStateDB returns unique handles, lookup
// works, and ReleaseStateDB actually removes the entry.
func TestHandleRegistry(t *testing.T) {
    db := statedb.NewDatabaseForTesting()
    s, err := statedb.New(common.Hash{}, db)
    if err != nil {
        t.Fatalf("failed to create StateDB: %v", err)
    }

    h := NewStateDB(s)
    if h == 0 {
        t.Fatalf("handle must be non-zero")
    }

    if _, ok := lookup(h); !ok {
        t.Fatalf("lookup failed for valid handle")
    }

    ReleaseStateDB(h)
    if _, ok := lookup(h); ok {
        t.Fatalf("handle should have been removed after release")
    }
}

// TestHandleRace ensures that concurrent handle operations are race-free.
func TestHandleRace(t *testing.T) {
    const n = 100
    db := statedb.NewDatabaseForTesting()

    wg := sync.WaitGroup{}
    wg.Add(n)

    handles := make(chan uintptr, n)

    for i := 0; i < n; i++ {
        go func() {
            defer wg.Done()
            s, _ := statedb.New(common.Hash{}, db)
            h := NewStateDB(s)
            handles <- h
        }()
    }

    wg.Wait()
    close(handles)

    for h := range handles {
        if _, ok := lookup(h); !ok {
            t.Fatalf("lookup failed for handle %d", h)
        }
        ReleaseStateDB(h)
    }
} 