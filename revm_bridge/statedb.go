package revmbridge

import (
    "encoding/binary"
    "math/big"
    "sync"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/state"
    "github.com/holiman/uint256"
    "github.com/ethereum/go-ethereum/core/tracing"
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
    // cache of codeHash -> code bytes populated lazily
    codeCache sync.Map // map[common.Hash][]byte
    // ---------------- block-level journal (phase-4.2) ----------------
    // pendingBasic records the final AccountInfo (balance, nonce, codeHash)
    // that should be written at block commit.
    pendingBasic map[common.Address]FFIAccountInfo

    // pendingStorage records the final value for storage slots that changed
    // during the block: pendingStorage[addr][slot] = value
    pendingStorage map[common.Address]map[common.Hash]common.Hash
    // blockHashResolver is optional – if non-nil it is used to satisfy
    // block_hash queries. The function should return the block hash for the
    // given number or the zero hash if not found.
    blockHashResolver func(number uint64) common.Hash
    // mu protects concurrent access because StateDB is **not** thread-safe.
    mu sync.Mutex
}

// ensureJournal lazily allocs the maps.
func (s *stateDBImpl) ensureJournal() {
    if s.pendingBasic == nil {
        s.pendingBasic = make(map[common.Address]FFIAccountInfo)
    }
    if s.pendingStorage == nil {
        s.pendingStorage = make(map[common.Address]map[common.Hash]common.Hash)
    }
}

// flushPending applies everything recorded in the block-level journal to the
// underlying StateDB and then clears the journal.
func (s *stateDBImpl) flushPending() {
    s.mu.Lock()
    defer s.mu.Unlock()

    if len(s.pendingBasic) == 0 && len(s.pendingStorage) == 0 {
        return
    }

    for addr, info := range s.pendingBasic {
        bal := ffiU256ToUint256Go(info.Balance)
        s.db.SetBalance(addr, bal, tracing.BalanceChangeTransfer)
        s.db.SetNonce(addr, info.Nonce, tracing.NonceChangeEoACall)
    }

    for addr, slots := range s.pendingStorage {
        for slot, val := range slots {
            s.db.SetState(addr, slot, val)
        }
    }

    // reset
    s.pendingBasic = nil
    s.pendingStorage = nil
}

// Basic returns the account info for `addr`.
func (s *stateDBImpl) Basic(addr common.Address) FFIAccountInfo {
    s.mu.Lock()
    defer s.mu.Unlock()

    // If there is a pending override, return it directly.
    if s.pendingBasic != nil {
        if info, ok := s.pendingBasic[addr]; ok {
            return info
        }
    }

    balance := s.db.GetBalance(addr)
    nonce := s.db.GetNonce(addr)
    codeHash := s.db.GetCodeHash(addr)

    if len(codeHash) != 0 && codeHash != (common.Hash{}) {
        if _, ok := s.codeCache.Load(codeHash); !ok {
            code := s.db.GetCode(addr)
            if len(code) > 0 {
                s.codeCache.Store(codeHash, append([]byte(nil), code...))
            }
        }
    }

    return FFIAccountInfo{
        Balance:  uint256ToFFIU256(balance),
        Nonce:    nonce,
        CodeHash: hashToFFI(codeHash),
    }
}

// CodeByHash returns the bytecode associated with `codeHash`. The returned slice
// is a copy – callers may mutate it freely.
func (s *stateDBImpl) CodeByHash(codeHash common.Hash) []byte {
    if v, ok := s.codeCache.Load(codeHash); ok {
        if b, ok2 := v.([]byte); ok2 {
            return append([]byte(nil), b...) // copy
        }
    }
    return nil
}

// Storage returns the value stored at `slot` in the account storage.
func (s *stateDBImpl) Storage(addr common.Address, slot common.Hash) FFIU256 {
    s.mu.Lock()
    defer s.mu.Unlock()

    // Check pending overlay first.
    if s.pendingStorage != nil {
        if accSlots, ok := s.pendingStorage[addr]; ok {
            if val, ok2 := accSlots[slot]; ok2 {
                return hashToU256(val)
            }
        }
    }

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

// convert package-local FFIU256 ➜ *uint256.Int
func ffiU256ToUint256Go(u FFIU256) *uint256.Int {
    i := new(uint256.Int)
    i.SetBytes(u[:])
    return i
}

// FlushPending applies and clears the pending changes for the given handle.
// It is intended to be called once at the end of a block before the consensus
// engine finalises the header.
func FlushPending(handle uintptr) {
    if st, ok := lookup(handle); ok && st != nil {
        st.flushPending()
    }
} 