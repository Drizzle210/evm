// Copyright Tharsis Labs Ltd.(Evmos)
// SPDX-License-Identifier:ENCL-1.0(https://github.com/evmos/evmos/blob/main/LICENSE)
package v5

import (
	"fmt"

	"cosmossdk.io/core/store"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	typesV4 "github.com/cosmos/evm/x/feemarket/migrations/v5/types"
	"github.com/cosmos/evm/x/feemarket/types"
)

// MigrateStore migrates the x/feemarket module state from the consensus version 4 to
// version 5. Specifically, it converts the base fee from Int to LegacyDec.
func MigrateStore(
	ctx sdk.Context,
	storeService store.KVStoreService,
	cdc codec.BinaryCodec,
) error {
	var (
		store    = storeService.OpenKVStore(ctx)
		paramsV4 typesV4.Params
		params   types.Params
	)

	paramsV4Bz, err := store.Get(types.ParamsKey)
	if err != nil {
		ctx.Logger().Error(fmt.Sprintf("migrate error Feemarket module get param store %s", err.Error()))
		return err
	}
	cdc.MustUnmarshal(paramsV4Bz, &paramsV4)

	params.NoBaseFee = paramsV4.NoBaseFee
	params.BaseFeeChangeDenominator = paramsV4.BaseFeeChangeDenominator
	params.ElasticityMultiplier = paramsV4.ElasticityMultiplier
	params.EnableHeight = paramsV4.EnableHeight
	params.BaseFee = math.LegacyNewDecFromInt(paramsV4.BaseFee) // convert to dec
	params.MinGasPrice = paramsV4.MinGasPrice
	params.MinGasMultiplier = paramsV4.MinGasMultiplier

	if err := params.Validate(); err != nil {
		ctx.Logger().Error(fmt.Sprintf("migrate error Feemarket module validate pamrams %s", err.Error()))
		return err
	}

	bz, err := cdc.Marshal(&params)
	if err != nil {
		ctx.Logger().Error(fmt.Sprintf("migrate error Feemarket module marshal pamrams %s", err.Error()))
		return err
	}

	store.Set(types.ParamsKey, bz)

	return nil
}
