package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	v8 "github.com/cosmos/evm/x/vm/migrations/v8"
)

// Migrator is a struct for handling in-place store migrations.
type Migrator struct {
	keeper Keeper
}

// NewMigrator returns a new Migrator instance.
func NewMigrator(keeper Keeper) Migrator {
	return Migrator{
		keeper: keeper,
	}
}

// Migrate3to4 migrates the store from consensus version 3 to 4.
func (m Migrator) Migrate3to4(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Evm Module migrate from version 3 to 4")
	return nil
}

// Migrate4to5 migrates the store from consensus version 4 to 5.
func (m Migrator) Migrate4to5(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Evm Module migrate from version 4 to 5")
	return nil
}

// Migrate5to6 migrates the store from consensus version 5 to 6.
func (m Migrator) Migrate5to6(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Evm Module migrate from version 5 to 6")
	return nil
}

// Migrate6to7 migrates the store from consensus version 6 to 7.
func (m Migrator) Migrate6to7(ctx sdk.Context) error {
	// just return nil because we not have anything to migrate here
	ctx.Logger().Info("Evm Module migrate from version 6 to 7")
	return nil
}

// Migrate7to8 migrates the store from consensus version 7 to 8.
func (m Migrator) Migrate7to8(ctx sdk.Context) error {
	ctx.Logger().Info("Evm Module migrate from version 7 to 8")
	v8.MigrateStore(ctx, m.keeper.storeService, m.keeper.cdc)
	return nil
}
