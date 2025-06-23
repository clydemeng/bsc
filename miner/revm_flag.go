//go:build !revm
// +build !revm

package miner

// revmBuild is a compile-time constant that reports whether the build was
// compiled with the `revm` tag enabled.
const revmBuild = false
