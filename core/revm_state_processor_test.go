//go:build revm
// +build revm

package core

import (
    "testing"

    "github.com/ethereum/go-ethereum/consensus/ethash"
    "github.com/ethereum/go-ethereum/core/rawdb"
    "github.com/ethereum/go-ethereum/params"
    "github.com/ethereum/go-ethereum/triedb"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/ethereum/go-ethereum/core/vm"
    "github.com/ethereum/go-ethereum/trie"
    "math/big"
)

// TestStateProcessorInit verifies that a StateProcessor can be constructed with
// a fresh in-memory HeaderChain. This is a very small smoke-test to ensure the
// REVM-backed state processor compiles and initialises without error.
func TestStateProcessorInit(t *testing.T) {
    db := rawdb.NewMemoryDatabase()

    // Commit a minimal genesis block to satisfy header-chain requirements.
    gspec := &Genesis{Config: params.MergedTestChainConfig}
    gspec.Commit(db, triedb.NewDatabase(db, nil))

    // Build a minimal HeaderChain using the merged test chain config and a
    // dummy (non-sealing) consensus engine.
    hc, err := NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() bool { return false })
    if err != nil {
        t.Fatalf("failed to create header chain: %v", err)
    }

    sp := NewStateProcessor(params.MergedTestChainConfig, hc)
    if sp == nil {
        t.Fatalf("expected non-nil StateProcessor")
    }
}

// TestRevmProcessSingleTx builds a simple block containing one transaction and
// feeds it through the REVM-backed StateProcessor, asserting it runs without
// error and produces one receipt.
func TestRevmProcessSingleTx(t *testing.T) {
    db := rawdb.NewMemoryDatabase()

    // 1. Prepare a minimal genesis allocating balance to a test account.
    privKey, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291") // same as state_processor_test.go
    sender := crypto.PubkeyToAddress(privKey.PublicKey)
    recv := common.HexToAddress("0x2000000000000000000000000000000000000002")

    alloc := types.GenesisAlloc{
        sender: {Balance: big.NewInt(1_000_000_000_000_000_000)}, // 1 ether
    }

    gspec := &Genesis{
        Config:  params.MergedTestChainConfig,
        BaseFee: big.NewInt(params.InitialBaseFee),
        Alloc:   alloc,
        GasLimit: params.GenesisGasLimit,
    }
    genesisBlock, err := gspec.Commit(db, triedb.NewDatabase(db, nil))
    if err != nil {
        t.Fatalf("failed to commit genesis: %v", err)
    }

    // 2. Build the HeaderChain and StateProcessor.
    hc, err := NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() bool { return false })
    if err != nil {
        t.Fatalf("failed to create header chain: %v", err)
    }
    sp := NewStateProcessor(gspec.Config, hc)

    // 3. Construct a signed legacy transaction.
    signer := types.LatestSigner(gspec.Config)
    tx, _ := types.SignTx(types.NewTransaction(
        0,               // nonce
        recv,            // to
        big.NewInt(1),   // value
        params.TxGas,    // gasLimit
        big.NewInt(875000000), // gasPrice
        nil),            // data
        signer,
        privKey,
    )

    // 4. Assemble the block containing the tx.
    header := &types.Header{
        ParentHash: genesisBlock.Hash(),
        Number:     big.NewInt(1),
        GasLimit:   8_000_000,
        Time:       genesisBlock.Time() + 12,
        BaseFee:    big.NewInt(params.InitialBaseFee),
        Coinbase:   common.Address{},
    }
    body := &types.Body{Transactions: []*types.Transaction{tx}}
    block := types.NewBlock(header, body, nil, trie.NewStackTrie(nil))

    // 5. Initialise a StateDB at the genesis root.
    statedb, err := state.New(genesisBlock.Root(), state.NewDatabase(triedb.NewDatabase(db, nil), nil))
    if err != nil {
        t.Fatalf("failed to create stateDB: %v", err)
    }

    // 6. Process the block.
    res, err := sp.Process(block, statedb, vm.Config{})
    if err != nil {
        t.Fatalf("process returned error: %v", err)
    }
    if res == nil || len(res.Receipts) != 1 {
        t.Fatalf("expected 1 receipt, got %d", len(res.Receipts))
    }
} 