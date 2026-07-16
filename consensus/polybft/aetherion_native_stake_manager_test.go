package polybft

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/0xPolygon/polygon-edge/consensus/polybft/validator"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

func bigEth(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(1e18))
}

func sumBig(xs []*big.Int) *big.Int {
	total := new(big.Int)
	for _, x := range xs {
		total.Add(total, x)
	}

	return total
}

func TestApplyPowerCap_NoCapWhenEqualOrUnset(t *testing.T) {
	t.Parallel()

	stakes := []*big.Int{bigEth(1000), bigEth(1000), bigEth(1000), bigEth(1000)}

	// 25% cap, four equal validators → nothing to clamp.
	capped := applyPowerCap(stakes, big.NewInt(2500))
	for i := range stakes {
		require.Zero(t, capped[i].Cmp(stakes[i]), "equal stakes must be untouched at 25%%")
	}

	// capBps at/above 100% or zero → no effective cap.
	for _, cap := range []*big.Int{big.NewInt(0), big.NewInt(10_000), big.NewInt(20_000), nil} {
		capped := applyPowerCap([]*big.Int{bigEth(9000), bigEth(1000)}, cap)
		require.Zero(t, capped[0].Cmp(bigEth(9000)))
		require.Zero(t, capped[1].Cmp(bigEth(1000)))
	}
}

func TestApplyPowerCap_ClampsWhale(t *testing.T) {
	t.Parallel()

	// 40 / 20 / 20 / 20, cap 25% → the whale is driven down until nobody exceeds 25%
	// of the (shrinking) total. Fixed point here is 20/20/20/20.
	stakes := []*big.Int{bigEth(40), bigEth(20), bigEth(20), bigEth(20)}
	capped := applyPowerCap(stakes, big.NewInt(2500))

	total := sumBig(capped)
	for i, p := range capped {
		// invariant: power_i * 10000 <= total * capBps
		lhs := new(big.Int).Mul(p, big.NewInt(10_000))
		rhs := new(big.Int).Mul(total, big.NewInt(2500))
		require.True(t, lhs.Cmp(rhs) <= 0, "validator %d exceeds the 25%% ceiling", i)
	}
}

// TestApplyPowerCap_Invariant fuzzes random stakes/caps and asserts the core property:
// after capping, no validator's power exceeds capBps of the total.
func TestApplyPowerCap_Invariant(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(100892))

	for iter := 0; iter < 2000; iter++ {
		n := 1 + r.Intn(12)
		stakes := make([]*big.Int, n)
		for i := range stakes {
			// stakes from 1 to 10^6 AETH
			stakes[i] = bigEth(1 + r.Int63n(1_000_000))
		}

		// cap that a set of n could actually satisfy needs capBps*n >= 10000, but the
		// water-fill converges for any positive cap; test a spread.
		capBps := big.NewInt(int64(1 + r.Intn(9999)))

		capped := applyPowerCap(stakes, capBps)
		total := sumBig(capped)
		if total.Sign() == 0 {
			continue
		}

		// The strict ceiling is only satisfiable with enough validators to spread power:
		// n * capBps >= 100%. Below that (e.g. a lone validator) the best achievable is
		// an equal split, so the guarantee weakens to "no one exceeds the equal share".
		feasible := int64(n)*capBps.Int64() >= 10_000

		for i, p := range capped {
			// capping never increases a validator's power
			require.True(t, p.Cmp(stakes[i]) <= 0)

			if feasible {
				lhs := new(big.Int).Mul(p, big.NewInt(10_000))
				rhs := new(big.Int).Mul(total, capBps)
				require.Truef(t, lhs.Cmp(rhs) <= 0,
					"iter %d validator %d: power %s exceeds cap %s bps of total %s",
					iter, i, p, capBps, total)
			} else {
				// power_i <= floor(total/n)  ⇒  power_i * n <= total
				lhs := new(big.Int).Mul(p, big.NewInt(int64(n)))
				require.Truef(t, lhs.Cmp(total) <= 0,
					"iter %d validator %d: power %s exceeds equal share of total %s (n=%d)",
					iter, i, p, total, n)
			}
		}
	}
}

func TestBuildValidatorSetDelta(t *testing.T) {
	t.Parallel()

	addr := func(b byte) types.Address {
		var a types.Address
		a[19] = b

		return a
	}
	meta := func(b byte, power int64) *validator.ValidatorMetadata {
		return &validator.ValidatorMetadata{
			Address:     addr(b),
			VotingPower: bigEth(power),
			IsActive:    true,
		}
	}

	old := validator.AccountSet{meta(1, 100), meta(2, 100), meta(3, 100)}
	// #2 removed, #3 power changed, #4 added, #1 unchanged.
	next := validator.AccountSet{meta(1, 100), meta(3, 250), meta(4, 100)}

	delta := buildValidatorSetDelta(old, next)

	require.Len(t, delta.Added, 1)
	require.Equal(t, addr(4), delta.Added[0].Address)
	require.Len(t, delta.Updated, 1)
	require.Equal(t, addr(3), delta.Updated[0].Address)
	// #2 was at index 1 in the old set
	require.True(t, delta.Removed.IsSet(1))
	require.False(t, delta.Removed.IsSet(0))
	require.False(t, delta.Removed.IsSet(2))
}

func TestUpdateValidatorSet_FrozenBeforeForkEpoch(t *testing.T) {
	// not parallel: mutates the package-level fork configuration
	origAddr := contracts.AetherionValidatorRegistryContract
	origEpoch := contracts.AetherionStakingForkEpoch

	t.Cleanup(func() {
		contracts.AetherionValidatorRegistryContract = origAddr
		contracts.AetherionStakingForkEpoch = origEpoch
	})

	contracts.AetherionValidatorRegistryContract = types.StringToAddress("0x1234")
	contracts.AetherionStakingForkEpoch = 100

	// nil blockchain is safe: before the fork epoch the manager never touches it.
	mgr := newNativeStakeManager(hclog.NewNullLogger(), nil, contracts.AetherionValidatorRegistryContract, 0)

	delta, err := mgr.UpdateValidatorSet(99, validator.AccountSet{})
	require.NoError(t, err)
	require.True(t, delta.IsEmpty(), "before the fork epoch the delta must be empty (frozen set)")
}

func TestAetherionValidatorRewardAmount(t *testing.T) {
	t.Parallel()

	// Epoch 0: base reward 50 AETH, node slice 12% → 6 AETH.
	require.Zero(t, aetherionValidatorRewardAmount(0).Cmp(bigEth(6)))

	// After one halving (epoch = interval): 25 AETH base, node slice 3 AETH.
	require.Zero(t, aetherionValidatorRewardAmount(210_240).Cmp(bigEth(3)))
}
