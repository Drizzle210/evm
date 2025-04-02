package eip712

import (
	"encoding/json"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/migrations/legacytx"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// ConstructUntypedEIP712Data returns the bytes to sign for a transaction.
func ConstructUntypedEIP712Data(
	chainID string,
	accnum, sequence, timeout uint64,
	fee legacytx.StdFee,
	msgs []sdk.Msg,
	memo string,
) []byte {
	signBytes := legacytx.StdSignBytes(chainID, accnum, sequence, timeout, fee, msgs, memo)
	var inInterface map[string]interface{}
	err := json.Unmarshal(signBytes, &inInterface)
	if err != nil {
		panic(err)
	}

	// remove msgs from the sign doc since we will be adding them as separate fields
	delete(inInterface, "msgs")

	// Add messages as separate fields
	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]
		legacyMsg, ok := msg.(legacytx.LegacyMsg)
		if !ok {
			panic(fmt.Errorf("expected %T when using amino JSON", (*legacytx.LegacyMsg)(nil)))
		}
		msgsBytes := json.RawMessage(legacyMsg.GetSignBytes())
		inInterface[fmt.Sprintf("msg%d", i+1)] = msgsBytes
	}

	bz, err := json.Marshal(inInterface)
	if err != nil {
		panic(err)
	}
	return sdk.MustSortJSON(bz)
}

// WrapTxToTypedData wraps an Amino-encoded Cosmos Tx JSON SignDoc
// bytestream into an EIP712-compatible TypedData request.
func WrapTxToTypedData(
	chainID uint64,
	data []byte,
) (apitypes.TypedData, error) {
	messagePayload, err := createEIP712MessagePayload(data)
	message := messagePayload.message
	if err != nil {
		return apitypes.TypedData{}, err
	}

	types, err := createEIP712Types(messagePayload)
	if err != nil {
		return apitypes.TypedData{}, err
	}

	domain := createEIP712Domain(chainID)

	typedData := apitypes.TypedData{
		Types:       types,
		PrimaryType: txField,
		Domain:      domain,
		Message:     message,
	}

	return typedData, nil
}
