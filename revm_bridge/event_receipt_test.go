//go:build revm
// +build revm

package revmbridge

import (
    "encoding/hex"
    "io/ioutil"
    "strings"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    statedb "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/tracing"
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/holiman/uint256"
)

// Test that a LOG1 emitted by the runtime is surfaced through the FFI and the
// receipt bloom is non-empty.
func TestReceipt_WithLogBloom(t *testing.T) {
    // load runtime hex
    raw, err := ioutil.ReadFile("event_runtime_hex.txt")
    if err != nil {
        t.Fatalf("failed to read runtime: %v", err)
    }
    runtimeHex := strings.TrimSpace(string(raw))
    runtime, _ := hex.DecodeString(runtimeHex)

    mem := statedb.NewDatabaseForTesting()
    sdb, _ := statedb.New(common.Hash{}, mem)

    caller := common.HexToAddress("0xaAaAaAaaAaAaAaaAaAAAAAAAAaaaAaAaAaaAaaAa")
    sdb.AddBalance(caller, uint256.MustFromDecimal("1000000000000000000"), tracing.BalanceChangeUnspecified)

    contract := common.HexToAddress("0xdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDdDd")
    sdb.CreateAccount(contract)
    sdb.SetCode(contract, runtime)

    handle := NewStateDB(sdb)
    exec, _ := NewRevmExecutorStateDB(handle)
    defer exec.Close()

    // simple call with no data
    receipt, err := exec.CallContractCommitReceipt(caller.Hex(), contract.Hex(), nil, "0x0", 100_000, 0, nil)
    if err != nil {
        t.Fatalf("exec failed: %v", err)
    }
    if len(receipt.Logs) != 1 {
        t.Fatalf("expected 1 log, got %d", len(receipt.Logs))
    }
    if receipt.Bloom == (types.Bloom{}) {
        t.Fatalf("expected non-empty bloom")
    }
} 