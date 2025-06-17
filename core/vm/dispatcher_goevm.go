//go:build !revm
// +build !revm

package vm

import (
    "github.com/ethereum/go-ethereum/core"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/types"
)

// Executor is a minimal abstraction that the VM dispatcher returns.
// For the Go-EVM build we simply expose a stub that marks itself as
// the canonical Go interpreter. Subsequent milestones will flesh out
// additional behaviour as required.

type Executor interface {
    // Engine identifies backend name ("go-evm", "revm" ...)
    Engine() string
}

// AdvancedExecutor is implemented by backends that can execute a pre-built
// Message and return a Go receipt directly (Milestone-4.3).
// The extra arguments match those required by the legacy ApplyTransaction path
// so StateProcessor can switch over without large changes.
type AdvancedExecutor interface {
    Executor
    ExecuteTx(msg *types.Message, tx *types.Transaction, txIdx int, gp *core.GasPool, sdb *state.StateDB, header *types.Header, evmCfg Config) (*types.Receipt, error)
}

type goExecutor struct{}

func (goExecutor) Engine() string { return "go-evm" }

// ExecuteTx executes the given message using the canonical go-ethereum path
// and returns the resulting receipt. This is a thin wrapper so that the
// StateProcessor can treat both backends uniformly.
func (goExecutor) ExecuteTx(msg *types.Message, tx *types.Transaction, txIdx int, gp *core.GasPool, sdb *state.StateDB, header *types.Header, evmCfg Config) (*types.Receipt, error) {
    // Build EVM instance identical to legacy path
    context := NewEVMBlockContext(header, nil, nil)
    evm := NewEVM(context, sdb, nil, evmCfg)

    used := new(uint64)
    receipt, err := core.ApplyTransactionWithEVM(msg, gp, sdb, header.Number, header.Hash(), tx, used, evm)
    return receipt, err
}

// NewExecutor returns the default Go-EVM executor when the build does **not**
// include the `revm` tag.
func NewExecutor(_ *state.StateDB) (Executor, error) {
    return goExecutor{}, nil
}

// _ is used to avoid unused import when the stub has no logic yet.
var _ = state.NewDatabaseForTesting // keep compiler happy without losing the import 