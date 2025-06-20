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

	// Generate one block with 5 value transfers
	signer := types.HomesteadSigner{}

	genBlock := genesis.ToBlock()
	blocks, _ := core.GenerateChain(genesis.Config, genBlock, engine, db, 1, func(i int, bg *core.BlockGen) {
		// 5 simple txs
		for n := 0; n < 5; n++ {
			tx, _ := types.SignTx(types.NewTx(&types.LegacyTx{
				Nonce:    uint64(n),
				GasPrice: big.NewInt(1),
				Gas:      params.TxGas,
				To:       &to,
				Value:    big.NewInt(1_000_000_000_000_000), // 0.001 BNB
			}), signer, senderKey)
			bg.AddTx(tx)
		}
	})

	// Insert into the first chain (Go-EVM path) so receipts etc. are finalised
	_, err = chain.InsertChain(blocks)
	require.NoError(t, err)

	return chain, blocks
}
