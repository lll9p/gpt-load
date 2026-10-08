package db

import (
	"fmt"

	"gorm.io/gorm"
)

func MigrateDatabase(db *gorm.DB) error {
	if err := V1_0_22_DropRetriesColumn(db); err != nil {
		return err
	}
	return V1_1_0_AddKeyHashColumn(db)
}

// HandleLegacyIndexes removes indexes left by previous versions.
func HandleLegacyIndexes(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
		for _, name := range []string{"idx_group_key", "idx_api_keys_group_id_key_value"} {
			if err := db.Exec("DROP INDEX IF EXISTS " + name).Error; err != nil {
				return fmt.Errorf("failed to drop legacy index %s: %w", name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported database type: %s", db.Dialector.Name())
	}
}
