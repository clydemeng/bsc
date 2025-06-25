//go:build revm
// +build revm

package revmbridge

/*
#cgo CFLAGS: -I${SRCDIR}/../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release
#include <revm_ffi.h>
*/
import "C"

// Clone creates a lightweight snapshot (deep clone) of the current StateDB-
// backed REVM instance. The returned executor shares the same Go StateDB
// handle but owns an independent Rust-side cache. Callers **must** invoke
// Close() on the clone to release resources.
func (e *RevmExecutorStateDB) Clone() *RevmExecutorStateDB {
	if e == nil || e.inst == nil {
		return nil
	}
	dup := C.revm_snapshot_clone(e.inst)
	if dup == nil {
		return nil
	}
	return &RevmExecutorStateDB{inst: dup, handle: e.handle}
}

// Commit merges the snapshot back into the given parent executor and also
// frees the snapshot. The receiver must be a clone previously created via
// (*RevmExecutorStateDB).Clone.
func (e *RevmExecutorStateDB) Commit(parent *RevmExecutorStateDB) {
	if e == nil || parent == nil || e.inst == nil || parent.inst == nil {
		return
	}
	C.revm_snapshot_commit(parent.inst, e.inst)
	// Immediately discard both cache layers on the parent so that any
	// subsequent look-ups observe the freshly merged state.
	C.revm_clear_caches_statedb(parent.inst)
	// After commit `e.inst` has been freed inside Rust – mark as nil to avoid
	// double-free in Close().
	e.inst = nil
}
