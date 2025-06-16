//go:build revm
// +build revm

package tests

import (
    "testing"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/vm"
)

// TestVMDispatcher_RevmFlag ensures that when the `revm` build-tag is enabled
// the central VM dispatcher returns a REVM-backed executor.
func TestVMDispatcher_RevmFlag(t *testing.T) {
    memDB := state.NewDatabaseForTesting()
    sdb, _ := state.New(common.Hash{}, memDB)

    exec, err := vm.NewExecutor(sdb)
    if err != nil {
        t.Fatalf("dispatcher returned error: %v", err)
    }

    t.Logf("[dispatcher] build-tag=REVM  executor=%T  engine=%s", exec, exec.Engine())

    if exec.Engine() != "revm" {
        t.Fatalf("expected REVM executor, got %s", exec.Engine())
    }
} 