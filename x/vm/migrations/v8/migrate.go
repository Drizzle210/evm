package v8

import (
	"fmt"

	"cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	legacytypes "github.com/cosmos/evm/x/vm/migrations/v8/types"
	"github.com/cosmos/evm/x/vm/types"
)

func MigrateStore(
	ctx sdk.Context,
	storeService store.KVStoreService,
	cdc codec.BinaryCodec,
) error {
	var (
		store        = storeService.OpenKVStore(ctx)
		legacyParams legacytypes.Params
		params       types.Params
	)

	legacyParamsBz, err := store.Get(types.KeyPrefixParams)
	if err != nil {
		ctx.Logger().Error(fmt.Sprintf("evm module migrate error %s", err.Error()))
		return err
	}
	cdc.MustUnmarshal(legacyParamsBz, &legacyParams)

	// migrate new Params
	params.EvmDenom = legacyParams.EvmDenom

	// Migrate old ExtraEIPs from int64 to string.
	params.ExtraEIPs = make([]string, 0, len(legacyParams.ExtraEIPs))
	for _, eip := range legacyParams.ExtraEIPs {
		eipName := fmt.Sprintf("ethereum_%d", eip)
		params.ExtraEIPs = append(params.ExtraEIPs, eipName)
	}

	// migrate chain config
	params.ChainConfig = types.ChainConfig{
		HomesteadBlock:      legacyParams.ChainConfig.HomesteadBlock,
		DAOForkBlock:        legacyParams.ChainConfig.DAOForkBlock,
		DAOForkSupport:      legacyParams.ChainConfig.DAOForkSupport,
		EIP150Block:         legacyParams.ChainConfig.EIP150Block,
		EIP150Hash:          legacyParams.ChainConfig.EIP150Hash,
		EIP155Block:         legacyParams.ChainConfig.EIP155Block,
		EIP158Block:         legacyParams.ChainConfig.EIP158Block,
		ByzantiumBlock:      legacyParams.ChainConfig.ByzantiumBlock,
		ConstantinopleBlock: legacyParams.ChainConfig.ConstantinopleBlock,
		PetersburgBlock:     legacyParams.ChainConfig.PetersburgBlock,
		IstanbulBlock:       legacyParams.ChainConfig.IstanbulBlock,
		MuirGlacierBlock:    legacyParams.ChainConfig.MuirGlacierBlock,
		BerlinBlock:         legacyParams.ChainConfig.BerlinBlock,
		LondonBlock:         legacyParams.ChainConfig.LondonBlock,
		ArrowGlacierBlock:   legacyParams.ChainConfig.ArrowGlacierBlock,
		GrayGlacierBlock:    legacyParams.ChainConfig.GrayGlacierBlock,
		MergeNetsplitBlock:  legacyParams.ChainConfig.MergeNetsplitBlock,
		ShanghaiBlock:       legacyParams.ChainConfig.ShanghaiBlock,
		CancunBlock:         legacyParams.ChainConfig.CancunBlock,
	}

	// migrate AllowUnprotectedTxs
	params.AllowUnprotectedTxs = legacyParams.AllowUnprotectedTxs

	// EVM channel
	// evm_channels is the list of channel identifiers from EVM compatible chains
	// we not have any channel here
	params.EVMChannels = []string{}

	// AccessControl
	params.AccessControl = types.DefaultAccessControl

	// ActiveStaticPrecompiles
	params.ActiveStaticPrecompiles = types.DefaultStaticPrecompiles

	if err := params.Validate(); err != nil {
		return err
	}

	bz := cdc.MustMarshal(&params)

	store.Set(types.KeyPrefixParams, bz)
	return nil
}
