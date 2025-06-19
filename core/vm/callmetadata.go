package vm

// CallMetadata carries the minimal fields required by the execution adapters
// to invoke a transaction on the underlying VM backend and obtain a receipt.
// It is intentionally lightweight and tag-free so that it is available in all
// build configurations, avoiding cross-package dependency issues.
//
// String encodings are used for addresses and values to keep the adapter
// boundary simple and avoid heavy dependency chains.
// When interacting with a specific backend (e.g. REVM via FFI) these values
// are re-encoded/decoded as necessary.
// NOTE: This type must stay in sync with the construction logic in
// core/tx_executor.go and the consumption logic in core/vm/dispatcher_revm.go.
type CallMetadata struct {
    From     string  // Hex-encoded sender address (0x…)
    To       string  // Hex-encoded recipient address, empty for contract creation
    Data     []byte  // Calldata
    ValueHex string  // Hex-encoded wei value (0x…)
    GasLimit uint64  // Provided gas
} 