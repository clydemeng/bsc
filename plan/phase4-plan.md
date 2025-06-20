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
* **Todo**  add **Code-hash overlay support** – journal code deployments in `pendingBasic` / `pendingCode` so that `CREATE/CREATE2` contracts become visible only after block flush.
* **Unit test**  `TestReceiptTranslation` – deploy a contract that emits logs; compare translated logs to canonical Go-EVM execution.

## Milestone 4.3.1 Block-Execution Parity (Simple)
* **Goal** Execute a block containing only value-transfer / basic contract-call
  transactions through both Go-EVM (BlockGen) and REVM and obtain identical
  receipts, `stateRoot`, `gasUsed`, and `logsBloom`.
* **Scope** "Simple" means: no SSTORE gas-refund scenarios, no self-destruct,
  no precompile calls, no blob transactions.
* **Task**
  1. Extend `tests/integration` with `block_exec_parity_simple_test.go`.
  2. Use `core.BlockGen` to build a 5–10 tx block.
  3. Run execution twice:
     * path-A (native Go-EVM) – baseline.
     * path-B (REVM via dispatcher with journal + single-flush).
  4. Compare receipts array and derived header fields; test fails on any
     mismatch.
* **Unit test** `TestBlockExecParity_Simple` – must be green under both
  `go test ./...` (legacy) and `go test ./... -tags=revm`.
* **Rationale** Serves as regression-safety net before tackling the full gas
  refund and precompile milestones.

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

# Directory Layout for workspace
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

in our workspace, there are 3 repositories.
- forked_revm:  it is forked from revm offical repo. Just for a  code reference so that we can learn how the revm works.
- revm_integration/revm_ffi_wrapper: it is a revm ffi wrapper, which uses revm 24.0.0 version. It expose ffi interfaces so that later on our forked_bsc code can use it
- forked_bsc:  it is a bsc repo forked from the official bsc code base.  It will use our revm_ffi_wrapper as block vm execution layer, to repalce the existing native bsc evm.


# Each milestone should leave the repo green (`go test ./...` & `cargo test`) and may be merged independently. 