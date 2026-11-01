package polybft

import (
	"math/big"
	"testing"

	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/state"
	itrie "github.com/0xPolygon/polygon-edge/state/immutable-trie"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/contract"
)

// storageSlotContractCode returns creation bytecode of a minimal contract with one uint256
// storage slot: setterSig(uint256) writes the slot, getterSig() returns it, any other
// selector reverts. It is hand-assembled so these tests do not depend on a `solc` binary
// on PATH (upstream compiled Solidity via ethgo testutil.Contract, which shells out to solc
// and failed with "failed to compile" wherever solc is not installed).
func storageSlotContractCode(t *testing.T, setterSig, getterSig string) []byte {
	t.Helper()

	setter, err := abi.NewMethod(setterSig)
	require.NoError(t, err)

	getter, err := abi.NewMethod(getterSig)
	require.NoError(t, err)

	runtime := []byte{0x60, 0x00, 0x35, 0x60, 0xe0, 0x1c} // selector := calldataload(0) >> 224
	runtime = append(runtime, 0x80, 0x63)                 // dup selector; push4 setter
	runtime = append(runtime, setter.ID()...)
	runtime = append(runtime, 0x14, 0x60, 0x1d, 0x57, 0x63) // eq -> jumpi 0x1d; push4 getter
	runtime = append(runtime, getter.ID()...)
	runtime = append(runtime,
		0x14, 0x60, 0x25, 0x57, // eq -> jumpi 0x25
		0x60, 0x00, 0x80, 0xfd, // revert(0, 0)
		0x5b, 0x60, 0x04, 0x35, 0x60, 0x00, 0x55, 0x00, // 0x1d: sstore(0, calldataload(4)); stop
		0x5b, 0x60, 0x00, 0x54, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3, // 0x25: return sload(0)
	)

	// init code: codecopy(0, 11, len(runtime)); return(0, len(runtime))
	initCode := []byte{0x60, byte(len(runtime)), 0x80, 0x60, 0x0b, 0x60, 0x00, 0x39, 0x60, 0x00, 0xf3}

	return append(initCode, runtime...)
}

func TestSystemState_GetNextCommittedIndex(t *testing.T) {
	t.Parallel()

	var sideChainBridgeABI, _ = abi.NewMethod(
		"function setNextCommittedIndex(uint256 _index) public payable",
	)

	// equivalent of:
	//   uint256 public lastCommittedId;
	//   function setNextCommittedIndex(uint256 _index) public payable { lastCommittedId = _index; }
	bin := storageSlotContractCode(t,
		"function setNextCommittedIndex(uint256 _index) public payable",
		"function lastCommittedId() public view returns (uint256)",
	)

	transition := newTestTransition(t, nil)

	// deploy a contract
	result := transition.Create2(types.Address{}, bin, big.NewInt(0), 1000000000)
	assert.NoError(t, result.Err)

	provider := &stateProvider{
		transition: transition,
	}

	systemState := NewSystemState(contracts.ValidatorSetContract, result.Address, provider)

	expectedNextCommittedIndex := uint64(45)
	input, err := sideChainBridgeABI.Encode([1]interface{}{expectedNextCommittedIndex})
	assert.NoError(t, err)

	_, err = provider.Call(ethgo.Address(result.Address), input, &contract.CallOpts{})
	assert.NoError(t, err)

	nextCommittedIndex, err := systemState.GetNextCommittedIndex()
	assert.NoError(t, err)
	assert.Equal(t, expectedNextCommittedIndex+1, nextCommittedIndex)
}

func TestSystemState_GetEpoch(t *testing.T) {
	t.Parallel()

	setEpochMethod, err := abi.NewMethod("function setEpoch(uint256 _epochId) public payable")
	require.NoError(t, err)

	// equivalent of:
	//   uint256 public currentEpochId;
	//   function setEpoch(uint256 _epochId) public payable { currentEpochId = _epochId; }
	bin := storageSlotContractCode(t,
		"function setEpoch(uint256 _epochId) public payable",
		"function currentEpochId() public view returns (uint256)",
	)

	transition := newTestTransition(t, nil)

	// deploy a contract
	result := transition.Create2(types.Address{}, bin, big.NewInt(0), 1000000000)
	assert.NoError(t, result.Err)

	provider := &stateProvider{
		transition: transition,
	}

	systemState := NewSystemState(result.Address, contracts.StateReceiverContract, provider)

	expectedEpoch := uint64(50)
	input, err := setEpochMethod.Encode([1]interface{}{expectedEpoch})
	require.NoError(t, err)

	_, err = provider.Call(ethgo.Address(result.Address), input, &contract.CallOpts{})
	require.NoError(t, err)

	epoch, err := systemState.GetEpoch()
	require.NoError(t, err)
	require.Equal(t, expectedEpoch, epoch)
}

func TestStateProvider_Txn_NotSupported(t *testing.T) {
	t.Parallel()

	transition := newTestTransition(t, nil)

	provider := &stateProvider{
		transition: transition,
	}

	_, err := provider.Txn(ethgo.ZeroAddress, createTestKey(t), []byte{0x1})
	require.ErrorIs(t, err, errSendTxnUnsupported)
}

func newTestTransition(t *testing.T, alloc map[types.Address]*chain.GenesisAccount) *state.Transition {
	t.Helper()

	st := itrie.NewState(itrie.NewMemoryStorage())

	ex := state.NewExecutor(&chain.Params{
		Forks: chain.AllForksEnabled,
		BurnContract: map[uint64]types.Address{
			0: types.ZeroAddress,
		},
	}, st, hclog.NewNullLogger())

	rootHash, err := ex.WriteGenesis(alloc, types.Hash{})
	require.NoError(t, err)

	ex.GetHash = func(h *types.Header) state.GetHashByNumber {
		return func(i uint64) types.Hash {
			return rootHash
		}
	}

	transition, err := ex.BeginTxn(
		rootHash,
		&types.Header{},
		types.ZeroAddress,
	)
	assert.NoError(t, err)

	return transition
}
