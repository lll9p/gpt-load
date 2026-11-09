// Package app manages application construction and lifecycle.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gpt-load/internal/keypool"
	"gpt-load/internal/services"
	"gpt-load/internal/types"
	"gpt-load/internal/version"

	"github.com/sirupsen/logrus"
)

type App struct {
	configManager types.ConfigManager
	db            *sql.DB
	httpServer    *http.Server
	keyService    *services.KeyService
	logService    *services.LogService
	cronChecker   *keypool.CronChecker
}

func (a *App) GracefulShutdownTimeout() time.Duration {
	return time.Duration(a.configManager.GetEffectiveServerConfig().GracefulShutdownTimeout) * time.Second
}

// Start binds HTTP before starting background tasks so bind errors fail startup.
func (a *App) Start() error {
	listener, err := net.Listen("tcp", a.httpServer.Addr)
	if err != nil {
		if closeErr := a.db.Close(); closeErr != nil {
			logrus.WithError(closeErr).Error("Failed to close database after HTTP bind failure")
		}
		return fmt.Errorf("failed to listen for HTTP requests: %w", err)
	}
	a.configManager.DisplayServerConfig()
	a.logService.Start()
	a.cronChecker.Start()
	go func() {
		logrus.Infof("GPT-Load Lite %s listening on %s", version.Version, a.httpServer.Addr)
		if err := a.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("HTTP server failed: %v", err)
		}
	}()
	return nil
}

// Stop closes the database only after all of its users have exited.
func (a *App) Stop(ctx context.Context) error {
	logrus.Info("Shutting down server...")
	drainCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		// Reserve 20% of the remaining budget for the final log flush.
		now := time.Now()
		var cancel context.CancelFunc
		drainCtx, cancel = context.WithDeadline(ctx, now.Add(deadline.Sub(now)*4/5))
		defer cancel()
	}
	var shutdownErrors []error
	if err := a.httpServer.Shutdown(drainCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("HTTP shutdown failed: %w", err))
	}
	if err := a.keyService.Stop(drainCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("key task shutdown failed: %w", err))
	}
	if err := a.cronChecker.Stop(drainCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("cron shutdown failed: %w", err))
	}
	if err := a.logService.Stop(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("log shutdown failed: %w", err))
	}
	if err := errors.Join(shutdownErrors...); err != nil {
		return fmt.Errorf("application shutdown failed; database remains open: %w", err)
	}
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	logrus.Info("Server exited gracefully")
	return nil
}
