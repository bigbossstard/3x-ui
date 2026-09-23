package service

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// ClientHostService manages the optional client -> HostGroup assignment.
// An empty assignment is intentionally the legacy mode: subscriptions contain
// all enabled Host rows belonging to the client's inbound.
type ClientHostService struct{}

func normalizeHostGroupIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *ClientHostService) GetGroupIDs(clientID int) ([]string, error) {
	if clientID <= 0 {
		return []string{}, nil
	}
	var ids []string
	err := database.GetDB().Model(&model.ClientHost{}).
		Where("client_id = ?", clientID).
		Order("group_id ASC").
		Pluck("group_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *ClientHostService) GetGroupIDsByEmail(email string) ([]string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return []string{}, nil
	}
	var rec model.ClientRecord
	if err := database.GetDB().Where("email = ?", email).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	return s.GetGroupIDs(rec.Id)
}

func (s *ClientHostService) ValidateGroupIDs(groupIDs []string) error {
	groupIDs = normalizeHostGroupIDs(groupIDs)
	if len(groupIDs) == 0 {
		return nil
	}
	var existing []string
	if err := database.GetDB().Model(&model.Host{}).
		Where("group_id IN ?", groupIDs).
		Distinct().
		Pluck("group_id", &existing).Error; err != nil {
		return err
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		existingSet[id] = struct{}{}
	}
	for _, id := range groupIDs {
		if _, ok := existingSet[id]; !ok {
			return common.NewError("host group not found:", id)
		}
	}
	return nil
}

func (s *ClientHostService) SetGroupIDs(clientID int, groupIDs []string) error {
	if clientID <= 0 {
		return common.NewError("client id must be positive")
	}
	groupIDs = normalizeHostGroupIDs(groupIDs)
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if len(groupIDs) > 0 {
			var existing []string
			if err := tx.Model(&model.Host{}).
				Where("group_id IN ?", groupIDs).
				Distinct().
				Pluck("group_id", &existing).Error; err != nil {
				return err
			}
			existingSet := make(map[string]struct{}, len(existing))
			for _, id := range existing {
				existingSet[id] = struct{}{}
			}
			for _, id := range groupIDs {
				if _, ok := existingSet[id]; !ok {
					return common.NewError("host group not found:", id)
				}
			}
		}
		if err := tx.Where("client_id = ?", clientID).Delete(&model.ClientHost{}).Error; err != nil {
			return err
		}
		if len(groupIDs) == 0 {
			return nil
		}
		rows := make([]model.ClientHost, 0, len(groupIDs))
		for _, groupID := range groupIDs {
			rows = append(rows, model.ClientHost{ClientId: clientID, GroupId: groupID})
		}
		return tx.Create(&rows).Error
	})
}

func (s *ClientHostService) SetGroupIDsByEmail(email string, groupIDs []string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return common.NewError("client email is required")
	}
	var rec model.ClientRecord
	if err := database.GetDB().Where("email = ?", email).First(&rec).Error; err != nil {
		return err
	}
	return s.SetGroupIDs(rec.Id, groupIDs)
}

func (s *ClientHostService) DeleteForClient(clientID int) error {
	if clientID <= 0 {
		return nil
	}
	return database.GetDB().Where("client_id = ?", clientID).Delete(&model.ClientHost{}).Error
}

func (s *ClientHostService) DeleteForGroup(groupID string) error {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	return database.GetDB().Where("group_id = ?", groupID).Delete(&model.ClientHost{}).Error
}
