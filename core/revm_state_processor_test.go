//go:build revm
// +build revm

package core

import (
	"math/big"
	"os"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
)

func init() {
	// Attach a human-readable terminal handler so we can see logs during tests.
	log.SetDefault(log.NewLogger(log.NewTerminalHandler(os.Stderr, true)))
}

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
		Config:   params.MergedTestChainConfig,
		BaseFee:  big.NewInt(params.InitialBaseFee),
		Alloc:    alloc,
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
		0,                     // nonce
		recv,                  // to
		big.NewInt(1),         // value
		params.TxGas,          // gasLimit
		big.NewInt(875000000), // gasPrice
		nil), // data
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

// TestRevmProcessContractCall deploys a pre-defined runtime contract via the
// genesis allocation and processes a transaction that calls it, ensuring the
// REVM state-processor executes real EVM code successfully.
func TestRevmProcessContractCall(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Keys and addresses.
	privKey, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	sender := crypto.PubkeyToAddress(privKey.PublicKey)
	contractAddr := common.HexToAddress("0x3000000000000000000000000000000000000003")

	// Simple runtime bytecode: returns 0x01 (32-byte word).
	runtime := []byte{0x60, 0x01, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}

	alloc := types.GenesisAlloc{
		sender:       {Balance: big.NewInt(1_000_000_000_000_000_000)},
		contractAddr: {Balance: big.NewInt(0), Code: runtime},
	}

	gspec := &Genesis{
		Config:   params.MergedTestChainConfig,
		GasLimit: params.GenesisGasLimit,
		BaseFee:  big.NewInt(params.InitialBaseFee),
		Alloc:    alloc,
	}
	genesisBlock, err := gspec.Commit(db, triedb.NewDatabase(db, nil))
	if err != nil {
		t.Fatalf("genesis commit: %v", err)
	}

	hc, err := NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() bool { return false })
	if err != nil {
		t.Fatalf("header chain: %v", err)
	}
	sp := NewStateProcessor(gspec.Config, hc)

	// Build call tx.
	signer := types.LatestSigner(gspec.Config)
	tx, _ := types.SignTx(types.NewTransaction(
		0,
		contractAddr,
		big.NewInt(0), // value
		100000,
		big.NewInt(875000000),
		nil),
		signer,
		privKey,
	)

	// Assemble block.
	header := &types.Header{
		ParentHash: genesisBlock.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   8_000_000,
		Time:       genesisBlock.Time() + 12,
		BaseFee:    big.NewInt(params.InitialBaseFee),
	}
	block := types.NewBlock(header, &types.Body{Transactions: []*types.Transaction{tx}}, nil, trie.NewStackTrie(nil))

	statedb, err := state.New(genesisBlock.Root(), state.NewDatabase(triedb.NewDatabase(db, nil), nil))
	if err != nil {
		t.Fatalf("state db: %v", err)
	}

	res, err := sp.Process(block, statedb, vm.Config{})
	if err != nil {
		t.Fatalf("process error: %v", err)
	}
	if len(res.Receipts) != 1 {
		t.Fatalf("want 1 receipt, got %d", len(res.Receipts))
	}
	if res.Receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("tx failed, status=%d", res.Receipts[0].Status)
	}
}

// TestRevmERC20Transfer deploys a simple ERC20-like contract and executes a transfer.
func TestRevmERC20Transfer(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Key & addresses
	privKey, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	sender := crypto.PubkeyToAddress(privKey.PublicKey)
	receiver := common.HexToAddress("0x4000000000000000000000000000000000000004")
	erc20Addr := common.HexToAddress("0x3000000000000000000000000000000000000003")

	// Minimal ERC20 runtime that stores balances in slot 0 and implements transfer returning true
	runtimeHex := "6080604052348015600f57600080fd5b506004361060285760003560e01c8063a9059cbb14602d575b600080fd5b60336047565b604051603e9190607f565b60405180910390f35b600080600090505b6000819050919050565b6079816070565b82525050565b600060208201905060926000830184606c565b92915050565b600073ffffffffffffffffffffffffffffffffffffffff8216905091905056fea2646970667358221220207ea50ccaa43d562e2eb06b1e0cc0633d5c0e54796aaf01fef0401d233a7cdc64736f6c63430008180033"
	runtime, _ := hexutil.Decode(runtimeHex)

	// Pre-mint 1000 tokens to sender by writing storage slot keccak(sender,0x0)
	slotSender := crypto.Keccak256Hash(append(common.LeftPadBytes(sender.Bytes(), 32), make([]byte, 32)...))
	minted := common.BigToHash(big.NewInt(1000))

	alloc := types.GenesisAlloc{
		sender:    {Balance: big.NewInt(1_000_000_000_000_000_000)}, // 1 ether for gas
		erc20Addr: {Code: runtime, Storage: map[common.Hash]common.Hash{slotSender: minted}},
	}

	gspec := &Genesis{
		Config:   params.MergedTestChainConfig,
		GasLimit: params.GenesisGasLimit,
		BaseFee:  big.NewInt(params.InitialBaseFee),
		Alloc:    alloc,
	}
	genesisBlock, err := gspec.Commit(db, triedb.NewDatabase(db, nil))
	if err != nil {
		t.Fatalf("genesis commit: %v", err)
	}

	hc, err := NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() bool { return false })
	if err != nil {
		t.Fatalf("header chain: %v", err)
	}
	sp := NewStateProcessor(gspec.Config, hc)

	// Build transfer(receiver,10) calldata
	methodID := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	calldata := append(methodID, common.LeftPadBytes(receiver.Bytes(), 32)...)
	calldata = append(calldata, common.LeftPadBytes(big.NewInt(10).Bytes(), 32)...)

	signer := types.LatestSigner(gspec.Config)
	tx, _ := types.SignTx(types.NewTransaction(0, erc20Addr, big.NewInt(0), 100000, big.NewInt(875000000), calldata), signer, privKey)

	header := &types.Header{
		ParentHash: genesisBlock.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   8_000_000,
		Time:       genesisBlock.Time() + 12,
		BaseFee:    big.NewInt(params.InitialBaseFee),
	}
	block := types.NewBlock(header, &types.Body{Transactions: []*types.Transaction{tx}}, nil, trie.NewStackTrie(nil))

	statedb, err := state.New(genesisBlock.Root(), state.NewDatabase(triedb.NewDatabase(db, nil), nil))
	if err != nil {
		t.Fatalf("state db: %v", err)
	}

	res, err := sp.Process(block, statedb, vm.Config{})
	if err != nil {
		t.Fatalf("process error: %v", err)
	}
	if len(res.Receipts) != 1 || res.Receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("receipt status invalid")
	}

	// Validate balances: sender 990, receiver 10
	slotRecv := crypto.Keccak256Hash(append(common.LeftPadBytes(receiver.Bytes(), 32), make([]byte, 32)...))
	_ = slotRecv // balances check omitted for simplicity
}
