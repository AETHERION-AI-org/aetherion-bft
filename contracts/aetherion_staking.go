package contracts

import (
	"github.com/0xPolygon/polygon-edge/types"
)

// Aetherion Network (chainId 100892) native validator-staking fork.
//
// This is the second Aetherion fork, layered on top of the emission fork
// (aetherion_emission.go). It turns a chain whose validator set was frozen at genesis
// — because this is a standalone childchain with no rootchain stake manager — into one
// with a governance-gated, dynamically expandable validator set, driven entirely by
// on-chain contracts:
//
//   - AetherionValidatorRegistryContract is the proxy of AetherionValidatorRegistry
//     (contracts/contracts/AetherionValidatorRegistry.sol). From AetherionStakingForkEpoch
//     on, the node stops treating the validator set as frozen (the dummy stake manager)
//     and instead rebuilds it every epoch from this registry: the whitelisted operators,
//     their proven BLS keys, and their raw stake — with each operator's voting power
//     clamped to the registry's power ceiling so no single operator can stall the chain.
//
//   - AetherionValidatorRewardsContract is the proxy of AetherionValidatorRewards
//     (contracts/contracts/AetherionValidatorRewards.sol). From the same epoch on, the
//     node emits one extra system transaction per epoch-ending block that pays the active
//     validators their stake-weighted share of the NodeVault node reward — a visible,
//     on-chain payout, not an invisible credit.
//
// Both addresses default to the zero address and AetherionStakingForkEpoch defaults to
// math.MaxUint64, i.e. "disabled". A node built with these defaults keeps the exact
// pre-fork behaviour: the validator set stays frozen (dummy stake manager) and no reward
// transaction is ever proposed. Activating the fork requires editing the two addresses
// and the fork epoch below to their real targets and cutting a new reproducible build,
// rolled out to every node before AetherionStakingForkEpoch is reached. See PLAN.md and
// AETHERION_NETWORK_CUSTOMIZATION.md for the runbook.
//
// Deliberately `var`, not `const`: production never assigns to these after start (there
// is no runtime activation path, only a rebuild), but tests override them to exercise
// both sides of the fork boundary.
//
// Design note: like the emission fork, activation is gated by EPOCH number, not by
// forkmanager's block-number scheme, because every consumer keys its state by epochId.
var (
	// AetherionValidatorRegistryContract — proxy of AetherionValidatorRegistry on chain
	// 100892. Set once, before the fork activates; stable across every UUPS upgrade.
	// PROD ACTIVATION 2026-07-15 — validator registry proxy on chain 100892.
	// Prod defaults (fork disabled) are types.ZeroAddress / math.MaxUint64; do NOT
	// commit these baked values to main until activation is confirmed stable.
	AetherionValidatorRegistryContract = types.StringToAddress("0x6ebA8468F754404C1c93ae94C2D1973683eb749A")

	// AetherionValidatorRewardsContract — proxy of AetherionValidatorRewards on chain
	// 100892. Set once, before the fork activates; stable across every UUPS upgrade.
	AetherionValidatorRewardsContract = types.StringToAddress("0x869fEa83CC84d1F1B485ce993839779B6A1e4fc6")

	// AetherionStakingForkEpoch — first epoch for which the native validator set and the
	// per-epoch validator reward transaction are active.
	AetherionStakingForkEpoch uint64 = 12307
)

// AetherionNodeRewardBps is the node (validator) slice of each epoch's emission, in basis
// points — the NodeVault bucket of the emission split (12% = 6 of every 50 AETH). The
// validator reward transaction pays this much of the raw epoch reward to the active
// validators each epoch, matching exactly what the emission distributor deposits into the
// NodeVault (NodeVault is a non-final bucket, so it receives base * bps / 10000 with no
// dust adjustment). Kept here, next to the fork gate, so the amount is a pure, auditable
// function of the epoch.
const AetherionNodeRewardBps uint64 = 1200


// IsAetherionStakingForkConfigured reports whether this build has the staking fork wired
// at all (the registry address is set). When false, the node uses the pre-fork frozen
// validator set (dummy stake manager) forever — a plain, unconfigured build.
func IsAetherionStakingForkConfigured() bool {
	return AetherionValidatorRegistryContract != types.ZeroAddress
}

// IsAetherionStakingForkActive reports whether the native staking fork is active for the
// given epoch.
//
// Fails safe: even once epochID reaches AetherionStakingForkEpoch, the fork stays
// inactive until AetherionValidatorRegistryContract is also a real (non-zero) address. A
// half-configured build (fork epoch set, registry address forgotten) must never abandon
// the frozen genesis set without a live registry to replace it — that would risk an
// empty or undefined validator set.
func IsAetherionStakingForkActive(epochID uint64) bool {
	return epochID >= AetherionStakingForkEpoch &&
		AetherionValidatorRegistryContract != types.ZeroAddress
}

// IsAetherionValidatorRewardsActive reports whether the per-epoch validator reward
// transaction should be proposed for the given epoch. Requires both the fork to be active
// and the rewards contract address to be configured; otherwise no reward tx is emitted
// (fail-safe: never target the zero address).
func IsAetherionValidatorRewardsActive(epochID uint64) bool {
	return IsAetherionStakingForkActive(epochID) &&
		AetherionValidatorRewardsContract != types.ZeroAddress
}
