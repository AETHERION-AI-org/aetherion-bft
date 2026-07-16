package contracts

import (
	"github.com/0xPolygon/polygon-edge/types"
)

// Aetherion Network (chainId 100892) emission distribution fork.
//
// AetherionEmissionDistributorContract is the proxy address of
// AetherionEmissionDistributor (contracts/contracts/AetherionEmissionDistributor.sol),
// deployed separately via Hardhat. Unlike the predeploy addresses above, it is NOT
// written at genesis — the proxy address is stable across every UUPS upgrade, so this
// value is set exactly once, before the fork activates, and never touched again.
//
// AetherionEmissionForkEpoch is the first epoch for which:
//   - the node emits a distributeEpoch(epochId) state transaction to the address above
//     (see consensus/polybft/fsm.go createDistributeEmissionTx), and
//   - the legacy RewardPool base reward (state/aetherion_halving.go) is permanently
//     zeroed, so the old and new emission paths can never both pay out for the same
//     epoch (double emission).
//
// These are the CANONICAL mainnet activation values. The fork went live on chain
// 100892 at epoch 12262 (2026-07-15) and is permanent: from that epoch on, the node
// zeroes the legacy RewardPool base reward and emits distributeEpoch(epochId) to the
// distributor proxy. Because activation is irreversible, these values are committed to
// main so any build reproduces the live chain — a node built from source MUST replay
// the fork boundary identically or it will diverge (verified: a second, independently
// keyed node resynced the whole chain through the boundary to a bit-identical post-fork
// state root). Before activation the design default was "disabled" (types.ZeroAddress /
// math.MaxUint64), which reproduced the pre-fork RewardPool-only behaviour; that mode is
// retained only for tests, which override these vars to exercise both sides of the
// boundary. See PLAN.md and AETHERION_NETWORK_CUSTOMIZATION.md for the full runbook.
//
// Deliberately `var`, not `const`: production code never assigns to these after
// process start (there is no runtime activation path, only a rebuild), but tests need
// to override them to exercise both sides of the fork boundary.
//
// Design note: activation is gated by EPOCH number, not by forkmanager's block-number
// scheme. Every consumer of this fork (the halving hook and the emission tx builder)
// already keys its state purely by epochId; PolyBFT's EpochSize is itself one of
// forkmanager's tunable params, so a block-number-keyed gate for an epoch-keyed
// feature could drift from the epoch math if EpochSize ever changes via an unrelated
// fork. A direct epoch-number comparison has no such unit-mismatch failure mode.
var (
	// LIVE on chain 100892 since 2026-07-15 — emission distributor proxy (stable across
	// every UUPS upgrade, never redeployed).
	AetherionEmissionDistributorContract = types.StringToAddress("0x347bB4E2eDb5135458F03488754905D511F38863")

	// First epoch for which the fork is active. Permanent; changing it would rewrite
	// history and fork the chain.
	AetherionEmissionForkEpoch uint64 = 12262
)

// IsAetherionEmissionForkActive reports whether the emission distributor fork is
// active for the given epoch.
//
// Fails safe: even once epochID reaches AetherionEmissionForkEpoch, the fork stays
// inactive until AetherionEmissionDistributorContract is also a real (non-zero)
// address. A half-configured build (fork epoch set, address forgotten) must never
// start zeroing the legacy RewardPool reward without a live replacement receiving it —
// that would silently burn emission instead of merely delaying it.
func IsAetherionEmissionForkActive(epochID uint64) bool {
	return epochID >= AetherionEmissionForkEpoch && AetherionEmissionDistributorContract != types.ZeroAddress
}
