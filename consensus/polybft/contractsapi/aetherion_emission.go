package contractsapi

import (
	"math/big"

	"github.com/umbracle/ethgo/abi"
)

// distributeEpochMethod encodes/decodes calls into
// AetherionEmissionDistributor.distributeEpoch(uint256 epochId) — see
// contracts/contracts/AetherionEmissionDistributor.sol.
//
// Unlike RewardPool and the other system contracts in gen_sc_data.go,
// AetherionEmissionDistributor is deployed separately via Hardhat, not baked into
// genesis, so there is no generated artifact (bytecode + full ABI) for it here. The
// method is declared by hand instead, mirroring the ad-hoc types already used for
// Uptime / stateSyncABIType in helper.go.
var distributeEpochMethod = abi.MustNewMethod("distributeEpoch(uint256 epochId)")

// DistributeEmissionFn is the state transaction payload for
// AetherionEmissionDistributor.distributeEpoch(epochId).
type DistributeEmissionFn struct {
	EpochID *big.Int `abi:"epochId"`
}

func (d *DistributeEmissionFn) Sig() []byte {
	return distributeEpochMethod.ID()
}

func (d *DistributeEmissionFn) EncodeAbi() ([]byte, error) {
	return distributeEpochMethod.Encode(d)
}

func (d *DistributeEmissionFn) DecodeAbi(buf []byte) error {
	return decodeMethod(distributeEpochMethod, buf, d)
}

var _ StateTransactionInput = &DistributeEmissionFn{}
