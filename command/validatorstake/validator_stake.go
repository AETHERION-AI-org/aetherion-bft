package validatorstake

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/0xPolygon/polygon-edge/command"
	"github.com/0xPolygon/polygon-edge/command/helper"
	"github.com/0xPolygon/polygon-edge/command/polybftsecrets"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/wallet"
	"github.com/0xPolygon/polygon-edge/crypto"
	"github.com/0xPolygon/polygon-edge/txrelayer"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/spf13/cobra"
	"github.com/umbracle/ethgo"
)

// This command locks an operator's own AETH as validator stake in
// AetherionValidatorRegistry. It is the step that turns a whitelisted-but-idle operator
// into a block producer: the registry treats an operator as active once
// `whitelisted && stake >= minStake`, so nothing about the block-producing set changes
// until this runs.
//
// The deposit must come from the operator's own address, because the registry credits
// `msg.sender`. That is why this is a node-side command rather than something governance
// can do on an operator's behalf: only the operator holds the key. The key is read from
// the local secrets store, used to sign, and never printed or transmitted.
//
// The new stake takes effect at the next epoch boundary, when the consensus layer reads
// the registry to rebuild the validator set.

const (
	// keccak256("stake()")[:4]
	stakeSelector = "3a4b66f1"
	// keccak256("minStake()")[:4]
	minStakeSelector = "375b3c0a"
	// keccak256("getValidator(address)")[:4]
	getValidatorSelector = "1904bb2e"
)

type stakeParams struct {
	dataDir     string
	configPath  string
	jsonRPC     string
	registryStr string
	amountStr   string
	insecure    bool
}

var params = &stakeParams{}

func GetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validator-stake",
		Short: "Lock AETH from this node's own key as validator stake",
		Run:   runCommand,
	}

	cmd.Flags().StringVar(&params.dataDir, polybftsecrets.AccountDirFlag, "",
		polybftsecrets.AccountDirFlagDesc)
	cmd.Flags().StringVar(&params.configPath, polybftsecrets.AccountConfigFlag, "",
		polybftsecrets.AccountConfigFlagDesc)
	cmd.Flags().StringVar(&params.jsonRPC, "jsonrpc", "http://127.0.0.1:8545",
		"the JSON-RPC endpoint to submit the deposit through")
	cmd.Flags().StringVar(&params.registryStr, "registry", "",
		"the AetherionValidatorRegistry proxy address")
	cmd.Flags().StringVar(&params.amountStr, "amount", "",
		"how much AETH to lock, in wei")
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

	amount, ok := new(big.Int).SetString(params.amountStr, 10)
	if !ok || amount.Sign() <= 0 {
		outputter.SetError(fmt.Errorf("invalid --amount %q: expected a positive integer in wei",
			params.amountStr))

		return
	}

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

	relayer, err := txrelayer.NewTxRelayer(txrelayer.WithIPAddress(params.jsonRPC))
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to initialize tx relayer: %w", err))

		return
	}

	// Preflight against the registry's own rules, so an operator learns why a deposit
	// would be rejected without paying gas to find out.
	minStake, err := readUint(relayer, operator, registry, minStakeSelector)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to read minStake: %w", err))

		return
	}

	if amount.Cmp(minStake) < 0 {
		outputter.SetError(fmt.Errorf(
			"amount %s wei is below the registry minimum of %s wei", amount, minStake))

		return
	}

	whitelisted, err := readWhitelisted(relayer, operator, registry)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to read validator record: %w", err))

		return
	}

	if !whitelisted {
		outputter.SetError(fmt.Errorf(
			"operator %s is not whitelisted in the registry yet; register it first "+
				"(see the validator-pop command) and try again", operator))

		return
	}

	balance, err := relayer.Client().Eth().GetBalance(account.Ecdsa.Address(), ethgo.Latest)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to read operator balance: %w", err))

		return
	}

	if balance.Cmp(amount) < 0 {
		outputter.SetError(fmt.Errorf(
			"operator %s holds %s wei, which is less than the %s wei it is trying to lock",
			operator, balance, amount))

		return
	}

	input, err := hexToBytes(stakeSelector)
	if err != nil {
		outputter.SetError(err)

		return
	}

	txn := &ethgo.Transaction{
		From:  account.Ecdsa.Address(),
		To:    (*ethgo.Address)(&registry),
		Input: input,
		Value: amount,
	}

	receipt, err := relayer.SendTransaction(txn, account.Ecdsa)
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to send stake transaction: %w", err))

		return
	}

	if receipt.Status != uint64(types.ReceiptSuccess) {
		outputter.SetError(fmt.Errorf("stake transaction reverted in block %d", receipt.BlockNumber))

		return
	}

	total, err := readStake(relayer, operator, registry)
	if err != nil {
		total = amount // the deposit landed; only the confirmation read failed
	}

	outputter.SetCommandResult(&stakeResult{
		Operator:    operator.String(),
		Registry:    registry.String(),
		Amount:      amount.String(),
		TotalStake:  total.String(),
		TxHash:      receipt.TransactionHash.String(),
		BlockNumber: receipt.BlockNumber,
	})
}

// readUint performs a no-argument view call returning a single uint256.
func readUint(relayer txrelayer.TxRelayer, from, to types.Address, selector string) (*big.Int, error) {
	input, err := hexToBytes(selector)
	if err != nil {
		return nil, err
	}

	res, err := relayer.Call(ethgo.Address(from), ethgo.Address(to), input)
	if err != nil {
		return nil, err
	}

	return parseUint(res)
}

// readWhitelisted reads getValidator(operator) and returns its `whitelisted` flag.
// The struct is (bytes blsKey, uint256 stake, uint256 pendingUnbond, uint64 unbondReadyAt,
// uint64 registeredAt, bool whitelisted). Because it opens with a dynamic `bytes`, the
// return is a tuple pointer: word 0 is the offset to the tuple, and the tuple's own head
// starts there. `whitelisted` is the sixth word of that head.
func readWhitelisted(relayer txrelayer.TxRelayer, operator, registry types.Address) (bool, error) {
	word, err := readValidatorWord(relayer, operator, registry, 5)
	if err != nil {
		return false, err
	}

	return word.Sign() != 0, nil
}

// readStake reads the `stake` field (second word of the tuple head) of getValidator.
func readStake(relayer txrelayer.TxRelayer, operator, registry types.Address) (*big.Int, error) {
	return readValidatorWord(relayer, operator, registry, 1)
}

func readValidatorWord(relayer txrelayer.TxRelayer, operator, registry types.Address,
	wordIndex int) (*big.Int, error) {
	input, err := hexToBytes(getValidatorSelector)
	if err != nil {
		return nil, err
	}

	input = append(input, leftPad32(operator.Bytes())...)

	res, err := relayer.Call(ethgo.Address(operator), ethgo.Address(registry), input)
	if err != nil {
		return nil, err
	}

	raw, err := hexToBytes(res)
	if err != nil {
		return nil, err
	}

	if len(raw) < 32 {
		return nil, fmt.Errorf("getValidator returned %d bytes, too short to decode", len(raw))
	}

	// Word 0 holds the byte offset of the tuple head within the return data.
	base := new(big.Int).SetBytes(raw[:32]).Int64()
	start := int(base) + wordIndex*32

	if start < 0 || start+32 > len(raw) {
		return nil, fmt.Errorf("getValidator returned %d bytes, too short for word %d",
			len(raw), wordIndex)
	}

	return new(big.Int).SetBytes(raw[start : start+32]), nil
}

func parseUint(res string) (*big.Int, error) {
	raw, err := hexToBytes(res)
	if err != nil {
		return nil, err
	}

	if len(raw) < 32 {
		return nil, fmt.Errorf("expected a 32-byte word, got %d bytes", len(raw))
	}

	return new(big.Int).SetBytes(raw[:32]), nil
}

func hexToBytes(s string) ([]byte, error) {
	s = trim0x(s)
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex string")
	}

	out := make([]byte, len(s)/2)

	for i := 0; i < len(out); i++ {
		var b int

		if _, err := fmt.Sscanf(s[2*i:2*i+2], "%02x", &b); err != nil {
			return nil, fmt.Errorf("invalid hex: %w", err)
		}

		out[i] = byte(b)
	}

	return out, nil
}

func trim0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}

	return s
}

func leftPad32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}

	padded := make([]byte, 32)
	copy(padded[32-len(b):], b)

	return padded
}

var _ = crypto.Keccak256 // selectors above are the precomputed keccak of their signatures

type stakeResult struct {
	Operator    string `json:"operator"`
	Registry    string `json:"registry"`
	Amount      string `json:"amount"`
	TotalStake  string `json:"totalStake"`
	TxHash      string `json:"txHash"`
	BlockNumber uint64 `json:"blockNumber"`
}

func (r *stakeResult) GetOutput() string {
	var buffer bytes.Buffer

	buffer.WriteString("\n[VALIDATOR STAKE LOCKED]\n")
	buffer.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Operator|%s", r.Operator),
		fmt.Sprintf("Registry|%s", r.Registry),
		fmt.Sprintf("Locked now (wei)|%s", r.Amount),
		fmt.Sprintf("Total stake (wei)|%s", r.TotalStake),
		fmt.Sprintf("Transaction|%s", r.TxHash),
		fmt.Sprintf("Block|%d", r.BlockNumber),
	}))
	buffer.WriteString("\n\nThe deposit is locked, not spent. This node joins the " +
		"block-producing set\nat the next epoch boundary, provided it is running with --seal.\n")

	return buffer.String()
}
