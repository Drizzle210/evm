package keeper_test

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"reflect"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	kmultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/evm/utils"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/cosmos/evm/x/vm/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	"github.com/ethereum/go-ethereum/common"
)

func (suite *KeeperTestSuite) TestBaseFee() {
	testCases := []struct {
		name            string
		enableLondonHF  bool
		enableFeemarket bool
		expectBaseFee   *big.Int
	}{
		{"not enable london HF, not enable feemarket", false, false, nil},
		{"enable london HF, not enable feemarket", true, false, big.NewInt(0)},
		{"enable london HF, enable feemarket", true, true, big.NewInt(1000000000)},
		{"not enable london HF, enable feemarket", false, true, nil},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.enableFeemarket = tc.enableFeemarket
			suite.enableLondonHF = tc.enableLondonHF
			suite.SetupTest()

			baseFee := suite.network.App.EVMKeeper.GetBaseFee(suite.network.GetContext())
			suite.Require().Equal(tc.expectBaseFee, baseFee)
		})
	}
	suite.enableFeemarket = false
	suite.enableLondonHF = true
}

func (suite *KeeperTestSuite) TestGetAccountStorage() {
	var ctx sdk.Context
	testCases := []struct {
		name     string
		malleate func() common.Address
	}{
		{
			name:     "Only accounts that are not a contract (no storage)",
			malleate: nil,
		},
		{
			name: "One contract (with storage) and other EOAs",
			malleate: func() common.Address {
				supply := big.NewInt(100)
				contractAddr := suite.DeployTestContract(suite.T(), ctx, suite.keyring.GetAddr(0), supply)
				return contractAddr
			},
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			ctx = suite.network.GetContext()

			var contractAddr common.Address
			if tc.malleate != nil {
				contractAddr = tc.malleate()
			}

			i := 0
			suite.network.App.AccountKeeper.IterateAccounts(ctx, func(account sdk.AccountI) bool {
				acc, ok := account.(*authtypes.BaseAccount)
				if !ok {
					// Ignore e.g. module accounts
					return false
				}

				address, err := utils.Bech32ToHexAddr(acc.Address)
				if err != nil {
					// NOTE: we panic in the test to see any potential problems
					// instead of skipping to the next account
					panic(fmt.Sprintf("failed to convert %s to hex address", err))
				}

				storage := suite.network.App.EVMKeeper.GetAccountStorage(ctx, address)

				if address == contractAddr {
					suite.Require().NotEqual(0, len(storage),
						"expected account %d to have non-zero amount of storage slots, got %d",
						i, len(storage),
					)
				} else {
					suite.Require().Len(storage, 0,
						"expected account %d to have %d storage slots, got %d",
						i, 0, len(storage),
					)
				}

				i++
				return false
			})
		})
	}
}

func (suite *KeeperTestSuite) TestGetAccountOrEmpty() {
	ctx := suite.network.GetContext()
	empty := statedb.Account{
		Balance:  new(big.Int),
		CodeHash: evmtypes.EmptyCodeHash,
	}

	supply := big.NewInt(100)
	contractAddr := suite.DeployTestContract(suite.T(), ctx, suite.keyring.GetAddr(0), supply)

	testCases := []struct {
		name     string
		addr     common.Address
		expEmpty bool
	}{
		{
			"unexisting account - get empty",
			common.Address{},
			true,
		},
		{
			"existing contract account",
			contractAddr,
			false,
		},
	}

	for _, tc := range testCases {
		suite.Run(tc.name, func() {
			res := suite.network.App.EVMKeeper.GetAccountOrEmpty(ctx, tc.addr)
			if tc.expEmpty {
				suite.Require().Equal(empty, res)
			} else {
				suite.Require().NotEqual(empty, res)
			}
		})
	}
}

func (suite *KeeperTestSuite) EventsContains(events sdk.Events, expectedEvent sdk.Event) {
	foundMatch := false
	for _, event := range events {
		if event.Type == expectedEvent.Type {
			if reflect.DeepEqual(attrsToMap(expectedEvent.Attributes), attrsToMap(event.Attributes)) {
				foundMatch = true
			}
		}
	}

	suite.Truef(foundMatch, "event of type %s not found or did not match", expectedEvent.Type)
}

func attrsToMap(attrs []abci.EventAttribute) []sdk.Attribute {
	out := []sdk.Attribute{}

	for _, attr := range attrs {
		out = append(out, sdk.NewAttribute(string(attr.Key), string(attr.Value)))
	}

	return out
}

// GetEvents returns emitted events on the sdk context
func (suite *KeeperTestSuite) GetEvents() sdk.Events {
	return suite.ctx.EventManager().Events()
}

func (suite *KeeperTestSuite) TestMsgSetMappingEvmAddress() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("orai", "oraipub")
	signer := "orai1knzg7jdc49ghnc2pkqg6vks8ccsk6efzfgv6gv"
	pubkey := "AvSl0d9JrHCW4mdEyHvZu076WxLgH0bBVLigUcFm4UjV"
	expectedEvmAddress, _ := types.PubkeyToEVMAddress(pubkey)

	castAddress := sdk.AccAddress(expectedEvmAddress[:])
	acc := suite.network.App.AccountKeeper.NewAccountWithAddress(suite.ctx, castAddress)
	acc.SetSequence(0)
	suite.network.App.AccountKeeper.SetAccount(suite.ctx, acc)

	// fixture for migrate nonce
	signerAddress, _ := sdk.AccAddressFromBech32(signer)
	signerAcc := suite.network.App.AccountKeeper.NewAccountWithAddress(suite.ctx, signerAddress)
	signerAcc.SetSequence(1)
	suite.network.App.AccountKeeper.SetAccount(suite.ctx, signerAcc)

	// fixture for migrate balance
	mintCoins := sdk.NewCoins(sdk.NewCoin(suite.EvmDenom(), sdkmath.NewInt(50)))
	suite.network.App.BankKeeper.MintCoins(suite.ctx, types.ModuleName, mintCoins)
	sentCoins := sdk.NewCoins(sdk.NewCoin(suite.EvmDenom(), sdkmath.NewInt(5)))
	moduleAcc := suite.network.App.AccountKeeper.GetModuleAccount(suite.ctx, types.ModuleName)
	suite.network.App.BankKeeper.SendCoins(suite.ctx, moduleAcc.GetAddress(), castAddress, sentCoins)
	suite.network.App.BankKeeper.SendCoins(suite.ctx, moduleAcc.GetAddress(), signerAddress, sentCoins)

	type errArgs struct {
		expectPass bool
		contains   string
	}

	tests := []struct {
		name     string
		msg      types.MsgSetMappingEvmAddress
		errArgs  errArgs
		malleate func()
	}{
		{
			"valid",
			types.NewMsgSetMappingEvmAddress(
				signer,
				pubkey,
			),
			errArgs{
				expectPass: true,
			},
			func() {},
		},
		{
			"invalid - invalid signer",
			types.NewMsgSetMappingEvmAddress(
				"foobar",
				pubkey,
			),
			errArgs{
				expectPass: false,
				contains:   "invalid signer address",
			},
			func() {},
		},
		{
			"invalid - invalid pubkey",
			types.NewMsgSetMappingEvmAddress(
				signer,
				"Avalv/HkKw5oBST0LP6Hb8v+kLX22/V97IndXM2O6GeZ",
			),
			errArgs{
				expectPass: false,
				contains:   "Signer does not match the given pubkey",
			},
			func() {},
		},
		{
			"valid with migrate nonce",
			types.NewMsgSetMappingEvmAddress(
				signer,
				pubkey,
			),
			errArgs{
				expectPass: true,
			},
			func() {
				acc.SetSequence(10)
				suite.network.App.AccountKeeper.SetAccount(suite.ctx, acc)
			},
		},
		{
			"valid with migrate balance",
			types.NewMsgSetMappingEvmAddress(
				signer,
				pubkey,
			),
			errArgs{
				expectPass: true,
			},
			func() {
				sentCoins := sdk.NewCoins(sdk.NewCoin(suite.EvmDenom(), sdkmath.NewInt(20)))
				suite.network.App.BankKeeper.SendCoins(suite.ctx, moduleAcc.GetAddress(), castAddress, sentCoins)
			},
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			tc.malleate()
			_, err := suite.network.App.EVMKeeper.SetMappingEvmAddress(sdk.WrapSDKContext(suite.ctx), &tc.msg)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)

				// validate user coin balance
				cosmosAccAddress := sdk.MustAccAddressFromBech32(signer)
				actualEvmAddress, _ := suite.network.App.EVMKeeper.GetEvmAddressMapping(suite.ctx, cosmosAccAddress)
				suite.Require().Equal(expectedEvmAddress.Hex(), actualEvmAddress.Hex(), "evm addresses dont match")

				// validate migrate nonce
				acc := suite.network.App.AccountKeeper.GetAccount(suite.ctx, castAddress)
				signerAcc := suite.network.App.AccountKeeper.GetAccount(suite.ctx, signerAddress)
				nonce := acc.GetSequence()
				signerNonce := signerAcc.GetSequence()
				suite.Require().GreaterOrEqual(signerNonce, nonce)

				// validate migrate balance
				castBalance := suite.network.App.BankKeeper.GetBalance(suite.ctx, castAddress, suite.EvmDenom())
				signerBalance := suite.network.App.BankKeeper.GetBalance(suite.ctx, signerAddress, suite.EvmDenom())
				fmt.Println("signer balance: ", signerBalance)
				suite.Require().GreaterOrEqual(signerBalance.Amount.Int64(), castBalance.Amount.Int64())
				if signerBalance.Amount.GT(castBalance.Amount) {
					suite.Require().Equal(castBalance.Amount.Int64(), int64(0))
				}

				// msg server event
				suite.EventsContains(suite.GetEvents(),
					sdk.NewEvent(
						sdk.EventTypeMessage,
						sdk.NewAttribute(sdk.AttributeKeyModule, types.AttributeValueCategory),
						sdk.NewAttribute(sdk.AttributeKeySender, signer),
					))

				// keeper event
				suite.EventsContains(suite.GetEvents(),
					sdk.NewEvent(
						types.EventTypeSetMappingEvmAddress,
						sdk.NewAttribute(types.AttributeKeyCosmosAddress, signer),
						sdk.NewAttribute(types.AttributeKeyEvmAddress, actualEvmAddress.Hex()),
						sdk.NewAttribute(types.AttributeKeyPubkey, pubkey),
					))
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}

func (suite *KeeperTestSuite) TestGetAccAddressBytesFromPubkey() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("orai", "oraipub")
	pubkeyString := "Ah4NweWyFaVG5xcOwY5I7Tm4mmfPgLtS+Qn3jvXLX0VP"
	compressedPubkeyBytes, _ := base64.StdEncoding.DecodeString(pubkeyString)
	ethPubkey := ethsecp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosPubkey := secp256k1.PubKey{Key: compressedPubkeyBytes}
	multisigPubkey := kmultisig.NewLegacyAminoPubKey(1, []cryptotypes.PubKey{&cosmosPubkey})
	cosmosAddress := sdk.AccAddress(cosmosPubkey.Address().Bytes())
	multisigAddress := sdk.AccAddress(multisigPubkey.Address().Bytes())
	cosmosAddressFromEvm := sdk.AccAddress(ethPubkey.Address().Bytes())
	evmAddress := common.BytesToAddress(ethPubkey.Address().Bytes())

	type errArgs struct {
		expectPass bool
		contains   string
	}

	tests := []struct {
		name               string
		errArgs            errArgs
		pubkey             cryptotypes.PubKey
		pubkeyType         string
		expectedAccAddress string
		malleate           func()
	}{
		{
			"secp256k1 pubkey valid",
			errArgs{
				expectPass: true,
			},
			&cosmosPubkey,
			"secp256k1",
			cosmosAddress.String(),
			func() {},
		},
		{
			"multisign pubkey valid",
			errArgs{
				expectPass: true,
			},
			multisigPubkey,
			"PubKeyMultisigThreshold",
			multisigAddress.String(),
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with no address mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			"eth_secp256k1",
			cosmosAddressFromEvm.String(),
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with addess mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			"eth_secp256k1",
			cosmosAddress.String(),
			func() {
				suite.network.App.EVMKeeper.SetAddressMapping(suite.ctx, cosmosAddress, evmAddress)
			},
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			tc.malleate()
			accAddress, err := suite.network.App.EVMKeeper.GetAccAddressBytesFromPubkey(suite.ctx, tc.pubkey)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)
				suite.Require().Equal(tc.expectedAccAddress, sdk.AccAddress(accAddress).String())
				suite.Require().Equal(tc.pubkeyType, tc.pubkey.Type())
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}

func (suite *KeeperTestSuite) TestValidateSignerEIP712Ante() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("orai", "oraipub")
	pubkeyString := "Ah4NweWyFaVG5xcOwY5I7Tm4mmfPgLtS+Qn3jvXLX0VP"
	compressedPubkeyBytes, _ := base64.StdEncoding.DecodeString(pubkeyString)
	ethPubkey := ethsecp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosPubkey := secp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosAddress := sdk.AccAddress(cosmosPubkey.Address().Bytes())
	cosmosAddressFromEvm := sdk.AccAddress(ethPubkey.Address().Bytes())
	evmAddress := common.BytesToAddress(ethPubkey.Address().Bytes())

	type errArgs struct {
		expectPass bool
		contains   string
	}

	tests := []struct {
		name     string
		errArgs  errArgs
		pubkey   cryptotypes.PubKey
		signer   sdk.AccAddress
		malleate func()
	}{
		{
			"secp256k1 pubkey valid",
			errArgs{
				expectPass: true,
			},
			&cosmosPubkey,
			cosmosAddress,
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with no address mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			cosmosAddressFromEvm,
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with addess mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			cosmosAddress,
			func() {
				suite.network.App.EVMKeeper.SetAddressMapping(suite.ctx, cosmosAddress, evmAddress)
			},
		},
		{
			"secp256k1 pubkey invalid signer don't match",
			errArgs{
				expectPass: false,
				contains:   "does not match signer",
			},
			&cosmosPubkey,
			cosmosAddressFromEvm,
			func() {
			},
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			tc.malleate()
			err := suite.network.App.EVMKeeper.ValidateSignerAnte(suite.ctx, tc.pubkey, tc.signer)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}
