package db

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// RetireAggregateGroups removes only identifiable aggregate data and obsolete schema.
func RetireAggregateGroups(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("unsupported database type: %s", db.Dialector.Name())
	}

	return db.Transaction(func(tx *gorm.DB) error {
		// Read metadata with errors propagated rather than treating failed probes as absence.
		tables, err := tx.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("inspect aggregate retirement tables: %w", err)
		}
		columns := make(map[string]map[string]bool)
		for _, table := range []string{"groups", "request_logs", "group_sub_groups", "group_hourly_stats"} {
			columns[table] = make(map[string]bool)
			for _, existing := range tables {
				if existing != table {
					continue
				}
				types, err := tx.Migrator().ColumnTypes(table)
				if err != nil {
					return fmt.Errorf("inspect %s columns: %w", table, err)
				}
				for _, column := range types {
					columns[table][column.Name()] = true
				}
			}
		}

		var parentQueries []string
		if columns["groups"]["group_type"] {
			var groupIDs []uint
			if err := tx.Raw("SELECT DISTINCT groups.id FROM groups JOIN api_keys ON api_keys.group_id = groups.id WHERE groups.group_type = 'aggregate' ORDER BY groups.id").Scan(&groupIDs).Error; err != nil {
				return fmt.Errorf("check aggregate group API keys: %w", err)
			}
			if len(groupIDs) > 0 {
				return fmt.Errorf("cannot retire aggregate groups: groups %v own API keys; export and move these keys before restarting", groupIDs)
			}
			parentQueries = append(parentQueries, "SELECT id FROM groups WHERE group_type = 'aggregate'")
		}
		if columns["request_logs"]["parent_group_id"] {
			parentQueries = append(parentQueries, "SELECT parent_group_id FROM request_logs WHERE parent_group_id > 0")
		}
		if columns["group_sub_groups"]["group_id"] {
			parentQueries = append(parentQueries, "SELECT group_id FROM group_sub_groups WHERE group_id > 0")
		}

		// Consume all surviving parent evidence before deleting any of its sources.
		if len(parentQueries) > 0 && columns["group_hourly_stats"]["group_id"] {
			if err := tx.Exec("DELETE FROM group_hourly_stats WHERE group_id IN (" + strings.Join(parentQueries, " UNION ") + ")").Error; err != nil {
				return fmt.Errorf("delete aggregate hourly statistics: %w", err)
			}
		}
		if columns["groups"]["group_type"] {
			if err := tx.Exec("DELETE FROM groups WHERE group_type = 'aggregate'").Error; err != nil {
				return fmt.Errorf("delete aggregate groups: %w", err)
			}
		}

		// All identifiers below are fixed. Native SQLite DROP COLUMN avoids the
		// driver's DropColumn table rebuild, which can affect api_keys foreign keys.
		for _, statement := range []string{
			"DROP TABLE IF EXISTS group_sub_groups",
			"DROP INDEX IF EXISTS idx_request_logs_parent_group_id",
			"DROP INDEX IF EXISTS idx_request_logs_parent_group_name",
		} {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("retire aggregate schema (%s): %w", statement, err)
			}
		}
		for _, column := range []struct{ table, name, statement string }{
			{"request_logs", "parent_group_id", "ALTER TABLE request_logs DROP COLUMN parent_group_id"},
			{"request_logs", "parent_group_name", "ALTER TABLE request_logs DROP COLUMN parent_group_name"},
			{"groups", "group_type", "ALTER TABLE groups DROP COLUMN group_type"},
		} {
			if columns[column.table][column.name] {
				if err := tx.Exec(column.statement).Error; err != nil {
					return fmt.Errorf("retire aggregate schema (%s): %w", column.statement, err)
				}
			}
		}
		return nil
	})
}
