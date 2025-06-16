//go:build revm
// +build revm

package revmbridge

import (
    "encoding/hex"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    statedb "github.com/ethereum/go-ethereum/core/state"
    "github.com/holiman/uint256"
)

// Simple runtime with read/write to storage slot0.
// Hex: 3615600c57600035600055005b60005460005260206000f3
var rwRuntime, _ = hex.DecodeString("3615600c57600035600055005b60005460005260206000f3")

func TestRevm_StateDB_ReadWrite(t *testing.T) {
    memDB := statedb.NewDatabaseForTesting()
    sdb, _ := statedb.New(common.Hash{}, memDB)

    user := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

    // Fund user with some BNB so that they can pay gas (gas price = 1 gwei).
    sdb.AddBalance(user, uint256.MustFromDecimal("1000000000000000000"), 0)

    // Deploy contract (by directly inserting code)
    contract := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    sdb.CreateAccount(contract)
    sdb.SetCode(contract, rwRuntime)

    handle := NewStateDB(sdb)
    exec, _ := NewRevmExecutorStateDB(handle)

    t.Logf("initial nonce=%d", sdb.GetNonce(user))

    // 1. Read balance (should be 0)
    output, err := exec.CallContract(user.Hex(), contract.Hex(), nil, "0x0", 1_000_000)
    if err != nil {
        t.Fatalf("initial read failed: %v", err)
    }
    if output != "0000000000000000000000000000000000000000000000000000000000000000" {
        t.Fatalf("expected zero, got %s", output)
    }

    // 2. Mint 99 tokens by calling with calldata = 99 (32-byte BE)
    data := make([]byte, 32)
    data[31] = 99 // 0x63
    if err := exec.CallContractCommit(user.Hex(), contract.Hex(), data, "0x0", 1_000_000); err != nil {
        t.Fatalf("mint failed: %v", err)
    }

    t.Logf("post-mint nonce=%d", sdb.GetNonce(user))

    // Check storage value directly via StateDB BEFORE flush – should still be zero
    slot0 := common.Hash{}
    valPre := sdb.GetState(contract, slot0)
    if valPre.Big().Uint64() != 0 {
        t.Fatalf("expected slot0 to be 0 before flush, got %s", valPre.String())
    }

    // 3. Read again – expect 99
    output2, err := exec.CallContract(user.Hex(), contract.Hex(), nil, "0x0", 1_000_000)
    if err != nil {
        t.Fatalf("second read failed: %v", err)
    }
    if output2[len(output2)-2:] != "63" { // last byte should be 0x63
        t.Fatalf("expected 99, got %s", output2)
    }

    // Now close the executor which triggers FlushPending internally
    exec.Close()

    // After flush the raw StateDB slot should be updated to 99 (0x63)
    valPost := sdb.GetState(contract, slot0)
    if valPost.Big().Uint64() != 99 {
        t.Fatalf("expected slot0 to be 99 after flush, got %s", valPost.String())
    }
} 