package tracing

// BalanceChangeReason is a description of the reason why a balance was changed.
type BalanceChangeReason int

const (
	BalanceChangeUnspecified            BalanceChangeReason = iota
	BalanceChangeNativeTransfer
	BalanceChangePrecompCost
	BalanceChangeReward
	BalanceChangeFee
	BalanceChangeIssuance
	BalanceChangeRefund
	BalanceChangeAirdrop
	BalanceChangeWithdrawal
	BalanceChangeRevmTransfer // New reason for REVM transfers
)

// NonceChangeReason is a description of the reason why a nonce was changed.
type NonceChangeReason int

const (
	NonceChangeUnspecified NonceChangeReason = iota
	NonceChangeRevm        // New reason for REVM nonce changes
)

// String returns a human-readable string for the reason.
func (r BalanceChangeReason) String() string {
	switch r {
	case BalanceChangeUnspecified:
		return "unspecified"
	case BalanceChangeNativeTransfer:
		return "native_transfer"
	case BalanceChangePrecompCost:
		return "precomp_cost"
	case BalanceChangeReward:
		return "reward"
	case BalanceChangeFee:
		return "fee"
	case BalanceChangeIssuance:
		return "issuance"
	case BalanceChangeRefund:
		return "refund"
	case BalanceChangeAirdrop:
		return "airdrop"
	case BalanceChangeWithdrawal:
		return "withdrawal"
	case BalanceChangeRevmTransfer:
		return "revm_transfer"
	}
	return "unknown"
}

// String returns a human-readable string for the reason.
func (r NonceChangeReason) String() string {
	switch r {
	case NonceChangeUnspecified:
		return "unspecified"
	case NonceChangeRevm:
		return "revm_nonce_change"
	}
	return "unknown"
} 