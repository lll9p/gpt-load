package db

import (
	"fmt"
	"gpt-load/internal/types"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewDB(configManager types.ConfigManager) (*gorm.DB, error) {
	dsn := configManager.GetDatabaseConfig().DSN
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_DSN is not configured")
	}

	var newLogger logger.Interface
	if configManager.GetLogConfig().Level == "debug" {
		newLogger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold: time.Second, LogLevel: logger.Info,
			IgnoreRecordNotFoundError: true, Colorful: true,
		})
	}

	var dialector gorm.Dialector
	isPostgres := strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
	if isPostgres {
		dialector = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	} else {
		// Windows drive paths are files, not URL schemes.
		isDrivePath := len(dsn) >= 3 && ((dsn[0] >= 'A' && dsn[0] <= 'Z') || (dsn[0] >= 'a' && dsn[0] <= 'z')) && dsn[1] == ':' && (dsn[2] == '/' || dsn[2] == '\\')
		if (!isDrivePath && regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`).MatchString(dsn)) || strings.Contains(dsn, "?") || strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "tcp(") || strings.Contains(dsn, "unix(") {
			return nil, fmt.Errorf("unsupported DATABASE_DSN: expected a SQLite file path or postgres:// or postgresql:// URL")
		}
		if err := os.MkdirAll(filepath.Dir(dsn), 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
		dialector = sqlite.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{Logger: newLogger, PrepareStmt: true})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	if isPostgres {
		sqlDB.SetMaxIdleConns(50)
		sqlDB.SetMaxOpenConns(500)
		sqlDB.SetConnMaxLifetime(time.Hour)
	} else {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		if err := db.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				return nil, fmt.Errorf("failed to set SQLite busy_timeout: %w; closing database: %v", err, closeErr)
			}
			return nil, fmt.Errorf("failed to set SQLite busy_timeout: %w", err)
		}
	}
	return db, nil
}
