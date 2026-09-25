package service

import (
	"sort"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// EffectiveClientAccess is the resolved access policy for one client.
// Individual HostGroup assignments intentionally take precedence over group
// HostGroup policies, preserving the client-host compatibility contract.
type EffectiveClientAccess struct {
	ClientID              int
	GroupNames            []string
	InboundIDs            []int
	HostGroupIDs          []string
	HasRestrictedInbound  bool
	UsesLegacyInbounds    bool
	IndividualHostGroups  bool
}

// ResolveClientAccess combines every membership using union semantics.
// Restricted inbound policies suppress legacy "all inbounds" fallback.
func ResolveClientAccess(clientID int) (EffectiveClientAccess, error) {
	access := EffectiveClientAccess{ClientID: clientID}
	if clientID <= 0 {
		return access, nil
	}
	db := database.GetDB()

	var memberships []model.ClientGroupMembership
	if err := db.Where("client_id = ?", clientID).
		Order("group_name ASC").Find(&memberships).Error; err != nil {
		return access, err
	}
	if len(memberships) == 0 {
		var client model.ClientRecord
		if err := db.First(&client, clientID).Error; err != nil {
			return access, err
		}
		if strings.TrimSpace(client.Group) != "" {
			memberships = []model.ClientGroupMembership{{ClientId: clientID, GroupName: strings.TrimSpace(client.Group)}}
		}
	}

	groupSeen := make(map[string]struct{}, len(memberships))
	inboundSet := map[int]struct{}{}
	hostSet := map[string]struct{}{}
	for _, membership := range memberships {
		group := strings.TrimSpace(membership.GroupName)
		if group == "" {
			continue
		}
		if _, seen := groupSeen[group]; seen {
			continue
		}
		groupSeen[group] = struct{}{}
		access.GroupNames = append(access.GroupNames, group)

		var state string
		if err := db.Table("client_groups").Where("name = ?", group).
			Pluck("policy_state", &state).Error; err != nil {
			return access, err
		}
		if state == model.ClientGroupPolicyRestricted {
			access.HasRestrictedInbound = true
			var inboundIDs []int
			if err := db.Model(&model.ClientGroupInbound{}).
				Where("group_name = ?", group).Pluck("inbound_id", &inboundIDs).Error; err != nil {
				return access, err
			}
			for _, id := range inboundIDs {
				inboundSet[id] = struct{}{}
			}
		}
		var hostGroups []string
		if err := db.Model(&model.ClientGroupHost{}).
			Where("group_name = ?", group).Pluck("host_group_id", &hostGroups).Error; err != nil {
			return access, err
		}
		for _, id := range hostGroups {
			if id != "" {
				hostSet[id] = struct{}{}
			}
		}
	}

	if !access.HasRestrictedInbound {
		access.UsesLegacyInbounds = true
	}
	access.InboundIDs = sortedInts(inboundSet)

	var individual []string
	if err := db.Model(&model.ClientHost{}).
		Where("client_id = ?", clientID).Order("group_id ASC").Pluck("group_id", &individual).Error; err != nil {
		return access, err
	}
	if len(individual) > 0 {
		access.IndividualHostGroups = true
		access.HostGroupIDs = uniqueSortedStrings(individual)
	} else {
		access.HostGroupIDs = uniqueSortedStringsFromSet(hostSet)
	}
	return access, nil
}

func sortedInts(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	return uniqueSortedStringsFromSet(set)
}

func uniqueSortedStringsFromSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
