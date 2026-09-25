package database

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestMigrateClientGroupMembershipsIsIdempotent(t *testing.T) {
	initMigrateDB(t)
	client := &model.ClientRecord{
		Email: "legacy-membership@example",
		Group: "legacy",
		Enable: true,
	}
	if err := GetDB().Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := migrateClientGroupMemberships(); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := migrateClientGroupMemberships(); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var rows []model.ClientGroupMembership
	if err := GetDB().Where("client_id = ?", client.Id).Find(&rows).Error; err != nil {
		t.Fatalf("load memberships: %v", err)
	}
	if len(rows) != 1 || rows[0].GroupName != "legacy" {
		t.Fatalf("memberships = %+v, want one legacy membership", rows)
	}
}

func TestMigrateClientGroupMembershipsLeavesEmptyGroupUnassigned(t *testing.T) {
	initMigrateDB(t)
	client := &model.ClientRecord{
		Email: "ungrouped@example",
		Group: "",
		Enable: true,
	}
	if err := GetDB().Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := migrateClientGroupMemberships(); err != nil {
		t.Fatalf("migration: %v", err)
	}
	var count int64
	if err := GetDB().Model(&model.ClientGroupMembership{}).
		Where("client_id = ?", client.Id).Count(&count).Error; err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if count != 0 {
		t.Fatalf("membership count = %d, want 0", count)
	}
}
