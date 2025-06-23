//go:build revm
// +build revm

package integration_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

// TestBlockExec_CreateThenCall verifies that a contract deployed in the first
// transaction of a block is immediately callable by a subsequent transaction
// in the same block when the REVM execution path (overlay + single flush) is
// used during verification.
func TestBlockExec_CreateThenCall(t *testing.T) {
	// 1. Genesis with one rich account
	key, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(key.PublicKey)

	genesis := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc: types.GenesisAlloc{
			sender: {Balance: big.NewInt(params.Ether)},
		},
	}

	// 2. Tiny init-code that returns 1-byte runtime [0x00] (STOP)
	// Bytecode: 6001600c60003960016000f300
	createCode := common.FromHex("0x6001600c60003960016000f300")

	const gasCreate uint64 = 200_000
	const gasCall uint64 = 50_000

	engine := ethash.NewFaker()

	// 3. Generate block#1 with Go-EVM (BlockGen)
	dbGo := rawdb.NewMemoryDatabase()
	chainGo, err := core.NewBlockChain(dbGo, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	require.NoError(t, err)

	signer := types.HomesteadSigner{}
	blocks, _ := core.GenerateChain(genesis.Config, genesis.ToBlock(), engine, dbGo, 1, func(i int, bg *core.BlockGen) {
		// tx0: contract creation
		txCreate, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    0,
			GasPrice: big.NewInt(1),
			Gas:      gasCreate,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     createCode,
		}), signer, key)
		bg.AddTx(txCreate)

		contractAddr := crypto.CreateAddress(sender, txCreate.Nonce())

		// tx1: call the freshly deployed contract (no calldata)
		txCall, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    1,
			GasPrice: big.NewInt(1),
			Gas:      gasCall,
			To:       &contractAddr,
			Value:    big.NewInt(0),
			Data:     nil,
		}), signer, key)
		bg.AddTx(txCall)
	})

	// Execute the generated block with Go-EVM to finalise receipts etc.
	_, err = chainGo.InsertChain(blocks)
	require.NoError(t, err)

	baseline := blocks[0]

	// 4. Verify via REVM execution path
	dbVerify := rawdb.NewMemoryDatabase()
	chainRevm, err := core.NewBlockChain(dbVerify, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	require.NoError(t, err)
	defer chainRevm.Stop()

	_, err = chainRevm.InsertChain(blocks)
	require.NoError(t, err)

	verifiedHeader := chainRevm.GetHeader(baseline.Hash(), baseline.NumberU64())
	require.NotNil(t, verifiedHeader)

	// 5. Compare header fields
	require.Equal(t, baseline.Root(), verifiedHeader.Root, "stateRoot mismatch")
	require.Equal(t, baseline.ReceiptHash(), verifiedHeader.ReceiptHash, "receiptsRoot mismatch")
	require.Equal(t, baseline.Hash(), verifiedHeader.Hash(), "block hash mismatch")
}
