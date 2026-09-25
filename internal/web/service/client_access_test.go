package service

import (
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestResolveClientAccessUnionsGroups(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&[]model.Inbound{{Remark: "one"}, {Remark: "two"}, {Remark: "three"}}).Error; err != nil {
		t.Fatalf("create inbounds: %v", err)
	}
	if err := db.Create(&[]model.ClientGroup{
		{Name: "alpha", PolicyState: model.ClientGroupPolicyRestricted},
		{Name: "beta", PolicyState: model.ClientGroupPolicyRestricted},
	}).Error; err != nil {
		t.Fatalf("create groups: %v", err)
	}
	client := &model.ClientRecord{Email: "multi@example", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&[]model.ClientGroupMembership{
		{ClientId: client.Id, GroupName: "beta"},
		{ClientId: client.Id, GroupName: "alpha"},
	}).Error; err != nil {
		t.Fatalf("create memberships: %v", err)
	}
	if err := db.Create(&[]model.ClientGroupInbound{
		{GroupName: "alpha", InboundId: 1},
		{GroupName: "beta", InboundId: 2},
	}).Error; err != nil {
		t.Fatalf("create inbound policies: %v", err)
	}
	if err := db.Create(&[]model.ClientGroupHost{
		{GroupName: "alpha", HostGroupId: "host-a"},
		{GroupName: "beta", HostGroupId: "host-b"},
	}).Error; err != nil {
		t.Fatalf("create host policies: %v", err)
	}

	got, err := ResolveClientAccess(client.Id)
	if err != nil {
		t.Fatalf("ResolveClientAccess: %v", err)
	}
	if !reflect.DeepEqual(got.GroupNames, []string{"alpha", "beta"}) {
		t.Fatalf("groups = %v", got.GroupNames)
	}
	if !reflect.DeepEqual(got.InboundIDs, []int{1, 2}) {
		t.Fatalf("inbounds = %v", got.InboundIDs)
	}
	if !reflect.DeepEqual(got.HostGroupIDs, []string{"host-a", "host-b"}) {
		t.Fatalf("hosts = %v", got.HostGroupIDs)
	}
	if got.UsesLegacyInbounds {
		t.Fatal("restricted union unexpectedly uses legacy inbounds")
	}
}

func TestResolveClientAccessIndividualHostsOverrideGroupUnion(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "alpha", PolicyState: model.ClientGroupPolicyRestricted}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	client := &model.ClientRecord{Email: "override@example", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientGroupMembership{ClientId: client.Id, GroupName: "alpha"}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if err := db.Create(&model.ClientGroupHost{GroupName: "alpha", HostGroupId: "group-host"}).Error; err != nil {
		t.Fatalf("create group host: %v", err)
	}
	if err := db.Create(&model.ClientHost{ClientId: client.Id, GroupId: "individual-host"}).Error; err != nil {
		t.Fatalf("create individual host: %v", err)
	}

	got, err := ResolveClientAccess(client.Id)
	if err != nil {
		t.Fatalf("ResolveClientAccess: %v", err)
	}
	if !got.IndividualHostGroups {
		t.Fatal("individual override was not detected")
	}
	if !reflect.DeepEqual(got.HostGroupIDs, []string{"individual-host"}) {
		t.Fatalf("hosts = %v, want individual override", got.HostGroupIDs)
	}
}

func TestResolveClientAccessUsesLegacyGroupWhenMembershipWasNotMigrated(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.Inbound{Remark: "legacy"}).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	client := &model.ClientRecord{Email: "legacy@example", Group: "legacy", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientGroup{
		Name: "legacy", PolicyState: model.ClientGroupPolicyLegacy,
	}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	got, err := ResolveClientAccess(client.Id)
	if err != nil {
		t.Fatalf("ResolveClientAccess: %v", err)
	}
	if !reflect.DeepEqual(got.GroupNames, []string{"legacy"}) || !got.UsesLegacyInbounds {
		t.Fatalf("legacy access = %+v", got)
	}
}
