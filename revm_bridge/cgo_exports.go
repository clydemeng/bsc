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
	"fmt"
	"math/big"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// -----------------------------------------------------------------------------
// Toggleable debug logger. Set `revmVerboseLogs` to true locally or re-enable
// through a dedicated build-tag file when deep diagnostics are needed.
// -----------------------------------------------------------------------------

var revmVerboseLogs = false

func dbg(format string, a ...interface{}) {
	if revmVerboseLogs {
		fmt.Printf(format, a...)
	}
}

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

	// If the account truly does not exist (zero balance, zero nonce, empty code
	// hash) *and* there is no pending overlay, signal "not found" to the Rust
	// side by returning 1.  This prevents REVM from treating every empty
	// account as existing, which in turn avoids false CreateCollision halts
	// during contract deployment.
	if info.Nonce == 0 {
		var zeroHash FFIHash
		isZeroBalance := true
		for _, b := range info.Balance {
			if b != 0 {
				isZeroBalance = false
				break
			}
		}
		if isZeroBalance && info.CodeHash == zeroHash {
			return 1 // not found
		}
	}

	// Developer-friendly log: BNB & BIGA side by side
	bnb := new(big.Int).SetBytes(info.Balance[:])
	biga := getBigaBalance(st.db, gAddr)
	dbg("[Go] READ  addr=%s  nonce=%d  BNB=%s  BIGA=%s\n", gAddr.Hex(), info.Nonce, bnb.String(), biga)

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
	dbg("[Go] READ_STORAGE addr=%s slot=%s value=%s\n", gAddr.Hex(), gSlot.Hex(), balInt.String())

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

	// Extract provided code hash from the FFI struct.
	var ffiCodeHash FFIHash
	C.memcpy(unsafe.Pointer(&ffiCodeHash), unsafe.Pointer(&info.code_hash), 32)

	st.mu.Lock()
	// Ensure journal maps are initialised.
	st.ensureJournal()

	st.pendingBasic[gAddr] = FFIAccountInfo{
		Balance:  bal,
		Nonce:    uint64(info.nonce),
		CodeHash: ffiCodeHash,
	}

	// Ensure that newly-created contracts have at least a placeholder code
	// entry so that WaitDeployed's CodeAt check observes non-empty bytecode
	// right after block commit. The real runtime code will be written by
	// flushPending once the placeholder is present.
	ch := ffiHashToCommon(ffiCodeHash)
	if ch != (common.Hash{}) {
		if _, ok := st.codeCache.Load(ch); !ok {
			st.codeCache.Store(ch, []byte{0x60, 0x00}) // PUSH0 (valid, 2 bytes)
		}
	}

	st.mu.Unlock()

	dbg("[Go] PENDING_BASIC addr=%s nonce=%d\n", gAddr.Hex(), uint64(info.nonce))
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
	dbg("[Go] PENDING_STORAGE addr=%s slot=%s value=%s\n", gAddr.Hex(), gSlot.Hex(), common.BytesToHash(bytes[:]).Hex())
	return 0
}

//export re_state_store_code
func re_state_store_code(handle C.size_t, code_hash C.FFIHash, code_ptr unsafe.Pointer, code_len C.uint32_t) C.int {
	st, ok := lookup(uintptr(handle))
	if !ok || st == nil {
		return -1
	}
	var gHash common.Hash
	C.memcpy(unsafe.Pointer(&gHash[0]), unsafe.Pointer(&code_hash.bytes[0]), 32)

	if code_ptr == nil || code_len == 0 {
		return -2
	}
	data := C.GoBytes(code_ptr, C.int(code_len))
	// Store a fresh copy; callers may free the original buffer.
	st.codeCache.Store(gHash, append([]byte(nil), data...))
	dbg("[Go] STORE_CODE hash=%s len=%d\n", gHash.Hex(), len(data))
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
