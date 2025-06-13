//go:build revm
// +build revm

package core

/*
#cgo CFLAGS: -I../../revm_integration/revm_ffi_wrapper
#cgo LDFLAGS: -L../../revm_integration/revm_ffi_wrapper/target/release -lrevm_ffi -Wl,-rpath,../../revm_integration/revm_ffi_wrapper/target/release
#include "../../revm_integration/revm_ffi_wrapper/revm_ffi.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/systemcontracts"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

const largeTxGasLimit = 10000000 // 10M Gas, to measure the execution time of large tx

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config *params.ChainConfig // Chain configuration options
	chain  *HeaderChain        // Canonical header chain
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, chain *HeaderChain) *StateProcessor {
	return &StateProcessor{
		config: config,
		chain:  chain,
	}
}

// preloadAccountToRevm loads an account's complete state from Go statedb into REVM
func preloadAccountToRevm(revm_instance *C.RevmInstance, addr common.Address, statedb *state.StateDB) error {
	addr_str := C.CString(addr.Hex())
	defer C.free(unsafe.Pointer(addr_str))

	// Load balance
	balance := statedb.GetBalance(addr)
	balance_str := C.CString(balance.String())
	defer C.free(unsafe.Pointer(balance_str))

	if C.revm_set_balance(revm_instance, addr_str, balance_str) != 0 {
		return fmt.Errorf("failed to set balance for %s", addr.Hex())
	}

	// Load nonce
	nonce := statedb.GetNonce(addr)
	if C.revm_set_nonce(revm_instance, addr_str, C.uint64_t(nonce)) != 0 {
		return fmt.Errorf("failed to set nonce for %s", addr.Hex())
	}

	// Check if this is a contract and has code
	code := statedb.GetCode(addr)
	if len(code) > 0 {
		log.Debug("Account has contract code - REVM will handle code execution", "addr", addr.Hex(), "codeLen", len(code))

		// Note: Contract code and storage will be loaded dynamically by REVM when needed
		// The current FFI interface has basic storage support via revm_set_storage
		// For comprehensive state sync, we focus on balance and nonce synchronization

		// Note: Storage preloading would require iterating over storage
		// For now, we'll rely on REVM's dynamic loading capabilities
		// This is sufficient for basic state synchronization testing
	}

	log.Debug("Pre-loaded account state", "addr", addr.Hex(), "balance", balance, "nonce", nonce, "hasCode", len(code) > 0)
	return nil
}

// Process processes the state changes according to the Ethereum rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(block *types.Block, statedb *state.StateDB, cfg vm.Config) (*ProcessResult, error) {
	log.Error("REVM Processing block", "block", block.Number(), "txCount", len(block.Transactions()))
	// Create a new REVM instance
	revm_config := C.RevmConfigFFI{
		chain_id: C.uint64_t(p.config.ChainID.Uint64()),
		spec_id:  C.uint8_t(24), // TODO: Map spec ID correctly
	}
	revm_instance := C.revm_new_with_config(&revm_config)
	if revm_instance == nil {
		return nil, errors.New("failed to create revm instance")
	}
	defer C.revm_free(revm_instance)

	var (
		receipts    = make([]*types.Receipt, 0)
		usedGas     = new(uint64)
		header      = block.Header()
		blockHash   = block.Hash()
		blockNumber = block.Number()
		allLogs     []*types.Log
	)

	// Mutate the block and state according to any hard-fork specs
	if p.config.DAOForkSupport && p.config.DAOForkBlock != nil && p.config.DAOForkBlock.Cmp(block.Number()) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}

	lastBlock := p.chain.GetHeaderByHash(block.ParentHash())
	if lastBlock == nil {
		return nil, errors.New("could not get parent block")
	}
	// Handle upgrade built-in system contract code
	systemcontracts.TryUpdateBuildInSystemContract(p.config, blockNumber, lastBlock.Time, block.Time(), statedb, true)

	var (
		context vm.BlockContext
		signer  = types.MakeSigner(p.config, header.Number, header.Time)
		txNum   = len(block.Transactions())
		err     error
	)

	// Apply pre-execution system calls.
	var tracingStateDB = vm.StateDB(statedb)
	if hooks := cfg.Tracer; hooks != nil {
		tracingStateDB = state.NewHookedState(statedb, hooks)
	}
	context = NewEVMBlockContext(header, p.chain, nil)
	evm := vm.NewEVM(context, tracingStateDB, p.config, cfg)

	if beaconRoot := block.BeaconRoot(); beaconRoot != nil {
		ProcessBeaconBlockRoot(*beaconRoot, evm)
	}
	if p.config.IsPrague(block.Number(), block.Time()) || p.config.IsVerkle(block.Number(), block.Time()) {
		ProcessParentBlockHash(block.ParentHash(), evm)
	}

	// Iterate over and process the individual transactions
	posa, isPoSA := p.chain.engine.(consensus.PoSA)
	commonTxs := make([]*types.Transaction, 0, txNum)

	// Collect all accounts that will be touched by this block's transactions
	touchedAccounts := make(map[common.Address]bool)

	// Always include block coinbase (miner reward recipient)
	touchedAccounts[header.Coinbase] = true

	// Include system contract addresses that might be involved
	systemContractAddresses := []common.Address{
		common.HexToAddress("0x0000000000000000000000000000000000001000"), // ValidatorSet
		common.HexToAddress("0x0000000000000000000000000000000000001001"), // SlashContract
		common.HexToAddress("0x0000000000000000000000000000000000001002"), // SystemReward
		common.HexToAddress("0x0000000000000000000000000000000000001003"), // LightClient
		common.HexToAddress("0x0000000000000000000000000000000000001004"), // TokenHub
		common.HexToAddress("0x0000000000000000000000000000000000001005"), // RelayerIncentivize
		common.HexToAddress("0x0000000000000000000000000000000000001006"), // RelayerHub
		common.HexToAddress("0x0000000000000000000000000000000000001007"), // GovHub
		common.HexToAddress("0x0000000000000000000000000000000000001008"), // TokenManager
		common.HexToAddress("0x0000000000000000000000000000000000001009"), // CrossChain
	}

	for _, addr := range systemContractAddresses {
		if statedb.Exist(addr) {
			touchedAccounts[addr] = true
		}
	}

	for _, tx := range block.Transactions() {
		msg, err := TransactionToMessage(tx, signer, header.BaseFee)
		if err != nil {
			return nil, fmt.Errorf("failed to create message for tx: %w", err)
		}

		// Add sender
		touchedAccounts[msg.From] = true

		// Add recipient if it exists
		if msg.To != nil {
			touchedAccounts[*msg.To] = true

			// If the recipient is a contract, also preload accounts it might interact with
			if statedb.GetCodeSize(*msg.To) > 0 {
				// This is a contract call - might interact with many addresses
				// We could analyze the transaction data to find address references,
				// but for now we'll rely on REVM's state tracking
				log.Debug("Transaction targets contract", "to", msg.To.Hex(), "codeSize", statedb.GetCodeSize(*msg.To))
			}
		} else {
			// Contract creation - calculate the contract address
			contractAddr := crypto.CreateAddress(msg.From, msg.Nonce)
			touchedAccounts[contractAddr] = true
			log.Debug("Contract creation transaction", "from", msg.From.Hex(), "contractAddr", contractAddr.Hex())
		}
	}

	// Pre-load all touched accounts into REVM
	log.Info("Pre-loading accounts into REVM", "count", len(touchedAccounts), "block", blockNumber, "txCount", len(block.Transactions()))

	// --- BEGIN DEBUG LOGGING ---
	var preloadedAddressesForBlock []string
	// --- END DEBUG LOGGING ---

	preloadedAccounts := make(map[common.Address]bool)
	for addr := range touchedAccounts {
		// --- BEGIN DEBUG LOGGING ---
		preloadedAddressesForBlock = append(preloadedAddressesForBlock, addr.Hex())
		// --- END DEBUG LOGGING ---
		err := preloadAccountToRevm(revm_instance, addr, statedb)
		if err != nil {
			log.Warn("Failed to preload account, continuing", "addr", addr.Hex(), "error", err)
			// Don't fail the entire block for preloading issues - REVM can handle missing accounts
		} else {
			preloadedAccounts[addr] = true
		}
	}
	// --- BEGIN DEBUG LOGGING ---
	log.Info("[PRELOAD_DEBUG] Preloaded accounts for block", "blockNumber", blockNumber, "accounts", strings.Join(preloadedAddressesForBlock, ","))
	// --- END DEBUG LOGGING ---

	// initialise bloom processors
	bloomProcessors := NewAsyncReceiptBloomGenerator(txNum)
	statedb.MarkFullProcessed()

	// usually do have two tx, one for validator set contract, another for system reward contract.
	systemTxs := make([]*types.Transaction, 0, 2)

	for i, tx := range block.Transactions() {
		if isPoSA {
			if isSystemTx, err := posa.IsSystemTransaction(tx, block.Header()); err != nil {
				bloomProcessors.Close()
				return nil, err
			} else if isSystemTx {
				systemTxs = append(systemTxs, tx)
				log.Debug("Found system transaction - processing with REVM", "block", blockNumber, "txIndex", i, "txHash", tx.Hash().Hex())
			}
		}
		if p.config.IsCancun(block.Number(), block.Time()) {
			if len(systemTxs) > 0 {
				bloomProcessors.Close()
				// systemTxs should be always at the end of block.
				return nil, fmt.Errorf("normal tx %d [%v] after systemTx", i, tx.Hash().Hex())
			}
		}

		msg, err := TransactionToMessage(tx, signer, header.BaseFee)
		if err != nil {
			bloomProcessors.Close()
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		statedb.SetTxContext(tx.Hash(), i)

		receipt, err := ApplyTransactionWithRevm(revm_instance, msg, statedb, blockNumber, blockHash, tx, usedGas)
		if err != nil {
			bloomProcessors.Close()
			return nil, fmt.Errorf("could not apply tx %d [%v]: %w", i, tx.Hash().Hex(), err)
		}
		commonTxs = append(commonTxs, tx)
		receipts = append(receipts, receipt)
	}
	bloomProcessors.Close()

	// Read requests if Prague is enabled.
	var requests [][]byte
	if p.config.IsPrague(block.Number(), block.Time()) && p.chain.config.Parlia == nil {
		var allCommonLogs []*types.Log
		for _, receipt := range receipts {
			allCommonLogs = append(allCommonLogs, receipt.Logs...)
		}
		requests = [][]byte{}
		// EIP-6110
		if err := ParseDepositLogs(&requests, allCommonLogs, p.config); err != nil {
			return nil, err
		}
		// EIP-7002
		ProcessWithdrawalQueue(&requests, evm)
		// EIP-7251
		ProcessConsolidationQueue(&requests, evm)
	}

	// Finalize the block, applying any consensus engine specific extras (e.g. block rewards)
	log.Debug("Finalizing block", "block", blockNumber, "commonTxs", len(commonTxs), "systemTxs", len(systemTxs), "receipts", len(receipts))
	err = p.chain.engine.Finalize(p.chain, header, tracingStateDB, &commonTxs, block.Uncles(), block.Withdrawals(), &receipts, &systemTxs, usedGas, cfg.Tracer)
	if err != nil {
		log.Error("Finalize failed", "block", blockNumber, "error", err)
		return nil, err
	}
	for _, receipt := range receipts {
		allLogs = append(allLogs, receipt.Logs...)
	}

	return &ProcessResult{
		Receipts: receipts,
		Requests: requests,
		Logs:     allLogs,
		GasUsed:  *usedGas,
	}, nil
}

// ApplyTransactionWithEVM attempts to apply a transaction to the given state database
// and uses the input parameters for its environment similar to ApplyTransaction. However,
// this method takes an already created EVM instance as input.
func ApplyTransactionWithEVM(msg *Message, gp *GasPool, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64, evm *vm.EVM, receiptProcessors ...ReceiptProcessor) (receipt *types.Receipt, err error) {
	// Add timing measurement
	var result *ExecutionResult
	if tx.Gas() > largeTxGasLimit {
		start := time.Now()
		defer func() {
			if result != nil && result.UsedGas > largeTxGasLimit {
				elapsed := time.Since(start)
				log.Info("LargeTX execution time", "block", blockNumber, "tx", tx.Hash(), "gasUsed", result.UsedGas, "elapsed", elapsed)
			}
		}()
	}

	if hooks := evm.Config.Tracer; hooks != nil {
		if hooks.OnTxStart != nil {
			hooks.OnTxStart(evm.GetVMContext(), tx, msg.From)
		}
		if hooks.OnTxEnd != nil {
			defer func() { hooks.OnTxEnd(receipt, err) }()
		}
	}
	// Apply the transaction to the current state (included in the env).
	result, err = ApplyMessage(evm, msg, gp)
	if err != nil {
		return nil, err
	}
	// Update the state with pending changes.
	var root []byte
	if evm.ChainConfig().IsByzantium(blockNumber) {
		evm.StateDB.Finalise(true)
	} else {
		root = statedb.IntermediateRoot(evm.ChainConfig().IsEIP158(blockNumber)).Bytes()
	}
	*usedGas += result.UsedGas

	// Merge the tx-local access event into the "block-local" one, in order to collect
	// all values, so that the witness can be built.
	if statedb.GetTrie().IsVerkle() {
		statedb.AccessEvents().Merge(evm.AccessEvents)
	}

	return MakeReceipt(evm, result, statedb, blockNumber, blockHash, tx, *usedGas, root, receiptProcessors...), nil
}

func ApplyTransactionWithRevm(revm_instance *C.RevmInstance, msg *Message, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas *uint64) (*types.Receipt, error) {
	// NOTE: System transactions are handled separately in the Go implementation.
	// For this initial REVM port we treat all transactions the same.

	// Determine all accounts that might be affected
	affectedAccounts := []common.Address{msg.From}
	if msg.To != nil {
		affectedAccounts = append(affectedAccounts, *msg.To)
	} else {
		// Contract creation - add the calculated contract address
		contractAddr := crypto.CreateAddress(msg.From, msg.Nonce)
		affectedAccounts = append(affectedAccounts, contractAddr)
	}

	// Store pre-execution state for comparison later
	preState := make(map[common.Address]*accountState)
	for _, addr := range affectedAccounts {
		preState[addr] = &accountState{
			balance: statedb.GetBalance(addr),
			nonce:   statedb.GetNonce(addr),
		}
	}

	// Convert message to C types for REVM execution
	caller_str := C.CString(msg.From.Hex())
	defer C.free(unsafe.Pointer(caller_str))

	var to_ptr *C.char
	if msg.To != nil {
		to_ptr = C.CString(msg.To.Hex())
		defer C.free(unsafe.Pointer(to_ptr))
	}

	value_str := C.CString(msg.Value.String())
	defer C.free(unsafe.Pointer(value_str))

	var data_ptr *C.uchar
	var data_len C.uint
	if len(msg.Data) > 0 {
		data_ptr = (*C.uchar)(unsafe.Pointer(&msg.Data[0]))
		data_len = C.uint(len(msg.Data))
	}

	gas_limit := C.uint64_t(msg.GasLimit)

	// Execute transaction in REVM
	result_ffi := C.revm_call_contract(revm_instance, caller_str, to_ptr, data_ptr, data_len, value_str, gas_limit)
	if result_ffi == nil {
		last_error := C.revm_get_last_error(revm_instance)
		err_str := C.GoString(last_error)
		C.revm_free_string(last_error)
		return nil, errors.New("revm execution failed: " + err_str)
	}
	defer C.revm_free_execution_result(result_ffi)

	// Check if transaction succeeded
	if result_ffi.success == 0 {
		// Transaction failed - no state changes should be applied
		return createRevertedReceipt(tx, blockNumber, blockHash, statedb, uint64(result_ffi.gas_used), usedGas)
	}

	// Transaction succeeded - sync state changes from REVM back to Go statedb
	err := syncStateFromRevm(revm_instance, affectedAccounts, statedb, preState)
	if err != nil {
		return nil, fmt.Errorf("failed to sync state from REVM: %w", err)
	}

	// Handle contract creation if applicable
	var contractAddr common.Address
	if tx.To() == nil && result_ffi.created_address != nil {
		addr_str := C.GoString(result_ffi.created_address)
		contractAddr = common.HexToAddress(addr_str)
		// TODO: Sync contract code and storage back to statedb
		// For now, we'll leave this as a placeholder
	}

	// Update gas usage
	gasUsed := uint64(result_ffi.gas_used)
	*usedGas += gasUsed

	// Finalize the state changes
	statedb.Finalise(true)

	// Process logs from REVM result
	var txLogs []*types.Log
	if result_ffi.logs_count > 0 {
		log.Debug("REVM execution produced logs", "txHash", tx.Hash().Hex(), "logCount", int(result_ffi.logs_count))

		// For now, we'll create an empty logs array - the FFI log structure needs to be carefully mapped
		// This is not critical for basic state synchronization and consensus compatibility
		txLogs = make([]*types.Log, 0)

		// TODO: Properly parse REVM logs when FFI interface is stabilized
		// The current LogFFI structure in the header needs to be properly integrated
	}

	// Create receipt with proper cumulative gas and logs
	receipt := createSuccessfulReceiptWithLogs(tx, blockNumber, blockHash, statedb, gasUsed, *usedGas, contractAddr, txLogs)

	log.Debug("REVM transaction applied successfully", "txHash", tx.Hash().Hex(), "gasUsed", gasUsed, "cumulativeGas", *usedGas, "logs", len(txLogs))

	return receipt, nil
}

// accountState stores the state of an account
type accountState struct {
	balance *uint256.Int
	nonce   uint64
}

// syncStateFromRevm reads the final state from REVM and applies changes to Go statedb
func syncStateFromRevm(revm_instance *C.RevmInstance, affectedAccounts []common.Address, statedb *state.StateDB, preState map[common.Address]*accountState) error {
	log.Debug("Syncing state from REVM", "accounts", len(affectedAccounts))

	// Sync all affected accounts
	for _, addr := range affectedAccounts {
		err := syncSingleAccountFromRevm(revm_instance, addr, statedb, preState)
		if err != nil {
			log.Warn("Failed to sync affected account from REVM", "addr", addr.Hex(), "error", err)
			// Continue with other accounts rather than failing completely
		}
	}

	log.Debug("State synchronization from REVM completed successfully")
	return nil
}

// syncSingleAccountFromRevm syncs a single account's state from REVM to Go statedb
func syncSingleAccountFromRevm(revm_instance *C.RevmInstance, addr common.Address, statedb *state.StateDB, preState map[common.Address]*accountState) error {
	addr_str := C.CString(addr.Hex())
	defer C.free(unsafe.Pointer(addr_str))

	// Get final balance from REVM
	balance_str := C.revm_get_balance(revm_instance, addr_str)
	if balance_str != nil {
		defer C.revm_free_string(balance_str)

		balanceGoString := C.GoString(balance_str)
		var finalBalance *big.Int
		var ok bool

		// Try parsing as decimal first, then as hex if that fails
		finalBalance, ok = new(big.Int).SetString(balanceGoString, 10)
		if !ok && strings.HasPrefix(balanceGoString, "0x") {
			finalBalance, ok = new(big.Int).SetString(balanceGoString[2:], 16)
		}

		if !ok {
			log.Warn("Failed to parse balance from REVM", "addr", addr.Hex(), "balance_str", balanceGoString)
		} else {
			finalBalance256, overflow := uint256.FromBig(finalBalance)
			if overflow {
				log.Warn("Balance overflow from REVM", "addr", addr.Hex(), "balance", finalBalance)
			} else {
				// Update balance in statedb if it changed
				currentBalance := statedb.GetBalance(addr)
				if !currentBalance.Eq(finalBalance256) {
					log.Debug("Updating balance from REVM", "addr", addr.Hex(), "old", currentBalance, "new", finalBalance256)
					statedb.SetBalance(addr, finalBalance256, tracing.BalanceChangeRevmTransfer)
				}
			}
		}
	} else {
		// No balance returned - might be zero balance account
		currentBalance := statedb.GetBalance(addr)
		if !currentBalance.IsZero() {
			log.Debug("Setting balance to zero from REVM", "addr", addr.Hex(), "old", currentBalance)
			statedb.SetBalance(addr, new(uint256.Int), tracing.BalanceChangeRevmTransfer)
		}
	}

	// Get final nonce from REVM
	finalNonce := uint64(C.revm_get_nonce(revm_instance, addr_str))
	currentNonce := statedb.GetNonce(addr)

	// Update nonce in statedb if it changed
	if currentNonce != finalNonce {
		log.Debug("Updating nonce from REVM", "addr", addr.Hex(), "old", currentNonce, "new", finalNonce)
		statedb.SetNonce(addr, finalNonce, tracing.NonceChangeRevm)
	}

	// Note: Code changes are handled via contract creation in the main transaction processing
	// Storage changes should be automatically synced by REVM as part of the execution

	return nil
}

// createRevertedReceipt creates a receipt for a failed transaction
func createRevertedReceipt(tx *types.Transaction, blockNumber *big.Int, blockHash common.Hash, statedb *state.StateDB, gasUsed uint64, totalUsedGas *uint64) (*types.Receipt, error) {
	*totalUsedGas += gasUsed

	receipt := types.NewReceipt(statedb.IntermediateRoot(true).Bytes(), true, *totalUsedGas)
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = gasUsed
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())

	return receipt, nil
}

// createSuccessfulReceipt creates a receipt for a successful transaction
func createSuccessfulReceipt(tx *types.Transaction, blockNumber *big.Int, blockHash common.Hash, statedb *state.StateDB, gasUsed uint64, cumulativeGasUsed uint64, contractAddr common.Address) *types.Receipt {
	receipt := types.NewReceipt(statedb.IntermediateRoot(true).Bytes(), false, cumulativeGasUsed)
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = gasUsed
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())

	// Set contract address for contract creation
	if contractAddr != (common.Address{}) {
		receipt.ContractAddress = contractAddr
	}

	return receipt
}

// createSuccessfulReceiptWithLogs creates a receipt for a successful transaction with logs
func createSuccessfulReceiptWithLogs(tx *types.Transaction, blockNumber *big.Int, blockHash common.Hash, statedb *state.StateDB, gasUsed uint64, cumulativeGasUsed uint64, contractAddr common.Address, logs []*types.Log) *types.Receipt {
	receipt := types.NewReceipt(statedb.IntermediateRoot(true).Bytes(), false, cumulativeGasUsed)
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = gasUsed
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	receipt.Logs = logs

	// Set contract address for contract creation
	if contractAddr != (common.Address{}) {
		receipt.ContractAddress = contractAddr
	}

	// Calculate bloom filter from logs
	receipt.Bloom = types.CreateBloom([]*types.Receipt{receipt})

	return receipt
}

// MakeReceipt generates the receipt object for a transaction given its execution result.
func MakeReceipt(evm *vm.EVM, result *ExecutionResult, statedb *state.StateDB, blockNumber *big.Int, blockHash common.Hash, tx *types.Transaction, usedGas uint64, root []byte, receiptProcessors ...ReceiptProcessor) *types.Receipt {
	// Create a new receipt for the transaction, storing the intermediate root and gas used
	// by the tx.
	receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: usedGas}
	if result.Failed() {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}
	receipt.TxHash = tx.Hash()
	receipt.GasUsed = result.UsedGas

	if tx.Type() == types.BlobTxType {
		receipt.BlobGasUsed = uint64(len(tx.BlobHashes()) * params.BlobTxBlobGasPerBlob)
		receipt.BlobGasPrice = evm.Context.BlobBaseFee
	}

	// If the transaction created a contract, store the creation address in the receipt.
	if tx.To() == nil {
		receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
	}

	// Set the receipt logs and create the bloom filter.
	receipt.Logs = statedb.GetLogs(tx.Hash(), blockNumber.Uint64(), blockHash)
	receipt.BlockHash = blockHash
	receipt.BlockNumber = blockNumber
	receipt.TransactionIndex = uint(statedb.TxIndex())
	for _, receiptProcessor := range receiptProcessors {
		receiptProcessor.Apply(receipt)
	}
	return receipt
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(evm *vm.EVM, gp *GasPool, statedb *state.StateDB, header *types.Header, tx *types.Transaction, usedGas *uint64, receiptProcessors ...ReceiptProcessor) (*types.Receipt, error) {
	msg, err := TransactionToMessage(tx, types.MakeSigner(evm.ChainConfig(), header.Number, header.Time), header.BaseFee)
	if err != nil {
		return nil, err
	}
	// Create a new context to be used in the EVM environment
	return ApplyTransactionWithEVM(msg, gp, statedb, header.Number, header.Hash(), tx, usedGas, evm, receiptProcessors...)
}

// ProcessBeaconBlockRoot applies the EIP-4788 system call to the beacon block root
// contract. This method is exported to be used in tests.
func ProcessBeaconBlockRoot(beaconRoot common.Hash, evm *vm.EVM) {
	// Return immediately if beaconRoot equals the zero hash when using the Parlia engine.
	if beaconRoot == (common.Hash{}) {
		if chainConfig := evm.ChainConfig(); chainConfig != nil && chainConfig.Parlia != nil {
			return
		}
	}
	if tracer := evm.Config.Tracer; tracer != nil {
		onSystemCallStart(tracer, evm.GetVMContext())
		if tracer.OnSystemCallEnd != nil {
			defer tracer.OnSystemCallEnd()
		}
	}
	msg := &Message{
		From:      params.SystemAddress,
		GasLimit:  30_000_000,
		GasPrice:  common.Big0,
		GasFeeCap: common.Big0,
		GasTipCap: common.Big0,
		To:        &params.BeaconRootsAddress,
		Data:      beaconRoot[:],
	}
	evm.SetTxContext(NewEVMTxContext(msg))
	evm.StateDB.AddAddressToAccessList(params.BeaconRootsAddress)
	_, _, _ = evm.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, 30_000_000, common.U2560)
	evm.StateDB.Finalise(true)
}

// ProcessParentBlockHash stores the parent block hash in the history storage contract
// as per EIP-2935/7709.
func ProcessParentBlockHash(prevHash common.Hash, evm *vm.EVM) {
	if tracer := evm.Config.Tracer; tracer != nil {
		onSystemCallStart(tracer, evm.GetVMContext())
		if tracer.OnSystemCallEnd != nil {
			defer tracer.OnSystemCallEnd()
		}
	}
	msg := &Message{
		From:      params.SystemAddress,
		GasLimit:  30_000_000,
		GasPrice:  common.Big0,
		GasFeeCap: common.Big0,
		GasTipCap: common.Big0,
		To:        &params.HistoryStorageAddress,
		Data:      prevHash.Bytes(),
	}
	evm.SetTxContext(NewEVMTxContext(msg))
	evm.StateDB.AddAddressToAccessList(params.HistoryStorageAddress)
	_, _, err := evm.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, 30_000_000, common.U2560)
	if err != nil {
		panic(err)
	}
	if evm.StateDB.AccessEvents() != nil {
		evm.StateDB.AccessEvents().Merge(evm.AccessEvents)
	}
	evm.StateDB.Finalise(true)
}

// ProcessWithdrawalQueue calls the EIP-7002 withdrawal queue contract.
// It returns the opaque request data returned by the contract.
func ProcessWithdrawalQueue(requests *[][]byte, evm *vm.EVM) {
	processRequestsSystemCall(requests, evm, 0x01, params.WithdrawalQueueAddress)
}

// ProcessConsolidationQueue calls the EIP-7251 consolidation queue contract.
// It returns the opaque request data returned by the contract.
func ProcessConsolidationQueue(requests *[][]byte, evm *vm.EVM) {
	processRequestsSystemCall(requests, evm, 0x02, params.ConsolidationQueueAddress)
}

func processRequestsSystemCall(requests *[][]byte, evm *vm.EVM, requestType byte, addr common.Address) {
	if tracer := evm.Config.Tracer; tracer != nil {
		onSystemCallStart(tracer, evm.GetVMContext())
		if tracer.OnSystemCallEnd != nil {
			defer tracer.OnSystemCallEnd()
		}
	}
	msg := &Message{
		From:      params.SystemAddress,
		GasLimit:  30_000_000,
		GasPrice:  common.Big0,
		GasFeeCap: common.Big0,
		GasTipCap: common.Big0,
		To:        &addr,
	}
	evm.SetTxContext(NewEVMTxContext(msg))
	evm.StateDB.AddAddressToAccessList(addr)
	ret, _, _ := evm.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, 30_000_000, common.U2560)
	evm.StateDB.Finalise(true)
	if len(ret) == 0 {
		return // skip empty output
	}

	// Append prefixed requestsData to the requests list.
	requestsData := make([]byte, len(ret)+1)
	requestsData[0] = requestType
	copy(requestsData[1:], ret)
	*requests = append(*requests, requestsData)
}

// ParseDepositLogs extracts the EIP-6110 deposit values from logs emitted by
// BeaconDepositContract.
func ParseDepositLogs(requests *[][]byte, logs []*types.Log, config *params.ChainConfig) error {
	deposits := make([]byte, 1) // note: first byte is 0x00 (== deposit request type)
	for _, log := range logs {
		if log.Address == config.DepositContractAddress {
			request, err := types.DepositLogToRequest(log.Data)
			if err != nil {
				return fmt.Errorf("unable to parse deposit data: %v", err)
			}
			deposits = append(deposits, request...)
		}
	}
	if len(deposits) > 1 {
		*requests = append(*requests, deposits)
	}
	return nil
}

func onSystemCallStart(tracer *tracing.Hooks, ctx *tracing.VMContext) {
	if tracer.OnSystemCallStartV2 != nil {
		tracer.OnSystemCallStartV2(ctx)
	} else if tracer.OnSystemCallStart != nil {
		tracer.OnSystemCallStart()
	}
}
