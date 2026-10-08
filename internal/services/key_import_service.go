package services

import (
	"fmt"
	"gpt-load/internal/models"
)

// KeyImportResult holds the result of an import task.
type KeyImportResult struct {
	AddedCount   int `json:"added_count"`
	IgnoredCount int `json:"ignored_count"`
}

// StartImportTask initiates a new asynchronous key import task.
func (s *KeyService) StartImportTask(group *models.Group, keysText string) (*TaskStatus, error) {
	keys := s.ParseKeysFromText(keysText)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}
	groupID := group.ID
	return s.startTask(TaskTypeKeyImport, group.Name, len(keys), func() {
		s.runImport(groupID, keys)
	})
}

func (s *KeyService) runImport(groupID uint, keys []string) {
	added, ignored, err := s.processAndCreateKeys(groupID, keys, s.updateProgress)
	if err == nil {
		s.updateProgress(len(keys))
	}
	s.endTask(KeyImportResult{AddedCount: added, IgnoredCount: ignored}, err)
}
