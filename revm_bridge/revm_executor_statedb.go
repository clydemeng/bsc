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
	_ "embed"
	"encoding/hex"
	"errors"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

//go:embed small_biga_runtime_hex.txt
var smallBigaRuntimeHex string

type RevmExecutorStateDB struct {
	inst   *C.RevmInstanceStateDB
	handle uintptr // opaque handle to StateDB for eventual flush
}

// NewRevmExecutorStateDB creates an EVM instance that pulls state from the given handle.
// The handle must have been obtained via NewStateDB and remain valid for the
// lifetime of the executor.
func NewRevmExecutorStateDB(handle uintptr) (*RevmExecutorStateDB, error) {
	var cfg C.RevmConfigFFI // zero-initialised – defaults are fine (chain 1, Prague)
	cfg.chain_id = 1
	cfg.spec_id = 19
	// Disable strict sender-code and nonce validation so that the replay
	// pathway behaves like go-ethereum's BlockGen (which skips these checks
	// for pre-generated test chains).
	cfg.disable_eip3607 = true
	cfg.disable_nonce_check = true

	inst := C.revm_new_with_statedb(C.size_t(handle), &cfg)
	if inst == nil {
		return nil, errors.New("failed to create REVM instance with statedb")
	}
	return &RevmExecutorStateDB{inst: inst, handle: handle}, nil
}

func (e *RevmExecutorStateDB) Close() {
	// Flush any pending journal changes before freeing resources.
	if e.handle != 0 {
		FlushPending(e.handle)
	}
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
	var cDataBuf unsafe.Pointer
	if len(data) > 0 {
		cDataBuf = C.CBytes(data) // allocates C memory and copies the bytes
		cDataPtr = (*C.uchar)(cDataBuf)
		defer C.free(cDataBuf)
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

// CallContractCommit executes a state-changing call and commits the result.
func (e *RevmExecutorStateDB) CallContractCommit(from, to string, data []byte, value string, gasLimit uint64) error {
	cFrom := C.CString(from)
	defer C.free(unsafe.Pointer(cFrom))
	cTo := C.CString(to)
	defer C.free(unsafe.Pointer(cTo))

	var cDataPtr *C.uchar
	var cDataBuf unsafe.Pointer
	if len(data) > 0 {
		cDataBuf = C.CBytes(data)
		cDataPtr = (*C.uchar)(cDataBuf)
		defer C.free(cDataBuf)
	}

	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cValue))

	res := C.revm_call_contract_statedb_commit(
		e.inst,
		cFrom,
		cTo,
		cDataPtr,
		C.uint(len(data)),
		cValue,
		C.uint64_t(gasLimit),
	)

	if res == nil {
		return errors.New("execution failed")
	}
	C.revm_free_execution_result(res)
	return nil
}

// SmallBigaRuntimeHex exposes the embedded runtime hex for external tests.
func SmallBigaRuntimeHex() string { return smallBigaRuntimeHex }

// ----------------------- Receipt translation helpers -----------------------

// logFromC converts a C.LogFFI into a Go *types.Log.
// assumes memory belongs to C result which will be freed by caller after use.
func logFromC(cLog *C.LogFFI) *types.Log {
	if cLog == nil {
		return nil
	}
	goLog := &types.Log{}
	goLog.Address = common.HexToAddress(C.GoString(cLog.address))

	// topics
	count := int(cLog.topics_count)
	if count > 0 {
		topicsSlice := (*[1 << 30]*C.char)(unsafe.Pointer(cLog.topics))[:count:count]
		goLog.Topics = make([]common.Hash, count)
		for i := 0; i < count; i++ {
			goLog.Topics[i] = common.HexToHash(C.GoString(topicsSlice[i]))
		}
	}

	if cLog.data_len > 0 {
		data := C.GoBytes(unsafe.Pointer(cLog.data), C.int(cLog.data_len))
		goLog.Data = append([]byte(nil), data...)
	}
	return goLog
}

// translateResult builds a Receipt from ExecutionResultFFI.
func translateResult(res *C.ExecutionResultFFI, tx *types.Transaction, cumulativeGas uint64) (*types.Receipt, error) {
	if res == nil {
		return nil, errors.New("nil result")
	}

	receipt := &types.Receipt{}
	if res.success == 0 {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.GasUsed = uint64(res.gas_used)
	receipt.CumulativeGasUsed = cumulativeGas + receipt.GasUsed
	if tx != nil {
		receipt.TxHash = tx.Hash()
		if tx.Type() == types.BlobTxType {
			receipt.BlobGasUsed = uint64(len(tx.BlobHashes()) * params.BlobTxBlobGasPerBlob)
		}
	}

	// logs
	count := int(res.logs_count)
	if count > 0 {
		logsPtr := (*[1 << 30]C.LogFFI)(res.logs)[:count:count]
		receipt.Logs = make([]*types.Log, count)
		for i := 0; i < count; i++ {
			receipt.Logs[i] = logFromC(&logsPtr[i])
		}
	}

	// compute bloom from logs
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})

	return receipt, nil
}

// CallContractCommitReceipt runs a transaction, but flushes the pending changes
// from the REVM journal into the Go statedb.
func (e *RevmExecutorStateDB) CallContractCommitReceipt(from, to string, data []byte, value string, gasLimit uint64, cumulativeGas uint64, tx *types.Transaction, txHash *[32]byte) (*types.Receipt, error) {
	cFrom := C.CString(from)
	defer C.free(unsafe.Pointer(cFrom))
	cTo := C.CString(to)
	defer C.free(unsafe.Pointer(cTo))

	var cDataPtr *C.uchar
	var cDataBuf unsafe.Pointer
	if len(data) > 0 {
		cDataBuf = C.CBytes(data)
		cDataPtr = (*C.uchar)(cDataBuf)
		defer C.free(cDataBuf)
	}

	cValue := C.CString(value)
	defer C.free(unsafe.Pointer(cValue))

	cDataLen := C.uint(len(data))
	cGasLimit := C.uint64_t(gasLimit)

	res := C.revm_call_contract_statedb_commit(e.inst, cFrom, cTo, cDataPtr, cDataLen, cValue, cGasLimit)
	defer C.revm_free_execution_result(res)

	return translateResult(res, tx, cumulativeGas)
}
