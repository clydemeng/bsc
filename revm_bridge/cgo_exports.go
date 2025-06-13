//go:build cgo && revmcb
// +build cgo,revmcb

package revmbridge

/*
#cgo CFLAGS: -I../../revm_integration/revm_ffi_wrapper
#include <stdint.h>
#include <string.h>

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
*/
import "C"

import (
    "unsafe"

    "github.com/ethereum/go-ethereum/common"
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
    st, ok := lookup(uintptr(handle))
    if !ok || st == nil || out_info == nil {
        return -1
    }

    info := st.Basic(cAddressToGo(addr))

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