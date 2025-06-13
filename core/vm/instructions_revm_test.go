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
	"encoding/hex"
	"testing"
)

// TestREVMBasicOps tests basic arithmetic operations using REVM
func TestREVMBasicOps(t *testing.T) {
	// Create REVM instance
	executor, err := NewRevmExecutor()
	if err != nil {
		t.Fatalf("Failed to create RevmExecutor: %v", err)
	}
	defer executor.Close()

	tests := []struct {
		name     string
		code     string
		input    string
		expected string
	}{
		{
			name:     "ADD",
			code:     "6001600201", // PUSH1 1 PUSH1 2 ADD
			input:    "",
			expected: "0000000000000000000000000000000000000000000000000000000000000003",
		},
		{
			name:     "SUB",
			code:     "6002600103", // PUSH1 2 PUSH1 1 SUB
			input:    "",
			expected: "0000000000000000000000000000000000000000000000000000000000000001",
		},
		{
			name:     "MUL",
			code:     "6002600202", // PUSH1 2 PUSH1 2 MUL
			input:    "",
			expected: "0000000000000000000000000000000000000000000000000000000000000004",
		},
	}

	// Setup a test account with some ETH for gas
	testAddr := "0x1000000000000000000000000000000000000001"
	err = executor.SetBalance(testAddr, "0x8ac7230489e80000") // 10 ETH
	if err != nil {
		t.Fatalf("Failed to set balance: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert hex string to bytes
			code, err := hex.DecodeString(tt.code)
			if err != nil {
				t.Fatalf("Failed to decode code hex: %v", err)
			}

			// Call contract
			outputHex, err := executor.CallContract(testAddr, testAddr, code, "0x0", 1000000)
			if err != nil {
				t.Fatalf("Contract execution failed: %v", err)
			}

			// Compare with expected result
			if outputHex != tt.expected {
				t.Errorf("Test %s failed: got %s, want %s", tt.name, outputHex, tt.expected)
			}
		})
	}
}

// TestREVMByteOp tests the BYTE operation using REVM
func TestREVMByteOp(t *testing.T) {
	// Create REVM instance
	executor, err := NewRevmExecutor()
	if err != nil {
		t.Fatalf("Failed to create RevmExecutor: %v", err)
	}
	defer executor.Close()

	tests := []struct {
		name     string
		value    string
		index    byte
		expected string
	}{
		{
			name:     "First byte",
			value:    "ABCDEF0908070605040302010000000000000000000000000000000000000000",
			index:    0,
			expected: "00000000000000000000000000000000000000000000000000000000000000AB",
		},
		{
			name:     "Second byte",
			value:    "ABCDEF0908070605040302010000000000000000000000000000000000000000",
			index:    1,
			expected: "00000000000000000000000000000000000000000000000000000000000000CD",
		},
	}

	// Setup a test account with some ETH for gas
	testAddr := "0x1000000000000000000000000000000000000001"
	err = executor.SetBalance(testAddr, "0x8ac7230489e80000") // 10 ETH
	if err != nil {
		t.Fatalf("Failed to set balance: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create bytecode: PUSH32 value PUSH1 index BYTE
			value, _ := hex.DecodeString(tt.value)
			code := append([]byte{0x7f}, value...) // PUSH32
			code = append(code, 0x60, tt.index)    // PUSH1 index
			code = append(code, 0x1a)              // BYTE

			// Call contract
			outputHex, err := executor.CallContract(testAddr, testAddr, code, "0x0", 1000000)
			if err != nil {
				t.Fatalf("Contract execution failed: %v", err)
			}

			// Compare with expected result
			if outputHex != tt.expected {
				t.Errorf("Test %s failed: got %s, want %s", tt.name, outputHex, tt.expected)
			}
		})
	}
}