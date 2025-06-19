package core

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

// stubEngine is a minimal consensus.Engine implementation used in unit tests
// and other off-chain execution paths that do not require full consensus rules.
// All methods are no-ops except Author, which returns the coinbase from the
// supplied header so that reward attribution works for block context creation.
type stubEngine struct{}

func (stubEngine) Author(h *types.Header) (common.Address, error) { return h.Coinbase, nil }

func (stubEngine) VerifyHeader(consensus.ChainHeaderReader, *types.Header) error { return nil }

func (stubEngine) VerifyHeaders(consensus.ChainHeaderReader, []*types.Header) (chan<- struct{}, <-chan error) {
	quit := make(chan struct{})
	results := make(chan error)
	go func() {
		<-quit
		close(results)
	}()
	return quit, results
}

func (stubEngine) VerifyUncles(consensus.ChainReader, *types.Block) error { return nil }
func (stubEngine) VerifyRequests(*types.Header, [][]byte) error           { return nil }
func (stubEngine) NextInTurnValidator(consensus.ChainHeaderReader, *types.Header) (common.Address, error) {
	return common.Address{}, nil
}
func (stubEngine) Prepare(consensus.ChainHeaderReader, *types.Header) error { return nil }
func (stubEngine) Finalize(consensus.ChainHeaderReader, *types.Header, vm.StateDB, *[]*types.Transaction, []*types.Header, []*types.Withdrawal, *[]*types.Receipt, *[]*types.Transaction, *uint64, *tracing.Hooks) error {
	return nil
}
func (stubEngine) FinalizeAndAssemble(consensus.ChainHeaderReader, *types.Header, *state.StateDB, *types.Body, []*types.Receipt, *tracing.Hooks) (*types.Block, []*types.Receipt, error) {
	return nil, nil, nil
}
func (stubEngine) Seal(consensus.ChainHeaderReader, *types.Block, chan<- *types.Block, <-chan struct{}) error {
	return nil
}
func (stubEngine) SealHash(*types.Header) common.Hash { return common.Hash{} }
func (stubEngine) CalcDifficulty(consensus.ChainHeaderReader, uint64, *types.Header) *big.Int {
	return big.NewInt(0)
}
func (stubEngine) APIs(consensus.ChainHeaderReader) []rpc.API { return nil }
func (stubEngine) Delay(consensus.ChainReader, *types.Header, *time.Duration) *time.Duration {
	return nil
}
func (stubEngine) Close() error { return nil }

// stubChain implements core.ChainContext with a static chain config and the
// stubEngine above. It is sufficient for constructing an EVM block context
// when no real blockchain backend is present (e.g. the simulated backend or
// isolated unit tests).
type stubChain struct {
	cfg *params.ChainConfig
}

func (stubChain) Engine() consensus.Engine                    { return stubEngine{} }
func (stubChain) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (s stubChain) Config() *params.ChainConfig               { return s.cfg }

// TxExecutor is an abstraction over a transaction execution backend (Go-EVM,
// REVM, ...). It hides the concrete engine behind a common interface that the
// consensus layer (StateProcessor) can use without branching on build tags.
//
// The interface purposefully mirrors the extra parameters required by the
// legacy ApplyTransaction* helpers so that we can refactor StateProcessor
// with minimal surface-area changes during Milestone 4.3.
type TxExecutor interface {
	// Engine returns a short human identifier ("go-evm", "revm" …).
	Engine() string

	// ExecuteTx runs the provided message/transaction and returns a Go-native receipt.
	// The original *types.Transaction is provided for log generation and hashing purposes.
	ExecuteTx(msg *Message, tx *types.Transaction, txIdx int, gp *GasPool, sdb *state.StateDB, header *types.Header, chainCtx ChainContext, evmCfg vm.Config) (*types.Receipt, error)
}

// NewTxExecutor constructs the build-tag-selected VM backend (via vm.NewExecutor)
// and ensures that it implements the TxExecutor contract. The returned adapter
// lives in Go land only – the underlying engine might pin CGO resources.
func NewTxExecutor(sdb *state.StateDB) (TxExecutor, error) {
	base, err := vm.NewExecutor(sdb)
	if err != nil {
		return nil, err
	}
	return &vmExecutorAdapter{inner: base, sdb: sdb}, nil
}

// vmExecutorAdapter maps the generic vm.Executor selected by build tags to
// concrete Go-EVM or REVM execution logic without importing core back into vm.
type vmExecutorAdapter struct {
	inner vm.Executor
	sdb   *state.StateDB
}

func (v *vmExecutorAdapter) Engine() string { return v.inner.Engine() }

// revmCaller matches the method exposed by revmExecutor for receipt generation.
type revmCaller interface {
	CallReceipt(meta *vm.CallMetadata, tx *types.Transaction) (*types.Receipt, error)
}

func (v *vmExecutorAdapter) ExecuteTx(msg *Message, tx *types.Transaction, txIdx int, gp *GasPool, sdb *state.StateDB, header *types.Header, chainCtx ChainContext, evmCfg vm.Config) (*types.Receipt, error) {
	switch v.inner.Engine() {
	case "go-evm":
		// Use the provided chain context when available so BLOCKHASH and other
		// header-related opcodes behave identically between miner and
		// validator paths. Fall back to a stub context in unit-test settings
		// where no blockchain backend exists.

		var bc vm.BlockContext
		if chainCtx != nil {
			bc = NewEVMBlockContext(header, chainCtx, nil)
		} else {
			// Fallback for isolated unit tests.
			cfg := params.TestChainConfig
			bc = NewEVMBlockContext(header, stubChain{cfg: cfg}, nil)
		}

		// If tracing is enabled, wrap the statedb so balance-change hooks fire.
		effectiveDB := vm.StateDB(sdb)
		if evmCfg.Tracer != nil {
			effectiveDB = state.NewHookedState(sdb, evmCfg.Tracer)
		}

		evm := vm.NewEVM(bc, effectiveDB, chainCtx.Config(), evmCfg)
		used := new(uint64)
		receipt, err := ApplyTransactionWithEVM(msg, gp, sdb, header.Number, header.Hash(), tx, used, evm)
		return receipt, err

	case "revm":
		// Use the FFI-backed REVM executor.
		rc, ok := v.inner.(revmCaller)
		if !ok {
			return nil, fmt.Errorf("revm executor missing CallReceipt")
		}

		// Attempt to adjust hard-fork rules dynamically if the backend supports it.
		if specSetter, ok := v.inner.(interface{ SetSpec(id uint8) }); ok {
			sid := vm.SpecID(chainCtx.Config(), header.Number.Uint64(), header.Time)
			specSetter.SetSpec(sid)
		}

		// Build the metadata placeholder from the core message.
		meta := &vm.CallMetadata{
			From:     msg.From.Hex(),
			To:       "", // filled below
			Data:     msg.Data,
			ValueHex: fmt.Sprintf("0x%s", msg.Value.Text(16)),
			GasLimit: msg.GasLimit,
		}
		if msg.To != nil {
			meta.To = msg.To.Hex()
		}

		receipt, err := rc.CallReceipt(meta, tx)
		if err != nil {
			return nil, err
		}
		// Account for gas in the consensus gas pool.
		if err := gp.SubGas(receipt.GasUsed); err != nil {
			return nil, err
		}

		return receipt, nil

	default:
		return nil, fmt.Errorf("unknown engine %s", v.inner.Engine())
	}
}
