//go:build revm
// +build revm

package revmbridge

import (
    "encoding/hex"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    statedb "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/tracing"
    "github.com/holiman/uint256"
)

// runtime bytecode: implements balanceOf(address) -> uint256 using BALANCE opcode.
// Assembly (12 bytes):
// 0x60 0x04      PUSH1 0x04        ; offset of address in calldata
// 0x35           CALLDATALOAD
// 0x31           BALANCE
// 0x60 0x00      PUSH1 0x00
// 0x52           MSTORE
// 0x60 0x20      PUSH1 0x20        ; length
// 0x60 0x00      PUSH1 0x00        ; offset
// 0xf3           RETURN
var runtimeBalanceOf, _ = hex.DecodeString("6004353160005260206000f3")

// (creation bytecode not used in this test)
// var creationBalanceOf ...

func TestRevm_StateDB_BalanceContract(t *testing.T) {
    // ---------- Prepare Go StateDB ----------
    memDB := statedb.NewDatabaseForTesting()
    sdb, err := statedb.New(common.Hash{}, memDB)
    if err != nil {
        t.Fatalf("failed to create StateDB: %v", err)
    }

    // target user whose balance we will query
    userAddr := common.HexToAddress("0x7777777777777777777777777777777777777777")
    bal := uint256.MustFromDecimal("1000000000000000000") // 1 ETH
    sdb.AddBalance(userAddr, bal, tracing.BalanceChangeUnspecified)

    // Contract account and code
    contractAddr := common.HexToAddress("0x9999999999999999999999999999999999999999")
    sdb.CreateAccount(contractAddr)
    sdb.SetCode(contractAddr, runtimeBalanceOf)

    // ---------- Register handle & build REVM ----------
    handle := NewStateDB(sdb)
    if handle == 0 {
        t.Fatalf("handle is zero")
    }
    defer ReleaseStateDB(handle)

    exec, err := NewRevmExecutorStateDB(handle)
    if err != nil {
        t.Fatalf("failed to create executor: %v", err)
    }
    defer exec.Close()

    // Prepare calldata: selector 0x70a08231 + user address (left-padded)
    selector := []byte{0x70, 0xa0, 0x82, 0x31}
    paddedAddr := common.LeftPadBytes(userAddr.Bytes(), 32)
    data := append(selector, paddedAddr...)

    // Execute view call (gas limit 1m, zero value)
    outputHex, err := exec.CallContract(userAddr.Hex(), contractAddr.Hex(), data, "0x0", 1_000_000)
    if err != nil {
        t.Fatalf("call failed: %v", err)
    }

    // output should equal the user's balance (32-byte big-endian hex)
    expected := hex.EncodeToString(common.LeftPadBytes(bal.ToBig().Bytes(), 32))
    if outputHex != expected {
        t.Fatalf("unexpected output, got %s want %s", outputHex, expected)
    }
} 