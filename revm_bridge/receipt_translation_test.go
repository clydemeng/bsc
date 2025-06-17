//go:build revm
// +build revm

package revmbridge

import (
    "encoding/hex"
    "strings"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    statedb "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/tracing"
    "github.com/holiman/uint256"
)

// ERC20 Transfer selector 0xddf252ad + indexed topics. Using the small BIGA runtime which emits Transfer logs.

func TestReceiptTranslation(t *testing.T) {
    mem := statedb.NewDatabaseForTesting()
    sdb, _ := statedb.New(common.Hash{}, mem)

    // accounts
    sender := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    sdb.AddBalance(sender, uint256.MustFromDecimal("1000000000000000000"), tracing.BalanceChangeUnspecified)

    biga := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
    sdb.CreateAccount(biga)

    // put runtime
    runtimeHex := SmallBigaRuntimeHex()
    runtimeBytes, _ := hex.DecodeString(strings.TrimSpace(runtimeHex))
    sdb.SetCode(biga, runtimeBytes)

    handle := NewStateDB(sdb)
    exec, _ := NewRevmExecutorStateDB(handle)
    defer exec.Close()

    // calldata: mint 10 tokens to sender
    selMint := [4]byte{0x40, 0xc1, 0x0f, 0x19}
    data := make([]byte, 4+32+32)
    copy(data, selMint[:])
    copy(data[4+32-len(sender.Bytes()):4+32], sender.Bytes())
    data[len(data)-1] = 10 // amount 10

    receipt, err := exec.CallContractCommitReceipt(sender.Hex(), biga.Hex(), data, "0x0", 5_000_000, 0, nil)
    if err != nil {
        t.Fatalf("execution failed: %v", err)
    }

    if receipt == nil {
        t.Fatalf("nil receipt")
    }
    if receipt.GasUsed == 0 {
        t.Fatalf("gas used should be >0")
    }
    t.Logf("got %d log(s), gasUsed=%d status=%d", len(receipt.Logs), receipt.GasUsed, receipt.Status)
} 