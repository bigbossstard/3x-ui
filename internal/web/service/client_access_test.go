package service

import (
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestResolveClientAccessUsesGroupPolicies(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.Inbound{Remark: "one"}).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if err := db.Create(&model.ClientGroup{Name: "premium", PolicyState: model.ClientGroupPolicyRestricted}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	client := &model.ClientRecord{Email: "grouped@example", Group: "premium", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientGroupInbound{GroupName: "premium", InboundId: 1}).Error; err != nil {
		t.Fatalf("create inbound policy: %v", err)
	}
	if err := db.Create(&model.ClientGroupHost{GroupName: "premium", HostGroupId: "group-host"}).Error; err != nil {
		t.Fatalf("create host policy: %v", err)
	}

	got, err := ResolveClientAccess(client.Id)
	if err != nil {
		t.Fatalf("ResolveClientAccess: %v", err)
	}
	if !reflect.DeepEqual(got.GroupNames, []string{"premium"}) ||
		!reflect.DeepEqual(got.InboundIDs, []int{1}) ||
		!reflect.DeepEqual(got.HostGroupIDs, []string{"group-host"}) {
		t.Fatalf("access = %+v", got)
	}
	if got.UsesLegacyInbounds {
		t.Fatal("restricted policy unexpectedly uses legacy inbounds")
	}
}

func TestResolveClientAccessIndividualHostsOverrideGroupPolicy(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "premium", PolicyState: model.ClientGroupPolicyRestricted}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	client := &model.ClientRecord{Email: "override@example", Group: "premium", Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientGroupHost{GroupName: "premium", HostGroupId: "group-host"}).Error; err != nil {
		t.Fatalf("create group host: %v", err)
	}
	if err := db.Create(&model.ClientHost{ClientId: client.Id, GroupId: "individual-host"}).Error; err != nil {
		t.Fatalf("create individual host: %v", err)
	}

	got, err := ResolveClientAccess(client.Id)
	if err != nil {
		t.Fatalf("ResolveClientAccess: %v", err)
	}
	if !got.IndividualHostGroups || !reflect.DeepEqual(got.HostGroupIDs, []string{"individual-host"}) {
		t.Fatalf("access = %+v", got)
	}
}
