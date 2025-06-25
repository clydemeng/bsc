//go:build revm
// +build revm

package vm

import (
	"fmt"

	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
)

// Executor is a minimal abstraction exposed by the VM dispatcher. For the
// `revm` build it wraps the CGO-backed REVM executor that persists changes back
// to the provided *state.StateDB.

type Executor interface {
	Engine() string
}

// The REVM backend exposes two optional interfaces consumed by core:
//   1) CallReceipt – for direct transaction execution.
//   2) SetSpec(id uint8) – to switch the active hard-fork rules at runtime.

type revmExecutor struct {
	inner *revmbridge.RevmExecutorStateDB
}

func (r *revmExecutor) Engine() string { return "revm" }

// SetSpec is a no-op placeholder that fulfils the optional interface queried
// by core/vm's adapter. The Rust side currently picks the Prague spec by
// default; future work can plumb this through the FFI if needed.
func (r *revmExecutor) SetSpec(id uint8) {}

// CallReceipt runs the provided message on the REVM backend and returns a
// fully-translated Go receipt (used by the vmExecutorAdapter in core).
func (r *revmExecutor) CallReceipt(meta *CallMetadata, tx *types.Transaction) (*types.Receipt, error) {
	if meta == nil {
		return nil, fmt.Errorf("nil metadata")
	}

	// Run each transaction on a fresh snapshot so we can switch to true COW
	// semantics once snapshot_commit is available. State mutations are
	// nonetheless persisted back to the Go StateDB via FFI callbacks.
	exec := r.inner.Clone()
	if exec == nil {
		exec = r.inner // fall back (should not happen)
	} else {
		defer exec.Close()
	}

	txHash := tx.Hash()
	receipt, err := exec.CallContractCommitReceipt(meta.From, meta.To, meta.Data, meta.ValueHex, meta.GasLimit, 0, tx, (*[32]byte)(&txHash))
	if err != nil {
		return nil, err
	}

	// Merge cache layers back into the parent so subsequent transactions see
	// the updated state without additional CGO look-ups.
	if exec != r.inner {
		exec.Commit(r.inner)
	}
	return receipt, nil
}

// Prefetch passes the (address,slot) pairs to the underlying REVM executor so
// that its CacheDB can be primed ahead of execution. The helper is optional –
// the method is discovered via interface assertion by callers.
func (r *revmExecutor) Prefetch(keys []revmbridge.BatchKey) {
	if len(keys) == 0 {
		return
	}
	r.inner.Prefetch(keys)
}

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
