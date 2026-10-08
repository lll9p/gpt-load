package services

import (
	"fmt"
	"gpt-load/internal/models"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ManualValidationResult holds the result of a manual validation task.
type ManualValidationResult struct {
	TotalKeys   int `json:"total_keys"`
	ValidKeys   int `json:"valid_keys"`
	InvalidKeys int `json:"invalid_keys"`
}

// StartValidationTask starts a new manual validation task for a given group.
func (s *KeyService) StartValidationTask(group *models.Group, status string) (*TaskStatus, error) {
	if status != "" && status != models.KeyStatusActive && status != models.KeyStatusInvalid {
		return nil, fmt.Errorf("invalid key status: %s", status)
	}
	groupCopy := *group
	effectiveConfig, err := s.KeyValidator.SettingsManager.GetEffectiveConfig(groupCopy.Config)
	if err != nil {
		return nil, fmt.Errorf("invalid config for group %s: %w", group.Name, err)
	}
	groupCopy.EffectiveConfig = effectiveConfig
	if groupCopy.EffectiveConfig.KeyValidationConcurrency <= 0 {
		return nil, fmt.Errorf("key validation concurrency must be positive")
	}
	taskStatus, err := s.reserveTask(TaskTypeKeyValidation, group.Name, 0)
	if err != nil {
		return nil, err
	}
	var keys []models.APIKey
	query := s.DB.Where("group_id = ?", group.ID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Find(&keys).Error; err != nil {
		taskErr := fmt.Errorf("failed to get keys for group %s with status '%s': %w", group.Name, status, err)
		s.endTask(nil, taskErr)
		s.tasks.wg.Done()
		return nil, taskErr
	}
	if len(keys) == 0 {
		taskErr := fmt.Errorf("no keys to validate in group %s", group.Name)
		s.endTask(nil, taskErr)
		s.tasks.wg.Done()
		return nil, taskErr
	}
	s.tasks.mu.Lock()
	s.tasks.status.Total = len(keys)
	taskStatus.Total = len(keys)
	s.tasks.mu.Unlock()
	go func() {
		defer s.tasks.wg.Done()
		s.runValidation(&groupCopy, keys, status)
	}()
	return taskStatus, nil
}

func (s *KeyService) runValidation(group *models.Group, keys []models.APIKey, status string) {
	logrus.WithFields(logrus.Fields{"group": group.Name, "status": status}).Info("Starting manual validation")
	jobs := make(chan models.APIKey, len(keys))
	results := make(chan bool, len(keys))
	var wg sync.WaitGroup
	for range group.EffectiveConfig.KeyValidationConcurrency {
		wg.Add(1)
		go s.validationWorker(&wg, group, jobs, results)
	}
	for _, key := range keys {
		jobs <- key
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()
	validCount := 0
	processedCount := 0
	lastUpdateTime := time.Now()
	for valid := range results {
		processedCount++
		if valid {
			validCount++
		}
		if time.Since(lastUpdateTime) > time.Second {
			s.updateProgress(processedCount)
			lastUpdateTime = time.Now()
		}
	}
	s.updateProgress(processedCount)
	result := ManualValidationResult{TotalKeys: len(keys), ValidKeys: validCount, InvalidKeys: len(keys) - validCount}
	logrus.Infof("Manual validation finished for group %s: %+v", group.Name, result)
	s.endTask(result, nil)
}

func (s *KeyService) validationWorker(wg *sync.WaitGroup, group *models.Group, jobs <-chan models.APIKey, results chan<- bool) {
	defer wg.Done()
	for key := range jobs {
		value, err := s.EncryptionSvc.Decrypt(key.KeyValue)
		if err != nil {
			logrus.WithError(err).WithField("key_id", key.ID).Error("Manual validation: Failed to decrypt key, marking as invalid")
			results <- false
			continue
		}
		key.KeyValue = value
		valid, _ := s.KeyValidator.ValidateSingleKey(&key, group)
		results <- valid
	}
}
