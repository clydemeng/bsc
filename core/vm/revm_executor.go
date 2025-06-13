//go:build revm
// +build revm

package vm

/*
#cgo CFLAGS: -I../../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L../../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,../../../revm_integration/revm_ffi_wrapper/target/release
#include <stdlib.h>
#include <string.h>
#include "revm_ffi.h"
*/
import "C"
import (
	"encoding/hex"
	"errors"
	"unsafe"
)

type RevmExecutor struct {
	instance *C.RevmInstance
}

func NewRevmExecutor() (*RevmExecutor, error) {
	instance := C.revm_new()
	if instance == nil {
		return nil, errors.New("failed to create REVM instance")
	}
	return &RevmExecutor{instance: instance}, nil
}

func (e *RevmExecutor) Close() {
	if e.instance != nil {
		C.revm_free(e.instance)
		e.instance = nil
	}
}

func (e *RevmExecutor) SetBalance(address string, balance string) error {
	cAddress := C.CString(address)
	defer C.free(unsafe.Pointer(cAddress))
	cBalance := C.CString(balance)
	defer C.free(unsafe.Pointer(cBalance))

	if C.revm_set_balance(e.instance, cAddress, cBalance) != 0 {
		return errors.New("failed to set balance")
	}
	return nil
}

func (e *RevmExecutor) CallContract(from, to string, data []byte, value string, gasLimit uint64) (string, error) {
	cFrom := C.CString(from)
	defer C.free(unsafe.Pointer(cFrom))
	cTo := C.CString(to)
	defer C.free(unsafe.Pointer(cTo))
	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cValue))

	var cDataPtr *C.uchar
	if len(data) > 0 {
		cDataPtr = (*C.uchar)(unsafe.Pointer(&data[0]))
	}

	result := C.revm_call_contract(
		e.instance,
		cFrom,
		cTo,
		cDataPtr,
		C.uint(len(data)),
		cValue,
		C.uint64_t(gasLimit),
	)

	if result == nil {
		return "", errors.New("contract execution failed: result is nil")
	}
	defer C.revm_free_execution_result(result)

	if result.success == 0 {
		return "", errors.New("contract execution failed")
	}

	output := make([]byte, result.output_len)
	if result.output_len > 0 {
		C.memcpy(unsafe.Pointer(&output[0]), unsafe.Pointer(result.output_data), C.size_t(result.output_len))
	}
	return hex.EncodeToString(output), nil
}

// DeployContract deploys the given bytecode from the specified deployer address and returns the created contract address.
func (e *RevmExecutor) DeployContract(deployer string, bytecode []byte, gasLimit uint64) (string, error) {
	if len(bytecode) == 0 {
		return "", errors.New("bytecode is empty")
	}

	cDeployer := C.CString(deployer)
	defer C.free(unsafe.Pointer(cDeployer))

	// Obtain pointer to bytecode slice
	cBytecodePtr := (*C.uchar)(unsafe.Pointer(&bytecode[0]))

	result := C.revm_deploy_contract(
		e.instance,
		cDeployer,
		cBytecodePtr,
		C.uint(len(bytecode)),
		C.uint64_t(gasLimit),
	)

	if result == nil {
		return "", errors.New("contract deployment failed: result is nil")
	}
	defer C.revm_free_deployment_result(result)

	if result.success == 0 {
		return "", errors.New("contract deployment failed")
	}

	if result.contract_address == nil {
		return "", errors.New("deployment succeeded but no contract address returned")
	}

	addr := C.GoString(result.contract_address)
	return addr, nil
} 