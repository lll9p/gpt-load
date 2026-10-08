package services

import (
	"fmt"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"sync"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// subGroupItem represents a sub-group with its weight and current weight for round-robin
type subGroupItem struct {
	name          string
	subGroupID    uint
	weight        int
	currentWeight int
}

// SelectSubGroup selects an appropriate sub-group for the given aggregate group
func (m *GroupManager) SelectSubGroup(group *models.Group) (*models.Group, error) {
	if group.GroupType != "aggregate" {
		return group, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	selector := m.selectors[group.ID]
	if selector == nil {
		return nil, fmt.Errorf("no valid sub-groups available for aggregate group '%s'", group.Name)
	}

	selectedName := selector.selectNext()
	if selectedName == "" {
		return nil, fmt.Errorf("no sub-groups with active keys for aggregate group '%s'", group.Name)
	}

	selectedGroup, ok := m.groups[selectedName]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}

	logrus.WithFields(logrus.Fields{
		"aggregate_group": group.Name,
		"selected_group":  selectedName,
	}).Debug("Selected sub-group from aggregate")

	return selectedGroup, nil
}

// createSelector creates a new selector for an aggregate group
func (m *GroupManager) createSelector(group *models.Group) *selector {
	if group.GroupType != "aggregate" || len(group.SubGroups) == 0 {
		return nil
	}

	var items []subGroupItem
	for _, sg := range group.SubGroups {
		items = append(items, subGroupItem{
			name:          sg.SubGroupName,
			subGroupID:    sg.SubGroupID,
			weight:        sg.Weight,
			currentWeight: 0,
		})
	}

	return &selector{
		subGroups:   items,
		keyProvider: m.keyProvider,
	}
}

// selector encapsulates the weighted round-robin algorithm for a single aggregate group
type selector struct {
	subGroups   []subGroupItem
	keyProvider *keypool.KeyProvider
	mu          sync.Mutex
}

// selectNext applies smooth weighted round-robin to sub-groups with active keys.
func (s *selector) selectNext() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	totalWeight := 0
	var best *subGroupItem
	for i := range s.subGroups {
		item := &s.subGroups[i]
		if !s.keyProvider.HasActiveKeys(item.subGroupID) {
			continue
		}
		totalWeight += item.weight
		item.currentWeight += item.weight
		if best == nil || item.currentWeight > best.currentWeight {
			best = item
		}
	}
	if best == nil {
		return ""
	}
	best.currentWeight -= totalWeight
	return best.name
}
