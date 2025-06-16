//go:build revm
// +build revm

package revmbridge

import (
    "encoding/hex"
    "strings"
    "testing"
    _ "embed"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/crypto"
    statedb "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/tracing"
    "github.com/holiman/uint256"
)

//go:embed small_biga_runtime_hex.txt
var bigaRuntimeHex string

func decodeBigaRuntime() []byte {
    cleaned := strings.ReplaceAll(bigaRuntimeHex, "\n", "")
    cleaned = strings.ReplaceAll(cleaned, "\r", "")
    data, _ := hex.DecodeString(cleaned)
    return data
}

func TestRevm_StateDB_BIGA_ReadWrite(t *testing.T) {
    // ---------------- Prepare Go StateDB ------------------
    memDB := statedb.NewDatabaseForTesting()
    sdb, _ := statedb.New(common.Hash{}, memDB)

    // Accounts
    accA := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    accB := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

    // Fund BNB balances: 1 and 2 BNB (1e18 base units)
    oneBNB := uint256.MustFromDecimal("1000000000000000000")
    twoBNB := uint256.MustFromDecimal("2000000000000000000")
    sdb.AddBalance(accA, oneBNB, tracing.BalanceChangeUnspecified)
    sdb.AddBalance(accB, twoBNB, tracing.BalanceChangeUnspecified)

    // Deploy BIGA contract (insert runtime code)
    bigaAddr := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
    sdb.CreateAccount(bigaAddr)
    sdb.SetCode(bigaAddr, decodeBigaRuntime())

    // ------------ Register StateDB handle & REVM ----------
    handle := NewStateDB(sdb)
    exec, _ := NewRevmExecutorStateDB(handle)
    defer exec.Close()

    // Helper to encode calldata
    encode := func(selector [4]byte, addr common.Address, amount uint64) []byte {
        data := make([]byte, 4+32+32)
        copy(data[0:4], selector[:])
        copy(data[4+32-len(addr.Bytes()):4+32], addr.Bytes())
        amt := uint256.NewInt(amount)
        amtBytes := amt.ToBig().Bytes()
        copy(data[4+32+32-len(amtBytes):], amtBytes)
        return data
    }

    // ---- Step 1: Mint 999 BIGA for account A ----
    var selMint = [4]byte{0x40, 0xc1, 0x0f, 0x19}
    if err := exec.CallContractCommit(accA.Hex(), bigaAddr.Hex(), encode(selMint, accA, 999), "0x0", 5_000_000); err != nil {
        t.Fatalf("mint failed: %v", err)
    }

    // Query balanceOf A (expect 999)
    var selBal = [4]byte{0x70, 0xa0, 0x82, 0x31}
    balAHex, err := exec.CallContract(accA.Hex(), bigaAddr.Hex(), append(selBal[:], common.LeftPadBytes(accA.Bytes(), 32)...), "0x0", 1_000_000)
    if err != nil {
        t.Fatalf("balanceOf call failed: %v", err)
    }
    t.Logf("balanceOf(A) after mint = %s", balAHex)
    if !strings.HasSuffix(balAHex, "03e7") { // 0x3e7 == 999
        t.Fatalf("unexpected balance for A after mint: %s", balAHex)
    }

    // ---- Step 2: Transfer 99 BIGA from A to B ----
    var selTransfer = [4]byte{0xa9, 0x05, 0x9c, 0xbb}
    if err := exec.CallContractCommit(accA.Hex(), bigaAddr.Hex(), encode(selTransfer, accB, 99), "0x0", 5_000_000); err != nil {
        t.Fatalf("transfer failed: %v", err)
    }

    // Query balances again
    balANowHex, _ := exec.CallContract(accA.Hex(), bigaAddr.Hex(), append(selBal[:], common.LeftPadBytes(accA.Bytes(), 32)...), "0x0", 1_000_000)
    balBHex, _ := exec.CallContract(accA.Hex(), bigaAddr.Hex(), append(selBal[:], common.LeftPadBytes(accB.Bytes(), 32)...), "0x0", 1_000_000)
    t.Logf("balanceOf(A) after transfer = %s", balANowHex)
    t.Logf("balanceOf(B) after transfer = %s", balBHex)

    if !strings.HasSuffix(balANowHex, "0384") { // 900 decimal = 0x384
        t.Fatalf("A balance wrong expected 900 got %s", balANowHex)
    }
    if !strings.HasSuffix(balBHex, "063") { // 99 decimal = 0x63
        t.Fatalf("B balance wrong expected 99 got %s", balBHex)
    }

    // ---- Inspect StateDB storage directly ----
    // slot 0: totalSupply
    slot0 := common.Hash{}
    rawTotal := sdb.GetState(bigaAddr, slot0)
    t.Logf("StateDB slot0 totalSupply raw: %s", rawTotal.Hex())

    // Helper to compute mapping slot keccak(pad(key) ++ pad(1))
    calcSlot := func(addr common.Address) common.Hash {
        key := append(common.LeftPadBytes(addr.Bytes(), 32), common.LeftPadBytes([]byte{1}, 32)...)
        return crypto.Keccak256Hash(key)
    }

    slotA := calcSlot(accA)
    slotB := calcSlot(accB)
    rawA := sdb.GetState(bigaAddr, slotA)
    rawB := sdb.GetState(bigaAddr, slotB)
    t.Logf("StateDB balance slot A (%s) = %s", slotA.Hex(), rawA.Hex())
    t.Logf("StateDB balance slot B (%s) = %s", slotB.Hex(), rawB.Hex())

    // simple numeric checks
    if rawA.Big().Uint64() != 900 {
        t.Fatalf("StateDB balance A expected 900 got %d", rawA.Big().Uint64())
    }
    if rawB.Big().Uint64() != 99 {
        t.Fatalf("StateDB balance B expected 99 got %d", rawB.Big().Uint64())
    }

    // ---- Step 3: totalSupply check ----
    var selTotal = [4]byte{0x18, 0x16, 0x0d, 0xdd}
    totalHex, _ := exec.CallContract(accA.Hex(), bigaAddr.Hex(), selTotal[:], "0x0", 1_000_000)
    t.Logf("totalSupply = %s", totalHex)
} 