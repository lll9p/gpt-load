package services

import (
	"fmt"
	"gpt-load/internal/models"
)

const deleteChunkSize = 1000

// KeyDeleteResult holds the result of a delete task.
type KeyDeleteResult struct {
	DeletedCount int `json:"deleted_count"`
	IgnoredCount int `json:"ignored_count"`
}

// StartDeleteTask initiates a new asynchronous key deletion task.
func (s *KeyService) StartDeleteTask(group *models.Group, keysText string) (*TaskStatus, error) {
	keys := s.ParseKeysFromText(keysText)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}
	groupID := group.ID
	return s.startTask(TaskTypeKeyDelete, group.Name, len(keys), func() {
		deleted, ignored, err := s.processAndDeleteKeys(groupID, keys, s.updateProgress)
		s.endTask(KeyDeleteResult{DeletedCount: deleted, IgnoredCount: ignored}, err)
	})
}

func (s *KeyService) processAndDeleteKeys(
	groupID uint,
	keys []string,
	progressCallback func(processed int),
) (deletedCount int, ignoredCount int, err error) {
	var totalDeletedCount int64
	for i := 0; i < len(keys); i += deleteChunkSize {
		end := min(i+deleteChunkSize, len(keys))
		deleted, err := s.KeyProvider.RemoveKeys(groupID, keys[i:end])
		if err != nil {
			return int(totalDeletedCount), len(keys) - int(totalDeletedCount), err
		}
		totalDeletedCount += deleted
		if progressCallback != nil {
			progressCallback(end)
		}
	}
	return int(totalDeletedCount), len(keys) - int(totalDeletedCount), nil
}
