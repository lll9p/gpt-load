package services

import (
	"context"
	"gpt-load/internal/models"
	"time"

	"github.com/sirupsen/logrus"
)

// cleanupExpiredLogs removes request logs older than the configured retention period.
func (s *LogService) cleanupExpiredLogs(ctx context.Context) {
	retentionDays := s.settingsManager.GetSettings().RequestLogRetentionDays
	if retentionDays <= 0 {
		logrus.Debug("Log retention is disabled (retention_days <= 0)")
		return
	}

	cutoffTime := time.Now().AddDate(0, 0, -retentionDays).UTC()
	result := s.DB.WithContext(ctx).Where("timestamp < ?", cutoffTime).Delete(&models.RequestLog{})
	if result.Error != nil {
		logrus.WithError(result.Error).Error("Failed to cleanup expired request logs")
		return
	}

	if result.RowsAffected > 0 {
		logrus.WithFields(logrus.Fields{
			"deleted_count":  result.RowsAffected,
			"cutoff_time":    cutoffTime.Format(time.RFC3339),
			"retention_days": retentionDays,
		}).Info("Successfully cleaned up expired request logs")
	} else {
		logrus.Debug("No expired request logs found to cleanup")
	}
}
