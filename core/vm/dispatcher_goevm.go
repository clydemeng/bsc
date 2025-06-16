//go:build !revm
// +build !revm

package vm

import "github.com/ethereum/go-ethereum/core/state"

// Executor is a minimal abstraction that the VM dispatcher returns.
// For the Go-EVM build we simply expose a stub that marks itself as
// the canonical Go interpreter. Subsequent milestones will flesh out
// additional behaviour as required.

type Executor interface {
    // Engine returns a human-readable short name identifying the backend.
    Engine() string
}

type goExecutor struct{}

func (goExecutor) Engine() string { return "go-evm" }

// NewExecutor returns the default Go-EVM executor when the build does **not**
// include the `revm` tag.
func NewExecutor(_ *state.StateDB) (Executor, error) {
    return goExecutor{}, nil
}

// _ is used to avoid unused import when the stub has no logic yet.
var _ = state.NewDatabaseForTesting // keep compiler happy without losing the import 