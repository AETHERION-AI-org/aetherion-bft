package contractsapi

import (
	"math/big"

	"github.com/umbracle/ethgo/abi"
)

// distributeValidatorRewardsMethod encodes/decodes calls into
// AetherionValidatorRewards.distribute(uint256 epochId, uint256 amount) — see
// contracts/contracts/AetherionValidatorRewards.sol.
//
// Like AetherionEmissionDistributor, this contract is deployed separately via Hardhat
// (not baked into genesis), so there is no generated artifact for it here; the method is
// declared by hand, mirroring DistributeEmissionFn in aetherion_emission.go.
var distributeValidatorRewardsMethod = abi.MustNewMethod("distribute(uint256 epochId, uint256 amount)")

// DistributeValidatorRewardsFn is the state transaction payload for
// AetherionValidatorRewards.distribute(epochId, amount).
type DistributeValidatorRewardsFn struct {
	EpochID *big.Int `abi:"epochId"`
	Amount  *big.Int `abi:"amount"`
}

func (d *DistributeValidatorRewardsFn) Sig() []byte {
	return distributeValidatorRewardsMethod.ID()
}

func (d *DistributeValidatorRewardsFn) EncodeAbi() ([]byte, error) {
	return distributeValidatorRewardsMethod.Encode(d)
}

func (d *DistributeValidatorRewardsFn) DecodeAbi(buf []byte) error {
	return decodeMethod(distributeValidatorRewardsMethod, buf, d)
}

var _ StateTransactionInput = &DistributeValidatorRewardsFn{}
