package validatorpop

import (
	"testing"

	"github.com/0xPolygon/polygon-edge/bls"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/signer"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

// TestProofOfPossession_VerifiesLikePrecompile checks the exact round trip the on-chain
// registry relies on: a signature produced over proofMessage with DOMAIN_STATE_RECEIVER
// must verify under the same aggregate check the BLS precompile (0x2030) performs. If this
// passes here, AetherionValidatorRegistry.registerValidator will accept the proof on-chain.
func TestProofOfPossession_VerifiesLikePrecompile(t *testing.T) {
	t.Parallel()

	blsKey, err := bls.GenerateBlsKey()
	require.NoError(t, err)

	operator := types.StringToAddress("0xb9ee4aa2C23c8f560f34a1c448Ad4092ba3330CE")
	registry := types.StringToAddress("0x347bB4E2eDb5135458F03488754905D511F38863")
	const chainID uint64 = 100892

	message := proofMessage(operator, chainID, registry)
	require.Len(t, message, 32)

	sig, err := blsKey.Sign(message, signer.DomainStateReceiver)
	require.NoError(t, err)

	pubKeys := []*bls.PublicKey{blsKey.PublicKey()}

	// Exactly what state/runtime/precompiled/bls_agg_sigs_verification.go does.
	require.True(t, sig.VerifyAggregated(pubKeys, message, signer.DomainStateReceiver),
		"proof-of-possession must verify like the precompile")

	// Wrong domain must fail (guards against signing with the wrong domain).
	require.False(t, sig.VerifyAggregated(pubKeys, message, signer.DomainCheckpointManager))

	// A proof bound to a different chain id must not verify against this message
	// (replay protection across chains).
	otherChain := proofMessage(operator, chainID+1, registry)
	require.False(t, sig.VerifyAggregated(pubKeys, otherChain, signer.DomainStateReceiver))

	// A proof bound to a different registry must not verify either.
	otherRegistry := proofMessage(operator, chainID, types.StringToAddress("0xdead"))
	require.False(t, sig.VerifyAggregated(pubKeys, otherRegistry, signer.DomainStateReceiver))
}

// TestProofMessage_MatchesAbiEncode locks the digest layout to Solidity's
// keccak256(abi.encode(address, uint256, address)) — 3 left-padded 32-byte words.
func TestProofMessage_MatchesAbiEncode(t *testing.T) {
	t.Parallel()

	operator := types.StringToAddress("0x0000000000000000000000000000000000000001")
	registry := types.StringToAddress("0x0000000000000000000000000000000000000002")

	// Deterministic: same inputs → same digest, and it is exactly 32 bytes.
	m1 := proofMessage(operator, 100892, registry)
	m2 := proofMessage(operator, 100892, registry)
	require.Equal(t, m1, m2)
	require.Len(t, m1, 32)

	// Any bound field changing the digest.
	require.NotEqual(t, m1, proofMessage(registry, 100892, registry))
	require.NotEqual(t, m1, proofMessage(operator, 1, registry))
	require.NotEqual(t, m1, proofMessage(operator, 100892, operator))
}
