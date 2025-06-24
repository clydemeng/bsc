//go:build revm
// +build revm

package revmbridge

/*
#cgo CFLAGS: -I${SRCDIR}/../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release
#include <revm_ffi.h>
*/
import "C"

import (
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
)

// BatchKey identifies a (address, storage slot) tuple to be prefetched into
// REVM's internal cache. An all-zero Slot indicates that only the account
// (balance, nonce, code hash) should be primed without touching storage.
//
// The struct purposefully mirrors the layout of the Rust-side FFIBatchKey so
// that we can build the C array in Go and pass it across the FFI boundary.
// Note: common.Hash is a 32-byte value (big-endian); Address is 20-bytes.
// Only the first 32 bytes of Slot are passed through, any higher-order data is
// ignored (identical to EVM semantics).

type BatchKey struct {
	Address common.Address
	Slot    common.Hash
}

// Prefetch attempts to load the provided keys into REVM's in-memory cache so
// that subsequent execution can resolve them without invoking the Go callback
// layer. The function is best-effort: unknown accounts/slots are silently
// ignored, and the call is a no-op if the slice is empty.
func (e *RevmExecutorStateDB) Prefetch(keys []BatchKey) {
	if len(keys) == 0 || e == nil || e.inst == nil {
		return
	}

	// Materialise a C array with one-to-one mapping.
	cKeys := make([]C.FFIBatchKey, len(keys))

	for i, k := range keys {
		// Address (20 bytes)
		var cAddr C.FFIAddress
		addrBytes := k.Address.Bytes()
		for j := 0; j < 20; j++ {
			cAddr.bytes[j] = C.uchar(addrBytes[j])
		}

		// Slot (32 bytes)
		var cSlot C.FFIHash
		slotBytes := k.Slot.Bytes()
		for j := 0; j < 32; j++ {
			cSlot.bytes[j] = C.uchar(slotBytes[j])
		}

		cKeys[i].address = cAddr
		cKeys[i].slot = cSlot
	}

	C.revm_prefetch_batch(e.inst, (*C.FFIBatchKey)(unsafe.Pointer(&cKeys[0])), C.size_t(len(cKeys)))
}
