//go:build !revm
// +build !revm

package integration_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

// dummyChainCtx is a minimal implementation of core.ChainContext that only provides
// access to the ChainConfig. It's sufficient for creating a BlockContext in tests.
type dummyChainCtx struct{ cfg *params.ChainConfig }

func (d dummyChainCtx) Engine() consensus.Engine { return nil }

func (d dummyChainCtx) GetHeader(_ common.Hash, _ uint64) *types.Header { return nil }

func (d dummyChainCtx) Config() *params.ChainConfig { return d.cfg }

// TestTxHash_GoEVM executes a simple value transfer using the native Go-EVM and
// logs the resulting transaction hash. Compile/run without the `revm` build tag.
func TestTxHash_GoEVM(t *testing.T) {
	// -------------------------------------------------------------------------
	// 1. Common setup
	// -------------------------------------------------------------------------

	// Create two deterministic accounts
	privKey, _ := crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
	fromAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	toAddr := common.HexToAddress("0x0D3ab14BBaD3D99F4203bd7a11aCB94882050E7e")

	chainCfg := params.TestChainConfig
	signer := types.LatestSignerForChainID(chainCfg.ChainID)

	header := &types.Header{
		Number:     big.NewInt(1),
		ParentHash: common.Hash{1},
		BaseFee:    big.NewInt(1_000_000_000), // 1 gwei
		Time:       10,
		GasLimit:   1000000,
		Difficulty: big.NewInt(1),
	}

	// Build a legacy transaction (simpler than blob tx for baseline)
	txData := &types.LegacyTx{
		Nonce:    0,
		GasPrice: big.NewInt(2_000_000_000), // 2 gwei > basefee
		Gas:      params.TxGas,
		To:       &toAddr,
		Value:    big.NewInt(0),
		Data:     nil,
	}
	tx, err := types.SignTx(types.NewTx(txData), signer, privKey)
	require.NoError(t, err)

	// -------------------------------------------------------------------------
	// 2. Run via Go-EVM
	// -------------------------------------------------------------------------
	// Create an in-memory StateDB and fund the sender.
	memDB := state.NewDatabaseForTesting()
	statedb, err := state.New(common.Hash{}, memDB)
	require.NoError(t, err)
	statedb.AddBalance(fromAddr, uint256.NewInt(1e18), tracing.BalanceChangeTransfer)

	blockCtx := core.NewEVMBlockContext(header, dummyChainCtx{cfg: chainCfg}, &fromAddr)
	evm := vm.NewEVM(blockCtx, statedb, chainCfg, vm.Config{})

	// Convert to message and execute
	msg, _ := core.TransactionToMessage(tx, signer, header.BaseFee)
	gasPool := new(core.GasPool).AddGas(header.GasLimit)
	_, err = core.ApplyMessage(evm, msg, gasPool)
	require.NoError(t, err)

	txHash := tx.Hash()
	t.Logf("[Go-EVM] Tx Hash: %s", txHash.Hex())
}
