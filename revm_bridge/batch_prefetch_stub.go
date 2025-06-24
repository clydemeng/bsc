//go:build !revm
// +build !revm

package revmbridge

import "github.com/ethereum/go-ethereum/common"

// BatchKey is a compile-time shim that allows non-REVM builds to compile
// code paths that reference the type. It carries no runtime semantics.
type BatchKey struct {
	Address common.Address
	Slot    common.Hash
}
