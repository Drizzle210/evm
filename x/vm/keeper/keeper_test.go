package keeper_test

import (
	"encoding/base64"
	"fmt"
	"math/big"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	cosmossecp256k1 "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/evm/utils"
	"github.com/cosmos/evm/x/vm/statedb"
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

func (suite *KeeperTestSuite) TestGetAccAddressBytesFromPubkey() {
	pubkeyString := "Ah4NweWyFaVG5xcOwY5I7Tm4mmfPgLtS+Qn3jvXLX0VP"
	compressedPubkeyBytes, _ := base64.StdEncoding.DecodeString(pubkeyString)
	ethPubkey := ethsecp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosPubkey := cosmossecp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosAddress := sdk.AccAddress(cosmosPubkey.Address().Bytes())
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
		expectedAccAddress string
		malleate           func()
	}{
		{
			"secp256k1 pubkey valid",
			errArgs{
				expectPass: true,
			},
			&cosmosPubkey,
			cosmosAddress.String(),
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with no address mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			cosmosAddressFromEvm.String(),
			func() {},
		},
		{
			"eth_secp256k1 pubkey valid with addess mapping",
			errArgs{
				expectPass: true,
			},
			&ethPubkey,
			cosmosAddress.String(),
			func() {
				suite.network.App.EVMKeeper.SetAddressMapping(suite.network.GetContext(), cosmosAddress, evmAddress)
			},
		},
	}

	for _, tc := range tests {
		suite.Run(tc.name, func() {
			suite.SetupTest()
			tc.malleate()
			accAddress, err := suite.network.App.EVMKeeper.GetAccAddressBytesFromPubkey(suite.network.GetContext(), tc.pubkey)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)
				suite.Require().Equal(tc.expectedAccAddress, sdk.AccAddress(accAddress).String())
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}

func (suite *KeeperTestSuite) TestValidateSignerEIP712Ante() {
	pubkeyString := "Ah4NweWyFaVG5xcOwY5I7Tm4mmfPgLtS+Qn3jvXLX0VP"
	compressedPubkeyBytes, _ := base64.StdEncoding.DecodeString(pubkeyString)
	ethPubkey := ethsecp256k1.PubKey{Key: compressedPubkeyBytes}
	cosmosPubkey := cosmossecp256k1.PubKey{Key: compressedPubkeyBytes}
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
				suite.network.App.EVMKeeper.SetAddressMapping(suite.network.GetContext(), cosmosAddress, evmAddress)
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
			err := suite.network.App.EVMKeeper.ValidateSignerAnte(suite.network.GetContext(), tc.pubkey, tc.signer)

			if tc.errArgs.expectPass {
				suite.Require().NoError(err)
			} else {
				suite.Require().Error(err)
				suite.Require().Contains(err.Error(), tc.errArgs.contains)
			}
		})
	}
}
