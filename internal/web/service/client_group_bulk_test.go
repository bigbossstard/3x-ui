package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestAddToGroupReportsOnlyChangedRecordsIncludingNull(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "paid"}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	rows := []model.ClientRecord{
		{Email: "same@example", UUID: "same", Group: "paid"},
		{Email: "other@example", UUID: "other", Group: "free"},
		{Email: "null@example", UUID: "null"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create clients: %v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "null@example").UpdateColumn("group_name", nil).Error; err != nil {
		t.Fatalf("set NULL group: %v", err)
	}

	got, err := (&ClientService{}).AddToGroup([]string{"same@example", "other@example", "null@example", "missing@example"}, "paid")
	if err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}
	if got != 2 {
		t.Fatalf("affected = %d, want 2 changed records", got)
	}
	got, err = (&ClientService{}).AddToGroup([]string{"same@example", "other@example", "null@example"}, "paid")
	if err != nil {
		t.Fatalf("second AddToGroup: %v", err)
	}
	if got != 0 {
		t.Fatalf("second affected = %d, want 0", got)
	}
}

func TestAddToRestrictedGroupRejectsDisallowedExistingAttachments(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	client := &model.ClientRecord{Email: "legacy@x", UUID: "legacy-uuid", Group: "legacy"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	allowed := &model.Inbound{Tag: "group-move-allowed", Port: 44531, Protocol: model.VLESS}
	blocked := &model.Inbound{Tag: "group-move-blocked", Port: 44532, Protocol: model.VLESS}
	if err := db.Create(allowed).Error; err != nil {
		t.Fatalf("create allowed inbound: %v", err)
	}
	if err := db.Create(blocked).Error; err != nil {
		t.Fatalf("create blocked inbound: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: blocked.Id}).Error; err != nil {
		t.Fatalf("attach blocked inbound: %v", err)
	}
	if err := db.Create(&model.ClientGroup{Name: "premium", PolicyState: model.ClientGroupPolicyRestricted}).Error; err != nil {
		t.Fatalf("create restricted group: %v", err)
	}
	if err := db.Create(&model.ClientGroupInbound{GroupName: "premium", InboundId: allowed.Id}).Error; err != nil {
		t.Fatalf("set group policy: %v", err)
	}

	if affected, err := (&ClientService{}).AddToGroup([]string{client.Email}, "premium"); err == nil || affected != 0 {
		t.Fatalf("AddToGroup = (%d, %v), want rejected without changes", affected, err)
	}
	var after model.ClientRecord
	if err := db.First(&after, client.Id).Error; err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if after.Group != "legacy" {
		t.Fatalf("client group = %q, want unchanged legacy group", after.Group)
	}
}
