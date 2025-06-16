# Milestone 4.2 — Block-level Journal & Commit (COMPLETED)

This note summarises every code change delivered for 4.2, the unit-test that
covers it and the exact command to run the test.  Keep it in sync with future
refactors.

| # | Area / file(s) touched | What changed | Covering test | One-liner to run |
|---|------------------------|--------------|---------------|-------------------|
|1|`revm_bridge/statedb.go`|`Basic` and `Storage` read pending overlays before hitting the trie.|`tests/block_commit_test.go`|`go test ./tests -tags revm -run TestBlockCommit_MergedChanges -v`|
|2|`revm_bridge/cgo_exports.go`|`re_state_set_basic` no longer calls `st.Basic` with the mutex held (dead-lock fix).|Same as #1|-|
|3|`revm_bridge/revm_executor_statedb.go`|Stores the StateDB handle; `Close()` now calls `FlushPending(handle)` (auto block-flush).|`integration_erc20_biga_test.go`,<br>`integration_statedb_read_write_test.go`,<br>`integration_statedb_test.go`|```
go test ./revm_bridge -tags revm -run TestRevm_StateDB_BIGA_ReadWrite -v
```
(and analogous commands for the other two)|
|4|`integration_erc20_biga_test.go`<br>`integration_statedb_read_write_test.go`<br>`integration_statedb_test.go`|Updated for journal semantics: manual flushes removed/adjusted; assertions added; callers funded for gas.|Those same tests|See #3 |
|5|`plan/phase4-plan.md`|Added TODO under 4.3: **Code-hash overlay support**.|—|—|

Full green run:
```bash
# from repository root
GOFLAGS="-tags=revm" go test ./revm_bridge ./tests -v
```

Success indicates Milestone 4.2 is fully integrated and verified. 