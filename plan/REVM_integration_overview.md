# REVM-on-BSC Project Overview

_Last updated: 2025-06-25_

---

## 1. Goal

Replace go-ethereum's legacy Go-EVM with the **Rust REVM** execution engine for Binance Smart Chain (BSC) while keeping all other Go consensus / networking layers intact.

Target success criteria
1. **Functional parity** – every existing unit & integration test passes; the heavy `TestBlockExecParity_Heavy` benchmark ends with identical state-root and receipt-root between legacy Go-EVM and REVM.
2. **Performance win** – block-generation speed ≥ 5 × faster than Go-EVM *and* verification speed no slower than Go-EVM.
3. **Maintainability** – minimal intrusive changes to go-ethereum, clean FFI boundary, automated tests.

## 2. High-level plan

| Milestone | Focus | Status |
|-----------|-------|--------|
| 4.1 | Compile REVM as FFI; basic tx execution | ✅ done |
| 4.2 | Batch prefetch cache to kill CGO latency | ✅ done (misses ↓ 300 k → 1 k) |
| 4.3 | Snapshot support for intra-block cloning | ✅ done (two-layer cache) |
| 4.4 | Finalise commit path; all tests green | ✅ all integration tests pass |
| 4.5 | Performance polish & metrics | ↪ current |
| 4.6 | Optional: native Rust LevelDB/Pebble reader | ⏳ future |
| 4.7 | Main-net dress rehearsal, docs | ⏳ future |

## 3. Where we are & what we achieved

* **Architecture** – single authoritative Go `state.StateDB`; REVM accesses it through a thin FFI shim (`GoDatabase`) wrapped in `CacheDB`.
* **Cache innovations**
  * Batch prefetch of sender/recipient/storage slots (hits RAM 99 %).
  * Inner cache (block-wide) + optional outer snapshot layer (per-tx) for cheap copy-on-write.
* **Correctness** – Heavy parity test passes; roots identical; > 800 other tests pass under `-tags=revm`.
* **Performance**
  * test versions are [forked_bsc](https://github.com/clydemeng/bsc-revm/commit/eb0def7994289278961671136a8bfd6382e788bb) and [revm_integration](https://github.com/clydemeng/revm_integration/commit/68b46911e89f05dc8afec3c0bca52e7227ffecec)
  * Test cmds are
    `
    go test ./tests/integration -run TestBlockExecParity_Heavy  -count=1 -v
    go test ./tests/integration -run TestBlockExecParity_Heavy -tags=revm -count=1 -v
    `
  * Block generation (REVM) ≈ **7 s** vs 30 + s Go-EVM  ➜ **> 4 × faster**.
  * Verification tx replay (REVM) **0.65–0.70 s** vs 0.77 s Go-EVM  ➜ parity/slightly better.
  * Cold CGO misses: ~900 per heavy chain.

### Benchmark used for all numbers in this document

We rely on the _`TestBlockExecParity_Heavy`_ integration test located at:

```
tests/integration/block_exec_parity_heavy_test.go
```

The test performs the following steps:

1. **Generate** 300 consecutive blocks (_`heavyBlocks`_) using **Go-EVM**.  
   • Each block contains 200 transactions (_`txsPerBlock`_), totalling **60 000 txs**.  
   • Transaction mix per block: 
     * 1 × ERC-20 contract creation (≈ 900 000 gas)
     * 199 × contract calls (alternating `transfer` / `approve`, ≈ 120 000 gas each)
   • London fork is pushed far in the future so the `baseFee` is 0 – better isolates VM cost.

2. **Verify** the exact same block sequence with a fresh `core.BlockChain` instance that re-executes all txs:
   • With **Go-EVM** when the test is built _without_ the `revm` tag.
   • With **Rust REVM** when built _with_ the `revm` tag (current default).

3. Assert that the final **state-root**, **receipt-root** and **header hash** are identical.

4. Print micro-bench numbers gathered by `revm_bridge.ResetProfileCounters()` – account & storage reads, execution wall-time, etc.

Because the generator side always runs first (7 s with REVM, 30 s with Go-EVM) the **"tx-replay" time quoted in the tables refers _only_ to the verification phase** (step 2).

## 4. Best theoretical result

With a fully native Rust StateDB (no CGO at all) and perfect prefetch:
* CGO cost → 0.
* Block generation could drop to ~6 s (pure execution & memory traffic).
* Verification could hit ~0.5 s.
* End-to-end heavy test ≈ **7 s total** (2 × faster than Go baseline and 25 × faster than original Parity).

## 5. Main obstacles ahead

Below list is ordered by expected engineering effort.

| # | Obstacle | Why it matters | Code that must change |
|---|----------|---------------|-----------------------|
| 1 | **CGO latency on cold reads** | Even after prefetch ≈ 900 account/storage look-ups miss the cache → each incurs ~700 µs CGO hop on macOS (slower on CI) | `revm_integration/revm_ffi_wrapper/src/go_db.rs`  (read callbacks) <br/> `revm_bridge/prefetch.go` (extend key enumeration) |
| 2 | **Native DB access (optional)** | Eliminates CGO completely, allows mmap / zero-copy RLP decoding, could save another 0.2 s per heavy run | New crate under `forked_revm/crates/state_db_rocks`; Go build tag to skip `GoDatabase`; `core/rawdb` read path untouched |
| 3 | **Consensus validation path duplication** | Go consensus still links against Go-EVM for header + receipt validation; we currently maintain _both_ code paths which doubles maintenance | `core/blockchain.go` (`InsertChain`) – introduce interface and toggle; `core/state_processor.go` – unify gas accounting; delete `vm/*` non-revm files eventually |
| 4 | **Complex diff propagation & snapshots** | Two-layer `CacheDB` needs flawless commit + flatten. Any bug silently corrupts trie; hunting these took most of Milestone 4.3 | `revm_integration/revm_ffi_wrapper/src/lib.rs` (commit logic) <br/> `revm_bridge/statedb.go` (`flushPending`) |
| 5 | **Memory pressure for huge blocks** | The block-wide cache can exceed 1 GB on main-net bursts; we need eviction or segmented snapshots | `forked_revm/crates/database/cache_db.rs` (LRU modes) <br/> `revm_bridge/prefetch.go` (size hints) |
| 6 | **Upstream forks / new opcodes** | Prague, Osaka, EOF-v1+v2 – keeping Go & Rust forks aligned requires on-going effort | `forked_revm` subtree + `go-ethereum` rebase scripts |

---
_This document captures the current state and roadmap; keep it updated after each major merge._ 