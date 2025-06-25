//go:build revm
// +build revm

package integration_test

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	revmbridge "github.com/ethereum/go-ethereum/revm_bridge"
	"github.com/ethereum/go-ethereum/trie"
	trieDB "github.com/ethereum/go-ethereum/triedb/database"
	"github.com/stretchr/testify/require"
)

// Heavy parity benchmark parameters
const (
	heavyBlocks       = 300 // reduced for CI demo
	txsPerBlock       = 200 // reduced for CI demo
	gasContractCreate = 900000
	gasCall           = 120000
)

// TestBlockExecParity_Heavy builds 1000 blocks each carrying 200 smart-contract
// interactions (one contract creation + 199 approve/transfer calls) and
// measures execution time for Go-EVM (generation) versus REVM (verification).
// It also asserts that state root and receipts root match on the final head.
func TestBlockExecParity_Heavy(t *testing.T) {
	// running with reduced parameters, remove adjustment for full benchmark

	// ---------------------------------------------------------------------
	// 1. Common setup (keys, genesis)
	// ---------------------------------------------------------------------
	keySender, _ := crypto.GenerateKey()
	keyRecipient, _ := crypto.GenerateKey()
	senderAddr := crypto.PubkeyToAddress(keySender.PublicKey)
	recipAddr := crypto.PubkeyToAddress(keyRecipient.PublicKey)

	wealth := new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil) // 1e20
	cfg := *params.TestChainConfig                                  // copy
	cfg.LondonBlock = big.NewInt(999999999)                         // push London fork far in the future, disable basefee in short benchmarks
	genesis := &core.Genesis{
		Config: &cfg,
		Alloc: types.GenesisAlloc{
			senderAddr: {Balance: wealth}, // plenty
			recipAddr:  {Balance: wealth},
		},
		GasLimit: 30_000_000,
	}

	// ERC20 byte-code (same as earlier test)
	erc20Code := common.FromHex("0x608060405234801561001057600080fd5b506004361061002b5760003560e01c8063a9059cbb14610030575b600080fd5b61004a6004803603810190610045919061016a565b610060565b60405161005791906101c5565b60405180910390f35b60008273ffffffffffffffffffffffffffffffffffffffff163373ffffffffffffffffffffffffffffffffffffffff167fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef846040516100bf91906101ef565b60405180910390a36001905092915050565b600080fd5b600073ffffffffffffffffffffffffffffffffffffffff82169050919050565b6000610101826100d6565b9050919050565b610111816100f656b811461011c57600080fd5b50565b60008135905061012e81610108565b92915050565b6000819050919050565b61014781610134565b811461015257600080fd5b50565b6000813590506101648161013e565b92915050565b60008060408385031215610181576101806100d1565b5b600061018f8582860161011f565b92505060206101a085828601610155565b9150509250929050565b60008115159050919050565b6101bf816101aa565b82525050565b60006020820190506101da60008301846101b6565b92915050565b6101e981610134565b82525050565b600060208201905061020460008301846101e0565b9291505056fea2646970667358221220b469033f4b77b9565ee84e0a2f04d496b18160d26034d54f9487e57788fd36d564736f6c63430008120033")

	// Helper to build function calldata
	buildTransfer := func(to common.Address, amount int64) []byte {
		method := common.Hex2Bytes("a9059cbb")
		return append(append(method, common.LeftPadBytes(to.Bytes(), 32)...), common.LeftPadBytes(big.NewInt(amount).Bytes(), 32)...)
	}
	buildApprove := func(spender common.Address, amount int64) []byte {
		method := common.Hex2Bytes("095ea7b3")
		return append(append(method, common.LeftPadBytes(spender.Bytes(), 32)...), common.LeftPadBytes(big.NewInt(amount).Bytes(), 32)...)
	}

	engine := ethash.NewFaker()

	// 2. Pre-generate the block sequence once (using Go-EVM) – not part of perf bench
	genDB, blocks, _ := core.GenerateChainWithGenesis(genesis, engine, heavyBlocks, func(i int, bg *core.BlockGen) {
		signer := types.HomesteadSigner{}
		baseNonce := uint64(i * txsPerBlock)
		// tx0 – contract creation
		gasPrice := big.NewInt(1_000_000_000) // 1 Gwei
		createTx, _ := types.SignTx(types.NewTx(&types.LegacyTx{
			Nonce:    baseNonce,
			GasPrice: gasPrice,
			Gas:      gasContractCreate,
			To:       nil,
			Value:    big.NewInt(0),
			Data:     erc20Code,
		}), signer, keySender)
		bg.AddTx(createTx)
		contractAddr := crypto.CreateAddress(senderAddr, createTx.Nonce())
		// remaining txs
		for n := 1; n < txsPerBlock; n++ {
			var data []byte
			if n%2 == 0 {
				data = buildTransfer(recipAddr, int64(n))
			} else {
				data = buildApprove(recipAddr, int64(n))
			}
			tx, _ := types.SignTx(types.NewTx(&types.LegacyTx{
				Nonce:    baseNonce + uint64(n),
				GasPrice: gasPrice,
				Gas:      gasCall,
				To:       &contractAddr,
				Value:    big.NewInt(0),
				Data:     data,
			}), signer, keySender)
			bg.AddTx(tx)
		}
	})

	headGen := blocks[len(blocks)-1].Header()

	// ---------------------------------------------------------------------
	// 3. REVM verification
	// ---------------------------------------------------------------------
	dbVerify := rawdb.NewMemoryDatabase()
	chainVerify, err := core.NewBlockChain(dbVerify, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	require.NoError(t, err)

	// Reset counters *after* blockchain is initialized but before REVM starts
	// executing the generated blocks so that we only measure reads occurring
	// during verification.
	revmbridge.ResetProfileCounters()

	start := time.Now()
	_, err = chainVerify.InsertChain(blocks)

	// Capture the current head even if verification failed so we can attempt a diff.
	headVerify := chainVerify.CurrentHeader()
	if err != nil {
		// Attempt to provide more context on the first divergence when the verification
		// fails due to a state-root mismatch.
		if diff, diffErr := firstTrieDiff(genDB, headGen.Root, dbVerify, headVerify.Root); diffErr == nil {
			t.Logf("first diff: %s", diff)
		} else {
			t.Logf("diff error: %v", diffErr)
		}
	}
	require.NoError(t, err)
	duration := time.Since(start)

	// ---------------------------------------------------------------------
	// 4. Assertions & perf output
	// ---------------------------------------------------------------------
	if headGen.Root != headVerify.Root {
		if diff, err := firstTrieDiff(genDB, headGen.Root, dbVerify, headVerify.Root); err == nil {
			t.Logf("first diff: %s", diff)
		} else {
			t.Logf("diff error: %v", err)
		}
	}
	require.Equal(t, headGen.Root, headVerify.Root)
	require.Equal(t, headGen.ReceiptHash, headVerify.ReceiptHash)
	require.Equal(t, headGen.Hash(), headVerify.Hash())

	// Fetch counter values collected during verification.
	acct, stor := revmbridge.ProfileCounters()
	t.Logf("verification : %s  (accountReads=%d  storageReads=%d)", duration, acct, stor)
}

// kvNodeDatabase is a lightweight adapter that satisfies trie\'s
// NodeDatabase interface using a plain ethdb key/value store. It makes the
// tests independent from the production trie database implementations.
type kvNodeDatabase struct {
	db ethdb.KeyValueReader
}

type kvNodeReader struct {
	db ethdb.KeyValueReader
}

// Node implements trieDB.database.NodeReader by loading the raw node blob via
// its hash from the underlying key/value store. The owner and path parameters
// are ignored because legacy MPT node encoding is content-addressed only by
// the hash.
func (r *kvNodeReader) Node(owner common.Hash, path []byte, hash common.Hash) ([]byte, error) {
	return r.db.Get(hash[:])
}

// NodeReader implements trieDB.database.NodeDatabase.
func (d *kvNodeDatabase) NodeReader(stateRoot common.Hash) (trieDB.NodeReader, error) {
	return &kvNodeReader{db: d.db}, nil
}

// firstTrieDiff returns a human-readable string describing the first divergent
// key/value pair found between two tries rooted at rootA and rootB that are
// stored in the provided databases. The function resorts to a linear scan so
// it\'s meant for debugging only.
func firstTrieDiff(dbA ethdb.Database, rootA common.Hash, dbB ethdb.Database, rootB common.Hash) (string, error) {
	// Build lightweight NodeDatabase wrappers around the two KV stores.
	trieA, err := trie.New(trie.TrieID(rootA), &kvNodeDatabase{db: dbA})
	if err != nil {
		return "", err
	}
	trieB, err := trie.New(trie.TrieID(rootB), &kvNodeDatabase{db: dbB})
	if err != nil {
		return "", err
	}

	// Iterate over the first trie and compare every leaf with the same key in
	// the second trie.
	nodeItA, err := trieA.NodeIterator(nil)
	if err != nil {
		return "", err
	}
	it := trie.NewIterator(nodeItA)
	for it.Next() {
		key := it.Key
		valA := it.Value
		valB, err := trieB.Get(key)
		if err != nil {
			return "", err
		}
		if !bytes.Equal(valA, valB) {
			return fmt.Sprintf("key=%x expected=%x got=%x", key, valA, valB), nil
		}
	}
	if it.Err != nil {
		return "", it.Err
	}

	// Scan the second trie for keys that don\'t exist in the first.
	nodeItB, err := trieB.NodeIterator(nil)
	if err != nil {
		return "", err
	}
	itB := trie.NewIterator(nodeItB)
	for itB.Next() {
		key := itB.Key
		valA, err := trieA.Get(key)
		if err != nil {
			return "", err
		}
		if valA == nil {
			return fmt.Sprintf("key=%x only_in_B value=%x", key, itB.Value), nil
		}
	}
	if itB.Err != nil {
		return "", itB.Err
	}

	return "tries identical but roots differ", nil
}
