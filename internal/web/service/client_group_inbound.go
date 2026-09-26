package service

import (
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// ClientGroupInboundService manages client-group -> inbound access policies.
type ClientGroupInboundService struct{}

type ClientGroupInboundPolicy struct {
	GroupName   string `json:"groupName"`
	PolicyState string `json:"policyState"`
	InboundIDs  []int  `json:"inboundIds"`
}

func normalizeInboundIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
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

func (s *ClientGroupInboundService) GetInboundIDs(groupName string) ([]int, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return []int{}, nil
	}
	var ids []int
	if err := database.GetDB().Model(&model.ClientGroupInbound{}).
		Where("group_name = ?", groupName).
		Order("inbound_id ASC").
		Pluck("inbound_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *ClientGroupInboundService) GetPolicy(groupName string) (ClientGroupInboundPolicy, error) {
	groupName = strings.TrimSpace(groupName)
	policy := ClientGroupInboundPolicy{
		GroupName:   groupName,
		PolicyState: model.ClientGroupPolicyLegacy,
		InboundIDs:  []int{},
	}
	if groupName == "" {
		return policy, nil
	}
	var group model.ClientGroup
	if err := database.GetDB().Where("name = ?", groupName).First(&group).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return policy, err
		}
		var count int64
		if err := database.GetDB().Model(&model.ClientRecord{}).
			Where("group_name = ?", groupName).Count(&count).Error; err != nil {
			return policy, err
		}
		if count == 0 {
			return policy, gorm.ErrRecordNotFound
		}
		return policy, nil
	}
	policy.PolicyState = group.PolicyState
	if policy.PolicyState == "" {
		policy.PolicyState = model.ClientGroupPolicyLegacy
	}
	ids, err := s.GetInboundIDs(groupName)
	if err != nil {
		return policy, err
	}
	policy.InboundIDs = ids
	return policy, nil
}

func (s *ClientGroupInboundService) RestrictedInboundIDs(groupName string) ([]int, bool, error) {
	policy, err := s.GetPolicy(groupName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []int{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return policy.InboundIDs, policy.PolicyState == model.ClientGroupPolicyRestricted, nil
}

func (s *ClientGroupInboundService) ValidateInboundAttachments(groupName string, inboundIDs []int) error {
	allowedIDs, restricted, err := s.RestrictedInboundIDs(groupName)
	if err != nil {
		return err
	}
	if !restricted {
		return nil
	}
	allowed := make(map[int]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range inboundIDs {
		if _, ok := allowed[id]; !ok {
			return common.NewError("inbound is outside the client's group policy:", id)
		}
	}
	return nil
}

func (s *ClientGroupInboundService) SetInboundIDs(groupName string, inboundIDs []int) (bool, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return false, common.NewError("client group name is required")
	}
	inboundIDs = normalizeInboundIDs(inboundIDs)
	if err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		var groupCount int64
		if err := tx.Model(&model.ClientGroup{}).Where("name = ?", groupName).Count(&groupCount).Error; err != nil {
			return err
		}
		if groupCount == 0 {
			if err := tx.Model(&model.ClientRecord{}).Where("group_name = ?", groupName).Count(&groupCount).Error; err != nil {
				return err
			}
		}
		if groupCount == 0 {
			return common.NewError("client group not found:", groupName)
		}
		if len(inboundIDs) > 0 {
			var existing []int
			if err := tx.Model(&model.Inbound{}).
				Where("id IN ?", inboundIDs).
				Pluck("id", &existing).Error; err != nil {
				return err
			}
			existingSet := make(map[int]struct{}, len(existing))
			for _, id := range existing {
				existingSet[id] = struct{}{}
			}
			for _, id := range inboundIDs {
				if _, ok := existingSet[id]; !ok {
					return common.NewError("inbound not found:", id)
				}
			}
		}
		if err := tx.Where("group_name = ?", groupName).Delete(&model.ClientGroupInbound{}).Error; err != nil {
			return err
		}
		result := tx.Model(&model.ClientGroup{}).Where("name = ?", groupName).
			Update("policy_state", model.ClientGroupPolicyRestricted)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Create(&model.ClientGroup{
				Name:        groupName,
				PolicyState: model.ClientGroupPolicyRestricted,
			}).Error; err != nil {
				return err
			}
		}
		rows := make([]model.ClientGroupInbound, 0, len(inboundIDs))
		for _, id := range inboundIDs {
			rows = append(rows, model.ClientGroupInbound{GroupName: groupName, InboundId: id})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	}); err != nil {
		return false, err
	}
	return (&ClientService{}).EnforceRestrictedGroupInboundPolicy(&InboundService{}, groupName)
}

func (s *ClientGroupInboundService) DeleteForClientGroup(groupName string) error {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return nil
	}
	return database.GetDB().Where("group_name = ?", groupName).Delete(&model.ClientGroupInbound{}).Error
}

func (s *ClientGroupInboundService) DeleteForInbound(inboundID int) error {
	if inboundID <= 0 {
		return nil
	}
	return database.GetDB().Where("inbound_id = ?", inboundID).Delete(&model.ClientGroupInbound{}).Error
}
