//go:build revm
// +build revm

package revmbridge

/*
#cgo CFLAGS: -I${SRCDIR}/../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release
#include <stdint.h>

// Forward declarations from Rust side
void revm_reset_miss_counters();
void revm_get_miss_counters(uintptr_t* out_accounts, uintptr_t* out_storage);
*/
import "C"

// ResetProfileCounters zeros the Rust-side miss counters.
func ResetProfileCounters() {
	C.revm_reset_miss_counters()
}

// ProfileCounters returns (accountMisses, storageMisses) since last reset.
func ProfileCounters() (int64, int64) {
	var acc, stor C.uintptr_t
	C.revm_get_miss_counters(&acc, &stor)
	return int64(acc), int64(stor)
}
