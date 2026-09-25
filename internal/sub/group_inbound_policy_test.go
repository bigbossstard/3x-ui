package sub

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestSub_ClientGroupInboundAssignmentFiltersInbounds(t *testing.T) {
	seedSubDB(t)
	first := seedSubInbound(t, "s-group-inbound", "first", 4454, 1, wsTLSStream)
	var client model.ClientRecord
	if err := database.GetDB().Where("sub_id = ?", "s-group-inbound").First(&client).Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	second := &model.Inbound{
		UserId: 1, Tag: "second", Enable: true, Listen: "203.0.113.6", Port: 4455,
		Protocol: model.VLESS, Remark: "second",
		Settings: first.Settings, StreamSettings: wsTLSStream, SubSortIndex: 2,
	}
	if err := database.GetDB().Create(second).Error; err != nil {
		t.Fatalf("create second inbound: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientInbound{ClientId: client.Id, InboundId: second.Id}).Error; err != nil {
		t.Fatalf("attach second inbound: %v", err)
	}
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("id = ?", client.Id).Update("group_name", "premium").Error; err != nil {
		t.Fatalf("set group: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientGroup{Name: "premium"}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientGroupInbound{GroupName: "premium", InboundId: first.Id}).Error; err != nil {
		t.Fatalf("assign inbound policy: %v", err)
	}

	links, _, _, _, err := NewSubService("").GetSubs("s-group-inbound", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if len(links) != 1 || !strings.Contains(links[0], ":4454") {
		t.Fatalf("links = %v, want only first inbound", links)
	}
}
