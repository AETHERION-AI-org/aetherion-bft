package contracts

import (
	"github.com/0xPolygon/polygon-edge/types"
)

// Aetherion Network (chainId 100892) user-rewards payout fork.
//
// This is the third Aetherion fork, layered on top of the emission fork
// (aetherion_emission.go) and the native staking fork (aetherion_staking.go).
//
// The emission fork already splits every epoch's AETH eight ways and pushes each share
// into its own module pool. Seven of those pools are holders: the money arrives and stays
// there until the multi-signature wallet withdraws it by hand. The staking fork fixed that
// for exactly one of them — NodeVault — by paying validators from it every epoch.
//
// This fork does the same for StakeFi, the largest share of all at 30% of the emission:
//
//   - AetherionRewardsRouterContract is the proxy of AetherionRewardsRouter
//     (contracts/contracts/AetherionRewardsRouter.sol). From AetherionRewardsRouterForkEpoch on,
//     the node emits one extra system transaction per epoch-ending block that moves the
//     staking slice out of the StakeFi pool and into whichever payout programme the
//     contract is pointed at.
//
// Note what this fork deliberately does NOT decide: who the money finally reaches. The
// contract hands each round to a governance-configured sink, so the payout policy can be
// attached, replaced or upgraded later with a single transaction and no node work at all.
// That is why this fork can be built, rehearsed and rolled out before the policy is
// settled — and why it must be: rolling a binary to every node is the slow, risky half.
//
// DEFAULTS ARE "DISABLED" (types.ZeroAddress / math.MaxUint64). A node built from this
// source keeps the exact pre-fork behaviour: no StakeFi transaction is ever proposed, and
// the emission keeps accumulating in the pool as it does today. Activating requires
// editing both values below to their real targets and cutting a new reproducible build,
// rolled out to EVERY node before AetherionRewardsRouterForkEpoch is reached. A node running an
// older binary past that epoch will diverge at the fork boundary — silently, until it
// stops syncing. See docs/PLAN.md and AETHERION_NETWORK_CUSTOMIZATION.md for the runbook.
//
// Deliberately `var`, not `const`: production never assigns to these after start (there
// is no runtime activation path, only a rebuild), but tests override them to exercise both
// sides of the fork boundary.
//
// Design note: like the two forks before it, activation is gated by EPOCH number, not by
// forkmanager's block-number scheme, because every consumer keys its state by epochId.
var (
	// AetherionRewardsRouterContract — proxy of AetherionRewardsRouter on chain 100892.
	// Set once, before the fork activates; stable across every UUPS upgrade.
	// Deployed 2026-09-14 at 0x5cf97538B410CB6a2D72989d97A74CcA2af39Fd1; it stays the zero
	// address here until ops/native-launch.sh step 4 bakes the fork.
	AetherionRewardsRouterContract = types.StringToAddress("0x5cf97538B410CB6a2D72989d97A74CcA2af39Fd1")

	// AetherionRewardsRouterForkEpoch — first epoch for which the per-epoch user-rewards
	// payout transaction is proposed. MaxUint64 means "never", the safe default.
	AetherionRewardsRouterForkEpoch uint64 = 21025
)

// Note what is deliberately NOT here: a copy of the StakeFi/affiliate percentages. The
// node passes the epoch's FULL reward and the contract asks the emission distributor for
// the split, so the two can never drift apart. AetherionNodeRewardBps (aetherion_staking.go) is the older
// pattern — a Go copy of the 12% that has to be edited by hand whenever the split is
// retuned — and this fork avoids repeating it.

// IsAetherionRewardsRouterActive reports whether the per-epoch user-rewards payout
// transaction should be proposed for the given epoch.
//
// Fails safe: even once epochID reaches AetherionRewardsRouterForkEpoch, the fork stays inactive
// until AetherionRewardsRouterContract is also a real (non-zero) address. A half-configured
// build (fork epoch set, address forgotten) must never start proposing a transaction aimed
// at the zero address.
func IsAetherionRewardsRouterActive(epochID uint64) bool {
	return epochID >= AetherionRewardsRouterForkEpoch &&
		AetherionRewardsRouterContract != types.ZeroAddress
}
