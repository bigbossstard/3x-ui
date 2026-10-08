package service

import (
    "errors"
    "sort"
    "strings"

    "github.com/mhsanaei/3x-ui/v3/internal/database"
    "github.com/mhsanaei/3x-ui/v3/internal/database/model"

    "gorm.io/gorm"
)

type ClientGroupAccess struct {
    Id         int   `json:"id"`
    Name       string `json:"name"`
    InboundIds []int `json:"inboundIds"`
}

func normalizeClientAccessMode(mode string, groupIDs []int) string {
    switch strings.ToLower(strings.TrimSpace(mode)) {
    case model.ClientAccessModeGroups:
        return model.ClientAccessModeGroups
    case model.ClientAccessModeLegacy:
        return model.ClientAccessModeLegacy
    case "":
        if len(groupIDs) > 0 {
            return model.ClientAccessModeGroups
        }
        return model.ClientAccessModeLegacy
    default:
        return model.ClientAccessModeLegacy
    }
}

func uniqueSortedInts(values []int) []int {
    seen := make(map[int]struct{}, len(values))
    out := make([]int, 0, len(values))
    for _, v := range values {
        if v <= 0 {
            continue
        }
        if _, ok := seen[v]; ok {
            continue
        }
        seen[v] = struct{}{}
        out = append(out, v)
    }
    sort.Ints(out)
    return out
}

func (s *ClientService) validateGroupIds(tx *gorm.DB, groupIDs []int) error {
    groupIDs = uniqueSortedInts(groupIDs)
    if len(groupIDs) == 0 {
        return nil
    }
    var count int64
    if err := tx.Model(&model.ClientGroup{}).Where("id IN ?", groupIDs).Count(&count).Error; err != nil {
        return err
    }
    if int(count) != len(groupIDs) {
        return errors.New("one or more access groups do not exist")
    }
    return nil
}

func (s *ClientService) validateInboundIds(tx *gorm.DB, inboundIDs []int) error {
    inboundIDs = uniqueSortedInts(inboundIDs)
    if len(inboundIDs) == 0 {
        return nil
    }
    var count int64
    if err := tx.Model(&model.Inbound{}).Where("id IN ?", inboundIDs).Count(&count).Error; err != nil {
        return err
    }
    if int(count) != len(inboundIDs) {
        return errors.New("one or more inbounds do not exist")
    }
    return nil
}

func (s *ClientService) ResolveInboundIdsForGroups(groupIDs []int) ([]int, error) {
    groupIDs = uniqueSortedInts(groupIDs)
    if len(groupIDs) == 0 {
        return []int{}, nil
    }
    db := database.GetDB()
    if err := s.validateGroupIds(db, groupIDs); err != nil {
        return nil, err
    }
    var ids []int
    if err := db.Table("client_group_inbounds").
        Where("group_id IN ?", groupIDs).
        Distinct("inbound_id").
        Order("inbound_id ASC").
        Pluck("inbound_id", &ids).Error; err != nil {
        return nil, err
    }
    if ids == nil {
        ids = []int{}
    }
    return ids, nil
}

func (s *ClientService) GetClientGroupIds(clientID int) ([]int, error) {
    var ids []int
    if err := database.GetDB().Table("client_group_members").
        Where("client_id = ?", clientID).
        Order("group_id ASC").
        Pluck("group_id", &ids).Error; err != nil {
        return nil, err
    }
    if ids == nil {
        ids = []int{}
    }
    return ids, nil
}

func (s *ClientService) SetClientGroupIds(clientID int, groupIDs []int) error {
    db := database.GetDB()
    groupIDs = uniqueSortedInts(groupIDs)
    if err := s.validateGroupIds(db, groupIDs); err != nil {
        return err
    }
    return db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Where("client_id = ?", clientID).Delete(&model.ClientGroupMember{}).Error; err != nil {
            return err
        }
        if len(groupIDs) == 0 {
            return nil
        }
        rows := make([]model.ClientGroupMember, 0, len(groupIDs))
        for _, groupID := range groupIDs {
            rows = append(rows, model.ClientGroupMember{ClientId: clientID, GroupId: groupID})
        }
        return tx.Create(&rows).Error
    })
}

func (s *ClientService) GetGroupAccess(name string) (ClientGroupAccess, error) {
    name = strings.TrimSpace(name)
    if name == "" {
        return ClientGroupAccess{}, errors.New("group name is required")
    }
    var group model.ClientGroup
    if err := database.GetDB().Where("name = ?", name).First(&group).Error; err != nil {
        return ClientGroupAccess{}, err
    }
    ids, err := s.GetGroupInboundIds(group.Id)
    if err != nil {
        return ClientGroupAccess{}, err
    }
    return ClientGroupAccess{Id: group.Id, Name: group.Name, InboundIds: ids}, nil
}

func (s *ClientService) GetGroupInboundIds(groupID int) ([]int, error) {
    var ids []int
    if err := database.GetDB().Table("client_group_inbounds").
        Where("group_id = ?", groupID).
        Order("inbound_id ASC").
        Pluck("inbound_id", &ids).Error; err != nil {
        return nil, err
    }
    if ids == nil {
        ids = []int{}
    }
    return ids, nil
}

func (s *ClientService) SetGroupInboundIds(groupID int, inboundIDs []int) error {
    db := database.GetDB()
    inboundIDs = uniqueSortedInts(inboundIDs)
    if err := s.validateInboundIds(db, inboundIDs); err != nil {
        return err
    }
    var group model.ClientGroup
    if err := db.Where("id = ?", groupID).First(&group).Error; err != nil {
        return err
    }
    return db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Where("group_id = ?", groupID).Delete(&model.ClientGroupInbound{}).Error; err != nil {
            return err
        }
        rows := make([]model.ClientGroupInbound, 0, len(inboundIDs))
        for _, inboundID := range inboundIDs {
            rows = append(rows, model.ClientGroupInbound{GroupId: groupID, InboundId: inboundID})
        }
        if len(rows) == 0 {
            return nil
        }
        return tx.Create(&rows).Error
    })
}

func (s *ClientService) SetClientAccess(inboundSvc *InboundService, clientID int, mode string, groupIDs []int) (bool, error) {
    db := database.GetDB()
    groupIDs = uniqueSortedInts(groupIDs)
    mode = normalizeClientAccessMode(mode, groupIDs)
    if err := s.validateGroupIds(db, groupIDs); err != nil {
        return false, err
    }
    if _, err := s.GetByID(clientID); err != nil {
        return false, err
    }
    if err := db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Model(&model.ClientRecord{}).Where("id = ?", clientID).
            UpdateColumn("access_mode", mode).Error; err != nil {
            return err
        }
        if err := tx.Where("client_id = ?", clientID).Delete(&model.ClientGroupMember{}).Error; err != nil {
            return err
        }
        if len(groupIDs) == 0 {
            return nil
        }
        rows := make([]model.ClientGroupMember, 0, len(groupIDs))
        for _, groupID := range groupIDs {
            rows = append(rows, model.ClientGroupMember{ClientId: clientID, GroupId: groupID})
        }
        return tx.Create(&rows).Error
    }); err != nil {
        return false, err
    }
    if mode != model.ClientAccessModeGroups {
        return false, nil
    }
    return s.ReconcileManagedClient(inboundSvc, clientID)
}

func (s *ClientService) AllowedInboundIdsForClient(clientID int) ([]int, error) {
    var ids []int
    if err := database.GetDB().Table("client_group_inbounds AS gi").
        Joins("JOIN client_group_members AS gm ON gm.group_id = gi.group_id").
        Where("gm.client_id = ?", clientID).
        Distinct("gi.inbound_id").
        Order("gi.inbound_id ASC").
        Pluck("gi.inbound_id", &ids).Error; err != nil {
        return nil, err
    }
    if ids == nil {
        ids = []int{}
    }
    return ids, nil
}

func (s *ClientService) ReconcileManagedClient(inboundSvc *InboundService, clientID int) (bool, error) {
    rec, err := s.GetByID(clientID)
    if err != nil {
        return false, err
    }
    if normalizeClientAccessMode(rec.AccessMode, nil) != model.ClientAccessModeGroups {
        return false, nil
    }
    desired, err := s.AllowedInboundIdsForClient(clientID)
    if err != nil {
        return false, err
    }
    current, err := s.GetInboundIdsForRecord(clientID)
    if err != nil {
        return false, err
    }
    have := make(map[int]struct{}, len(current))
    for _, id := range current {
        have[id] = struct{}{}
    }
    desiredSet := make(map[int]struct{}, len(desired))
    for _, id := range desired {
        desiredSet[id] = struct{}{}
    }
    toAttach := make([]int, 0)
    toDetach := make([]int, 0)
    for _, id := range desired {
        if _, ok := have[id]; !ok {
            toAttach = append(toAttach, id)
        }
    }
    for _, id := range current {
        if _, ok := desiredSet[id]; !ok {
            toDetach = append(toDetach, id)
        }
    }
    needRestart := false
    if len(toAttach) > 0 {
        nr, err := s.Attach(inboundSvc, clientID, toAttach)
        needRestart = needRestart || nr
        if err != nil {
            return needRestart, err
        }
    }
    if len(toDetach) > 0 {
        nr, err := s.Detach(inboundSvc, clientID, toDetach)
        needRestart = needRestart || nr
        if err != nil {
            return needRestart, err
        }
    }
    return needRestart, nil
}

func (s *ClientService) SetGroupAccessByName(inboundSvc *InboundService, name string, inboundIDs []int) (int, bool, error) {
    name = strings.TrimSpace(name)
    if name == "" {
        return 0, false, errors.New("group name is required")
    }
    var group model.ClientGroup
    if err := database.GetDB().Where("name = ?", name).First(&group).Error; err != nil {
        return 0, false, err
    }
    if err := s.SetGroupInboundIds(group.Id, inboundIDs); err != nil {
        return 0, false, err
    }
    var clientIDs []int
    if err := database.GetDB().Table("client_group_members").
        Where("group_id = ?", group.Id).
        Order("client_id ASC").
        Pluck("client_id", &clientIDs).Error; err != nil {
        return 0, false, err
    }
    needRestart := false
    for _, clientID := range clientIDs {
        nr, err := s.ReconcileManagedClient(inboundSvc, clientID)
        needRestart = needRestart || nr
        if err != nil {
            return len(clientIDs), needRestart, err
        }
    }
    return len(clientIDs), needRestart, nil
}

func (s *ClientService) DeleteGroupWithAccess(inboundSvc *InboundService, name string) (int, bool, error) {
    name = strings.TrimSpace(name)
    if name == "" {
        return 0, false, errors.New("group name is required")
    }
    var group model.ClientGroup
    if err := database.GetDB().Where("name = ?", name).First(&group).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
        return 0, false, err
    }
    var managedIDs []int
    if group.Id > 0 {
        if err := database.GetDB().Table("client_group_members AS gm").
            Joins("JOIN clients c ON c.id = gm.client_id").
            Where("gm.group_id = ? AND c.access_mode = ?", group.Id, model.ClientAccessModeGroups).
            Pluck("gm.client_id", &managedIDs).Error; err != nil {
            return 0, false, err
        }
    }
    affected, err := s.DeleteGroup(name)
    if err != nil {
        return affected, false, err
    }
    needRestart := false
    for _, clientID := range managedIDs {
        nr, rerr := s.ReconcileManagedClient(inboundSvc, clientID)
        needRestart = needRestart || nr
        if rerr != nil {
            return affected, needRestart, rerr
        }
    }
    return affected, needRestart, nil
}

func cleanupClientAccessRelations(tx *gorm.DB, clientID int) error {
    return tx.Where("client_id = ?", clientID).Delete(&model.ClientGroupMember{}).Error
}

func cleanupDeletedGroupAccess(tx *gorm.DB, groupID int) error {
    if groupID <= 0 {
        return nil
    }
    if err := tx.Where("group_id = ?", groupID).Delete(&model.ClientGroupInbound{}).Error; err != nil {
        return err
    }
    return tx.Where("group_id = ?", groupID).Delete(&model.ClientGroupMember{}).Error
}
