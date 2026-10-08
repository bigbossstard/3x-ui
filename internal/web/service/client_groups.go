package service

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"

	"gorm.io/gorm"
)

type GroupSummary struct {
	Id          int   `json:"id"`
	Name        string `json:"name"`
	ClientCount int   `json:"clientCount"`
	TrafficUsed int64 `json:"trafficUsed"`
	Up          int64 `json:"up"`
	Down        int64 `json:"down"`
	InboundIds  []int `json:"inboundIds,omitempty"`
}

func (s *ClientService) ListGroups() ([]GroupSummary, error) {
	db := database.GetDB()
	// email is unique in both clients and client_traffics, so the LEFT JOIN
	// never double-counts a client's traffic.
	var legacyDerived []GroupSummary
	if err := db.Table("clients AS c").
		Select("c.group_name AS name, COUNT(*) AS client_count, COALESCE(SUM(ct.up + ct.down), 0) AS traffic_used, COALESCE(SUM(ct.up), 0) AS up, COALESCE(SUM(ct.down), 0) AS down").
		Joins("LEFT JOIN client_traffics ct ON ct.email = c.email").
		Where("c.group_name <> ''").
		Where("(c.access_mode IS NULL OR c.access_mode = '' OR c.access_mode = 'legacy')").
		Group("c.group_name").
		Scan(&legacyDerived).Error; err != nil {
		return nil, err
	}

	var managedDerived []GroupSummary
	if err := db.Table("client_group_members AS gm").
		Select("cg.name AS name, COUNT(DISTINCT c.id) AS client_count, COALESCE(SUM(ct.up + ct.down), 0) AS traffic_used, COALESCE(SUM(ct.up), 0) AS up, COALESCE(SUM(ct.down), 0) AS down").
		Joins("JOIN client_groups cg ON cg.id = gm.group_id").
		Joins("JOIN clients c ON c.id = gm.client_id").
		Joins("LEFT JOIN client_traffics ct ON ct.email = c.email").
		Where("c.access_mode = 'groups'").
		Group("cg.id, cg.name").
		Scan(&managedDerived).Error; err != nil {
		return nil, err
	}
	var stored []model.ClientGroup
	if err := db.Find(&stored).Error; err != nil {
		return nil, err
	}
	accessByGroup := make(map[int][]int, len(stored))
	if len(stored) > 0 {
		var links []model.ClientGroupInbound
		if err := db.Order("group_id ASC").Order("inbound_id ASC").Find(&links).Error; err != nil {
			return nil, err
		}
		for _, link := range links {
			accessByGroup[link.GroupId] = append(accessByGroup[link.GroupId], link.InboundId)
		}
	}
	type groupAgg struct {
		count int
		up    int64
		down  int64
	}
	baseUp := make(map[string]int64, len(stored))
	baseDown := make(map[string]int64, len(stored))
	merged := make(map[string]groupAgg, len(legacyDerived)+len(managedDerived)+len(stored))
	for _, g := range stored {
		merged[g.Name] = groupAgg{}
		baseUp[g.Name] = g.ResetUp
		baseDown[g.Name] = g.ResetDown
	}
	for _, g := range legacyDerived {
		merged[g.Name] = groupAgg{count: g.ClientCount, up: g.Up, down: g.Down}
	}
	for _, g := range managedDerived {
		current := merged[g.Name]
		merged[g.Name] = groupAgg{count: current.count + g.ClientCount, up: current.up + g.Up, down: current.down + g.Down}
	}
	out := make([]GroupSummary, 0, len(merged))
	for name, agg := range merged {
		up := max(agg.up-baseUp[name], 0)
		down := max(agg.down-baseDown[name], 0)
		id := 0
		var inboundIds []int
		for _, storedGroup := range stored {
			if storedGroup.Name == name {
				id = storedGroup.Id
				inboundIds = accessByGroup[storedGroup.Id]
				break
			}
		}
		if inboundIds == nil { inboundIds = []int{} }
		out = append(out, GroupSummary{Id: id, Name: name, ClientCount: agg.count, TrafficUsed: up + down, Up: up, Down: down, InboundIds: inboundIds})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// adjustGroupBaselinesForRemovedTraffic shifts group baselines down by the clients'
// current counters so ListGroups totals survive a traffic reset or client delete (#5675).
func adjustGroupBaselinesForRemovedTraffic(tx *gorm.DB, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	type groupDelta struct {
		Name string
		Up   int64
		Down int64
	}
	totals := make(map[string]*groupDelta)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var part []groupDelta
		if err := tx.Table("clients AS c").
			Select("c.group_name AS name, COALESCE(SUM(ct.up), 0) AS up, COALESCE(SUM(ct.down), 0) AS down").
			Joins("JOIN client_traffics ct ON ct.email = c.email").
			Where("c.group_name <> '' AND c.email IN ?", batch).
			Group("c.group_name").
			Scan(&part).Error; err != nil {
			return err
		}
		for i := range part {
			if agg, ok := totals[part[i].Name]; ok {
				agg.Up += part[i].Up
				agg.Down += part[i].Down
			} else {
				totals[part[i].Name] = &part[i]
			}
		}
	}
	for name, d := range totals {
		if d.Up == 0 && d.Down == 0 {
			continue
		}
		res := tx.Model(&model.ClientGroup{}).Where("name = ?", name).Updates(map[string]any{
			"reset_up":   gorm.Expr("reset_up - ?", d.Up),
			"reset_down": gorm.Expr("reset_down - ?", d.Down),
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			if err := tx.Create(&model.ClientGroup{Name: name, ResetUp: -d.Up, ResetDown: -d.Down}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ClientService) EmailsByGroup(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return []string{}, nil
	}
	db := database.GetDB()
	set := make(map[string]string)

	var legacy []model.ClientRecord
	if err := db.Model(&model.ClientRecord{}).
		Where("group_name = ?", name).
		Where("(access_mode IS NULL OR access_mode = '' OR access_mode = 'legacy')").
		Find(&legacy).Error; err != nil {
		return nil, err
	}
	for _, rec := range legacy {
		if strings.TrimSpace(rec.Email) != "" {
			set[strings.ToLower(rec.Email)] = rec.Email
		}
	}

	var group model.ClientGroup
	if err := db.Where("name = ?", name).First(&group).Error; err == nil {
		var managed []model.ClientRecord
		if err := db.Table("client_group_members AS gm").
			Joins("JOIN clients c ON c.id = gm.client_id").
			Where("gm.group_id = ? AND c.access_mode = 'groups'", group.Id).
			Find(&managed).Error; err != nil {
			return nil, err
		}
		for _, rec := range managed {
			if strings.TrimSpace(rec.Email) != "" {
				set[strings.ToLower(rec.Email)] = rec.Email
			}
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	emails := make([]string, 0, len(set))
	for _, email := range set {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	return emails, nil
}

func (s *ClientService) ResetGroupTraffic(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return common.NewError("group name is required")
	}
	db := database.GetDB()
	emails, err := s.EmailsByGroup(name)
	if err != nil {
		return err
	}
	var agg struct {
		Up   int64
		Down int64
	}
	if len(emails) > 0 {
		if err := db.Table("client_traffics").
			Select("COALESCE(SUM(up), 0) AS up, COALESCE(SUM(down), 0) AS down").
			Where("email IN ?", emails).
			Scan(&agg).Error; err != nil {
			return err
		}
	}
	var count int64
	if err := db.Model(&model.ClientGroup{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return db.Create(&model.ClientGroup{Name: name, ResetUp: agg.Up, ResetDown: agg.Down}).Error
	}
	return db.Model(&model.ClientGroup{}).Where("name = ?", name).
		Updates(map[string]any{"reset_up": agg.Up, "reset_down": agg.Down}).Error
}

func (s *ClientService) CreateGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return common.NewError("group name is required")
	}
	db := database.GetDB()
	var count int64
	if err := db.Model(&model.ClientGroup{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return common.NewError("group already exists")
	}
	return db.Create(&model.ClientGroup{Name: name}).Error
}

func (s *ClientService) RenameGroup(oldName, newName string) (int, error) {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return 0, common.NewError("old group name is required")
	}
	if newName == "" {
		return 0, common.NewError("new group name is required")
	}
	if oldName == newName {
		return 0, nil
	}
	return s.replaceGroupValue(oldName, newName)
}

func (s *ClientService) DeleteGroup(name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, common.NewError("group name is required")
	}
	var group model.ClientGroup
	if err := database.GetDB().Where("name = ?", name).First(&group).Error; err == nil {
		if err := cleanupDeletedGroupAccess(database.GetDB(), group.Id); err != nil { return 0, err }
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	return s.replaceGroupValue(name, "")
}

func (s *ClientService) AddToGroupAccessAware(inboundSvc *InboundService, emails []string, group string) (int, bool, error) {
	group = strings.TrimSpace(group)
	if group == "" || len(emails) == 0 {
		return 0, false, nil
	}
	db := database.GetDB()
	var grp model.ClientGroup
	if err := db.Where("name = ?", group).First(&grp).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		if err := s.CreateGroup(group); err != nil {
			return 0, false, err
		}
		if err := db.Where("name = ?", group).First(&grp).Error; err != nil {
			return 0, false, err
		}
	} else if err != nil {
		return 0, false, err
	}

	var records []model.ClientRecord
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := db.Where("email IN ?", batch).Find(&rows).Error; err != nil {
			return 0, false, err
		}
		records = append(records, rows...)
	}

	legacyEmails := make([]string, 0, len(records))
	managedIDs := make([]int, 0, len(records))
	managedSeen := make(map[int]struct{})
	for _, rec := range records {
		if rec.AccessMode == model.ClientAccessModeGroups {
			var exists int64
			if err := db.Model(&model.ClientGroupMember{}).
				Where("client_id = ? AND group_id = ?", rec.Id, grp.Id).
				Count(&exists).Error; err != nil {
				return 0, false, err
			}
			if exists == 0 {
				if err := db.Create(&model.ClientGroupMember{ClientId: rec.Id, GroupId: grp.Id}).Error; err != nil {
					return 0, false, err
				}
			}
			if _, seen := managedSeen[rec.Id]; !seen {
				managedSeen[rec.Id] = struct{}{}
				managedIDs = append(managedIDs, rec.Id)
			}
			continue
		}
		legacyEmails = append(legacyEmails, rec.Email)
	}

	affected := 0
	if len(legacyEmails) > 0 {
		n, err := s.AddToGroup(legacyEmails, group)
		if err != nil {
			return affected, false, err
		}
		affected += n
	}

	needRestart := false
	for _, clientID := range managedIDs {
		changed, err := s.ReconcileManagedClient(inboundSvc, clientID)
		needRestart = needRestart || changed
		if err != nil {
			return affected + 1, needRestart, err
		}
		affected++
	}
	return affected, needRestart, nil
}

func (s *ClientService) RemoveFromGroupAccessAware(inboundSvc *InboundService, emails []string, group string) (int, bool, error) {
	group = strings.TrimSpace(group)
	if group == "" || len(emails) == 0 {
		return 0, false, nil
	}
	db := database.GetDB()
	var grp model.ClientGroup
	groupErr := db.Where("name = ?", group).First(&grp).Error
	if errors.Is(groupErr, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if groupErr != nil {
		return 0, false, groupErr
	}

	var records []model.ClientRecord
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := db.Where("email IN ?", batch).Find(&rows).Error; err != nil {
			return 0, false, err
		}
		records = append(records, rows...)
	}

	legacyEmails := make([]string, 0, len(records))
	managedIDs := make([]int, 0, len(records))
	for _, rec := range records {
		if rec.AccessMode == model.ClientAccessModeGroups {
			res := db.Where("client_id = ? AND group_id = ?", rec.Id, grp.Id).Delete(&model.ClientGroupMember{})
			if res.Error != nil {
				return 0, false, res.Error
			}
			if res.RowsAffected > 0 {
				managedIDs = append(managedIDs, rec.Id)
			}
			continue
		}
		if strings.TrimSpace(rec.Group) == group {
			legacyEmails = append(legacyEmails, rec.Email)
		}
	}

	affected := 0
	if len(legacyEmails) > 0 {
		n, err := s.AddToGroup(legacyEmails, "")
		if err != nil {
			return affected, false, err
		}
		affected += n
	}
	needRestart := false
	for _, clientID := range managedIDs {
		nr, err := s.ReconcileManagedClient(inboundSvc, clientID)
		needRestart = needRestart || nr
		if err != nil {
			return affected + 1, needRestart, err
		}
		affected++
	}
	return affected, needRestart, nil
}

func (s *ClientService) RemoveFromGroup(emails []string) (int, error) {
	return s.AddToGroup(emails, "")
}

func (s *ClientService) AddToGroup(emails []string, group string) (int, error) {
	group = strings.TrimSpace(group)
	if len(emails) == 0 {
		return 0, nil
	}
	db := database.GetDB()

	if group != "" {
		var exists int64
		if err := db.Model(&model.ClientGroup{}).Where("name = ?", group).Count(&exists).Error; err != nil {
			return 0, err
		}
		if exists == 0 {
			var derived int64
			if err := db.Model(&model.ClientRecord{}).Where("group_name = ?", group).Count(&derived).Error; err != nil {
				return 0, err
			}
			if derived == 0 {
				if err := db.Create(&model.ClientGroup{Name: group}).Error; err != nil {
					return 0, err
				}
			}
		}
	}

	var records []model.ClientRecord
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := db.Where("email IN ?", batch).
			Where("group_name IS NULL OR group_name <> ?", group).
			Find(&rows).Error; err != nil {
			return 0, err
		}
		records = append(records, rows...)
	}
	if len(records) == 0 {
		return 0, nil
	}
	affectedEmails := make([]string, 0, len(records))
	for _, r := range records {
		affectedEmails = append(affectedEmails, r.Email)
	}

	tx := db.Begin()
	var affected int64
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		result := tx.Model(&model.ClientRecord{}).
			Where("email IN ?", batch).
			Where("group_name IS NULL OR group_name <> ?", group).
			UpdateColumn("group_name", group)
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		affected += result.RowsAffected
	}

	var inboundIDs []int
	inboundIDSeen := make(map[int]struct{})
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		var ids []int
		if err := tx.Table("client_inbounds").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Distinct("client_inbounds.inbound_id").
			Pluck("inbound_id", &ids).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		for _, id := range ids {
			if _, ok := inboundIDSeen[id]; !ok {
				inboundIDSeen[id] = struct{}{}
				inboundIDs = append(inboundIDs, id)
			}
		}
	}

	emailSet := make(map[string]struct{}, len(affectedEmails))
	for _, e := range affectedEmails {
		emailSet[e] = struct{}{}
	}

	for _, ibID := range inboundIDs {
		var ib model.Inbound
		if err := tx.First(&ib, ibID).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		modified := false
		for i := range clients {
			cm, ok := clients[i].(map[string]any)
			if !ok {
				continue
			}
			email, _ := cm["email"].(string)
			if _, hit := emailSet[email]; !hit {
				continue
			}
			if group == "" {
				delete(cm, "group")
			} else {
				cm["group"] = group
			}
			clients[i] = cm
			modified = true
		}
		if modified {
			settings["clients"] = clients
			newSettings, err := json.Marshal(settings)
			if err != nil {
				continue
			}
			ib.Settings = string(newSettings)
			if err := tx.Save(&ib).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *ClientService) replaceGroupValue(oldName, newName string) (int, error) {
	db := database.GetDB()
	if newName == "" {
		if err := db.Where("name = ?", oldName).Delete(&model.ClientGroup{}).Error; err != nil {
			return 0, err
		}
	} else {
		if err := db.Model(&model.ClientGroup{}).Where("name = ?", oldName).Update("name", newName).Error; err != nil {
			return 0, err
		}
	}
	var records []model.ClientRecord
	if err := db.Where("group_name = ?", oldName).Find(&records).Error; err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}
	affectedEmails := make([]string, 0, len(records))
	for _, r := range records {
		affectedEmails = append(affectedEmails, r.Email)
	}

	tx := db.Begin()
	if err := tx.Model(&model.ClientRecord{}).
		Where("group_name = ?", oldName).
		UpdateColumn("group_name", newName).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	var inboundIDs []int
	inboundIDSeen := make(map[int]struct{})
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		var ids []int
		if err := tx.Table("client_inbounds").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Distinct("client_inbounds.inbound_id").
			Pluck("inbound_id", &ids).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		for _, id := range ids {
			if _, ok := inboundIDSeen[id]; !ok {
				inboundIDSeen[id] = struct{}{}
				inboundIDs = append(inboundIDs, id)
			}
		}
	}

	for _, ibID := range inboundIDs {
		var ib model.Inbound
		if err := tx.First(&ib, ibID).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		modified := false
		for i := range clients {
			cm, ok := clients[i].(map[string]any)
			if !ok {
				continue
			}
			if g, ok := cm["group"].(string); ok && g == oldName {
				if newName == "" {
					delete(cm, "group")
				} else {
					cm["group"] = newName
				}
				clients[i] = cm
				modified = true
			}
		}
		if modified {
			settings["clients"] = clients
			newSettings, err := json.Marshal(settings)
			if err != nil {
				continue
			}
			ib.Settings = string(newSettings)
			if err := tx.Save(&ib).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(records), nil
}
