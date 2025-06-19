//go:build revm
// +build revm

package integration_test

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	statedb "github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

// TestTxHash_REVM executes the same simple transfer as TestTxHash_GoEVM but
// through the REVM CGO executor and logs the resulting transaction hash. This
// test is only compiled/executed when the `revm` build tag is specified.
func TestTxHash_REVM(t *testing.T) {
	// ---------------------------------------------------------------
	// 1. Prepare identical transaction
	// ---------------------------------------------------------------
	privKey, _ := crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
	fromAddr := crypto.PubkeyToAddress(privKey.PublicKey)
	toAddr := common.HexToAddress("0x0D3ab14BBaD3D99F4203bd7a11aCB94882050E7e")

	chainCfg := params.TestChainConfig
	signer := types.LatestSignerForChainID(chainCfg.ChainID)

	// Build the same legacy transaction (gas price 2 gwei)
	txData := &types.LegacyTx{
		Nonce:    0,
		GasPrice: big.NewInt(2_000_000_000),
		Gas:      params.TxGas,
		To:       &toAddr,
		Value:    big.NewInt(0),
		Data:     nil,
	}
	tx, err := types.SignTx(types.NewTx(txData), signer, privKey)
	require.NoError(t, err)

	// ---------------------------------------------------------------
	// 2. Build in-memory StateDB and REVM executor
	// ---------------------------------------------------------------
	memDB := statedb.NewDatabaseForTesting()
	sdb, err := statedb.New(common.Hash{}, memDB)
	require.NoError(t, err)
	sdb.AddBalance(fromAddr, uint256.NewInt(1e18), tracing.BalanceChangeTransfer)

	// Register handle and create executor
	handle := revmbridge.NewStateDB(sdb)
	require.NotZero(t, handle, "handle should not be zero")
	defer revmbridge.ReleaseStateDB(handle)

	exec, err := revmbridge.NewRevmExecutorStateDB(handle)
	require.NoError(t, err)
	defer exec.Close()

	// ---------------------------------------------------------------
	// 3. Execute via REVM
	// ---------------------------------------------------------------

	txHash := tx.Hash()
	receipt, err := exec.CallContractCommitReceipt(fromAddr.Hex(), toAddr.Hex(), nil, "0x0", tx.Gas(), 0, tx, (*[32]byte)(&txHash))
	require.NoError(t, err, "execution failed")

	fmt.Printf("[REVM] Tx Hash: %s\n", receipt.TxHash.Hex())
}
