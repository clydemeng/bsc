# REVM-on-BSC Integration – Project Overview

_Last updated: 2025-06-26_

---

## 1  Project goal

Replace go-ethereum's Go-EVM interpreter with the **Rust REVM** engine for Binance Smart Chain (BSC) while keeping all other Go layers (consensus, networking, RPC) untouched.

Success criteria
1. **Functional parity** – every existing unit & integration test passes; the heavy `TestBlockExecParity_Heavy` benchmark ends with identical state / receipt roots under both engines.
2. **Performance gain** – verified blocks execute at least as fast as Go-EVM; long-term we target ≥ 4× faster block *generation* once REVM is used on the miner path.
3. **Maintainability** – minimal invasive patches on upstream go-ethereum; clean FFI boundary; CI coverage.

---

## 2  Road-map

| Milestone | Focus | Status |
|-----------|-------|--------|
| 4.1 | Compile REVM as CGO FFI, execute a single tx | ✅ done |
| 4.2 | Batch-prefetch cache, cut CGO misses | ✅ 300 k → 1 k |
| 4.3 | Snapshot & commit path, heavy parity green | ✅ all tests pass |
| 4.4 | Performance polish, profiling | ↪ current |
| 4.5 | Switch *block generation* to REVM | ⏳ planned |
| 4.6 | Native Rust LevelDB/Pebble reader (no CGO) | ◻ idea |
| 4.7 | Main-net dress rehearsal & release docs | ◻ future |

---

## 3  Benchmark that drives all numbers

File: `tests/integration/block_exec_parity_heavy_test.go`
Test versions: [forked_bsc](https://github.com/clydemeng/bsc-revm/commit/eb0def7994289278961671136a8bfd6382e788bb) and [revm_integration](https://github.com/clydemeng/revm_integration/commit/68b46911e89f05dc8afec3c0bca52e7227ffecec)

Scenario used for every table:
* **300** blocks (`heavyBlocks`) × **200** transactions each (`txsPerBlock`) → **60 000 TXs** total.
* Transaction mix per block:
  * 1 × ERC-20 **contract creation** (~900 k gas)
  * 199 × **transfer / approve** calls (~120 k gas each)
* London fork is postponed so `baseFee` = 0 – isolates interpreter cost.
* Blocks are first **generated** with Go-EVM; then a fresh `core.BlockChain` **verifies** them, replaying every TX with the engine selected by the build tag.

Timing nomenclature in this document
* **Generation time** – `core.GenerateChainWithGenesis`, always Go-EVM right now.
* **Tx-replay time** – `chain.InsertChain` verification phase only.

---

## 4  Where we stand (June 2025)

### 4.1  Architecture snapshot
```
LevelDB/Pebble   ← authoritative trie on disk
│
└─ state.StateDB  (Go)
   └─ GoDatabase              – thin CGO bridge
      └─ CacheDB<GoDatabase>  – block-wide RAM cache (prefetch fills >99 %)
```
Verification path (consensus, tests):
* Build-tag **`revm`** → REVM executes every TX via FFI, commits diff back to StateDB.
* Build without tag → legacy Go-EVM executes.

Generation path (miners, BlockGen helper):
* **Still uses Go-EVM** unconditionally – REVM not wired in yet.

### 4.2  Performance with current 300×200 benchmark
| Phase | Engine | Wall-time (M-series 8-core) |
|-------|--------|-----------------------------|
| Block generation | Go-EVM (both builds) | **≈ 7.9 s** |
| Verification tx-replay | Go-EVM | 0.77 s |
| Verification tx-replay | **Rust REVM** | **0.66 s** |

So far only the **verification** step benefits (≈ 15 % faster). Generation will speed up once path 4.5 is implemented.

Batch transactions with reth-bsc(ffi) should transmit transactions from Go to Rust. For simplicity, we used RLP for transmission, which takes a lot of time for encoding and decoding. We excluded the time consumed by the encoding/decoding process. Here is the comparison:
| Lang | exec a block |
|-------|--------|
| Go   | per block:  871.458µs/  1.985167ms/ 3.491958ms/ 892.875µs/ 939.417µs/ 945.208µs |
| Rust | per block:   (655.584µs (init bscExecutor and execute txs), 387.625µs (only execute txs)) / (659.292µs, 381.792µs) / (654µs, 373.75µs) /(636.417µs, 363.459µs) |

---

## 5  Best-case potential (after Milestone 4.5 & 4.6)
* Replace Go-EVM in `BlockGen` & miner with REVM → expected 4–5× faster generation → ~2 s for the 300-block workload.
* Native Rust trie reader (no CGO) + deeper prefetch → cut remaining 900 cache-misses, push verification to ~0.5 s.
* End-to-end heavy test ≈ 3 s (vs 8.6 s today, vs 38 s original).

---

## 6  Key obstacles and required code changes

| # | Challenge | Why it matters | Touch-points |
|---|-----------|---------------|--------------|
| 1 | **Wire REVM into block generation** | Unlock the big speed-up; required for miner adoption | `core/chain_makers.go` (`BlockGen.addTx`) – replace direct `vm.NewEVM` + `ApplyTransaction` with `TxExecutor.ExecuteTx` used in `state_processor.go` |
| 2 | **CGO latency on last ~900 cold reads** | Each miss costs ~700 µs; hurts verification outliers | `revm_bridge/prefetch.go` (broaden key enumeration) <br/> `revm_ffi_wrapper/src/go_db.rs` (callback batching) |
| 3 | **Native LevelDB/Pebble reader** | Would remove CGO entirely, enable mmap decode | new crate `forked_revm/crates/state_db_pebble` + build-tag gating |
| 4 | **Snapshot merge complexity** | Bugs silently corrupt trie; demands exhaustive tests | `revm_ffi_wrapper/src/lib.rs` (commit logic) <br/> `revm_bridge/statedb.go` (`flushPending`) |
| 5 | **Memory pressure** | Block-wide cache can exceed 1 GB on main-net bursts | `forked_revm/crates/database/cache_db.rs` (LRU) + eviction policy |
| 6 | **Upstream maintenance** | Prague, Osaka, EOF-v1/2 – dual-language forks drift | Periodic rebase scripts for `forked_bsc` & `forked_revm` |

---
_Keep this document updated after each milestone._ 
