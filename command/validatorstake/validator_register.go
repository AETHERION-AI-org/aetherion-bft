package validatorstake

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/0xPolygon/polygon-edge/command"
	"github.com/0xPolygon/polygon-edge/command/helper"
	"github.com/0xPolygon/polygon-edge/command/polybftsecrets"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/signer"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/wallet"
	"github.com/0xPolygon/polygon-edge/crypto"
	"github.com/0xPolygon/polygon-edge/txrelayer"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/spf13/cobra"
	"github.com/umbracle/ethgo"
)

// This command registers this node as a validator, with nobody's permission.
//
// The registry admits an operator on two facts it can verify itself: a BLS
// proof-of-possession, which proves the caller holds the block-signing key, and being
// msg.sender, which proves it holds the operator key. There is no application, no
// approval and no waiting on a human. Registration alone grants nothing: the operator is
// idle until it locks stake (see validator-stake), which is the barrier that actually
// costs something.
//
// Both keys are read locally, used to sign, and never printed or transmitted.

const (
	// keccak256("registerSelf(bytes,bytes)")[:4]
	registerSelfSelector = "04d111be"
)

type registerParams struct {
	dataDir     string
	configPath  string
	jsonRPC     string
	registryStr string
	chainID     uint64
	insecure    bool
}

var regParams = &registerParams{}

func GetRegisterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validator-register",
		Short: "Register this node as a validator (permissionless, no approval needed)",
		Run:   runRegisterCommand,
	}

	cmd.Flags().StringVar(&regParams.dataDir, polybftsecrets.AccountDirFlag, "",
		polybftsecrets.AccountDirFlagDesc)
	cmd.Flags().StringVar(&regParams.configPath, polybftsecrets.AccountConfigFlag, "",
		polybftsecrets.AccountConfigFlagDesc)
	cmd.Flags().StringVar(&regParams.jsonRPC, "jsonrpc", "http://127.0.0.1:8545",
		"the JSON-RPC endpoint to submit the registration through")
	cmd.Flags().StringVar(&regParams.registryStr, "registry", "",
		"the AetherionValidatorRegistry proxy address")
	cmd.Flags().Uint64Var(&regParams.chainID, "chain-id", 100892,
		"the chain id the proof-of-possession is bound to")
	cmd.Flags().BoolVar(&regParams.insecure, "insecure", false,
		"use the insecure local secrets store")

	cmd.MarkFlagsMutuallyExclusive(polybftsecrets.AccountDirFlag, polybftsecrets.AccountConfigFlag)

	return cmd
}

func runRegisterCommand(cmd *cobra.Command, _ []string) {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	if err := types.IsValidAddress(regParams.registryStr); err != nil {
		outputter.SetError(fmt.Errorf("invalid --registry address %q: %w", regParams.registryStr, err))

		return
	}

	registry := types.StringToAddress(regParams.registryStr)

	secretsManager, err := polybftsecrets.GetSecretsManager(regParams.dataDir, regParams.configPath,
		regParams.insecure)
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

	relayer, err := txrelayer.NewTxRelayer(txrelayer.WithIPAddress(regParams.jsonRPC))
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to initialize tx relayer: %w", err))

		return
	}

	// Registering twice reverts. Say so plainly instead of spending gas to be told.
	whitelisted, err := readWhitelisted(relayer, operator, registry)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to read validator record: %w", err))

		return
	}

	if whitelisted {
		outputter.SetCommandResult(&registerResult{
			Operator:     operator.String(),
			Registry:     registry.String(),
			BLSPublicKey: hexEncode(account.Bls.PublicKey().Marshal()),
			AlreadyDone:  true,
		})

		return
	}

	// proofMessage = keccak256(abi.encode(operator, chainId, registry)), signed by the BLS
	// key under the state-receiver domain. Identical to what validator-pop emits and to
	// what AetherionValidatorRegistry._verifyProofOfPossession checks.
	message := popMessage(operator, regParams.chainID, registry)

	sig, err := account.Bls.Sign(message, signer.DomainStateReceiver)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to sign proof-of-possession: %w", err))

		return
	}

	sigBytes, err := sig.Marshal()
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to marshal proof-of-possession: %w", err))

		return
	}

	blsKey := account.Bls.PublicKey().Marshal()

	input, err := encodeRegisterSelf(blsKey, sigBytes)
	if err != nil {
		outputter.SetError(err)

		return
	}

	txn := &ethgo.Transaction{
		From:  account.Ecdsa.Address(),
		To:    (*ethgo.Address)(&registry),
		Input: input,
	}

	receipt, err := relayer.SendTransaction(txn, account.Ecdsa)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to send registration: %w", err))

		return
	}

	if receipt.Status != uint64(types.ReceiptSuccess) {
		outputter.SetError(fmt.Errorf("registration reverted in block %d", receipt.BlockNumber))

		return
	}

	outputter.SetCommandResult(&registerResult{
		Operator:     operator.String(),
		Registry:     registry.String(),
		BLSPublicKey: hexEncode(blsKey),
		TxHash:       receipt.TransactionHash.String(),
		BlockNumber:  receipt.BlockNumber,
	})
}

// encodeRegisterSelf builds calldata for registerSelf(bytes,bytes). Both arguments are
// dynamic, so the head holds their offsets and the tail holds length-prefixed, 32-byte
// padded contents.
func encodeRegisterSelf(blsKey, pop []byte) ([]byte, error) {
	selector, err := hexToBytes(registerSelfSelector)
	if err != nil {
		return nil, err
	}

	head := make([]byte, 0, 64)
	head = append(head, leftPad32(big64(64))...) // offset of blsKey: two head words
	head = append(head, leftPad32(big64(int64(64+32+padded(len(blsKey)))))...)

	out := append(selector, head...)
	out = append(out, encodeBytes(blsKey)...)
	out = append(out, encodeBytes(pop)...)

	return out, nil
}

func encodeBytes(b []byte) []byte {
	out := leftPad32(big64(int64(len(b))))
	out = append(out, b...)

	if rem := len(b) % 32; rem != 0 {
		out = append(out, make([]byte, 32-rem)...)
	}

	return out
}

func padded(n int) int {
	if rem := n % 32; rem != 0 {
		return n + (32 - rem)
	}

	return n
}

type registerResult struct {
	Operator     string `json:"operator"`
	Registry     string `json:"registry"`
	BLSPublicKey string `json:"blsPublicKey"`
	TxHash       string `json:"txHash,omitempty"`
	BlockNumber  uint64 `json:"blockNumber,omitempty"`
	AlreadyDone  bool   `json:"alreadyRegistered,omitempty"`
}

func (r *registerResult) GetOutput() string {
	var buffer bytes.Buffer

	if r.AlreadyDone {
		buffer.WriteString("\n[VALIDATOR ALREADY REGISTERED]\n")
		buffer.WriteString(helper.FormatKV([]string{
			fmt.Sprintf("Operator|%s", r.Operator),
			fmt.Sprintf("Registry|%s", r.Registry),
		}))
		buffer.WriteString("\n\nNothing to do. Lock stake with validator-stake to start producing blocks.\n")

		return buffer.String()
	}

	buffer.WriteString("\n[VALIDATOR REGISTERED]\n")
	buffer.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Operator|%s", r.Operator),
		fmt.Sprintf("Registry|%s", r.Registry),
		fmt.Sprintf("BLS public key|%s", r.BLSPublicKey),
		fmt.Sprintf("Transaction|%s", r.TxHash),
		fmt.Sprintf("Block|%d", r.BlockNumber),
	}))
	buffer.WriteString("\n\nThe chain verified the proof-of-possession against its BLS precompile.\n" +
		"Registered but idle: lock stake with validator-stake to join the block-producing set.\n")

	return buffer.String()
}

// popMessage builds keccak256(abi.encode(address operator, uint256 chainId, address
// registry)) — the digest AetherionValidatorRegistry.proofMessage computes and
// _verifyProofOfPossession checks. Binding the proof to all three means a proof made for
// one operator, chain or registry can never be replayed against another.
func popMessage(operator types.Address, chainID uint64, registry types.Address) []byte {
	buf := make([]byte, 0, 96)
	buf = append(buf, leftPad32(operator.Bytes())...)
	buf = append(buf, leftPad32(new(big.Int).SetUint64(chainID).Bytes())...)
	buf = append(buf, leftPad32(registry.Bytes())...)

	return crypto.Keccak256(buf)
}

func big64(n int64) []byte {
	return big.NewInt(n).Bytes()
}

func hexEncode(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}
