package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Migrator is a struct for handling in-place store migrations.
type Migrator struct {
	keeper Keeper
}

// NewMigrator returns a new Migrator.
func NewMigrator(keeper Keeper) Migrator {
	return Migrator{
		keeper: keeper,
	}
}

// Migrate1to2 migrates the store from consensus version 1 to 2
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Erc20 Module migrate from version 1 to 2")
	return nil
}

// Migrate2to3 migrates the store from consensus version 2 to 3
func (m Migrator) Migrate2to3(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Erc20 Module migrate from version 2 to 3")
	return nil
}

// Migrate3to4 migrates the store from consensus version 3 to 4
func (m Migrator) Migrate3to4(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Erc20 Module migrate from version 3 to 4")
	return nil
}
