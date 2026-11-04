package keypool

import (
	"container/list"
	"fmt"
	"gpt-load/internal/encryption"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"sync"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type pooledKey struct {
	key    models.APIKey
	active *list.Element
}

type groupPool struct {
	mu      sync.Mutex
	keys    map[uint]*pooledKey
	active  list.List
	removed bool
}

type KeyProvider struct {
	db            *gorm.DB
	encryptionSvc encryption.Service
	mu            sync.RWMutex
	groups        map[uint]*groupPool
}

func NewProvider(db *gorm.DB, encryptionSvc encryption.Service) *KeyProvider {
	return &KeyProvider{db: db, encryptionSvc: encryptionSvc, groups: make(map[uint]*groupPool)}
}

func (p *KeyProvider) pool(groupID uint, create bool) *groupPool {
	p.mu.RLock()
	pool := p.groups[groupID]
	p.mu.RUnlock()
	if pool != nil || !create {
		return pool
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pool = p.groups[groupID]
	if pool == nil {
		pool = &groupPool{keys: make(map[uint]*pooledKey)}
		p.groups[groupID] = pool
	}
	return pool
}

func (pool *groupPool) put(key models.APIKey) {
	entry := pool.keys[key.ID]
	if entry == nil {
		entry = &pooledKey{}
		pool.keys[key.ID] = entry
	}
	entry.key = key
	if key.Status == models.KeyStatusActive && entry.active == nil {
		entry.active = pool.active.PushBack(key.ID)
	} else if key.Status != models.KeyStatusActive && entry.active != nil {
		pool.active.Remove(entry.active)
		entry.active = nil
	}
}

func (pool *groupPool) remove(keyID uint) {
	if entry := pool.keys[keyID]; entry != nil {
		if entry.active != nil {
			pool.active.Remove(entry.active)
		}
		delete(pool.keys, keyID)
	}
}

func (p *KeyProvider) SelectKey(groupID uint, strategy string, excluded map[uint]bool) (*models.APIKey, error) {
	pool := p.pool(groupID, false)
	if pool == nil {
		return nil, app_errors.ErrNoActiveKeys
	}
	pool.mu.Lock()
	if pool.removed || pool.active.Len() == 0 {
		pool.mu.Unlock()
		return nil, app_errors.ErrNoActiveKeys
	}
	element := pool.active.Front()
	for element != nil && excluded[element.Value.(uint)] {
		element = element.Next()
	}
	if element == nil {
		pool.mu.Unlock()
		return nil, app_errors.ErrNoActiveKeys
	}
	key := pool.keys[element.Value.(uint)].key
	if strategy == models.KeySelectionRoundRobin {
		pool.active.MoveToBack(element)
	}
	pool.mu.Unlock()

	value, err := p.encryptionSvc.Decrypt(key.KeyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt key %d: %w", key.ID, err)
	}
	key.KeyValue = value
	return &key, nil
}

func (p *KeyProvider) HasActiveKeys(groupID uint) bool {
	pool := p.pool(groupID, false)
	if pool == nil {
		return false
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return !pool.removed && pool.active.Len() > 0
}

// UpdateStatus persists and publishes changes under the group's lock.
func (p *KeyProvider) UpdateStatus(apiKey *models.APIKey, group *models.Group, isSuccess bool, errorMessage string) {
	if !isSuccess && app_errors.IsUnCounted(errorMessage) {
		return
	}
	pool := p.pool(group.ID, false)
	if pool == nil {
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	entry := pool.keys[apiKey.ID]
	if pool.removed || entry == nil {
		return
	}
	key := entry.key
	if isSuccess {
		if key.FailureCount == 0 && key.Status == models.KeyStatusActive {
			return
		}
		key.FailureCount = 0
		key.Status = models.KeyStatusActive
	} else {
		if key.Status == models.KeyStatusInvalid {
			return
		}
		key.FailureCount++
		threshold := group.EffectiveConfig.BlacklistThreshold
		if threshold > 0 && key.FailureCount >= int64(threshold) {
			key.Status = models.KeyStatusInvalid
		}
	}

	var affected int64
	err := p.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.APIKey{}).Where("id = ? AND group_id = ?", key.ID, group.ID).
			Updates(map[string]any{"failure_count": key.FailureCount, "status": key.Status})
		affected = result.RowsAffected
		return result.Error
	})
	if err != nil {
		logrus.WithError(err).WithField("keyID", key.ID).Error("Failed to persist key status")
		return
	}
	// A request finishing after deletion must never restore the key.
	if affected == 0 {
		pool.remove(key.ID)
		return
	}
	pool.put(key)
	if !isSuccess && key.Status == models.KeyStatusInvalid {
		logrus.WithField("keyID", key.ID).Warn("Key reached blacklist threshold, disabling")
	}
}

// LoadKeysFromDB initializes the pool before request handling starts.
func (p *KeyProvider) LoadKeysFromDB() error {
	groups := make(map[uint]*groupPool)
	var keys []models.APIKey
	err := p.db.FindInBatches(&keys, 10000, func(tx *gorm.DB, batch int) error {
		for _, key := range keys {
			pool := groups[key.GroupID]
			if pool == nil {
				pool = &groupPool{keys: make(map[uint]*pooledKey)}
				groups[key.GroupID] = pool
			}
			pool.put(key)
		}
		return nil
	}).Error
	if err != nil {
		return fmt.Errorf("failed to load keys: %w", err)
	}
	p.mu.Lock()
	p.groups = groups
	p.mu.Unlock()
	return nil
}

func (p *KeyProvider) AddKeys(groupID uint, keys []models.APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	for _, key := range keys {
		if key.GroupID != groupID {
			return fmt.Errorf("key group does not match group %d", groupID)
		}
	}
	pool := p.pool(groupID, true)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.removed {
		return fmt.Errorf("group %d has been removed", groupID)
	}
	err := p.db.Transaction(func(tx *gorm.DB) error {
		var group models.Group
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&group, groupID).Error; err != nil {
			return err
		}
		return tx.Create(&keys).Error
	})
	if err != nil {
		return err
	}
	for _, key := range keys {
		pool.put(key)
	}
	return nil
}

func (p *KeyProvider) keyHashes(values []string) []string {
	hashes := make([]string, 0, len(values))
	for _, value := range values {
		if hash := p.encryptionSvc.Hash(value); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	return hashes
}

func (p *KeyProvider) RemoveKeys(groupID uint, keyValues []string) (int64, error) {
	hashes := p.keyHashes(keyValues)
	if len(hashes) == 0 {
		return 0, nil
	}
	return p.changeKeys(groupID, false, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("key_hash IN ?", hashes)
	})
}

func (p *KeyProvider) RestoreKeys(groupID uint) (int64, error) {
	return p.changeKeys(groupID, true, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("status = ?", models.KeyStatusInvalid)
	})
}

func (p *KeyProvider) RestoreMultipleKeys(groupID uint, keyValues []string) (int64, error) {
	hashes := p.keyHashes(keyValues)
	if len(hashes) == 0 {
		return 0, nil
	}
	return p.changeKeys(groupID, true, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("key_hash IN ? AND status = ?", hashes, models.KeyStatusInvalid)
	})
}

func (p *KeyProvider) RemoveInvalidKeys(groupID uint) (int64, error) {
	return p.changeKeys(groupID, false, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("status = ?", models.KeyStatusInvalid)
	})
}

func (p *KeyProvider) RemoveAllKeys(groupID uint) (int64, error) {
	return p.changeKeys(groupID, false, func(tx *gorm.DB) *gorm.DB { return tx })
}

func (p *KeyProvider) changeKeys(groupID uint, restore bool, filter func(*gorm.DB) *gorm.DB) (int64, error) {
	pool := p.pool(groupID, true)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.removed {
		return 0, fmt.Errorf("group %d has been removed", groupID)
	}
	var keys []models.APIKey
	var count int64
	err := p.db.Transaction(func(tx *gorm.DB) error {
		if err := filter(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id = ?", groupID)).Find(&keys).Error; err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		ids := make([]uint, len(keys))
		for i, key := range keys {
			ids[i] = key.ID
		}
		query := tx.Model(&models.APIKey{}).Where("id IN ? AND group_id = ?", ids, groupID)
		var result *gorm.DB
		if restore {
			result = query.Updates(map[string]any{"status": models.KeyStatusActive, "failure_count": 0})
		} else {
			result = query.Delete(&models.APIKey{})
		}
		count = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return 0, err
	}
	for _, key := range keys {
		if restore {
			key.Status = models.KeyStatusActive
			key.FailureCount = 0
			pool.put(key)
		} else {
			pool.remove(key.ID)
		}
	}
	return count, nil
}

// RemoveGroup drops the in-memory group after its database deletion commits.
func (p *KeyProvider) RemoveGroup(groupID uint) {
	pool := p.pool(groupID, false)
	if pool == nil {
		return
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.removed {
		return
	}
	pool.removed = true
	pool.keys = nil
	pool.active.Init()
	p.mu.Lock()
	delete(p.groups, groupID)
	p.mu.Unlock()
}
