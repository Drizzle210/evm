package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	v5 "github.com/cosmos/evm/x/feemarket/migrations/v5"
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

// Migrate3to4 migrates the store from consensus version 3 to 4
func (m Migrator) Migrate3to4(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Feemarket Module migrate from version 3 to 4")
	return nil
}

// Migrate4to5 migrates the store from consensus version 4 to 5
func (m Migrator) Migrate4to5(ctx sdk.Context) error {
	ctx.Logger().Info("Feemarket Module migrate from version 4 to 5")
	return v5.MigrateStore(ctx, m.keeper.storeKey, m.keeper.cdc)
}
