package state

import (
	"math/big"

	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/types"
)

const (
	aetherionNetworkChainID int64 = 100892

	// RewardPool.baseReward storage slot in the RewardPool proxy at 0x105.
	aetherionRewardPoolBaseRewardSlot uint64 = 53

	// distributeRewardFor(uint256,(address,uint256)[])
	aetherionDistributeRewardForSelector = "\x8a\x9c\xd8\x2d"

	// 4 years of 10-minute epochs: 365 * 24 * 6 * 4.
	aetherionHalvingIntervalEpochs uint64 = 210240
)

var (
	aetherionInitialEpochReward = new(big.Int).Mul(big.NewInt(50), big.NewInt(1e18))
	aetherionBaseRewardSlot     = types.BytesToHash(new(big.Int).SetUint64(aetherionRewardPoolBaseRewardSlot).Bytes())
)

func (t *Transition) applyAetherionRewardHalving(msg *types.Transaction) {
	if t.ctx.ChainID != aetherionNetworkChainID || !isAetherionRewardDistributionTx(msg) {
		return
	}

	epochID, ok := decodeAetherionRewardEpoch(msg.Input)
	if !ok {
		return
	}

	reward := calculateAetherionEpochReward(epochID)
	rewardHash := types.BytesToHash(reward.Bytes())

	if t.GetStorage(contracts.RewardPoolContract, aetherionBaseRewardSlot) == rewardHash {
		return
	}

	t.SetState(contracts.RewardPoolContract, aetherionBaseRewardSlot, rewardHash)
}

func isAetherionRewardDistributionTx(msg *types.Transaction) bool {
	return msg.Type == types.StateTx &&
		msg.To != nil &&
		*msg.To == contracts.RewardPoolContract &&
		len(msg.Input) >= 36 &&
		string(msg.Input[:4]) == aetherionDistributeRewardForSelector
}

func decodeAetherionRewardEpoch(input []byte) (uint64, bool) {
	if len(input) < 36 {
		return 0, false
	}

	epochID := new(big.Int).SetBytes(input[4:36])
	if !epochID.IsUint64() {
		return 0, false
	}

	return epochID.Uint64(), true
}

func calculateAetherionEpochReward(epochID uint64) *big.Int {
	if contracts.IsAetherionEmissionForkActive(epochID) {
		// Emission moved to AetherionEmissionDistributor (see
		// consensus/polybft/fsm.go createDistributeEmissionTx). The legacy RewardPool
		// base reward is permanently zeroed from the fork epoch onward, so the two
		// paths can never both pay out for the same epoch (double emission). Fails
		// safe: IsAetherionEmissionForkActive stays false — and this legacy path keeps
		// paying — until a real distributor address is also configured.
		return big.NewInt(0)
	}

	return RawAetherionEpochReward(epochID)
}

// RawAetherionEpochReward returns the halving-scheduled base reward for an epoch —
// 50 AETH shifted right by floor(epochID / interval) — WITHOUT the emission-fork zeroing
// applied by calculateAetherionEpochReward. This is the full per-epoch emission budget:
// the emission distributor splits it across the module pools, and the validator reward
// transaction takes the node slice of it (see consensus/polybft/fsm.go
// createDistributeValidatorRewardsTx). Pure function of epochID, so every node agrees.
func RawAetherionEpochReward(epochID uint64) *big.Int {
	halvings := epochID / aetherionHalvingIntervalEpochs
	reward := new(big.Int).Set(aetherionInitialEpochReward)

	if halvings == 0 {
		return reward
	}

	if halvings >= 256 {
		return big.NewInt(0)
	}

	return reward.Rsh(reward, uint(halvings))
}
