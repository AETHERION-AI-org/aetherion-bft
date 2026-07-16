package validatorpop

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/0xPolygon/polygon-edge/command"
	"github.com/0xPolygon/polygon-edge/command/helper"
	"github.com/0xPolygon/polygon-edge/command/polybftsecrets"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/signer"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/wallet"
	"github.com/0xPolygon/polygon-edge/crypto"
	"github.com/0xPolygon/polygon-edge/helper/hex"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/spf13/cobra"
)

// This command produces the on-chain proof-of-possession an operator needs to be admitted
// to AetherionValidatorRegistry. The registry verifies, via the network's BLS precompile
// (0x2030, domain DOMAIN_STATE_RECEIVER), that the operator holds the private key behind
// the BLS public key it registers — closing the rogue-key attack. The message the operator
// signs is bound to the operator address, chain id, and registry address so a proof made
// for one of them can never be replayed on another:
//
//	proofMessage = keccak256(abi.encode(operator, chainId, registry))
//
// This mirrors AetherionValidatorRegistry.proofMessage / _verifyProofOfPossession exactly.
// The operator runs this with its own validator secrets; the resulting BLS public key and
// signature are then handed to whoever drives the governance Safe, which calls
// registerValidator(operator, blsKey, proofOfPossession). The private key never leaves the
// operator's machine and is never printed.

type popParams struct {
	dataDir     string
	configPath  string
	chainID     uint64
	registryStr string
	insecure    bool
}

var params = &popParams{}

func GetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validator-pop",
		Short: "Generate a BLS proof-of-possession for AetherionValidatorRegistry",
		Run:   runCommand,
	}

	cmd.Flags().StringVar(&params.dataDir, polybftsecrets.AccountDirFlag, "",
		polybftsecrets.AccountDirFlagDesc)
	cmd.Flags().StringVar(&params.configPath, polybftsecrets.AccountConfigFlag, "",
		polybftsecrets.AccountConfigFlagDesc)
	cmd.Flags().Uint64Var(&params.chainID, "chain-id", 100892,
		"the target chain id the registry lives on")
	cmd.Flags().StringVar(&params.registryStr, "registry", "",
		"the AetherionValidatorRegistry proxy address the proof is bound to")
	cmd.Flags().BoolVar(&params.insecure, "insecure", false,
		"use the insecure local secrets store")

	cmd.MarkFlagsMutuallyExclusive(polybftsecrets.AccountDirFlag, polybftsecrets.AccountConfigFlag)

	return cmd
}

func runCommand(cmd *cobra.Command, _ []string) {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	if err := types.IsValidAddress(params.registryStr); err != nil {
		outputter.SetError(fmt.Errorf("invalid --registry address %q: %w", params.registryStr, err))

		return
	}

	registry := types.StringToAddress(params.registryStr)

	secretsManager, err := polybftsecrets.GetSecretsManager(params.dataDir, params.configPath, params.insecure)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to resolve secrets manager: %w", err))

		return
	}

	account, err := wallet.NewAccountFromSecret(secretsManager)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to load validator account: %w", err))

		return
	}

	operator := types.Address(account.Ecdsa.Address())

	// proofMessage = keccak256(abi.encode(operator, chainId, registry)) — the same 32-byte
	// digest AetherionValidatorRegistry.proofMessage computes. abi.encode left-pads each
	// value to 32 bytes.
	message := proofMessage(operator, params.chainID, registry)

	signature, err := account.Bls.Sign(message, signer.DomainStateReceiver)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to sign proof-of-possession: %w", err))

		return
	}

	sigBytes, err := signature.Marshal()
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to marshal signature: %w", err))

		return
	}

	outputter.SetCommandResult(&popResult{
		Operator:          operator.String(),
		ChainID:           params.chainID,
		Registry:          registry.String(),
		BLSPublicKey:      hex.EncodeToHex(account.Bls.PublicKey().Marshal()),
		ProofMessage:      hex.EncodeToHex(message),
		ProofOfPossession: hex.EncodeToHex(sigBytes),
	})
}

// proofMessage builds keccak256(abi.encode(address operator, uint256 chainId, address
// registry)) — each argument left-padded to a 32-byte word, exactly as Solidity's
// abi.encode does, then hashed.
func proofMessage(operator types.Address, chainID uint64, registry types.Address) []byte {
	buf := make([]byte, 0, 96)
	buf = append(buf, leftPad32(operator.Bytes())...)
	buf = append(buf, leftPad32(new(big.Int).SetUint64(chainID).Bytes())...)
	buf = append(buf, leftPad32(registry.Bytes())...)

	return crypto.Keccak256(buf)
}

func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}

	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)

	return padded
}

type popResult struct {
	Operator          string `json:"operator"`
	ChainID           uint64 `json:"chainId"`
	Registry          string `json:"registry"`
	BLSPublicKey      string `json:"blsPublicKey"`
	ProofMessage      string `json:"proofMessage"`
	ProofOfPossession string `json:"proofOfPossession"`
}

func (r *popResult) GetOutput() string {
	var buffer bytes.Buffer

	buffer.WriteString("\n[VALIDATOR PROOF-OF-POSSESSION]\n")
	buffer.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Operator|%s", r.Operator),
		fmt.Sprintf("Chain ID|%d", r.ChainID),
		fmt.Sprintf("Registry|%s", r.Registry),
		fmt.Sprintf("BLS public key|%s", r.BLSPublicKey),
		fmt.Sprintf("Proof message|%s", r.ProofMessage),
		fmt.Sprintf("Proof-of-possession|%s", r.ProofOfPossession),
	}))
	buffer.WriteString("\n\nHand the BLS public key and proof-of-possession to the governance " +
		"Safe, which calls:\n  registerValidator(operator, blsPublicKey, proofOfPossession)\n")

	return buffer.String()
}
