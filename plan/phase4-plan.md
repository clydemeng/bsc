# Phase-4 – Full-Node Integration Plan

Goal: run a BSC full-node whose EVM execution path is **entirely** handled by the Rust REVM engine via the FFI bridge.
We will reach the goal incrementally through test-driven sub-tasks.

---

## Milestone 4.1 Core VM Switch-Hook
* **Task**  insert a feature flag (`revm`) into Go-Ethereum's VM dispatcher so every transaction routes to `revm_bridge` when the flag is on.
* **Unit test**  `TestVMDispatcher_RevmFlag` ‑ ensure the correct executor type is chosen.

## Milestone 4.2 Block-level Journal & Commit
* **Task**  collect per-tx `GoDatabase.commit` calls, merge them, and flush to BSC's `StateDB` only at end of block (respecting refund gas, touched-set, etc.).
* **Unit test**  `TestBlockCommit_MergedChanges` – run two txs in one block that touch the same slot; assert final state is correct and no double-commit occurs.

## Milestone 4.3 Receipt & Log Translation
* **Task**  convert REVM `ExecutionResult` ⇒ Go `types.Receipt` + `types.Log` preserving topics/order.
* **Unit test**  `TestReceiptTranslation` – deploy a contract that emits logs; compare translated logs to canonical Go-EVM execution.

## Milestone 4.4 Intrinsic Gas / Refund Consistency
* **Task**  re-calculate gas used & refunded from REVM side and feed result into Go consensus engine.
* **Unit test**  `TestGasAccountingParity` – craft txs with SSTORE refund & self-destruct; ensure Go header gasUsed matches REVM output.

## Milestone 4.5 Precompile Coverage
* **Task**  wire Go precompile set into REVM (via precompile provider) or fall back to Go implementation.
* **Unit test**  `TestPrecompile_sha256` etc. – call each precompile via EVM and assert expected output.

## Milestone 4.6 Chain Sync Dry-Run (Dev Chain)
* **Task**  start a `--dev` chain, mine 128 blocks with random contracts through REVM path.
* **Integration test**  `go test ./cmd/geth-revm -run TestDevChainSync` – ensure chain progresses and state root matches Go-EVM for the same tx set.

## Milestone 4.7 Mainnet Segment Replay
* **Task**  replay a fixed BSC mainnet range (e.g. 1 000 blocks) under REVM and compare state root to reference node.
* **Integration test**  script under `scripts/replay-segment.sh` – CI optional.

## Milestone 4.8 Genesis-to-Head Full Replay (Final)
* **Task**  boot from genesis and run to latest head; measure performance.
* **Manual run**  documented in README; not required for automated CI.

---

### Directory Layout for Phase 4
```
forked_bsc/
└── plan/
    └── phase4-plan.md   ← you are here
└── revm_bridge/
    └── ... new dispatcher / commit / translation code ...
└── tests/
    ├── vm_dispatcher_test.go
    ├── block_commit_test.go
    ├── receipt_translation_test.go
    ├── gas_accounting_test.go
    ├── precompile_test.go
    └── dev_chain_sync_test.go
scripts/
└── replay-segment.sh
```

Each milestone should leave the repo green (`go test ./...` & `cargo test`) and may be merged independently. 