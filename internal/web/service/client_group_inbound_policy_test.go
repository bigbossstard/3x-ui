package service

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// TestClientGroupInboundEmptyAssignmentIsRestricted distinguishes an explicit
// empty restricted policy from an untouched legacy group.
func TestClientGroupInboundEmptyAssignmentIsRestricted(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "empty"}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := (&ClientGroupInboundService{}).SetInboundIDs("empty", nil); err != nil {
		t.Fatalf("set empty assignment: %v", err)
	}
	var group model.ClientGroup
	if err := db.Where("name = ?", "empty").First(&group).Error; err != nil {
		t.Fatalf("load group: %v", err)
	}
	if group.PolicyState != model.ClientGroupPolicyRestricted {
		t.Fatalf("policy state = %q, want %q", group.PolicyState, model.ClientGroupPolicyRestricted)
	}
	policy, err := (&ClientGroupInboundService{}).GetPolicy("empty")
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if policy.PolicyState != model.ClientGroupPolicyRestricted || len(policy.InboundIDs) != 0 {
		t.Fatalf("policy = %+v, want restricted empty policy", policy)
	}
}
