//go:build revm
// +build revm

package vm

import (
    "github.com/ethereum/go-ethereum/core/state"
    revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
    "fmt"
)

// Executor is a minimal abstraction exposed by the VM dispatcher. For the
// `revm` build it wraps the CGO-backed REVM executor that persists changes back
// to the provided *state.StateDB.

type Executor interface {
    Engine() string
}

type revmExecutor struct {
    inner *revmbridge.RevmExecutorStateDB
}

func (r *revmExecutor) Engine() string { return "revm" }

// NewExecutor constructs a REVM-backed executor when compiled with the `revm`
// build-tag. It registers the provided StateDB, obtains an opaque handle, and
// boots a fresh REVM instance using that handle.
func NewExecutor(sdb *state.StateDB) (Executor, error) {
    if sdb == nil {
        return nil, fmt.Errorf("statedb is nil")
    }
    handle := revmbridge.NewStateDB(sdb)
    exec, err := revmbridge.NewRevmExecutorStateDB(handle)
    if err != nil {
        return nil, fmt.Errorf("revm: %w", err)
    }
    return &revmExecutor{inner: exec}, nil
} 