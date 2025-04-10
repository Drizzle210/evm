package keeper

import (
	"bytes"
	"fmt"
	"math/big"

	"cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store/prefix"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	paramstypes "github.com/cosmos/cosmos-sdk/x/params/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/evm/x/vm/core/vm"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/evm/x/vm/wrappers"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// Keeper grants access to the EVM module state and implements the go-ethereum StateDB interface.
type Keeper struct {
	// Protobuf codec
	cdc codec.BinaryCodec
	// Store key required for the EVM Prefix KVStore. It is required by:
	// - storing account's Storage State
	// - storing account's Code
	// - storing transaction Logs
	// - storing Bloom filters by block height. Needed for the Web3 API.
	storeService store.KVStoreService

	// key to access the transient store, which is reset on every block during Commit
	transientKey storetypes.StoreKey

	// the address capable of executing a MsgUpdateParams message. Typically, this should be the x/gov module account.
	authority sdk.AccAddress

	// access to account state
	accountKeeper types.AccountKeeper

	// bankWrapper is used to convert the Cosmos SDK coin used in the EVM to the
	// proper decimal representation.
	bankWrapper types.BankWrapper

	// access historical headers for EVM state transition execution
	stakingKeeper types.StakingKeeper
	// fetch EIP1559 base fee and parameters
	feeMarketWrapper *wrappers.FeeMarketWrapper
	// erc20Keeper interface needed to instantiate erc20 precompiles
	erc20Keeper types.Erc20Keeper

	// Tracer used to collect execution traces from the EVM transaction execution
	tracer string

	// Legacy subspace
	ss paramstypes.Subspace

	// precompiles defines the map of all available precompiled smart contracts.
	// Some of these precompiled contracts might not be active depending on the EVM
	// parameters.
	precompiles map[common.Address]vm.PrecompiledContract
}

// NewKeeper generates new evm module keeper
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService store.KVStoreService,
	transientKey storetypes.StoreKey,
	authority sdk.AccAddress,
	ak types.AccountKeeper,
	bankKeeper types.BankKeeper,
	sk types.StakingKeeper,
	fmk types.FeeMarketKeeper,
	erc20Keeper types.Erc20Keeper,
	tracer string,
	ss paramstypes.Subspace,
) *Keeper {
	// ensure evm module account is set
	if addr := ak.GetModuleAddress(types.ModuleName); addr == nil {
		panic("the EVM module account has not been set")
	}

	// ensure the authority account is correct
	if err := sdk.VerifyAddressFormat(authority); err != nil {
		panic(err)
	}

	bankWrapper := wrappers.NewBankWrapper(bankKeeper)
	feeMarketWrapper := wrappers.NewFeeMarketWrapper(fmk)

	// NOTE: we pass in the parameter space to the CommitStateDB in order to use custom denominations for the EVM operations
	return &Keeper{
		cdc:              cdc,
		authority:        authority,
		accountKeeper:    ak,
		bankWrapper:      bankWrapper,
		stakingKeeper:    sk,
		feeMarketWrapper: feeMarketWrapper,
		storeService:     storeService,
		transientKey:     transientKey,
		tracer:           tracer,
		erc20Keeper:      erc20Keeper,
		ss:               ss,
	}
}

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", types.ModuleName)
}

// ----------------------------------------------------------------------------
// Block Bloom
// Required by Web3 API.
// ----------------------------------------------------------------------------

// EmitBlockBloomEvent emit block bloom events
func (k Keeper) EmitBlockBloomEvent(ctx sdk.Context, bloom ethtypes.Bloom) {
	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeBlockBloom,
			sdk.NewAttribute(types.AttributeKeyEthereumBloom, string(bloom.Bytes())),
		),
	)
}

// GetAuthority returns the x/evm module authority address
func (k Keeper) GetAuthority() sdk.AccAddress {
	return k.authority
}

// GetBlockBloomTransient returns bloom bytes for the current block height
func (k Keeper) GetBlockBloomTransient(ctx sdk.Context) *big.Int {
	store := prefix.NewStore(ctx.TransientStore(k.transientKey), types.KeyPrefixTransientBloom)
	heightBz := sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())) //nolint:gosec // G115 // won't exceed uint64
	bz := store.Get(heightBz)
	if len(bz) == 0 {
		return big.NewInt(0)
	}

	return new(big.Int).SetBytes(bz)
}

// SetBlockBloomTransient sets the given bloom bytes to the transient store. This value is reset on
// every block.
func (k Keeper) SetBlockBloomTransient(ctx sdk.Context, bloom *big.Int) {
	store := prefix.NewStore(ctx.TransientStore(k.transientKey), types.KeyPrefixTransientBloom)
	heightBz := sdk.Uint64ToBigEndian(uint64(ctx.BlockHeight())) //nolint:gosec // G115 // won't exceed uint64
	store.Set(heightBz, bloom.Bytes())
}

// ----------------------------------------------------------------------------
// Tx
// ----------------------------------------------------------------------------

// SetTxIndexTransient set the index of processing transaction
func (k Keeper) SetTxIndexTransient(ctx sdk.Context, index uint64) {
	store := ctx.TransientStore(k.transientKey)
	store.Set(types.KeyPrefixTransientTxIndex, sdk.Uint64ToBigEndian(index))
}

// GetTxIndexTransient returns EVM transaction index on the current block.
func (k Keeper) GetTxIndexTransient(ctx sdk.Context) uint64 {
	store := ctx.TransientStore(k.transientKey)
	return sdk.BigEndianToUint64(store.Get(types.KeyPrefixTransientTxIndex))
}

// ----------------------------------------------------------------------------
// Log
// ----------------------------------------------------------------------------

// GetLogSizeTransient returns EVM log index on the current block.
func (k Keeper) GetLogSizeTransient(ctx sdk.Context) uint64 {
	store := ctx.TransientStore(k.transientKey)
	return sdk.BigEndianToUint64(store.Get(types.KeyPrefixTransientLogSize))
}

// SetLogSizeTransient fetches the current EVM log index from the transient store, increases its
// value by one and then sets the new index back to the transient store.
func (k Keeper) SetLogSizeTransient(ctx sdk.Context, logSize uint64) {
	store := ctx.TransientStore(k.transientKey)
	store.Set(types.KeyPrefixTransientLogSize, sdk.Uint64ToBigEndian(logSize))
}

// ----------------------------------------------------------------------------
// Storage
// ----------------------------------------------------------------------------

// GetAccountStorage return state storage associated with an account
func (k Keeper) GetAccountStorage(ctx sdk.Context, address common.Address) types.Storage {
	storage := types.Storage{}

	k.ForEachStorage(ctx, address, func(key, value common.Hash) bool {
		storage = append(storage, types.NewState(key, value))
		return true
	})

	return storage
}

// ----------------------------------------------------------------------------
// Account
// ----------------------------------------------------------------------------

// Tracer return a default vm.Tracer based on current keeper state
func (k Keeper) Tracer(ctx sdk.Context, msg core.Message, ethCfg *params.ChainConfig) vm.EVMLogger {
	return types.NewTracer(k.tracer, msg, ethCfg, ctx.BlockHeight())
}

// GetAccountWithoutBalance load nonce and codehash without balance,
// more efficient in cases where balance is not needed.
func (k *Keeper) GetAccountWithoutBalance(ctx sdk.Context, addr common.Address) *statedb.Account {
	cosmosAddr := k.GetCosmosAddressMapping(ctx, addr)
	acct := k.accountKeeper.GetAccount(ctx, cosmosAddr)
	if acct == nil {
		return nil
	}

	codeHashBz := k.GetCodeHash(ctx, addr).Bytes()

	return &statedb.Account{
		Nonce:    acct.GetSequence(),
		CodeHash: codeHashBz,
	}
}

// GetAccountOrEmpty returns empty account if not exist.
func (k *Keeper) GetAccountOrEmpty(ctx sdk.Context, addr common.Address) statedb.Account {
	acct := k.GetAccount(ctx, addr)
	if acct != nil {
		return *acct
	}

	// empty account
	return statedb.Account{
		Balance:  new(big.Int),
		CodeHash: types.EmptyCodeHash,
	}
}

// GetNonce returns the sequence number of an account, returns 0 if not exists.
func (k *Keeper) GetNonce(ctx sdk.Context, addr common.Address) uint64 {
	cosmosAddr := k.GetCosmosAddressMapping(ctx, addr)
	acct := k.accountKeeper.GetAccount(ctx, cosmosAddr)
	if acct == nil {
		return 0
	}

	return acct.GetSequence()
}

// GetBalance load account's balance of gas token.
func (k *Keeper) GetBalance(ctx sdk.Context, addr common.Address) *big.Int {
	cosmosAddr := k.GetCosmosAddressMapping(ctx, addr)

	// Get the balance via bank wrapper to convert it to 18 decimals if needed.
	coin := k.bankWrapper.GetBalance(ctx, cosmosAddr, types.GetEVMCoinDenom())

	return coin.Amount.BigInt()
}

// GetBaseFee returns current base fee, return values:
// - `nil`: london hardfork not enabled.
// - `0`: london hardfork enabled but feemarket is not enabled.
// - `n`: both london hardfork and feemarket are enabled.
func (k Keeper) GetBaseFee(ctx sdk.Context) *big.Int {
	ethCfg := types.GetEthChainConfig()
	if !types.IsLondon(ethCfg, ctx.BlockHeight()) {
		return nil
	}
	baseFee := k.feeMarketWrapper.GetBaseFee(ctx)
	if baseFee == nil {
		// return 0 if feemarket not enabled.
		baseFee = big.NewInt(0)
	}
	return baseFee
}

// GetMinGasMultiplier returns the MinGasMultiplier param from the fee market module
func (k Keeper) GetMinGasMultiplier(ctx sdk.Context) math.LegacyDec {
	return k.feeMarketWrapper.GetParams(ctx).MinGasMultiplier
}

// GetMinGasPrice returns the MinGasPrice param from the fee market module
// adapted according to the evm denom decimals
func (k Keeper) GetMinGasPrice(ctx sdk.Context) math.LegacyDec {
	return k.feeMarketWrapper.GetParams(ctx).MinGasPrice
}

// ResetTransientGasUsed reset gas used to prepare for execution of current cosmos tx, called in ante handler.
func (k Keeper) ResetTransientGasUsed(ctx sdk.Context) {
	store := ctx.TransientStore(k.transientKey)
	store.Delete(types.KeyPrefixTransientGasUsed)
}

// GetTransientGasUsed returns the gas used by current cosmos tx.
func (k Keeper) GetTransientGasUsed(ctx sdk.Context) uint64 {
	store := ctx.TransientStore(k.transientKey)
	return sdk.BigEndianToUint64(store.Get(types.KeyPrefixTransientGasUsed))
}

// SetTransientGasUsed sets the gas used by current cosmos tx.
func (k Keeper) SetTransientGasUsed(ctx sdk.Context, gasUsed uint64) {
	store := ctx.TransientStore(k.transientKey)
	bz := sdk.Uint64ToBigEndian(gasUsed)
	store.Set(types.KeyPrefixTransientGasUsed, bz)
}

// AddTransientGasUsed accumulate gas used by each eth msgs included in current cosmos tx.
func (k Keeper) AddTransientGasUsed(ctx sdk.Context, gasUsed uint64) (uint64, error) {
	result := k.GetTransientGasUsed(ctx) + gasUsed
	if result < gasUsed {
		return 0, errorsmod.Wrap(types.ErrGasOverflow, "transient gas used")
	}
	k.SetTransientGasUsed(ctx, result)
	return result, nil
}

// GetEvmAddressMapping returns the account for a given address.
func (k Keeper) GetEvmAddressMapping(ctx sdk.Context, addr sdk.AccAddress) (*common.Address, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, _ := store.Get(types.EvmAddressMappingStoreKey(addr))
	if bz == nil {
		return nil, fmt.Errorf("There is no evm address mapped to %s.", addr.String())
	}
	evmAddress := common.BytesToAddress(bz)
	return &evmAddress, nil
}

// GetCosmosAddressMapping returns the account for a given address.
func (k Keeper) getCosmosAddressMapping(ctx sdk.Context, evmAddress common.Address) (*sdk.AccAddress, error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, _ := store.Get(types.CosmosAddressMappingStoreKey(evmAddress))
	if bz == nil {
		return nil, fmt.Errorf("There is no cosmos address mapped to %s.", evmAddress.String())
	}
	cosmosAddress := sdk.AccAddress(bz)
	return &cosmosAddress, nil
}

func (k Keeper) GetCosmosAddressMapping(ctx sdk.Context, evmAddress common.Address) sdk.AccAddress {
	cosmosAddress := sdk.AccAddress(evmAddress.Bytes())
	cosmosAddr, err := k.getCosmosAddressMapping(ctx, evmAddress)
	if err == nil {
		cosmosAddress = *cosmosAddr
	}
	return cosmosAddress
}

// SetAddressMapping sets the a mapping of an evm address for a given cosmos address.
func (k Keeper) SetAddressMapping(ctx sdk.Context, cosmosAddress sdk.AccAddress, evmAddress common.Address) {
	store := k.storeService.OpenKVStore(ctx)
	evmMappingKey := types.EvmAddressMappingStoreKey(cosmosAddress)
	cosmosMappingKey := types.CosmosAddressMappingStoreKey(evmAddress)
	store.Set(evmMappingKey, evmAddress.Bytes())
	store.Set(cosmosMappingKey, cosmosAddress.Bytes())
}

// migrate balance from address before mapping to after mapping
func (k Keeper) MigrateNonce(ctx sdk.Context, evmAddress common.Address, mappedCosmosAddress sdk.AccAddress) error {
	castAddress := sdk.AccAddress(evmAddress[:])
	castAcc := k.accountKeeper.GetAccount(ctx, castAddress)
	if castAcc == nil {
		return nil
	}
	castNonce := castAcc.GetSequence()
	mappedAcc := k.accountKeeper.GetAccount(ctx, mappedCosmosAddress)
	if mappedAcc == nil {
		return nil
	}
	mappedNonce := mappedAcc.GetSequence()

	if castNonce > mappedNonce {
		err := mappedAcc.SetSequence(castNonce)
		if err != nil {
			return err
		}
		k.accountKeeper.SetAccount(ctx, mappedAcc)
	}
	return nil
}

func (k Keeper) MigrateBalance(ctx sdk.Context, evmAddress common.Address, mappedCosmosAddress sdk.AccAddress) error {
	castAddress := sdk.AccAddress(evmAddress[:])
	castAddrBalances := k.bankWrapper.SpendableCoins(ctx, castAddress)
	if !castAddrBalances.IsZero() {
		if err := k.bankWrapper.SendCoins(ctx, castAddress, mappedCosmosAddress, castAddrBalances); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) ValidateSignerAnte(ctx sdk.Context, pk cryptotypes.PubKey, signer sdk.AccAddress) error {
	accAddressFromPubkey, err := k.GetAccAddressBytesFromPubkey(ctx, pk)
	if err != nil {
		return err
	}
	// we convert signer AccAddress to evm address because in eip712, the signer is bytes() of evm address
	evmAddressFromSigner := common.BytesToAddress(signer)
	signerFromEvmAddressSigner := k.GetCosmosAddressMapping(ctx, evmAddressFromSigner)

	if !bytes.Equal(accAddressFromPubkey, signerFromEvmAddressSigner.Bytes()) {
		return errorsmod.Wrapf(sdkerrors.ErrorInvalidSigner,
			"Signer from pubkey %s does not match signer from GetSigners %s", sdk.AccAddress(accAddressFromPubkey).String(), signerFromEvmAddressSigner.String())
	}
	return nil
}

func (k Keeper) GetAccAddressBytesFromPubkey(ctx sdk.Context, pk cryptotypes.PubKey) ([]byte, error) {
	var addressFromPubkey []byte
	if pk.Type() == ethsecp256k1.KeyType {
		evmAddressFromPubkey, err := types.PubkeyBytesToEVMAddress(pk.Bytes())
		if err != nil {
			return nil, errorsmod.Wrapf(sdkerrors.ErrInvalidPubKey,
				"Pubkey is invalid to convert to evm address: %s", pk.String())
		}
		signerFromPubkey := k.GetCosmosAddressMapping(ctx, *evmAddressFromPubkey)
		addressFromPubkey = signerFromPubkey.Bytes()
		return addressFromPubkey, nil
	}

	addressFromPubkey = pk.Address().Bytes()
	return addressFromPubkey, nil
}

func (k Keeper) SetMappingEvmAddressInner(
	ctx sdk.Context,
	msgSigner string,
	msgPubKey string,
) error {
	_, err := sdk.AccAddressFromBech32(msgSigner)
	if err != nil {
		return errorsmod.Wrap(sdkerrors.ErrorInvalidSigner, fmt.Sprintf("invalid signer address: %s", err.Error()))
	}

	cosmosAddress, err := types.PubkeyToCosmosAddress(msgPubKey)
	if err != nil {
		return err
	}

	// we check if cosmos address has mapped or not
	_, err = k.GetEvmAddressMapping(ctx, cosmosAddress)
	if err == nil {
		// no-op since there's already a mapping
		return nil
	}

	/**
	 * 	here we don't check msgSigner and cosmos address because we want to pass pubkey as argument
	 *	then we mapped cosmos and evm address generated by this pubkey with any signer
	 */
	// already checked at validateBasic, but double check here to make sure
	// if msgSigner != cosmosAddress.String() {
	// 	return errorsmod.Wrap(
	// 		sdkerrors.ErrInvalidPubKey,
	// 		"Signer does not match the given pubkey",
	// 	)
	// }

	evmAddress, err := types.PubkeyToEVMAddress(msgPubKey)
	if err != nil {
		return err
	}

	k.SetAddressMapping(ctx, cosmosAddress, *evmAddress)
	err = k.MigrateNonce(ctx, *evmAddress, cosmosAddress)
	if err != nil {
		return err
	}
	err = k.MigrateBalance(ctx, *evmAddress, cosmosAddress)
	if err != nil {
		return err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeSetMappingEvmAddress,
		sdk.NewAttribute(types.AttributeKeyCosmosAddress, cosmosAddress.String()),
		sdk.NewAttribute(types.AttributeKeyEvmAddress, evmAddress.Hex()),
		sdk.NewAttribute(types.AttributeKeyPubkey, msgPubKey),
	))

	ctx.EventManager().EmitEvent(
		sdk.NewEvent(
			sdk.EventTypeMessage,
			sdk.NewAttribute(sdk.AttributeKeyModule, types.AttributeValueCategory),
			sdk.NewAttribute(sdk.AttributeKeySender, msgSigner),
		),
	)

	return nil
}
