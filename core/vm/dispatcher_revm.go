//go:build revm
// +build revm

package vm

import (
    "github.com/ethereum/go-ethereum/core/state"
    revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
    "fmt"
    "github.com/ethereum/go-ethereum/core"
    "github.com/ethereum/go-ethereum/core/types"
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

// ExecuteTx runs the provided message on the REVM backend and returns a
// fully-translated Go receipt. The StateDB changes are already persisted via
// the CGO bridge, we only need to track gas accounting at the consensus layer.
func (r *revmExecutor) ExecuteTx(msg *types.Message, txIdx int, gp *core.GasPool, _ *state.StateDB, _ *types.Header, _ Config) (*types.Receipt, error) {
    if msg == nil {
        return nil, fmt.Errorf("nil message")
    }
    from := msg.From.Hex()
    var to string
    if msg.To() != nil {
        to = msg.To().Hex()
    }
    valueStr := fmt.Sprintf("0x%s", msg.Value().Text(16))
    receipt, err := r.inner.CallContractCommitReceipt(from, to, msg.Data(), valueStr, msg.Gas(), 0, msg.Transaction())
    if err != nil {
        return nil, err
    }
    // Update block-level gas pool
    _ = gp.SubGas(receipt.GasUsed)
    return receipt, nil
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