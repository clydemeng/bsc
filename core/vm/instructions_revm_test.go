// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

//go:build revm
// +build revm

package vm

import (
	"strings"
	"testing"
)

func TestRevmAdd(t *testing.T) {
	executor, err := NewRevmExecutor()
	if err != nil {
		t.Fatalf("Failed to create RevmExecutor: %v", err)
	}
	defer executor.Close()

	deployer := "0x1000000000000000000000000000000000000001"

	// Give the deployer some balance so the deployment succeeds.
	if err := executor.SetBalance(deployer, "0x56BC75E2D63100000"); err != nil { // 100 ETH
		t.Fatalf("failed to fund deployer: %v", err)
	}

	// Creation bytecode that deploys a contract whose runtime code performs 1 + 2 and returns the result.
	creationCode := []byte{
		0x60, 0x0d, 0x60, 0x0c, 0x60, 0x00, 0x39, 0x60, 0x0d, 0x60, 0x00, 0xf3, // copy runtime and return
		0x60, 0x01, 0x60, 0x02, 0x01, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3, // runtime: 1 2 ADD store return
	}

	contractAddr, err := executor.DeployContract(deployer, creationCode, 1_000_000)
	if err != nil {
		t.Fatalf("Contract deployment failed: %v", err)
	}

	// Call the deployed contract with empty calldata
	outputHex, err := executor.CallContract(deployer, contractAddr, nil, "0x0", 1_000_000)
	if err != nil {
		t.Fatalf("Contract execution failed: %v", err)
	}

	expected := "0000000000000000000000000000000000000000000000000000000000000003"
	if !strings.EqualFold(outputHex, expected) {
		t.Errorf("Unexpected output: got %s, want %s", outputHex, expected)
	}
} 