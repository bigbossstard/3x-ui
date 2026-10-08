package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedRestrictedGroupClient(t *testing.T, groupName string) *model.ClientRecord {
	t.Helper()
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{
		Name: groupName, PolicyState: model.ClientGroupPolicyRestricted,
	}).Error; err != nil {
		t.Fatalf("create restricted group: %v", err)
	}
	client := &model.ClientRecord{
		Email: "restricted-member@x", UUID: "11111111-2222-4333-8444-555555555555",
		SubID: "restricted-member-sub", Group: groupName, Enable: true,
	}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	return client
}

func TestUpdateCannotRemoveClientFromRestrictedGroup(t *testing.T) {
	setupConflictDB(t)
	client := seedRestrictedGroupClient(t, "premium")
	update := model.Client{Email: client.Email, ID: client.UUID, SubID: client.SubID, Enable: false}

	if _, err := (&ClientService{}).Update(&InboundService{}, client.Id, update, 0); err == nil {
		t.Fatal("Update cleared restricted group membership")
	}
	var after model.ClientRecord
	if err := database.GetDB().First(&after, client.Id).Error; err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if after.Group != "premium" {
		t.Fatalf("group after rejected update = %q, want premium", after.Group)
	}
}

func TestDeleteCannotRemoveRestrictedGroupWithMembers(t *testing.T) {
	setupConflictDB(t)
	seedRestrictedGroupClient(t, "premium")

	if _, err := (&ClientService{}).DeleteGroup("premium"); err == nil {
		t.Fatal("DeleteGroup deleted a restricted group with members")
	}
	var count int64
	if err := database.GetDB().Model(&model.ClientGroup{}).Where("name = ?", "premium").Count(&count).Error; err != nil {
		t.Fatalf("count group: %v", err)
	}
	if count != 1 {
		t.Fatalf("group row count = %d, want 1", count)
	}
}

func TestBulkRemoveCannotRemoveClientFromRestrictedGroup(t *testing.T) {
	setupConflictDB(t)
	client := seedRestrictedGroupClient(t, "premium")

	if _, err := (&ClientService{}).RemoveFromGroup([]string{client.Email}); err == nil {
		t.Fatal("RemoveFromGroup removed a client from a restricted group")
	}
	var after model.ClientRecord
	if err := database.GetDB().First(&after, client.Id).Error; err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if after.Group != "premium" {
		t.Fatalf("group after rejected removal = %q, want premium", after.Group)
	}
}
