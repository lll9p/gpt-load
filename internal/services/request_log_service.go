package services

import (
	"context"
	"fmt"
	"gpt-load/internal/models"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultLogFlushBatchSize = 200

// Start starts periodic log flushing and cleanup.
func (s *LogService) Start() {
	go s.runLoop()
}

func (s *LogService) runLoop() {
	defer close(s.done)

	if err := s.flush(s.runCtx); err != nil {
		logrus.WithError(err).Error("Failed to flush request logs batch")
	}
	s.cleanupExpiredLogs(s.runCtx)

	interval := s.flushInterval()
	flushTicker := time.NewTicker(interval)
	defer flushTicker.Stop()
	cleanupTicker := time.NewTicker(2 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-s.runCtx.Done():
			return
		case <-flushTicker.C:
			if newInterval := s.flushInterval(); newInterval != interval {
				flushTicker.Reset(newInterval)
				interval = newInterval
			}
			if err := s.flush(s.runCtx); err != nil {
				logrus.WithError(err).Error("Failed to flush request logs batch")
			}
		case <-cleanupTicker.C:
			s.cleanupExpiredLogs(s.runCtx)
		}
	}
}

func (s *LogService) flushInterval() time.Duration {
	interval := time.Duration(s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes) * time.Minute
	if interval == 0 {
		// Keep draining logs buffered before switching to synchronous writes.
		return time.Minute
	}
	return interval
}

// Stop cancels periodic work and flushes pending logs within the shutdown context.
func (s *LogService) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	s.stopping = true
	s.lifecycleMu.Unlock()
	s.cancel()

	drained := make(chan struct{})
	go func() {
		s.recordsWG.Wait()
		<-s.done
		close(drained)
	}()

	select {
	case <-drained:
		if err := s.flush(ctx); err != nil {
			return fmt.Errorf("failed to flush request logs during shutdown: %w", err)
		}
		logrus.Info("LogService stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for log service shutdown: %w", ctx.Err())
	}
}

// Record writes synchronously or buffers a snapshot of the request log.
func (s *LogService) Record(log *models.RequestLog) error {
	s.lifecycleMu.Lock()
	if s.stopping {
		s.lifecycleMu.Unlock()
		return fmt.Errorf("log service is stopping")
	}
	s.recordsWG.Add(1)
	s.lifecycleMu.Unlock()
	defer s.recordsWG.Done()

	log.ID = uuid.NewString()
	log.Timestamp = time.Now()

	if s.settingsManager.GetSettings().RequestLogWriteIntervalMinutes == 0 {
		return s.writeLogsToDB(context.Background(), []*models.RequestLog{log})
	}

	snapshot := *log
	s.pendingMu.Lock()
	s.pending = append(s.pending, &snapshot)
	s.pendingMu.Unlock()
	return nil
}

func (s *LogService) flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.pendingMu.Lock()
		count := min(len(s.pending), DefaultLogFlushBatchSize)
		if count == 0 {
			s.pendingMu.Unlock()
			return nil
		}
		logs := s.pending[:count]
		s.pendingMu.Unlock()

		if err := s.writeLogsToDB(ctx, logs); err != nil {
			return err
		}

		s.pendingMu.Lock()
		clear(s.pending[:count])
		s.pending = s.pending[count:]
		s.pendingMu.Unlock()
		logrus.Infof("Successfully flushed %d request logs", count)
	}
}

// writeLogsToDB writes a batch of request logs to the database
func (s *LogService) writeLogsToDB(ctx context.Context, logs []*models.RequestLog) error {
	if len(logs) == 0 {
		return nil
	}

	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(logs, len(logs)).Error; err != nil {
			return fmt.Errorf("failed to batch insert request logs: %w", err)
		}

		type keyUsageStat struct {
			Count      int64
			LastUsedAt time.Time
		}

		groupedKeyStats := make(map[uint]map[string]keyUsageStat)
		for _, log := range logs {
			if !log.IsSuccess || log.KeyHash == "" {
				continue
			}

			if _, exists := groupedKeyStats[log.GroupID]; !exists {
				groupedKeyStats[log.GroupID] = make(map[string]keyUsageStat)
			}

			stats := groupedKeyStats[log.GroupID][log.KeyHash]
			stats.Count++
			if stats.LastUsedAt.IsZero() || log.Timestamp.After(stats.LastUsedAt) {
				stats.LastUsedAt = log.Timestamp
			}
			groupedKeyStats[log.GroupID][log.KeyHash] = stats
		}

		if len(groupedKeyStats) > 0 {
			for groupID, keyStats := range groupedKeyStats {
				var requestCountCase strings.Builder
				requestCountCase.WriteString("CASE key_hash ")

				var lastUsedAtCase strings.Builder
				lastUsedAtCase.WriteString("CASE key_hash ")

				requestCountArgs := make([]any, 0, len(keyStats)*2)
				lastUsedAtArgs := make([]any, 0, len(keyStats)*3)
				keyHashes := make([]string, 0, len(keyStats))

				for keyHash, stats := range keyStats {
					requestCountCase.WriteString("WHEN ? THEN request_count + ? ")
					requestCountArgs = append(requestCountArgs, keyHash, stats.Count)

					lastUsedAtCase.WriteString("WHEN ? THEN CASE WHEN last_used_at IS NULL OR last_used_at < ? THEN ? ELSE last_used_at END ")
					lastUsedAtArgs = append(lastUsedAtArgs, keyHash, stats.LastUsedAt, stats.LastUsedAt)

					keyHashes = append(keyHashes, keyHash)
				}

				requestCountCase.WriteString("ELSE request_count END")
				lastUsedAtCase.WriteString("ELSE last_used_at END")

				if err := tx.Model(&models.APIKey{}).
					Where("group_id = ? AND key_hash IN ?", groupID, keyHashes).
					Updates(map[string]any{
						"request_count": gorm.Expr(requestCountCase.String(), requestCountArgs...),
						"last_used_at":  gorm.Expr(lastUsedAtCase.String(), lastUsedAtArgs...),
					}).Error; err != nil {
					return fmt.Errorf("failed to batch update api_key stats: %w", err)
				}
			}
		}

		// Update hourly group statistics.
		hourlyStats := make(map[struct {
			Time    time.Time
			GroupID uint
		}]struct{ Success, Failure int64 })
		for _, log := range logs {
			if log.RequestType == models.RequestTypeRetry {
				continue
			}
			hourlyTime := log.Timestamp.Truncate(time.Hour)
			key := struct {
				Time    time.Time
				GroupID uint
			}{Time: hourlyTime, GroupID: log.GroupID}

			counts := hourlyStats[key]
			if log.IsSuccess {
				counts.Success++
			} else {
				counts.Failure++
			}
			hourlyStats[key] = counts

			if log.ParentGroupID > 0 {
				parentKey := struct {
					Time    time.Time
					GroupID uint
				}{Time: hourlyTime, GroupID: log.ParentGroupID}

				parentCounts := hourlyStats[parentKey]
				if log.IsSuccess {
					parentCounts.Success++
				} else {
					parentCounts.Failure++
				}
				hourlyStats[parentKey] = parentCounts
			}
		}

		if len(hourlyStats) > 0 {
			for key, counts := range hourlyStats {
				err := tx.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "time"}, {Name: "group_id"}},
					DoUpdates: clause.Assignments(map[string]any{
						"success_count": gorm.Expr("group_hourly_stats.success_count + ?", counts.Success),
						"failure_count": gorm.Expr("group_hourly_stats.failure_count + ?", counts.Failure),
						"updated_at":    time.Now(),
					}),
				}).Create(&models.GroupHourlyStat{
					Time:         key.Time,
					GroupID:      key.GroupID,
					SuccessCount: counts.Success,
					FailureCount: counts.Failure,
				}).Error

				if err != nil {
					return fmt.Errorf("failed to upsert group hourly stat: %w", err)
				}
			}
		}

		return nil
	})
}
