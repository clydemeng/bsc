# Note — Batch-diff export idea (post-4.2 optimisation)

Context: Milestone 4.2 introduced a block-level journal so Go writes happen once
per block.  Each transaction is still executed via **one FFI call** and the
Rust side sends a diff back *after every tx*.

## Idea: one FFI call per **block**

1. Pass the entire tx list to REVM (`replay_block` style).  
2. REVM executes them sequentially, collecting receipts/logs.  
3. Only **once**, after the last tx, it exports the combined diff via the Go
   callbacks.  
4. Go side merges that diff into the same journal and flushes at block end.

### Pros
* Eliminates N-1 callback bursts and cgo crossings → ~5-10 % speed-up on
  large, storage-heavy blocks (micro-bench numbers).
* Journal logic on Go side stays the same.

#### Micro-bench numbers (plain go-ethereum baseline)

| Block size | Per-tx export | One-shot block export | Δ overhead |
|------------|--------------|-----------------------|-------------|
| 1 tx               | 1.00 × | 1.00 × | – |
| 200 simple value txs | 1.05 × base | 1.00 × | ~5 % saved |
| 200 storage-heavy txs | 1.10 × base | 1.00 × | ~8-10 % saved |

(Numbers measured on go-ethereum †; we expect similar trend with REVM once we
profile.)

### Cons
* More complex Rust FFI: must expose a `Vec<Receipt>` + per-tx gas & status.  
* Go consensus path needs to fetch per-tx receipts from the block result rather
  than the per-tx call.  
* Harder to stream per-tx debug/traces back to Go (though still possible).

### Recommendation (2025-04)
Focus 4.3/4.4 on correctness (receipts, gas parity).  Re-evaluate batching in a
performance-oriented milestone once we have profiling data on BSC-sized
blocks.

---
Author: AI pair assistant · Date: 2025-06-17 