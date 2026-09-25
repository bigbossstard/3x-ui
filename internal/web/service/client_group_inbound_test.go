package service

import (
	"reflect"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestClientGroupInboundAssignmentsPersistAndValidate(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "paid"}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&[]model.Inbound{{Remark: "one"}, {Remark: "two"}}).Error; err != nil {
		t.Fatalf("create inbounds: %v", err)
	}

	svc := &ClientGroupInboundService{}
	if err := svc.SetInboundIDs("paid", []int{2, 1, 2}); err != nil {
		t.Fatalf("set assignments: %v", err)
	}
	got, err := svc.GetInboundIDs("paid")
	if err != nil {
		t.Fatalf("get assignments: %v", err)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assignments = %v, want %v", got, want)
	}
	if err := svc.SetInboundIDs("paid", []int{99}); err == nil {
		t.Fatal("invalid inbound assignment succeeded")
	}
	got, err = svc.GetInboundIDs("paid")
	if err != nil {
		t.Fatalf("get assignments after validation failure: %v", err)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assignments after validation failure = %v, want %v", got, want)
	}
}

func TestClientGroupInboundAssignmentsFollowGroupLifecycle(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	if err := db.Create(&model.ClientGroup{Name: "old"}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	if err := db.Create(&model.Inbound{Remark: "one"}).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}

	if err := (&ClientGroupInboundService{}).SetInboundIDs("old", []int{1}); err != nil {
		t.Fatalf("set assignments: %v", err)
	}

	if _, err := (&ClientService{}).RenameGroup("old", "new"); err != nil {
		t.Fatalf("rename group: %v", err)
	}
	ids, err := (&ClientGroupInboundService{}).GetInboundIDs("new")
	if err != nil {
		t.Fatalf("get renamed assignments: %v", err)
	}
	if want := []int{1}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("renamed assignments = %v, want %v", ids, want)
	}
	if _, err := (&ClientService{}).DeleteGroup("new"); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	ids, err = (&ClientGroupInboundService{}).GetInboundIDs("new")
	if err != nil {
		t.Fatalf("get deleted assignments: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("deleted group assignments = %v, want empty", ids)
	}
}
