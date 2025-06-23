//go:build revm
// +build revm

package revmbridge_test

import (
	"crypto/ecdsa"
	"encoding/hex"
	"io/ioutil"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/holiman/uint256"
)

// Mock chain context
type mockChainContext struct {
	config *params.ChainConfig
	engine consensus.Engine
}

func newMockChainContext(cfg *params.ChainConfig) *mockChainContext {
	return &mockChainContext{
		config: cfg,
		engine: &mockConsensusEngine{},
	}
}

func (mcc *mockChainContext) Engine() consensus.Engine {
	return mcc.engine
}

func (mcc *mockChainContext) GetHeader(common.Hash, uint64) *types.Header {
	return nil // Not needed for this specific test logic path
}

func (mcc *mockChainContext) Config() *params.ChainConfig {
	return mcc.config
}

// Mock consensus engine
type mockConsensusEngine struct{}

func (mce *mockConsensusEngine) Author(header *types.Header) (common.Address, error) {
	return common.Address{}, nil // Return a zero address, error nil
}
func (mce *mockConsensusEngine) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header) error {
	return nil
}
func (mce *mockConsensusEngine) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header) (chan<- struct{}, <-chan error) {
	return nil, nil
}
func (mce *mockConsensusEngine) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	return nil
}
func (mce *mockConsensusEngine) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	return nil
}
func (mce *mockConsensusEngine) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state vm.StateDB, txs *[]*types.Transaction, uncles []*types.Header, withdrawals []*types.Withdrawal, receipts *[]*types.Receipt, systemTxs *[]*types.Transaction, usedGas *uint64, tracer *tracing.Hooks) error {
	return nil
}
func (mce *mockConsensusEngine) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, body *types.Body, receipts []*types.Receipt, tracer *tracing.Hooks) (*types.Block, []*types.Receipt, error) {
	return nil, nil, nil
}
func (mce *mockConsensusEngine) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	return nil
}
func (mce *mockConsensusEngine) SealHash(header *types.Header) common.Hash { return common.Hash{} }
func (mce *mockConsensusEngine) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	return nil
}
func (mce *mockConsensusEngine) APIs(chain consensus.ChainHeaderReader) []rpc.API { return nil }
func (mce *mockConsensusEngine) Close() error                                     { return nil }
func (mce *mockConsensusEngine) SetThreads(threads int)                           {}
func (mce *mockConsensusEngine) Threads() int                                     { return 0 }

// Add the missing Delay method
func (mce *mockConsensusEngine) Delay(chain consensus.ChainReader, header *types.Header, leftOver *time.Duration) *time.Duration {
	var d time.Duration
	return &d
}

// Add the missing NextInTurnValidator method
func (mce *mockConsensusEngine) NextInTurnValidator(chain consensus.ChainHeaderReader, header *types.Header) (common.Address, error) {
	return common.Address{}, nil // Return zero address and no error
}

// Add the missing VerifyRequests method
func (mce *mockConsensusEngine) VerifyRequests(header *types.Header, Requests [][]byte) error {
	return nil
}

// TestReceiptParity_GoEVM_vs_REVM executes the same simple transaction on both
// the legacy Go-EVM execution path and the new REVM backend, then asserts that
// the produced receipts match (status, gas used, bloom, logs).
func TestReceiptParity_GoEVM_vs_REVM(t *testing.T) {
	// ---------------------------------------------------------------------
	// 0. Load the example runtime that emits a single LOG1 event (same helper
	//    used by TestReceipt_WithLogBloom).
	// ---------------------------------------------------------------------
	raw, err := ioutil.ReadFile("event_runtime_hex.txt")
	if err != nil {
		t.Fatalf("failed to read runtime hex: %v", err)
	}
	runtime, _ := hex.DecodeString(strings.TrimSpace(string(raw)))

	// Addresses for caller and contract
	callerKey, _ := crypto.GenerateKey()
	callerAddr := crypto.PubkeyToAddress(*callerKey.Public().(*ecdsa.PublicKey))
	contractAddr := common.HexToAddress("0xD0c0fFEEcafeDeAdbEeF000000000000000000000")

	// Helper to initialise a fresh in-memory StateDB with identical state.
	newState := func() *state.StateDB {
		mem := state.NewDatabaseForTesting()
		sdb, _ := state.New(common.Hash{}, mem)
		sdb.AddBalance(callerAddr, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)
		sdb.CreateAccount(contractAddr)
		sdb.SetCode(contractAddr, runtime)
		return sdb
	}

	gasLimit := uint64(200_000)

	// ------------------------------------------------------------------
	// 1. Execute via Go-EVM to obtain the reference receipt.
	// ------------------------------------------------------------------
	sdbGo := newState()
	header := &types.Header{Number: big.NewInt(1), GasLimit: 30_000_000, Difficulty: big.NewInt(0)} // Set difficulty for PoS context
	chainCfg := params.TestChainConfig                                                              // Use a test chain config

	mockChain := newMockChainContext(chainCfg) // Create mock chain context

	// Build a legacy transaction calling the contract with no data.
	tx := types.NewTransaction(0, contractAddr, big.NewInt(0), gasLimit, big.NewInt(1), nil)
	signer := types.LatestSignerForChainID(big.NewInt(1))
	tx, _ = types.SignTx(tx, signer, callerKey)

	gp := new(core.GasPool).AddGas(header.GasLimit)
	context := core.NewEVMBlockContext(header, mockChain, nil) // Use mockChain
	evm := vm.NewEVM(context, sdbGo, chainCfg, vm.Config{})    // Pass chainCfg
	var gasUsedGoEVM uint64
	receiptGoEVM, errGoEVM := core.ApplyTransaction(evm, gp, sdbGo, header, tx, &gasUsedGoEVM)
	if errGoEVM != nil {
		t.Fatalf("Go-EVM ApplyTransaction failed: %v", errGoEVM)
	}

	// ------------------------------------------------------------------
	// 2. Execute via REVM backend.
	// ------------------------------------------------------------------
	sdbRevm := newState()
	handle := revmbridge.NewStateDB(sdbRevm)
	exec, _ := revmbridge.NewRevmExecutorStateDB(handle)
	defer exec.Close()

	revmReceipt, err := exec.CallContractCommitReceipt(callerAddr.Hex(), contractAddr.Hex(), nil, "0x0", gasLimit, 0, tx, nil)
	if err != nil {
		t.Fatalf("REVM execution error: %v", err)
	}

	// ------------------------------------------------------------------
	// 3. Compare key fields.
	// ------------------------------------------------------------------
	if revmReceipt.Status != receiptGoEVM.Status {
		t.Fatalf("status mismatch: go=%d revm=%d", receiptGoEVM.Status, revmReceipt.Status)
	}
	if revmReceipt.GasUsed != gasUsedGoEVM {
		t.Fatalf("gasUsed mismatch: go=%d revm=%d", gasUsedGoEVM, revmReceipt.GasUsed)
	}
	if revmReceipt.Bloom != receiptGoEVM.Bloom {
		t.Fatalf("bloom mismatch")
	}
	if len(receiptGoEVM.Logs) != len(revmReceipt.Logs) {
		t.Fatalf("log len mismatch: go=%d revm=%d", len(receiptGoEVM.Logs), len(revmReceipt.Logs))
	}
	for i, l := range receiptGoEVM.Logs {
		rl := revmReceipt.Logs[i]
		if l.Address != rl.Address || !logsEqual(l, rl) {
			t.Fatalf("log %d mismatch: go=%+v revm=%+v", i, l, rl)
		}
	}
}

// logsEqual compares topics and data.
func logsEqual(a, b *types.Log) bool {
	if len(a.Topics) != len(b.Topics) || !strings.EqualFold(hex.EncodeToString(a.Data), hex.EncodeToString(b.Data)) {
		return false
	}
	for i := range a.Topics {
		if a.Topics[i] != b.Topics[i] {
			return false
		}
	}
	return true
}
