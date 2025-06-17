//go:build revm
// +build revm

package revmbridge

import (
    "crypto/ecdsa"
    "encoding/hex"
    "io/ioutil"
    "math/big"
    "strings"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/ethereum/go-ethereum/core/vm"
    "github.com/ethereum/go-ethereum/crypto"
)

// TestReceiptParity_GoEVM_vs_REVM executes the same simple transaction on both
// the legacy Go-EVM execution path and the new REVM backend, then asserts that
// the produced receipts match (status, gas used, bloom, logs).
func TestReceiptParity_GoEVM_vs_REVM(t *testing.T) {
    // ---------------------------------------------------------------------
    // 0. Load the example runtime that emits a single LOG1 event (same helper
    //    used by TestReceipt_WithLogBloom).
    // ---------------------------------------------------------------------
    raw, err := ioutil.ReadFile("event_runtime_hex.txt")
    if err != nil {
        t.Fatalf("failed to read runtime hex: %v", err)
    }
    runtime, _ := hex.DecodeString(strings.TrimSpace(string(raw)))

    // Addresses for caller and contract
    callerKey, _ := crypto.GenerateKey()
    callerAddr := crypto.PubkeyToAddress(callerKey.Public().(ecdsa.PublicKey))
    contractAddr := common.HexToAddress("0xD0c0fFEEcafeDeAdbEeF000000000000000000000")

    // Helper to initialise a fresh in-memory StateDB with identical state.
    newState := func() *state.StateDB {
        mem := state.NewDatabaseForTesting()
        sdb, _ := state.New(common.Hash{}, mem)
        sdb.AddBalance(callerAddr, big.NewInt(1e18))
        sdb.CreateAccount(contractAddr)
        sdb.SetCode(contractAddr, runtime)
        return sdb
    }

    gasLimit := uint64(200_000)

    // ------------------------------------------------------------------
    // 1. Execute via Go-EVM to obtain the reference receipt.
    // ------------------------------------------------------------------
    sdbGo := newState()
    header := &types.Header{Number: big.NewInt(1), GasLimit: 30_000_000}

    // Build a legacy transaction calling the contract with no data.
    tx := types.NewTransaction(0, contractAddr, big.NewInt(0), gasLimit, big.NewInt(1), nil)
    signer := types.LatestSignerForChainID(big.NewInt(1))
    tx, _ = types.SignTx(tx, signer, callerKey)

    msg, err := core.TransactionToMessage(tx, signer, nil)
    if err != nil {
        t.Fatalf("failed to build message: %v", err)
    }
    gp := new(core.GasPool).AddGas(header.GasLimit)
    context := core.NewEVMBlockContext(header, nil, nil)
    evm := vm.NewEVM(context, sdbGo, nil, vm.Config{})
    usedGas := new(uint64)
    refReceipt, err := core.ApplyTransactionWithEVM(msg, gp, sdbGo, header.Number, header.Hash(), tx, usedGas, evm)
    if err != nil {
        t.Fatalf("Go-EVM execution error: %v", err)
    }

    // ------------------------------------------------------------------
    // 2. Execute via REVM backend.
    // ------------------------------------------------------------------
    sdbRevm := newState()
    handle := NewStateDB(sdbRevm)
    exec, _ := NewRevmExecutorStateDB(handle)
    defer exec.Close()

    revmReceipt, err := exec.CallContractCommitReceipt(callerAddr.Hex(), contractAddr.Hex(), nil, "0x0", gasLimit, 0, tx)
    if err != nil {
        t.Fatalf("REVM execution error: %v", err)
    }

    // ------------------------------------------------------------------
    // 3. Compare key fields.
    // ------------------------------------------------------------------
    if refReceipt.Status != revmReceipt.Status {
        t.Fatalf("status mismatch: go=%d revm=%d", refReceipt.Status, revmReceipt.Status)
    }
    if refReceipt.GasUsed != revmReceipt.GasUsed {
        t.Fatalf("gasUsed mismatch: go=%d revm=%d", refReceipt.GasUsed, revmReceipt.GasUsed)
    }
    if refReceipt.Bloom != revmReceipt.Bloom {
        t.Fatalf("bloom mismatch")
    }
    if len(refReceipt.Logs) != len(revmReceipt.Logs) {
        t.Fatalf("log len mismatch: go=%d revm=%d", len(refReceipt.Logs), len(revmReceipt.Logs))
    }
    for i, l := range refReceipt.Logs {
        rl := revmReceipt.Logs[i]
        if l.Address != rl.Address || !logsEqual(l, rl) {
            t.Fatalf("log %d mismatch: go=%+v revm=%+v", i, l, rl)
        }
    }
}

// logsEqual compares topics and data.
func logsEqual(a, b *types.Log) bool {
    if len(a.Topics) != len(b.Topics) || !strings.EqualFold(hex.EncodeToString(a.Data), hex.EncodeToString(b.Data)) {
        return false
    }
    for i := range a.Topics {
        if a.Topics[i] != b.Topics[i] {
            return false
        }
    }
    return true
} 