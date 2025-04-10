package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/erc20/types"
)

// Migrator is a struct for handling in-place store migrations.
type Migrator struct {
	keeper         Keeper
	legacySubspace types.Subspace
}

// NewMigrator returns a new Migrator.
func NewMigrator(keeper Keeper) Migrator {
	return Migrator{
		keeper: keeper,
	}
}

// Migrate1to2 migrates the store from consensus version 2 to 3
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	return nil
}

// Migrate1to2 migrates the store from consensus version 2 to 3
func (m Migrator) Migrate2to3(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	return nil
}

// Migrate1to2 migrates the store from consensus version 2 to 3
func (m Migrator) Migrate3to4(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	return nil
}
