package revmbridge

import (
    "encoding/binary"
    "math/big"
    "sync"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/holiman/uint256"
)

// -----------------------------------------------------------------------------
// Public FFI-compatible types (mirror of Rust side, see STATE_DB_FFI.md)
// -----------------------------------------------------------------------------

type FFIAddress [20]byte

type FFIHash [32]byte

type FFIU256 [32]byte

type FFIAccountInfo struct {
    Balance  FFIU256
    Nonce    uint64
    CodeHash FFIHash
}

// -----------------------------------------------------------------------------
// Implementation of the REVM database callbacks on top of go-ethereum StateDB
// -----------------------------------------------------------------------------

type stateDBImpl struct {
    db *state.StateDB
    // blockHashResolver is optional – if non-nil it is used to satisfy
    // block_hash queries. The function should return the block hash for the
    // given number or the zero hash if not found.
    blockHashResolver func(number uint64) common.Hash
    // mu protects concurrent access because StateDB is **not** thread-safe.
    mu sync.Mutex
}

// Basic returns the account info for `addr`.
func (s *stateDBImpl) Basic(addr common.Address) FFIAccountInfo {
    s.mu.Lock()
    defer s.mu.Unlock()

    balance := s.db.GetBalance(addr)
    nonce := s.db.GetNonce(addr)
    codeHash := s.db.GetCodeHash(addr)

    return FFIAccountInfo{
        Balance:  uint256ToFFIU256(balance),
        Nonce:    nonce,
        CodeHash: hashToFFI(codeHash),
    }
}

// CodeByHash returns the bytecode associated with `codeHash`. The returned slice
// is a copy – callers may mutate it freely.
func (s *stateDBImpl) CodeByHash(codeHash common.Hash) []byte {
    // TODO: The underlying StateDB does not currently expose a direct mapping
    // from `codeHash` to bytecode. Implementing a robust lookup requires an
    // auxiliary index. For the time being we return nil to signal "not found".
    return nil
}

// Storage returns the value stored at `slot` in the account storage.
func (s *stateDBImpl) Storage(addr common.Address, slot common.Hash) FFIU256 {
    s.mu.Lock()
    defer s.mu.Unlock()

    value := s.db.GetState(addr, slot)
    return hashToU256(value)
}

// BlockHash resolves the canonical block hash for a given block number.
func (s *stateDBImpl) BlockHash(number uint64) FFIHash {
    if s.blockHashResolver == nil {
        return FFIHash{}
    }
    h := s.blockHashResolver(number)
    return hashToFFI(h)
}

// -----------------------------------------------------------------------------
// Helper conversion functions
// -----------------------------------------------------------------------------

func uint256ToFFIU256(i *uint256.Int) FFIU256 {
    var out FFIU256
    if i == nil {
        return out
    }
    be := i.ToBig()
    bytes := be.Bytes() // big-endian, length <= 32
    copy(out[32-len(bytes):], bytes)
    return out
}

func hashToFFI(h common.Hash) FFIHash {
    var out FFIHash
    copy(out[:], h[:])
    return out
}

// -- Optional: big-int helpers -------------------------------------------------

func bigToU256(b *big.Int) FFIU256 {
    var out FFIU256
    if b == nil {
        return out
    }
    bytes := b.Bytes()
    copy(out[32-len(bytes):], bytes)
    return out
}

// encodeUint64LE encodes u64 little-endian. Useful for debug.
func encodeUint64LE(v uint64) [8]byte {
    var b [8]byte
    binary.LittleEndian.PutUint64(b[:], v)
    return b
}

func hashToU256(h common.Hash) FFIU256 {
    var out FFIU256
    copy(out[:], h[:])
    return out
} 