package core

import (
    "fmt"

    "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/ethereum/go-ethereum/core/vm"
)

// TxExecutor is an abstraction over a transaction execution backend (Go-EVM,
// REVM, ...). It hides the concrete engine behind a common interface that the
// consensus layer (StateProcessor) can use without branching on build tags.
//
// The interface purposefully mirrors the extra parameters required by the
// legacy ApplyTransaction* helpers so that we can refactor StateProcessor
// with minimal surface-area changes during Milestone 4.3.
type TxExecutor interface {
    // Engine returns a short human identifier ("go-evm", "revm" …).
    Engine() string

    // ExecuteTx runs the provided message/transaction and returns a Go-native receipt.
    // The original *types.Transaction is provided for log generation and hashing purposes.
    ExecuteTx(msg *types.Message, tx *types.Transaction, txIdx int, gp *GasPool, sdb *state.StateDB, header *types.Header, evmCfg vm.Config) (*types.Receipt, error)
}

// NewTxExecutor constructs the build-tag-selected VM backend (via vm.NewExecutor)
// and ensures that it implements the TxExecutor contract. The returned adapter
// lives in Go land only – the underlying engine might pin CGO resources.
func NewTxExecutor(sdb *state.StateDB) (TxExecutor, error) {
    base, err := vm.NewExecutor(sdb)
    if err != nil {
        return nil, err
    }
    adv, ok := base.(vm.AdvancedExecutor)
    if !ok {
        return nil, fmt.Errorf("executor %T does not implement AdvancedExecutor", base)
    }
    return &vmExecutorAdapter{inner: adv}, nil
}

// vmExecutorAdapter bridges the vm.AdvancedExecutor implemented behind build
// tags to the consensus-layer TxExecutor interface.
type vmExecutorAdapter struct {
    inner vm.AdvancedExecutor
}

func (v *vmExecutorAdapter) Engine() string { return v.inner.Engine() }

func (v *vmExecutorAdapter) ExecuteTx(msg *types.Message, tx *types.Transaction, txIdx int, gp *GasPool, sdb *state.StateDB, header *types.Header, evmCfg vm.Config) (*types.Receipt, error) {
    return v.inner.ExecuteTx(msg, tx, txIdx, gp, sdb, header, evmCfg)
} 