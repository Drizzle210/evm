package cosmos

import (
	"bytes"
	"encoding/base64"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cosmossecp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authante "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/auth/migrations/legacytx"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	anteinterfaces "github.com/cosmos/evm/ante/interfaces"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/evm/ethereum/eip712"
	"github.com/cosmos/evm/types"
	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

var evmCodec codec.ProtoCodecMarshaler

var (
	// simulation signature values used to estimate gas consumption
	key                = make([]byte, cosmossecp256k1.PubKeySize)
	simSecp256k1Pubkey = &cosmossecp256k1.PubKey{Key: key}

	// _ authsigning.SigVerifiableTx = (*legacytx.StdTx)(nil) // assert StdTx implements SigVerifiableTx
)

func init() {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	evmCodec = codec.NewProtoCodec(registry)
}

// Deprecated: LegacyEip712SigVerificationDecorator Verify all signatures for a tx and return an error if any are invalid. Note,
// the LegacyEip712SigVerificationDecorator decorator will not get executed on ReCheck.
// NOTE: As of v10, EIP-712 signature verification is handled by the ethsecp256k1 public key (see ethsecp256k1.go)
//
// CONTRACT: Pubkeys are set in context for all signers before this decorator runs
// CONTRACT: Tx must implement SigVerifiableTx interface
type LegacyEip712SigVerificationDecorator struct {
	ak        anteinterfaces.AccountKeeper
	evmKeeper anteinterfaces.EVMKeeper
}

// Deprecated: NewLegacyEip712SigVerificationDecorator creates a new LegacyEip712SigVerificationDecorator
func NewLegacyEip712SigVerificationDecorator(
	ak anteinterfaces.AccountKeeper,
	evmKeeper anteinterfaces.EVMKeeper,
) LegacyEip712SigVerificationDecorator {
	return LegacyEip712SigVerificationDecorator{
		ak:        ak,
		evmKeeper: evmKeeper,
	}
}

// AnteHandle handles validation of EIP712 signed cosmos txs.
// it is not run on RecheckTx
func (svd LegacyEip712SigVerificationDecorator) AnteHandle(ctx sdk.Context,
	tx sdk.Tx,
	simulate bool,
	next sdk.AnteHandler,
) (newCtx sdk.Context, err error) {
	// no need to verify signatures on recheck tx
	if ctx.IsReCheckTx() {
		return next(ctx, tx, simulate)
	}

	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, errorsmod.Wrapf(errortypes.ErrInvalidType, "tx %T doesn't implement authsigning.SigVerifiableTx", tx)
	}

	authSignTx, ok := tx.(authsigning.Tx)
	if !ok {
		return ctx, errorsmod.Wrapf(errortypes.ErrInvalidType, "tx %T doesn't implement the authsigning.Tx interface", tx)
	}

	// stdSigs contains the sequence number, account number, and signatures.
	// When simulating, this would just be a 0-length slice.
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return ctx, err
	}

	signerAddrs, err := sigTx.GetSigners()
	if err != nil {
		return ctx, err
	}

	// EIP712 allows just one signature
	if len(sigs) != 1 {
		return ctx, errorsmod.Wrapf(
			errortypes.ErrTooManySignatures,
			"invalid number of signers (%d);  EIP712 signatures allows just one signature",
			len(sigs),
		)
	}

	// check that signer length and signature length are the same
	if len(sigs) != len(signerAddrs) {
		return ctx, errorsmod.Wrapf(errortypes.ErrorInvalidSigner, "invalid number of signers;  expected: %d, got %d", len(signerAddrs), len(sigs))
	}

	// EIP712 has just one signature, avoid looping here and only read index 0
	i := 0
	sig := sigs[i]
	err = svd.evmKeeper.ValidateSignerAnte(ctx, sig.PubKey, signerAddrs[i])
	if err != nil {
		return ctx, err
	}

	acc, err := authante.GetSignerAcc(ctx, svd.ak, signerAddrs[i])
	if err != nil {
		return ctx, err
	}

	// retrieve pubkey
	pubKey := acc.GetPubKey()
	if !simulate && pubKey == nil {
		return ctx, errorsmod.Wrap(errortypes.ErrInvalidPubKey, "pubkey on account is not set")
	}

	// Check account sequence number.
	if sig.Sequence != acc.GetSequence() {
		return ctx, errorsmod.Wrapf(
			errortypes.ErrWrongSequence,
			"account sequence mismatch, expected %d, got %d", acc.GetSequence(), sig.Sequence,
		)
	}

	// retrieve signer data
	genesis := ctx.BlockHeight() == 0
	chainID := ctx.ChainID()

	var accNum uint64
	if !genesis {
		accNum = acc.GetAccountNumber()
	}

	signerData := authsigning.SignerData{
		ChainID:       chainID,
		AccountNumber: accNum,
		Sequence:      acc.GetSequence(),
	}

	if simulate {
		return next(ctx, tx, simulate)
	}

	if err := VerifySignature(pubKey, signerData, sig.Data, authSignTx); err != nil {
		errMsg := fmt.Errorf("signature verification failed; please verify account number (%d) and chain-id (%s): %w", accNum, chainID, err)
		return ctx, errorsmod.Wrap(errortypes.ErrUnauthorized, errMsg.Error())
	}

	return next(ctx, tx, simulate)
}

// SetPubKeyDecorator sets PubKeys in context for any signer which does not already have pubkey set
// PubKeys must be set in context for all signers before any other sigverify decorators run
// CONTRACT: Tx must implement SigVerifiableTx interface
type SetPubKeyDecorator struct {
	ak        anteinterfaces.AccountKeeper
	evmKeeper anteinterfaces.EVMKeeper
}

func NewSetPubKeyDecorator(ak anteinterfaces.AccountKeeper, ek anteinterfaces.EVMKeeper) SetPubKeyDecorator {
	return SetPubKeyDecorator{
		ak:        ak,
		evmKeeper: ek,
	}
}

func (spkd SetPubKeyDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return ctx, errorsmod.Wrap(errortypes.ErrTxDecode, "invalid tx type")
	}

	pubkeys, err := sigTx.GetPubKeys()
	if err != nil {
		return ctx, err
	}
	signers, err := sigTx.GetSigners()
	if err != nil {
		return ctx, err
	}

	for i, pk := range pubkeys {
		// PublicKey was omitted from slice since it has already been set in context
		if pk == nil {
			if !simulate {
				continue
			}
			pk = simSecp256k1Pubkey
		}

		err := spkd.evmKeeper.ValidateSignerAnte(ctx, pk, signers[i])
		if !simulate && err != nil {
			return ctx, err
		}

		acc := spkd.ak.GetAccount(ctx, signers[i])
		if acc == nil {
			return ctx, errorsmod.Wrapf(errortypes.ErrNotFound, "account of signer %s is not found", common.BytesToAddress(signers[i]).String())
		}
		// account already has pubkey set,no need to reset
		if acc.GetPubKey() != nil {
			continue
		}
		err = acc.SetPubKey(pk)
		if err != nil {
			return ctx, errorsmod.Wrap(errortypes.ErrInvalidPubKey, err.Error())
		}
		spkd.ak.SetAccount(ctx, acc)
	}

	// Also emit the following events, so that txs can be indexed by these
	// indices:
	// - signature (via `tx.signature='<sig_as_base64>'`),
	// - concat(address,"/",sequence) (via `tx.acc_seq='cosmos1abc...def/42'`).
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return ctx, err
	}

	var events sdk.Events
	for i, sig := range sigs {
		events = append(events, sdk.NewEvent(sdk.EventTypeTx,
			sdk.NewAttribute(sdk.AttributeKeyAccountSequence, fmt.Sprintf("%s/%d", signers[i], sig.Sequence)),
		))

		sigBzs, err := signatureDataToBz(sig.Data)
		if err != nil {
			return ctx, err
		}
		for _, sigBz := range sigBzs {
			events = append(events, sdk.NewEvent(sdk.EventTypeTx,
				sdk.NewAttribute(sdk.AttributeKeySignature, base64.StdEncoding.EncodeToString(sigBz)),
			))
		}
	}

	ctx.EventManager().EmitEvents(events)

	return next(ctx, tx, simulate)
}

// VerifySignature verifies a transaction signature contained in SignatureData abstracting over different signing modes
// and single vs multi-signatures.
func VerifySignature(
	pubKey cryptotypes.PubKey,
	signerData authsigning.SignerData,
	sigData signing.SignatureData,
	tx authsigning.Tx,
) error {
	switch data := sigData.(type) {
	case *signing.SingleSignatureData:
		if data.SignMode != signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON {
			return errorsmod.Wrapf(errortypes.ErrNotSupported, "unexpected SignatureData %T: wrong SignMode", sigData)
		}

		// Note: this prevents the user from sending trash data in the signature field
		if len(data.Signature) != 0 {
			return errorsmod.Wrap(errortypes.ErrTooManySignatures, "invalid signature value; EIP712 must have the cosmos transaction signature empty")
		}

		// @contract: this code is reached only when Msg has Web3Tx extension (so this custom Ante handler flow),
		// and the signature is SIGN_MODE_LEGACY_AMINO_JSON which is supported for EIP712 for now

		msgs := tx.GetMsgs()
		if len(msgs) == 0 {
			return errorsmod.Wrap(errortypes.ErrNoSignatures, "tx doesn't contain any msgs to verify signature")
		}

		txBytes := legacytx.StdSignBytes(
			signerData.ChainID,
			signerData.AccountNumber,
			signerData.Sequence,
			tx.GetTimeoutHeight(),
			legacytx.StdFee{
				Amount: tx.GetFee(),
				Gas:    tx.GetGas(),
			},
			msgs, tx.GetMemo(),
		)

		signerChainID, err := types.ParseChainID(signerData.ChainID)
		if err != nil {
			return errorsmod.Wrapf(err, "failed to parse chain-id: %s", signerData.ChainID)
		}

		txWithExtensions, ok := tx.(authante.HasExtensionOptionsTx)
		if !ok {
			return errorsmod.Wrap(errortypes.ErrUnknownExtensionOptions, "tx doesn't contain any extensions")
		}
		opts := txWithExtensions.GetExtensionOptions()
		if len(opts) != 1 {
			return errorsmod.Wrap(errortypes.ErrUnknownExtensionOptions, "tx doesn't contain expected amount of extension options")
		}

		extOpt, ok := opts[0].GetCachedValue().(*types.ExtensionOptionsWeb3Tx)
		if !ok {
			return errorsmod.Wrap(errortypes.ErrUnknownExtensionOptions, "unknown extension option")
		}

		if extOpt.TypedDataChainID != signerChainID.Uint64() {
			return errorsmod.Wrap(errortypes.ErrInvalidChainID, "invalid chain-id")
		}

		if len(extOpt.FeePayer) == 0 {
			return errorsmod.Wrap(errortypes.ErrUnknownExtensionOptions, "no feePayer on ExtensionOptionsWeb3Tx")
		}
		feePayer, err := sdk.AccAddressFromBech32(extOpt.FeePayer)
		if err != nil {
			return errorsmod.Wrap(err, "failed to parse feePayer from ExtensionOptionsWeb3Tx")
		}

		feeDelegation := &eip712.FeeDelegationOptions{
			FeePayer: feePayer,
		}

		typedData, err := eip712.LegacyWrapTxToTypedData(evmCodec, extOpt.TypedDataChainID, msgs[0], txBytes, feeDelegation)
		if err != nil {
			return errorsmod.Wrap(err, "failed to create EIP-712 typed data from tx")
		}

		sigHash, _, err := apitypes.TypedDataAndHash(typedData)
		if err != nil {
			return err
		}

		feePayerSig := extOpt.FeePayerSig
		if len(feePayerSig) != ethcrypto.SignatureLength {
			return errorsmod.Wrap(errortypes.ErrorInvalidSigner, "signature length doesn't match typical [R||S||V] signature 65 bytes")
		}

		// Remove the recovery offset if needed (ie. Metamask eip712 signature)
		if feePayerSig[ethcrypto.RecoveryIDOffset] == 27 || feePayerSig[ethcrypto.RecoveryIDOffset] == 28 {
			feePayerSig[ethcrypto.RecoveryIDOffset] -= 27
		}

		feePayerPubkey, err := secp256k1.RecoverPubkey(sigHash, feePayerSig)
		if err != nil {
			return errorsmod.Wrap(err, "failed to recover delegated fee payer from sig")
		}

		ecPubKey, err := ethcrypto.UnmarshalPubkey(feePayerPubkey)
		if err != nil {
			return errorsmod.Wrap(err, "failed to unmarshal recovered fee payer pubkey")
		}

		compressedPubkey := ethcrypto.CompressPubkey(ecPubKey)
		ethPk := &ethsecp256k1.PubKey{
			Key: compressedPubkey,
		}
		cosmosPk := &cosmossecp256k1.PubKey{Key: compressedPubkey}

		if !pubKey.Equals(ethPk) && !pubKey.Equals(cosmosPk) {
			return errorsmod.Wrapf(errortypes.ErrInvalidPubKey, "feePayer pubkey %s is different from transaction eth pubkey %s and cosmos pubkey%s", pubKey, ethPk, cosmosPk)
		}

		recoveredFeePayerAcc := sdk.AccAddress(ethPk.Address().Bytes())
		if bytes.Equal(pubKey.Bytes(), cosmosPk.Key) {
			recoveredFeePayerAcc = sdk.AccAddress(cosmosPk.Address().Bytes())
		}

		if !recoveredFeePayerAcc.Equals(feePayer) {
			return errorsmod.Wrapf(errortypes.ErrorInvalidSigner, "failed to verify delegated fee payer %s signature", recoveredFeePayerAcc)
		}

		// VerifySignature of ethsecp256k1 accepts 64 byte signature [R||S]
		// WARNING! Under NO CIRCUMSTANCES try to use pubKey.VerifySignature there
		if !secp256k1.VerifySignature(pubKey.Bytes(), sigHash, feePayerSig[:len(feePayerSig)-1]) {
			return errorsmod.Wrap(errortypes.ErrorInvalidSigner, "unable to verify signer signature of EIP712 typed data")
		}

		return nil
	default:
		return errorsmod.Wrapf(errortypes.ErrTooManySignatures, "unexpected SignatureData %T", sigData)
	}
}

// signatureDataToBz converts a SignatureData into raw bytes signature.
// For SingleSignatureData, it returns the signature raw bytes.
// For MultiSignatureData, it returns an array of all individual signatures,
// as well as the aggregated signature.
func signatureDataToBz(data signing.SignatureData) ([][]byte, error) {
	if data == nil {
		return nil, fmt.Errorf("got empty SignatureData")
	}

	switch data := data.(type) {
	case *signing.SingleSignatureData:
		return [][]byte{data.Signature}, nil
	case *signing.MultiSignatureData:
		sigs := [][]byte{}
		var err error

		for _, d := range data.Signatures {
			nestedSigs, err := signatureDataToBz(d)
			if err != nil {
				return nil, err
			}
			sigs = append(sigs, nestedSigs...)
		}

		multisig := cryptotypes.MultiSignature{
			Signatures: sigs,
		}
		aggregatedSig, err := multisig.Marshal()
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, aggregatedSig)

		return sigs, nil
	default:
		return nil, errortypes.ErrInvalidType.Wrapf("unexpected signature data type %T", data)
	}
}
