//go:build revm
// +build revm

package core

import (
	"encoding/hex"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
		nil),                  // data
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

// TestRevmERC20Transfer deploys the real BIGA ERC-20 contract and transfers 10 tokens.
func TestRevmERC20Transfer(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Keys & addresses
	privKey, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	sender := crypto.PubkeyToAddress(privKey.PublicKey)
	receiver := common.HexToAddress("0x4000000000000000000000000000000000000004")

	// Load BIGA creation byte-code
	bytecodePath := "../../revm_integration/revm_ffi_wrapper/examples/bytecode/BIGA.bin"
	data, err := os.ReadFile(bytecodePath)
	if err != nil {
		t.Fatalf("read BIGA bytecode: %v", err)
	}
	bytecodeStr := strings.TrimSpace(string(data))
	if strings.HasPrefix(bytecodeStr, "0x") {
		bytecodeStr = bytecodeStr[2:]
	}
	creationCode, err := hex.DecodeString(bytecodeStr)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}

	// Genesis: give sender ETH for gas
	gspec := &Genesis{
		Config:   params.MergedTestChainConfig,
		GasLimit: 30_000_000,
		BaseFee:  big.NewInt(params.InitialBaseFee),
		Alloc: types.GenesisAlloc{
			sender: {Balance: big.NewInt(1e18)},
		},
	}
	genesisBlock, err := gspec.Commit(db, triedb.NewDatabase(db, nil))
	if err != nil {
		t.Fatalf("genesis commit: %v", err)
	}

	hc, _ := NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() bool { return false })
	sp := NewStateProcessor(gspec.Config, hc)

	signer := types.LatestSigner(gspec.Config)

	// -------- Block #1: deploy BIGA --------
	createTx, _ := types.SignTx(types.NewContractCreation(0, big.NewInt(0), 5_000_000, big.NewInt(1), creationCode), signer, privKey)
	header1 := &types.Header{
		ParentHash: genesisBlock.Hash(),
		Number:     big.NewInt(1),
		GasLimit:   30_000_000,
		Time:       genesisBlock.Time() + 12,
		BaseFee:    big.NewInt(params.InitialBaseFee),
	}
	block1 := types.NewBlock(header1, &types.Body{Transactions: []*types.Transaction{createTx}}, nil, trie.NewStackTrie(nil))

	statedb1, err := state.New(genesisBlock.Root(), state.NewDatabase(triedb.NewDatabase(db, nil), nil))
	if err != nil {
		t.Fatalf("state db: %v", err)
	}
	if _, err := sp.Process(block1, statedb1, vm.Config{}); err != nil {
		t.Fatalf("process block1: %v", err)
	}

	// Derive contract address (nonce 0)
	erc20Addr := crypto.CreateAddress(sender, 0)

	// -------- Block #2: call transfer(receiver,10) --------
	// Build calldata
	methodID := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	calldata := append(methodID, common.LeftPadBytes(receiver.Bytes(), 32)...)
	calldata = append(calldata, common.LeftPadBytes(big.NewInt(10).Bytes(), 32)...)

	tx2, _ := types.SignTx(types.NewTransaction(1, erc20Addr, big.NewInt(0), 200_000, big.NewInt(1), calldata), signer, privKey)

	header2 := &types.Header{
		ParentHash: block1.Hash(),
		Number:     big.NewInt(2),
		GasLimit:   30_000_000,
		Time:       header1.Time + 12,
		BaseFee:    big.NewInt(params.InitialBaseFee),
	}
	block2 := types.NewBlock(header2, &types.Body{Transactions: []*types.Transaction{tx2}}, nil, trie.NewStackTrie(nil))

	rootAfterBlock1 := statedb1.IntermediateRoot(false)
	statedb2, _ := state.New(rootAfterBlock1, state.NewDatabase(triedb.NewDatabase(db, nil), nil))
	res, err := sp.Process(block2, statedb2, vm.Config{})
	if err != nil {
		t.Fatalf("process block2: %v", err)
	}
	if len(res.Receipts) != 1 || res.Receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Fatalf("transfer tx failed")
	}

	// Print balances from REVM Go-statedb (might not reflect runtime yet)
	slotSender := crypto.Keccak256Hash(append(common.LeftPadBytes(sender.Bytes(), 32), make([]byte, 32)...))
	slotRecv := crypto.Keccak256Hash(append(common.LeftPadBytes(receiver.Bytes(), 32), make([]byte, 32)...))
	balSender := statedb2.GetState(erc20Addr, slotSender).Big()
	balRecv := statedb2.GetState(erc20Addr, slotRecv).Big()
	t.Logf("Sender tokens: %s, Receiver tokens: %s", balSender, balRecv)
}
