package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func seedGroupPolicyClient(t *testing.T, nodeID *int, enabled bool) (*model.ClientRecord, *model.Inbound, *model.Inbound) {
	t.Helper()
	db := database.GetDB()
	record := &model.ClientRecord{Email: "policy-client@x", Group: "premium", Enable: true}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create client record: %v", err)
	}
	makeInbound := func(tag string, port int) *model.Inbound {
		t.Helper()
		inbound := &model.Inbound{
			UserId: 1, NodeID: nodeID, Tag: tag, Enable: true, Port: port, Protocol: model.VLESS,
			Settings:       fmt.Sprintf(`{"clients":[{"email":%q,"id":"11111111-2222-4333-8444-555555555555","enable":%t}]}`, record.Email, enabled),
			StreamSettings: `{"network":"tcp","security":"none"}`,
		}
		if err := db.Create(inbound).Error; err != nil {
			t.Fatalf("create inbound %s: %v", tag, err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: record.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatalf("attach client to %s: %v", tag, err)
		}
		return inbound
	}
	return record, makeInbound("policy-allowed", 44431), makeInbound("policy-blocked", 44432)
}

type groupPolicyLocalRuntime struct {
	fakeNodeRuntime
	removed atomic.Int32
}

func (r *groupPolicyLocalRuntime) RemoveUser(context.Context, *model.Inbound, string) error {
	r.removed.Add(1)
	return nil
}

func TestEnforceGroupInboundPolicyDetachesDisallowedLocalInbound(t *testing.T) {
	setupConflictDB(t)
	setRestartOnClientDisable(t, false)
	mgr := useTestRuntimeManager(t)
	rt := &groupPolicyLocalRuntime{}
	mgr.SetLocalRuntimeOverride(rt)
	_, allowed, blocked := seedGroupPolicyClient(t, nil, true)

	needRestart, err := (&ClientGroupInboundService{}).SetInboundIDs("premium", []int{allowed.Id})
	if err != nil {
		t.Fatalf("SetInboundIDs: %v", err)
	}
	if needRestart {
		t.Fatal("successful local remove should not request a restart when restart-on-disable is false")
	}
	if got := rt.removed.Load(); got != 1 {
		t.Fatalf("local runtime RemoveUser calls = %d, want 1", got)
	}
	ids, err := (&ClientService{}).GetInboundIdsForEmail(nil, "policy-client@x")
	if err != nil {
		t.Fatalf("GetInboundIdsForEmail: %v", err)
	}
	if len(ids) != 1 || ids[0] != allowed.Id {
		t.Fatalf("remaining inbound IDs = %v, want [%d] (removed %d)", ids, allowed.Id, blocked.Id)
	}
}

func TestEnforceGroupInboundPolicyMarksOfflineNodeDirty(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	node := &model.Node{
		Name: "policy-offline", Address: "127.0.0.1", Port: 2096,
		ApiToken: "policy-token", Enable: true, Status: "offline",
	}
	if err := db.Create(node).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	nodeID := node.Id
	_, allowed, _ := seedGroupPolicyClient(t, &nodeID, false)

	needRestart, err := (&ClientGroupInboundService{}).SetInboundIDs("premium", []int{allowed.Id})
	if err != nil {
		t.Fatalf("SetInboundIDs: %v", err)
	}
	if needRestart {
		t.Fatal("offline node policy update should defer to reconciliation, not restart local Xray")
	}
	_, _, dirty, _, err := (&NodeService{}).NodeSyncState(nodeID)
	if err != nil {
		t.Fatalf("NodeSyncState: %v", err)
	}
	if !dirty {
		t.Fatal("offline node must be marked dirty so reconcile applies the detached client")
	}
	ids, err := (&ClientService{}).GetInboundIdsForEmail(nil, "policy-client@x")
	if err != nil {
		t.Fatalf("GetInboundIdsForEmail: %v", err)
	}
	if len(ids) != 1 || ids[0] != allowed.Id {
		t.Fatalf("remaining inbound IDs = %v, want only allowed inbound %d", ids, allowed.Id)
	}
}

func TestEnforceGroupInboundPolicyDeletesUserOnOnlineNode(t *testing.T) {
	setupBulkDB(t)
	nodeID, fake := setupNodeRuntime(t)
	client := model.Client{ID: uuid.NewString(), Email: "policy-online@x", Group: "premium", Enable: true}
	inbound := nodeInbound(t, nodeID, 44433, []model.Client{client})

	needRestart, err := (&ClientGroupInboundService{}).SetInboundIDs("premium", nil)
	if err != nil {
		t.Fatalf("SetInboundIDs: %v", err)
	}
	if needRestart {
		t.Fatal("remote deletion should not request a local Xray restart")
	}
	if got := fake.deleteUser.Load(); got != 1 {
		t.Fatalf("remote DeleteUser calls = %d, want 1", got)
	}
	ids, err := (&ClientService{}).GetInboundIdsForEmail(nil, client.Email)
	if err != nil {
		t.Fatalf("GetInboundIdsForEmail: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("remaining inbound IDs = %v, want none (inbound %d)", ids, inbound.Id)
	}
}

func TestCreateRejectsAttachmentOutsideRestrictedGroupPolicy(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	group := &model.ClientGroup{Name: "premium", PolicyState: model.ClientGroupPolicyRestricted}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	allowed := &model.Inbound{Tag: "create-allowed", Port: 44501, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	blocked := &model.Inbound{Tag: "create-blocked", Port: 44502, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	if err := db.Create(allowed).Error; err != nil {
		t.Fatalf("create allowed inbound: %v", err)
	}
	if err := db.Create(blocked).Error; err != nil {
		t.Fatalf("create blocked inbound: %v", err)
	}
	if err := db.Create(&model.ClientGroupInbound{GroupName: group.Name, InboundId: allowed.Id}).Error; err != nil {
		t.Fatalf("set allowed inbound: %v", err)
	}

	needRestart, err := (&ClientService{}).Create(&InboundService{}, &ClientCreatePayload{
		Client:     model.Client{Email: "new-rejected@x", Group: group.Name, Enable: true},
		InboundIds: []int{blocked.Id},
	})
	if err == nil {
		t.Fatal("Create succeeded for an inbound outside the group policy")
	}
	if needRestart {
		t.Fatal("rejected Create must not request an Xray restart")
	}
	var count int64
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "new-rejected@x").Count(&count).Error; err != nil {
		t.Fatalf("count client record: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected client record count = %d, want 0", count)
	}
}

func TestUpdateMovingClientIntoRestrictedGroupDetachesBlockedInbound(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	record := &model.ClientRecord{
		Email: "move-to-restricted@x", SubID: "move-sub", UUID: "11111111-2222-4333-8444-555555555555",
		Group: "legacy", Enable: true,
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	blocked := &model.Inbound{
		Tag: "move-blocked", Port: 44511, Protocol: model.VLESS, Enable: true,
		Settings:       fmt.Sprintf(`{"clients":[{"email":%q,"id":%q,"enable":false}]}`, record.Email, record.UUID),
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	allowed := &model.Inbound{
		Tag: "move-allowed", Port: 44512, Protocol: model.VLESS, Enable: true,
		Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if err := db.Create(blocked).Error; err != nil {
		t.Fatalf("create blocked inbound: %v", err)
	}
	if err := db.Create(allowed).Error; err != nil {
		t.Fatalf("create allowed inbound: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: record.Id, InboundId: blocked.Id}).Error; err != nil {
		t.Fatalf("attach legacy inbound: %v", err)
	}
	if err := db.Create(&model.ClientGroup{Name: "premium", PolicyState: model.ClientGroupPolicyRestricted}).Error; err != nil {
		t.Fatalf("create restricted group: %v", err)
	}
	if err := db.Create(&model.ClientGroupInbound{GroupName: "premium", InboundId: allowed.Id}).Error; err != nil {
		t.Fatalf("set allowed inbound: %v", err)
	}

	update := model.Client{Email: record.Email, SubID: record.SubID, ID: record.UUID, Group: "premium", Enable: false}
	needRestart, err := (&ClientService{}).Update(&InboundService{}, record.Id, update, 0)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if needRestart {
		t.Fatal("disabled local attachment removal should not request restart")
	}
	ids, err := (&ClientService{}).GetInboundIdsForRecord(record.Id)
	if err != nil {
		t.Fatalf("GetInboundIdsForRecord: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("remaining inbounds = %v, want disallowed inbound %d removed", ids, blocked.Id)
	}
}

func TestSyncInboundRejectsRestrictedGroupOutsideAllowedInbound(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{
		Name: "premium", PolicyState: model.ClientGroupPolicyRestricted,
	}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	allowed := &model.Inbound{Tag: "sync-allowed", Port: 44521, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	blocked := &model.Inbound{Tag: "sync-blocked", Port: 44522, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	if err := db.Create(allowed).Error; err != nil {
		t.Fatalf("create allowed inbound: %v", err)
	}
	if err := db.Create(blocked).Error; err != nil {
		t.Fatalf("create blocked inbound: %v", err)
	}
	if err := db.Create(&model.ClientGroupInbound{GroupName: "premium", InboundId: allowed.Id}).Error; err != nil {
		t.Fatalf("set allowed inbound: %v", err)
	}

	err := (&ClientService{}).SyncInbound(nil, blocked.Id, []model.Client{
		{Email: "sync-blocked@x", Group: "premium", Enable: true},
	})
	if err == nil {
		t.Fatal("SyncInbound accepted a client on an inbound outside its restricted group policy")
	}
	var count int64
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "sync-blocked@x").Count(&count).Error; err != nil {
		t.Fatalf("count rejected client record: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected client record count = %d, want 0", count)
	}
}
