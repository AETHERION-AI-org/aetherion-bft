package contractsapi

import (
	"math/big"

	"github.com/umbracle/ethgo/abi"
)

// distributeEpochRewardsMethod encodes/decodes calls into
// AetherionRewardsRouter.distributeEpochRewards(uint256 epochId, uint256 reward) — see
// contracts/contracts/AetherionRewardsRouter.sol.
//
// Like AetherionEmissionDistributor and AetherionValidatorRewards, this contract is
// deployed separately via Hardhat (not baked into genesis), so there is no generated
// artifact for it here; the method is declared by hand.
//
// The name matters. decodeStateTransaction dispatches on the 4-byte selector alone, so two
// system contracts must never share a signature — and `distribute(uint256,uint256)` was
// already taken by AetherionValidatorRewards. Had this one reused it, a rewards-router
// transaction would decode as a validator one, fail hash verification and be rejected by
// every node. The distinct name is what keeps the two apart.
var distributeEpochRewardsMethod = abi.MustNewMethod("distributeEpochRewards(uint256 epochId, uint256 reward)")

// DistributeEpochRewardsFn is the state transaction payload for
// AetherionRewardsRouter.distributeEpochRewards(epochId, reward).
//
// `reward` is the epoch's FULL emission, not one pool's slice: the contract asks the
// emission distributor how that divides, so the shares can never drift out of step with
// what the distributor actually deposited.
type DistributeEpochRewardsFn struct {
	EpochID *big.Int `abi:"epochId"`
	Reward  *big.Int `abi:"reward"`
}

func (d *DistributeEpochRewardsFn) Sig() []byte {
	return distributeEpochRewardsMethod.ID()
}

func (d *DistributeEpochRewardsFn) EncodeAbi() ([]byte, error) {
	return distributeEpochRewardsMethod.Encode(d)
}

func (d *DistributeEpochRewardsFn) DecodeAbi(buf []byte) error {
	return decodeMethod(distributeEpochRewardsMethod, buf, d)
}

var _ StateTransactionInput = &DistributeEpochRewardsFn{}
