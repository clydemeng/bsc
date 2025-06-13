package revmbridge

import (
    "sync"
    "sync/atomic"

    "github.com/ethereum/go-ethereum/core/state"
)

// handleMap keeps a global registry of active StateDB handles that can be
// referenced from Rust via FFI callbacks. The key type is `uintptr` because
// that's what cgo uses when passing opaque pointers around.
var handleMap sync.Map // map[uintptr]*stateDBImpl

// handleSeq is an atomically-incremented counter that yields unique, non-zero
// handles. We start from 1 to reserve the zero value for "null".
var handleSeq uintptr

// NewStateDB registers a *state.StateDB instance and returns a stable handle
// that can safely cross the FFI boundary.
//
// There is intentionally **no** reverse lookup from *state.StateDB ➜ handle; if
// you need that, store the handle in your own struct.
func NewStateDB(db *state.StateDB) uintptr {
    if db == nil {
        return 0
    }
    h := atomic.AddUintptr(&handleSeq, 1)
    handleMap.Store(h, &stateDBImpl{db: db})
    return h
}

// ReleaseStateDB removes the previously registered handle. After this call any
// attempt from Rust to access the pointer will fail with a null-pointer error
// (we will return an error code from the callback).
func ReleaseStateDB(h uintptr) {
    handleMap.Delete(h)
}

// lookup tries to fetch the *stateDBImpl associated with the given handle. The
// boolean return value signals whether the handle was found.
func lookup(h uintptr) (*stateDBImpl, bool) {
    if v, ok := handleMap.Load(h); ok {
        return v.(*stateDBImpl), true
    }
    return nil, false
} 