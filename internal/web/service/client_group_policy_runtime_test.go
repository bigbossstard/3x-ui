package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

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
		t.Fatalf("EnforceGroupInboundPolicy: %v", err)
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
		t.Fatalf("EnforceGroupInboundPolicy: %v", err)
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
