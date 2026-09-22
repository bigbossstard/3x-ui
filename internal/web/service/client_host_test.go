package service

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestClientHostServiceSetAndClear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dir)
	if err := database.InitDB(filepath.Join(dir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	db := database.GetDB()
	client := &model.ClientRecord{Email: "host-test@example.com", SubID: "host-test-sub"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	for _, groupID := range []string{"group-a", "group-b"} {
		if err := db.Create(&model.Host{GroupId: groupID, InboundId: 1, Remark: groupID}).Error; err != nil {
			t.Fatalf("create host %s: %v", groupID, err)
		}
	}

	svc := &ClientHostService{}
	if err := svc.SetGroupIDs(client.Id, []string{"group-b", "group-a", "group-a"}); err != nil {
		t.Fatalf("SetGroupIDs: %v", err)
	}
	got, err := svc.GetGroupIDs(client.Id)
	if err != nil {
		t.Fatalf("GetGroupIDs: %v", err)
	}
	if len(got) != 2 || got[0] != "group-a" || got[1] != "group-b" {
		t.Fatalf("unexpected assignments: %#v", got)
	}

	if err := svc.SetGroupIDs(client.Id, nil); err != nil {
		t.Fatalf("clear assignments: %v", err)
	}
	got, err = svc.GetGroupIDs(client.Id)
	if err != nil {
		t.Fatalf("GetGroupIDs after clear: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty assignments, got %#v", got)
	}

	if err := svc.SetGroupIDs(client.Id, []string{"missing"}); err == nil {
		t.Fatal("missing host group unexpectedly accepted")
	}
}
