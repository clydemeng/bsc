//go:build cgo && revm
// +build cgo,revm

package revmbridge

/*
#cgo CFLAGS: -I../../revm_integration/revm_ffi_wrapper
#include <stdint.h>
#include <string.h>
#include <stdio.h>

// Fallback definitions — the canonical layout lives in Rust, but we redefine
// them here so that `cgo` knows the sizes and can generate the Go bindings. The
// struct layout **must** remain in sync with `statedb_types.rs` and
// `STATE_DB_FFI.md`.

typedef struct {
    uint8_t bytes[20];
} FFIAddress;

typedef struct {
    uint8_t bytes[32];
} FFIHash;

typedef struct {
    uint8_t bytes[32];
} FFIU256;

typedef struct {
    FFIU256 balance;
    uint64_t nonce;
    FFIHash code_hash;
} FFIAccountInfo;

// ------------------- debug helpers (instrumentation) --------------------
// (left intentionally blank – verbose handle-level traces removed)
*/
import "C"

import (
    "unsafe"
    "fmt"
    "math/big"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/holiman/uint256"
)

// helper to convert C.FFIAddress → go common.Address
func cAddressToGo(addr C.FFIAddress) common.Address {
    var out common.Address
    C.memcpy(unsafe.Pointer(&out[0]), unsafe.Pointer(&addr.bytes[0]), 20)
    return out
}

func goHashToC(h FFIHash) C.FFIHash {
    return *(*C.FFIHash)(unsafe.Pointer(&h))
}

func goU256ToC(u FFIU256) C.FFIU256 {
    return *(*C.FFIU256)(unsafe.Pointer(&u))
}

//export re_state_basic
func re_state_basic(handle C.uintptr_t, addr C.FFIAddress, out_info *C.FFIAccountInfo) C.int {
    gAddr := cAddressToGo(addr)

    st, ok := lookup(uintptr(handle))
    if !ok || st == nil || out_info == nil {
        return -1
    }

    info := st.Basic(gAddr)

    // Developer-friendly log: BNB & BIGA side by side
    bnb := new(big.Int).SetBytes(info.Balance[:])
    biga := getBigaBalance(st.db, gAddr)
    fmt.Printf("[Go] READ  addr=%s  nonce=%d  BNB=%s  BIGA=%s\n", gAddr.Hex(), info.Nonce, bnb.String(), biga)

    // Fill the C struct
    out_info.balance = goU256ToC(info.Balance)
    out_info.nonce = C.uint64_t(info.Nonce)
    out_info.code_hash = goHashToC(info.CodeHash)
    return 0
}

//export re_state_storage
func re_state_storage(handle C.uintptr_t, addr C.FFIAddress, slot C.FFIHash, out_val *C.FFIU256) C.int {
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil || out_val == nil {
        return -1
    }

    gAddr := cAddressToGo(addr)
    var gSlot common.Hash
    C.memcpy(unsafe.Pointer(&gSlot[0]), unsafe.Pointer(&slot.bytes[0]), 32)

    val := st.Storage(gAddr, gSlot)

    // Compact log of the read
    balInt := new(big.Int).SetBytes(val[:])
    fmt.Printf("[Go] READ_STORAGE addr=%s slot=%s value=%s\n", gAddr.Hex(), gSlot.Hex(), balInt.String())

    *out_val = goU256ToC(val)
    return 0
}

//export re_state_block_hash
func re_state_block_hash(handle C.uintptr_t, number C.uint64_t, out_hash *C.FFIHash) C.int {
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil || out_hash == nil {
        return -1
    }
    h := st.BlockHash(uint64(number))
    *out_hash = goHashToC(h)
    return 0
}

//export re_state_code
func re_state_code(handle C.uintptr_t, code_hash C.FFIHash, out_ptr *unsafe.Pointer, out_len *C.uint32_t) C.int {
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil || out_ptr == nil || out_len == nil {
        return -1
    }
    var gHash common.Hash
    C.memcpy(unsafe.Pointer(&gHash[0]), unsafe.Pointer(&code_hash.bytes[0]), 32)

    code := st.CodeByHash(gHash)
    if len(code) == 0 {
        *out_ptr = nil
        *out_len = 0
        return 1 // not found
    }
    cbuf := C.CBytes(code)
    *out_ptr = cbuf
    *out_len = C.uint32_t(len(code))
    return 0
}

//export re_state_set_basic
func re_state_set_basic(handle C.size_t, addr C.FFIAddress, info C.FFIAccountInfo) C.int {
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil {
        return -1
    }
    gAddr := cAddressToGo(addr)
    // Convert incoming balance
    var bal FFIU256
    C.memcpy(unsafe.Pointer(&bal), unsafe.Pointer(&info.balance), 32)

    // We need the current codeHash for the account but must avoid a recursive
    // lock on st.mu (st.Basic also acquires the same mutex). Fetch it directly
    // from the underlying StateDB instead.
    st.mu.Lock()
    codeHash := st.db.GetCodeHash(gAddr)

    // Ensure journal maps are initialised.
    st.ensureJournal()

    st.pendingBasic[gAddr] = FFIAccountInfo{
        Balance:  bal,
        Nonce:    uint64(info.nonce),
        CodeHash: hashToFFI(codeHash),
    }
    st.mu.Unlock()

    fmt.Printf("[Go] PENDING_BASIC addr=%s nonce=%d\n", gAddr.Hex(), uint64(info.nonce))
    return 0
}

// helper convert
func ffiU256ToUint256(u C.FFIU256) *uint256.Int {
    var bytes [32]byte
    C.memcpy(unsafe.Pointer(&bytes[0]), unsafe.Pointer(&u.bytes[0]), 32)
    i := new(uint256.Int)
    i.SetBytes(bytes[:])
    return i
}

//export re_state_set_storage
func re_state_set_storage(handle C.size_t, addr C.FFIAddress, slot C.FFIHash, value C.FFIU256) C.int {
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil {
        return -1
    }
    gAddr := cAddressToGo(addr)
    var gSlot common.Hash
    C.memcpy(unsafe.Pointer(&gSlot[0]), unsafe.Pointer(&slot.bytes[0]), 32)
    var bytes [32]byte
    C.memcpy(unsafe.Pointer(&bytes[0]), unsafe.Pointer(&value.bytes[0]), 32)
    st.ensureJournal()
    st.mu.Lock()
    slots := st.pendingStorage[gAddr]
    if slots == nil {
        slots = make(map[common.Hash]common.Hash)
        st.pendingStorage[gAddr] = slots
    }
    slots[gSlot] = common.BytesToHash(bytes[:])
    st.mu.Unlock()
    fmt.Printf("[Go] PENDING_STORAGE addr=%s slot=%s value=%s\n", gAddr.Hex(), gSlot.Hex(), common.BytesToHash(bytes[:]).Hex())
    return 0
}

// -----------------------------------------------------------------------------
// Helper utilities for prettier logs
// -----------------------------------------------------------------------------

var bigaContractAddr = common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")

// getBigaBalance returns the BIGA token balance for `addr` by reading the
// standard `mapping(address => uint256) balances` at storage slot 1.
// The mapping layout is `keccak256(abi.encode(addr, uint256(1)))`.
func getBigaBalance(db *state.StateDB, addr common.Address) string {
    // Compute slot = keccak(pad(addr) || pad(1))
    key := append(common.LeftPadBytes(addr.Bytes(), 32), common.LeftPadBytes([]byte{1}, 32)...)
    slot := crypto.Keccak256Hash(key)
    raw := db.GetState(bigaContractAddr, slot)
    return raw.Big().String()
} 