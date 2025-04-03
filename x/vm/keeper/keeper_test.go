package keeper_test

import (
	"fmt"
	"math/big"
	"reflect"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

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

	type errArgs struct {
		expectPass bool
		contains   string
	}

	tests := []struct {
		name    string
		msg     types.MsgSetMappingEvmAddress
		errArgs errArgs
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
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			_, err := suite.network.App.EVMKeeper.SetMappingEvmAddress(sdk.WrapSDKContext(suite.ctx), &tc.msg)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)

				// validate user coin balance
				cosmosAccAddress := sdk.MustAccAddressFromBech32(signer)
				actualEvmAddress, _ := suite.network.App.EVMKeeper.GetEvmAddressMapping(suite.ctx, cosmosAccAddress)
				suite.Require().Equal(expectedEvmAddress.Hex(), actualEvmAddress.Hex(), "evm addresses dont match")

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

func (suite *KeeperTestSuite) TestMsgDeleteMappingEvmAddress() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount("orai", "oraipub")
	signer := "orai1knzg7jdc49ghnc2pkqg6vks8ccsk6efzfgv6gv"
	pubkey := "AvSl0d9JrHCW4mdEyHvZu076WxLgH0bBVLigUcFm4UjV"

	msg := types.NewMsgSetMappingEvmAddress(signer, pubkey)
	suite.network.App.EVMKeeper.SetMappingEvmAddress(sdk.WrapSDKContext(suite.ctx), &msg)

	type errArgs struct {
		expectPass bool
		contains   string
	}

	tests := []struct {
		name    string
		msg     types.MsgDeleteMappingEvmAddress
		errArgs errArgs
	}{
		{
			"invalid - invalid signer",
			types.NewMsgDeleteMappingEvmAddress(
				"foobar",
			),
			errArgs{
				expectPass: false,
				contains:   "invalid signer address",
			},
		},
		{
			"valid",
			types.NewMsgDeleteMappingEvmAddress(
				signer,
			),
			errArgs{
				expectPass: true,
			},
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			_, err := suite.network.App.EVMKeeper.DeleteMappingEvmAddress(sdk.WrapSDKContext(suite.ctx), &tc.msg)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)

				// validate user coin balance
				cosmosAccAddress := sdk.MustAccAddressFromBech32(signer)
				actualEvmAddress, _ := suite.network.App.EVMKeeper.GetEvmAddressMapping(suite.ctx, cosmosAccAddress)
				suite.Require().Nil(actualEvmAddress)

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
						types.EventTypeDeleteMappingEvmAddress,
						sdk.NewAttribute(types.AttributeKeyCosmosAddress, signer),
					))
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}
