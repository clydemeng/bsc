//go:build revm
// +build revm

package core

import (
    "testing"

    "github.com/ethereum/go-ethereum/consensus/ethash"
    "github.com/ethereum/go-ethereum/core/rawdb"
    "github.com/ethereum/go-ethereum/params"
    "github.com/ethereum/go-ethereum/triedb"
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