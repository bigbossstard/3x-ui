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
		inboundIDs, err := clientSvc.GetInboundIdsForRecord(client.Id)
		if err != nil {
			failures = append(failures, fmt.Errorf("load attachments for client %q: %w", client.Email, err))
			continue
		}
		for _, inboundID := range inboundIDs {
			if _, keep := allowed[inboundID]; keep {
				continue
			}
			restart, err := clientSvc.DelInboundClientByEmail(inboundSvc, inboundID, client.Email, true, false)
			needRestart = needRestart || restart
			if err != nil && !errors.Is(err, ErrClientNotInInbound) {
				failures = append(failures, fmt.Errorf("detach client %q from inbound %d: %w", client.Email, inboundID, err))
			}
		}
	}
	return needRestart, errors.Join(failures...)
}
