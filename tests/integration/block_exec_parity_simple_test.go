//go:build revm
// +build revm

package integration_test

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

// TestBlockExecParity_Simple builds a single block containing only simple
// value-transfer transactions, produces it with Go-EVM (via BlockGen) and then
// verifies it through the REVM execution path. The header, receipts-root and
// state-root must be identical.
func TestBlockExecParity_Simple(t *testing.T) {
	// ---------------------------------------------------------------------
	// 1. Genesis with two funded accounts
	// ---------------------------------------------------------------------
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	addr1 := crypto.PubkeyToAddress(key1.PublicKey)
	addr2 := crypto.PubkeyToAddress(key2.PublicKey)

	genesis := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc: types.GenesisAlloc{
			addr1: {Balance: big.NewInt(params.Ether)},
			addr2: {Balance: big.NewInt(params.Ether)},
		},
	}

	// ---------------------------------------------------------------------
	// 2. Build block#1 with Go-EVM via BlockGen
	// ---------------------------------------------------------------------
	dbGo := rawdb.NewMemoryDatabase()
	engine := ethash.NewFaker()
	_, blocks := buildChainWithTxs(t, dbGo, genesis, engine, key1, addr2)

	baselineBlock := blocks[0]

	// ---------------------------------------------------------------------
	// 3. Import the same block into a fresh chain using the REVM executor
	// ---------------------------------------------------------------------
	dbRevm := rawdb.NewMemoryDatabase()

	chain2, err := core.NewBlockChain(dbRevm, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	require.NoError(t, err)
	defer chain2.Stop()

	_, err = chain2.InsertChain(blocks)
	require.NoError(t, err)

	headerAfter := chain2.GetHeader(baselineBlock.Hash(), baselineBlock.NumberU64())
	require.NotNil(t, headerAfter)

	// ---------------------------------------------------------------------
	// 4. Compare header fields that depend on execution
	// ---------------------------------------------------------------------
	require.Equal(t, baselineBlock.Header().Root, headerAfter.Root, "stateRoot mismatch")
	require.Equal(t, baselineBlock.Header().ReceiptHash, headerAfter.ReceiptHash, "receiptsRoot mismatch")
	require.Equal(t, baselineBlock.Header().Hash(), headerAfter.Hash(), "block hash mismatch")
}

// buildChainWithTxs creates a chain with 1 block carrying 5 simple transfers
// from addr1 to addr2 and returns the blockchain and the generated blocks.
func buildChainWithTxs(t *testing.T, db ethdb.Database, genesis *core.Genesis, engine consensus.Engine, senderKey *ecdsa.PrivateKey, to common.Address) (*core.BlockChain, []*types.Block) {
	// Genesis will be written by GenerateChainWithGenesis below.
	chain, err := core.NewBlockChain(db, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	require.NoError(t, err)

	// Generate one block: deploy ERC20 + two transfers
	signer := types.HomesteadSigner{}

	// Simple ERC20 runtime+constructor bytecode (same as used in api_test)
	erc20Code := common.FromHex("0x608060405234801561001057600080fd5b506004361061002b5760003560e01c8063a9059cbb14610030575b600080fd5b61004a6004803603810190610045919061016a565b610060565b60405161005791906101c5565b60405180910390f35b60008273ffffffffffffffffffffffffffffffffffffffff163373ffffffffffffffffffffffffffffffffffffffff167fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef846040516100bf91906101ef565b60405180910390a36001905092915050565b600080fd5b600073ffffffffffffffffffffffffffffffffffffffff82169050919050565b6000610101826100d6565b9050919050565b610111816100f6565b811461011c57600080fd5b50565b60008135905061012e81610108565b92915050565b6000819050919050565b61014781610134565b811461015257600080fd5b50565b6000813590506101648161013e565b92915050565b60008060408385031215610181576101806100d1565b5b600061018f8582860161011f565b92505060206101a085828601610155565b9150509250929050565b60008115159050919050565b6101bf816101aa565b82525050565b60006020820190506101da60008301846101b6565b92915050565b6101e981610134565b82525050565b600060208201905061020460008301846101e0565b9291505056fea2646970667358221220b469033f4b77b9565ee84e0a2f04d496b18160d26034d54f9487e57788fd36d564736f6c63430008120033")

	// Precompute contract address (nonce 0)
	fromAddr := crypto.PubkeyToAddress(senderKey.PublicKey)
	contractAddr := crypto.CreateAddress(fromAddr, 0)

	blocks, _ := core.GenerateChain(genesis.Config, genesis.ToBlock(), engine, db, 1, func(i int, bg *core.BlockGen) {
		// tx0: contract creation
		createTx, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    0,
			GasPrice: big.NewInt(1),
			Gas:      800000,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     erc20Code,
		}), signer, senderKey)
		bg.AddTx(createTx)

		// Helper to build transfer calldata: transfer(to,value)
		transferData := func(seq int64) []byte {
			method := common.Hex2Bytes("a9059cbb")
			padAddr := common.LeftPadBytes(to.Bytes(), 32)
			padVal := common.LeftPadBytes(big.NewInt(seq).Bytes(), 32)
			return append(append(method, padAddr...), padVal...)
		}
		// tx1: transfer 11
		call1, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    1,
			GasPrice: big.NewInt(1),
			Gas:      100000,
			To:       &contractAddr,
			Value:    big.NewInt(0),
			Data:     transferData(11),
		}), signer, senderKey)
		bg.AddTx(call1)
		// tx2: transfer 22
		call2, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    2,
			GasPrice: big.NewInt(1),
			Gas:      100000,
			To:       &contractAddr,
			Value:    big.NewInt(0),
			Data:     transferData(22),
		}), signer, senderKey)
		bg.AddTx(call2)
	})

	// Insert into the first chain (Go-EVM path) so receipts etc. are finalised
	_, err = chain.InsertChain(blocks)
	require.NoError(t, err)

	return chain, blocks
}
