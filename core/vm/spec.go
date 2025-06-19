package vm

import (
    "math/big"
    "github.com/ethereum/go-ethereum/params"
)

// SpecID maps Ethereum fork rules (as exposed by ChainConfig) to the numeric
// IDs understood by the REVM FFI layer. The mapping follows the same order as
// in revm_ffi_wrapper/src/lib.rs.
func SpecID(cfg *params.ChainConfig, num uint64, ts uint64) uint8 {
    bn := new(big.Int).SetUint64(num)
    switch {
    case cfg.IsOsaka(bn, ts):
        return 20
    case cfg.IsPrague(bn, ts):
        return 19
    case cfg.IsCancun(bn, ts):
        return 17
    case cfg.IsShanghai(bn, ts):
        return 16
    case cfg.IsLondon(bn):
        if cfg.IsArrowGlacier(bn) {
            return 13 // Arrow Glacier (EIP-4345)
        }
        if cfg.IsGrayGlacier(bn) {
            return 14 // Gray Glacier (EIP-5133)
        }
        return 12 // London
    case cfg.IsBerlin(bn):
        return 11
    case cfg.IsIstanbul(bn):
        return 9
    case cfg.IsPetersburg(bn):
        return 8
    case cfg.IsConstantinople(bn):
        return 7
    case cfg.IsByzantium(bn):
        return 6
    case cfg.IsEIP158(bn):
        return 5 // Spurious Dragon
    case cfg.IsEIP150(bn):
        return 4 // Tangerine
    case cfg.IsHomestead(bn):
        return 2
    default:
        return 0 // Frontier
    }
} 