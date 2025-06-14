//go:build revm
// +build revm

package revmbridge

/*
#cgo CFLAGS: -I${SRCDIR}/../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,${SRCDIR}/../../revm_integration/revm_ffi_wrapper/target/release
#include <stdlib.h>
#include <string.h>
#include <revm_ffi.h>
*/
import "C"

import (
    "encoding/hex"
    "errors"
    "unsafe"
)

type RevmExecutorStateDB struct {
    inst *C.RevmInstanceStateDB
}

// NewRevmExecutorStateDB creates an EVM instance that pulls state from the given handle.
// The handle must have been obtained via NewStateDB and remain valid for the
// lifetime of the executor.
func NewRevmExecutorStateDB(handle uintptr) (*RevmExecutorStateDB, error) {
    var cfg C.RevmConfigFFI // zero-initialised – defaults are fine (chain 1, Prague)
    cfg.chain_id = 1
    cfg.spec_id = 19

    inst := C.revm_new_with_statedb(C.size_t(handle), &cfg)
    if inst == nil {
        return nil, errors.New("failed to create REVM instance with statedb")
    }
    return &RevmExecutorStateDB{inst: inst}, nil
}

func (e *RevmExecutorStateDB) Close() {
    if e.inst != nil {
        C.revm_free_statedb_instance(e.inst)
        e.inst = nil
    }
}

// CallContract executes a readonly call (CALL opcode) against the given contract.
// Parameters mirror RevmExecutor.CallContract.
func (e *RevmExecutorStateDB) CallContract(from, to string, data []byte, value string, gasLimit uint64) (string, error) {
    cFrom := C.CString(from)
    defer C.free(unsafe.Pointer(cFrom))
    cTo := C.CString(to)
    defer C.free(unsafe.Pointer(cTo))

    var cDataPtr *C.uchar
    if len(data) > 0 {
        cDataPtr = (*C.uchar)(unsafe.Pointer(&data[0]))
    }

    cValue := C.CString(value)
    defer C.free(unsafe.Pointer(cValue))

    result := C.revm_call_contract_statedb(
        e.inst,
        cFrom,
        cTo,
        cDataPtr,
        C.uint(len(data)),
        cValue,
        C.uint64_t(gasLimit),
    )

    if result == nil {
        return "", errors.New("contract execution failed: result nil")
    }
    defer C.revm_free_execution_result(result)

    if result.success == 0 {
        return "", errors.New("contract execution reverted")
    }

    output := make([]byte, result.output_len)
    if result.output_len > 0 {
        C.memcpy(unsafe.Pointer(&output[0]), unsafe.Pointer(result.output_data), C.size_t(result.output_len))
    }
    return hex.EncodeToString(output), nil
} 