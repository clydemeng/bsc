//go:build revm
// +build revm

package tests

import (
    "encoding/hex"
    "strings"
    "testing"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/crypto"
    statedb "github.com/ethereum/go-ethereum/core/state"
    revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
    "github.com/holiman/uint256"
)

func decodeBigaRuntime() []byte {
    bigaRuntimeHex := revmbridge.SmallBigaRuntimeHex()
    cleaned := strings.ReplaceAll(bigaRuntimeHex, "\n", "")
    cleaned = strings.ReplaceAll(cleaned, "\r", "")
    data, _ := hex.DecodeString(cleaned)
    return data
}

func TestBlockCommit_MergedChanges(t *testing.T) {
    memDB := statedb.NewDatabaseForTesting()
    sdb, _ := statedb.New(common.Hash{}, memDB)

    // Accounts
    accA := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    accB := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

    sdb.AddBalance(accA, uint256.MustFromDecimal("1000000000000000000"), 0)

    // Deploy BIGA runtime
    bigaAddr := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
    sdb.CreateAccount(bigaAddr)
    sdb.SetCode(bigaAddr, decodeBigaRuntime())

    handle := revmbridge.NewStateDB(sdb)
    exec, _ := revmbridge.NewRevmExecutorStateDB(handle)
    defer exec.Close()

    encode := func(sel [4]byte, addr common.Address, amt uint64) []byte {
        data := make([]byte, 4+32+32)
        copy(data, sel[:])
        copy(data[4+32-len(addr.Bytes()):4+32], addr.Bytes())
        a := uint256.NewInt(amt).ToBig().Bytes()
        copy(data[len(data)-len(a):], a)
        return data
    }

    selMint := [4]byte{0x40, 0xc1, 0x0f, 0x19}
    selTransfer := [4]byte{0xa9, 0x05, 0x9c, 0xbb}

    // Tx1: mint 100 to A
    _ = exec.CallContractCommit(accA.Hex(), bigaAddr.Hex(), encode(selMint, accA, 100), "0x0", 5_000_000)
    // Tx2: transfer 30 to B
    _ = exec.CallContractCommit(accA.Hex(), bigaAddr.Hex(), encode(selTransfer, accB, 30), "0x0", 5_000_000)

    // Pending changes have NOT been flushed yet; storage slot balance for A should be zero
    calcSlot := func(addr common.Address) common.Hash {
        key := append(common.LeftPadBytes(addr.Bytes(), 32), common.LeftPadBytes([]byte{1}, 32)...)
        return crypto.Keccak256Hash(key)
    }
    slotA := calcSlot(accA)
    rawPre := sdb.GetState(bigaAddr, slotA)
    if rawPre.Big().Uint64() != 0 {
        t.Fatalf("storage was updated before flush, got %d", rawPre.Big().Uint64())
    }

    // Flush end-of-block
    revmbridge.FlushPending(handle)

    rawPost := sdb.GetState(bigaAddr, slotA)
    if rawPost.Big().Uint64() != 70 { // 100-30
        t.Fatalf("expected 70 after flush, got %d", rawPost.Big().Uint64())
    }
    t.Logf("flush successful, A balance slot=%d", rawPost.Big().Uint64())
} 