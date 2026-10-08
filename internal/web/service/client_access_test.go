package service

import (
    "path/filepath"
    "reflect"
    "testing"

    "github.com/mhsanaei/3x-ui/v3/internal/database"
    "github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestClientGroupAccessResolution_UsesUnionAndPreservesMembership(t *testing.T) {
    dbDir := t.TempDir()
    t.Setenv("XUI_DB_FOLDER", dbDir)
    if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
        t.Fatalf("InitDB failed: %v", err)
    }
    t.Cleanup(func() { _ = database.CloseDB() })

    db := database.GetDB()
    svc := ClientService{}

    g1 := &model.ClientGroup{Name: "staff"}
    g2 := &model.ClientGroup{Name: "premium"}
    if err := db.Create(g1).Error; err != nil {
        t.Fatalf("create group 1: %v", err)
    }
    if err := db.Create(g2).Error; err != nil {
        t.Fatalf("create group 2: %v", err)
    }

    inbounds := []model.Inbound{
        {Remark: "one", Tag: "one", Port: 31001, Protocol: model.VLESS, Enable: true},
        {Remark: "two", Tag: "two", Port: 31002, Protocol: model.VLESS, Enable: true},
        {Remark: "three", Tag: "three", Port: 31003, Protocol: model.VLESS, Enable: true},
    }
    if err := db.Create(&inbounds).Error; err != nil {
        t.Fatalf("create inbounds: %v", err)
    }

    if err := svc.SetGroupInboundIds(g1.Id, []int{inbounds[0].Id, inbounds[1].Id, inbounds[1].Id}); err != nil {
        t.Fatalf("set group 1 access: %v", err)
    }
    if err := svc.SetGroupInboundIds(g2.Id, []int{inbounds[1].Id, inbounds[2].Id}); err != nil {
        t.Fatalf("set group 2 access: %v", err)
    }

    client := &model.ClientRecord{
        Email:      "access-test@example.com",
        UUID:       "11111111-1111-1111-1111-111111111111",
        SubID:      "access-test-sub",
        Enable:     true,
        AccessMode: model.ClientAccessModeGroups,
    }
    if err := db.Create(client).Error; err != nil {
        t.Fatalf("create client: %v", err)
    }

    if err := svc.SetClientGroupIds(client.Id, []int{g2.Id, g1.Id, g1.Id}); err != nil {
        t.Fatalf("set client groups: %v", err)
    }

    groupIDs, err := svc.GetClientGroupIds(client.Id)
    if err != nil {
        t.Fatalf("get client groups: %v", err)
    }
    if want := []int{g1.Id, g2.Id}; !reflect.DeepEqual(groupIDs, want) {
        t.Fatalf("group membership = %v, want %v", groupIDs, want)
    }

    got, err := svc.AllowedInboundIdsForClient(client.Id)
    if err != nil {
        t.Fatalf("resolve client access: %v", err)
    }
    want := []int{inbounds[0].Id, inbounds[1].Id, inbounds[2].Id}
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("allowed inbounds = %v, want %v", got, want)
    }

    gotFromGroups, err := svc.ResolveInboundIdsForGroups([]int{g2.Id, g1.Id, g2.Id})
    if err != nil {
        t.Fatalf("resolve group access: %v", err)
    }
    if !reflect.DeepEqual(gotFromGroups, want) {
        t.Fatalf("group union = %v, want %v", gotFromGroups, want)
    }

    none, err := svc.ResolveInboundIdsForGroups(nil)
    if err != nil {
        t.Fatalf("resolve empty groups: %v", err)
    }
    if len(none) != 0 {
        t.Fatalf("empty group set returned %v, want no access", none)
    }
}

func TestManagedClientCannotUseDirectInboundMutation(t *testing.T) {
	dbDir := t.TempDir()
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	client := &model.ClientRecord{
		Email:      "managed-direct-test@example.com",
		UUID:       "22222222-2222-2222-2222-222222222222",
		SubID:      "managed-direct-test-sub",
		Enable:     true,
		AccessMode: model.ClientAccessModeGroups,
	}
	if err := database.GetDB().Create(client).Error; err != nil {
		t.Fatalf("create managed client: %v", err)
	}

	svc := ClientService{}
	if _, err := svc.Attach(nil, client.Id, []int{1}); err == nil {
		t.Fatal("managed client was allowed to use direct attach")
	}
	if _, err := svc.Detach(nil, client.Id, []int{1}); err == nil {
		t.Fatal("managed client was allowed to use direct detach")
	}
}

func TestManagedInboundAuthorizationIsEnforcedAtSyncBoundary(t *testing.T) {
	dbDir := t.TempDir()
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	db := database.GetDB()
	svc := ClientService{}
	group := &model.ClientGroup{Name: "allowed"}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	inbounds := []model.Inbound{
		{Remark: "allowed", Tag: "allowed", Port: 32001, Protocol: model.VLESS, Enable: true},
		{Remark: "denied", Tag: "denied", Port: 32002, Protocol: model.VLESS, Enable: true},
	}
	if err := db.Create(&inbounds).Error; err != nil {
		t.Fatalf("create inbounds: %v", err)
	}
	if err := svc.SetGroupInboundIds(group.Id, []int{inbounds[0].Id}); err != nil {
		t.Fatalf("grant inbound: %v", err)
	}
	client := &model.ClientRecord{
		Email: "sync-auth@example.com", UUID: "33333333-3333-3333-3333-333333333333",
		SubID: "sync-auth-sub", Enable: true, AccessMode: model.ClientAccessModeGroups,
	}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := svc.SetClientGroupIds(client.Id, []int{group.Id}); err != nil {
		t.Fatalf("set client group: %v", err)
	}
	incoming := []model.Client{*client.ToClient()}
	if err := svc.SyncInbound(db, inbounds[1].Id, incoming); err != nil {
		t.Fatalf("SyncInbound denied: %v", err)
	}
	var linkCount int64
	if err := db.Model(&model.ClientInbound{}).
		Where("client_id = ? AND inbound_id = ?", client.Id, inbounds[1].Id).
		Count(&linkCount).Error; err != nil {
		t.Fatalf("count denied link: %v", err)
	}
	if linkCount != 0 {
		t.Fatal("managed client was attached to an unauthorized inbound")
	}
	if err := svc.SyncInbound(db, inbounds[0].Id, incoming); err != nil {
		t.Fatalf("SyncInbound allowed: %v", err)
	}
	if err := db.Model(&model.ClientInbound{}).
		Where("client_id = ? AND inbound_id = ?", client.Id, inbounds[0].Id).
		Count(&linkCount).Error; err != nil {
		t.Fatalf("count allowed link: %v", err)
	}
	if linkCount != 1 {
		t.Fatal("managed client was not attached to an authorized inbound")
	}
}

func TestDeletingInboundRemovesGroupGrant(t *testing.T) {
	dbDir := t.TempDir()
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	db := database.GetDB()
	group := &model.ClientGroup{Name: "cleanup"}
	inbound := &model.Inbound{Remark: "cleanup", Tag: "cleanup", Port: 32003, Protocol: model.VLESS, Enable: true}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	svc := ClientService{}
	if err := svc.SetGroupInboundIds(group.Id, []int{inbound.Id}); err != nil {
		t.Fatalf("grant inbound: %v", err)
	}
	if err := cleanupDeletedInboundAccess(db, inbound.Id); err != nil {
		t.Fatalf("cleanup inbound grant: %v", err)
	}
	ids, err := svc.GetGroupInboundIds(group.Id)
	if err != nil {
		t.Fatalf("read group grants: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("group grant survived inbound cleanup: %v", ids)
	}
}
