package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// EnforceGroupInboundPolicy removes existing attachments outside the group's
// restricted allowlist. It does not auto-attach clients to newly allowed
// inbounds, which may require protocol-specific credential generation.
func (s *ClientService) EnforceGroupInboundPolicy(inboundSvc *InboundService, groupName string, allowedInboundIDs []int) (bool, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return false, nil
	}

	allowed := make(map[int]struct{}, len(allowedInboundIDs))
	for _, id := range allowedInboundIDs {
		allowed[id] = struct{}{}
	}
	var clients []model.ClientRecord
	if err := database.GetDB().Where("group_name = ?", groupName).Order("id ASC").Find(&clients).Error; err != nil {
		return false, err
	}

	clientSvc := ClientService{}
	var needRestart bool
	var failures []error
	for _, client := range clients {
		restart, err := clientSvc.enforceClientInboundAllowlist(inboundSvc, &client, allowed)
		needRestart = needRestart || restart
		if err != nil {
			failures = append(failures, err)
		}
	}
	return needRestart, errors.Join(failures...)
}

func (s *ClientService) EnforceRestrictedGroupInboundPolicy(inboundSvc *InboundService, groupName string) (bool, error) {
	allowedInboundIDs, restricted, err := (&ClientGroupInboundService{}).RestrictedInboundIDs(groupName)
	if err != nil || !restricted {
		return false, err
	}
	return s.EnforceGroupInboundPolicy(inboundSvc, groupName, allowedInboundIDs)
}

func (s *ClientService) EnforceClientGroupInboundPolicy(inboundSvc *InboundService, clientID int, groupName string) (bool, error) {
	allowedInboundIDs, restricted, err := (&ClientGroupInboundService{}).RestrictedInboundIDs(groupName)
	if err != nil || !restricted {
		return false, err
	}
	client, err := s.GetByID(clientID)
	if err != nil {
		return false, err
	}
	allowed := make(map[int]struct{}, len(allowedInboundIDs))
	for _, id := range allowedInboundIDs {
		allowed[id] = struct{}{}
	}
	return s.enforceClientInboundAllowlist(inboundSvc, client, allowed)
}

func (s *ClientService) enforceClientInboundAllowlist(inboundSvc *InboundService, client *model.ClientRecord, allowed map[int]struct{}) (bool, error) {
	inboundIDs, err := s.GetInboundIdsForRecord(client.Id)
	if err != nil {
		return false, fmt.Errorf("load attachments for client %q: %w", client.Email, err)
	}
	var needRestart bool
	var failures []error
	for _, inboundID := range inboundIDs {
		if _, keep := allowed[inboundID]; keep {
			continue
		}
		restart, err := s.DelInboundClientByEmail(inboundSvc, inboundID, client.Email, true, false)
		needRestart = needRestart || restart
		if err != nil && !errors.Is(err, ErrClientNotInInbound) {
			failures = append(failures, fmt.Errorf("detach client %q from inbound %d: %w", client.Email, inboundID, err))
		}
	}
	return needRestart, errors.Join(failures...)
}
