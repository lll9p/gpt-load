package services

import (
	"encoding/json"
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/failover"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"
	"sync"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// GroupManager stores group snapshots.
type GroupManager struct {
	db              *gorm.DB
	settingsManager *config.SystemSettingsManager
	mu              sync.RWMutex
	reloadMu        sync.Mutex
	groups          map[string]*models.Group
}

// NewGroupManager creates an uninitialized group manager.
func NewGroupManager(db *gorm.DB, settingsManager *config.SystemSettingsManager) *GroupManager {
	return &GroupManager{db: db, settingsManager: settingsManager}
}

// Initialize loads the initial group snapshot.
func (gm *GroupManager) Initialize() error {
	return gm.Reload()
}

// Reload replaces groups after a successful load.
func (gm *GroupManager) Reload() error {
	gm.reloadMu.Lock()
	defer gm.reloadMu.Unlock()
	var groups []*models.Group
	if err := gm.db.Find(&groups).Error; err != nil {
		return fmt.Errorf("failed to load groups from db: %w", err)
	}

	groupMap := make(map[string]*models.Group, len(groups))
	for _, group := range groups {
		g := *group
		effectiveConfig, err := gm.settingsManager.GetEffectiveConfig(g.Config)
		if err != nil {
			return fmt.Errorf("failed to load config for group %s: %w", g.Name, err)
		}
		g.EffectiveConfig = effectiveConfig
		g.ProxyKeysMap = utils.StringToSet(g.ProxyKeys, ",")

		matcher, err := failover.ParseStatusCodeMatcher(g.EffectiveConfig.FailoverStatusCodes)
		if err != nil {
			return fmt.Errorf("invalid failover status codes for group %s: %w", g.Name, err)
		}
		g.FailoverStatusCodeMatcher = matcher

		// Parse header rules with error handling
		if len(group.HeaderRules) > 0 {
			if err := json.Unmarshal(group.HeaderRules, &g.HeaderRuleList); err != nil {
				return fmt.Errorf("invalid header rules for group %s: %w", g.Name, err)
			}
		} else {
			g.HeaderRuleList = []models.HeaderRule{}
		}

		g.ModelRedirectMap = make(map[string]string, len(g.ModelRedirectRules))
		for key, value := range g.ModelRedirectRules {
			valueStr, ok := value.(string)
			if !ok {
				return fmt.Errorf("invalid model redirect rule %s for group %s: expected string, got %T", key, g.Name, value)
			}
			g.ModelRedirectMap[key] = valueStr
		}

		groupMap[g.Name] = &g
		logrus.WithFields(logrus.Fields{
			"group_name":                 g.Name,
			"header_rules_count":         len(g.HeaderRuleList),
			"model_redirect_rules_count": len(g.ModelRedirectMap),
			"model_redirect_strict":      g.ModelRedirectStrict,
		}).Debug("Loaded group with effective config")
	}

	gm.mu.Lock()
	gm.groups = groupMap
	gm.mu.Unlock()
	return nil
}

// GetGroupByName retrieves a single group by its name from the cache.
func (gm *GroupManager) GetGroupByName(name string) (*models.Group, error) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	group, ok := gm.groups[name]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return group, nil
}
