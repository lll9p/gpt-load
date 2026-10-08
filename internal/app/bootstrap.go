package app

import (
	"embed"
	"fmt"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/config"
	"gpt-load/internal/db"
	migrations "gpt-load/internal/db/migrations"
	"gpt-load/internal/encryption"
	"gpt-load/internal/handler"
	"gpt-load/internal/httpclient"
	"gpt-load/internal/i18n"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"gpt-load/internal/proxy"
	"gpt-load/internal/router"
	"gpt-load/internal/services"
	"gpt-load/internal/utils"

	"github.com/sirupsen/logrus"
)

// NewApp initializes all dependencies before any background work starts.
func NewApp(buildFS embed.FS, indexPage []byte) (_ *App, err error) {
	configManager, err := config.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize configuration: %w", err)
	}
	utils.SetupLogger(configManager)
	database, err := db.NewDB(configManager)
	if err != nil {
		return nil, err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}
	defer func() {
		if err != nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				logrus.WithError(closeErr).Error("Failed to close database after initialization failure")
			}
		}
	}()
	encryptionSvc, err := encryption.NewService(configManager.GetEncryptionKey())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize encryption: %w", err)
	}
	if err := i18n.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize i18n: %w", err)
	}
	if err := migrations.HandleLegacyIndexes(database); err != nil {
		return nil, err
	}
	if err := database.AutoMigrate(
		&models.SystemSetting{}, &models.Group{}, &models.GroupSubGroup{},
		&models.APIKey{}, &models.RequestLog{}, &models.GroupHourlyStat{},
	); err != nil {
		return nil, fmt.Errorf("database schema migration failed: %w", err)
	}
	if err := migrations.MigrateDatabase(database); err != nil {
		return nil, fmt.Errorf("database data migration failed: %w", err)
	}
	settings := config.NewSystemSettingsManager(database)
	if err := settings.EnsureSettingsInitialized(configManager.GetAuthConfig()); err != nil {
		return nil, fmt.Errorf("failed to initialize system settings: %w", err)
	}
	if err := settings.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to load settings snapshot: %w", err)
	}
	provider := keypool.NewProvider(database, encryptionSvc)
	if err := provider.LoadKeysFromDB(); err != nil {
		return nil, err
	}
	groups := services.NewGroupManager(database, settings, provider)
	if err := groups.Initialize(); err != nil {
		return nil, fmt.Errorf("failed to load group snapshot: %w", err)
	}
	clients := httpclient.NewHTTPClientManager()
	channels := channel.NewFactory(settings, clients)
	validator := keypool.NewKeyValidator(keypool.KeyValidatorParams{
		DB: database, ChannelFactory: channels, SettingsManager: settings,
		KeypoolProvider: provider, EncryptionSvc: encryptionSvc,
	})
	keys := services.NewKeyService(database, provider, validator, encryptionSvc)
	groupService := services.NewGroupService(database, settings, groups, keys, encryptionSvc)
	logs := services.NewLogService(database, settings, encryptionSvc)
	cron := keypool.NewCronChecker(database, settings, validator, encryptionSvc)
	server := handler.NewServer(handler.NewServerParams{
		DB: database, Config: configManager, SettingsManager: settings,
		GroupManager: groups, GroupService: groupService, KeyService: keys,
		LogService: logs, EncryptionSvc: encryptionSvc,
	})
	proxyServer, err := proxy.NewProxyServer(provider, groups, settings, channels, logs, encryptionSvc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize proxy: %w", err)
	}
	engine, err := router.NewRouter(server, proxyServer, configManager, groups, buildFS, indexPage)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize router assets: %w", err)
	}
	serverConfig := configManager.GetEffectiveServerConfig()
	return &App{
		configManager: configManager, db: sqlDB, keyService: keys,
		logService: logs, cronChecker: cron,
		httpServer: &http.Server{
			Addr: fmt.Sprintf("%s:%d", serverConfig.Host, serverConfig.Port), Handler: engine,
			ReadTimeout:    time.Duration(serverConfig.ReadTimeout) * time.Second,
			WriteTimeout:   time.Duration(serverConfig.WriteTimeout) * time.Second,
			IdleTimeout:    time.Duration(serverConfig.IdleTimeout) * time.Second,
			MaxHeaderBytes: 1 << 20,
		},
	}, nil
}
