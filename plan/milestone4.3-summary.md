# Milestone 4.3 – REVM ⇆ StateDB Integration Status  

_Last updated: 2025-06-25_

## 1  Execution engine
* All transaction execution is carried out by **Rust REVM** (`librevm_ffi.so`).
* Go consensus / block-processing code calls into REVM through `revm_bridge` FFI helpers.

## 2  State access path
```
Go leveldb/pebble   ← authoritative data on disk
│
└─▶ state.StateDB      (Go)
    └─▶ GoDatabase     (Rust FFI callbacks)
        └─▶ CacheDB<GoDatabase>        – block-wide cache
            └─▶ CacheDB<…>::nest()     – per-tx snapshot (COW)
```
* Reads: served from the two-layer `CacheDB` hierarchy; cold keys fall back to `GoDatabase` (1 CGO round-trip).
* Writes: collected in the outer cache, then committed via `FlushPending` → Go StateDB; afterwards `revm_clear_caches_statedb` wipes caches to ensure Go logic (e.g. miner reward) sees fresh data.
* Result: ~99.8 % of lookups hit RAM, cold misses ≈ 1 k for the heavy test (down from 300 k original).

## 3  Data synchronisation
* Commit diff walks every touched account / slot and invokes `re_state_set_basic` / `re_state_set_storage` callbacks, so **Go and Rust views stay bit-for-bit identical**.
* No remaining manual "dual-write" paths; heavy parity test roots converge.

## 4  Performance snapshot
| Phase | Time | Notes |
|-------|------|-------|
| Block generation (REVM) | ~8 s | identical to earlier batch-prefetch run |
| Go-EVM verification | <1 s* | *when debug prints disabled* |
| **Total heavy test** | **≈9 s** | passes, zero root mismatches |

Earlier 2-minute run was due to ~250 000 debug log lines; removing them restores original speed.

## 5  Remaining overhead
* A handful of cold keys per heavy chain still cross the CGO boundary – unavoidable with the current hybrid approach but negligible in practice.

## 6  What we are _not_ doing (yet)
* There is **no "native Rust StateDB"** that reads Pebble/LevelDB directly.  The authoritative trie/database stays in Go.
* Therefore each cold miss still incurs one CGO call; eliminating that would require a full Rust DB adapter and consensus changes on the Go side.

## 7  Potential future work
1. Implement a Rust `Database` that opens the on-disk DB directly, removing CGO for cold reads.
2. Drop Go-EVM verification or switch it to external root-hash checks.
3. Fine-tune cache flushing (e.g. clear only outer cache once per block) to save a few percent.

---
Milestone 4.3 objectives – functional correctness, parity with Go-EVM, and competitive performance – are now met. 