package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/suoten/jt-engine/pkg/storage"
)

func TestGeofence_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Save
	g := &storage.Geofence{
		ID:        "gf-1",
		Name:      "园区围栏",
		Type:      1,
		OrgID:     "org-1",
		Params:    `{"radius":500}`,
		StartTime: time.Now(),
		EndTime:   time.Now().Add(24 * time.Hour),
	}
	if err := s.SaveGeofence(ctx, g); err != nil {
		t.Fatalf("SaveGeofence: %v", err)
	}

	// Get
	got, err := s.GetGeofence(ctx, "gf-1")
	if err != nil {
		t.Fatalf("GetGeofence: %v", err)
	}
	if got.Name != "园区围栏" {
		t.Errorf("Name = %q, want 园区围栏", got.Name)
	}
	if got.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", got.OrgID)
	}

	// List
	result, err := s.ListGeofences(ctx, storage.ListOptions{OrgID: "org-1", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListGeofences: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d, want 1", result.Total)
	}

	// Update（INSERT OR REPLACE）
	g.Name = "园区围栏-更新"
	if err := s.SaveGeofence(ctx, g); err != nil {
		t.Fatalf("SaveGeofence update: %v", err)
	}
	got2, err := s.GetGeofence(ctx, "gf-1")
	if err != nil {
		t.Fatalf("GetGeofence after update: %v", err)
	}
	if got2.Name != "园区围栏-更新" {
		t.Errorf("Name after update = %q, want 园区围栏-更新", got2.Name)
	}

	// Delete
	if err := s.DeleteGeofence(ctx, "gf-1"); err != nil {
		t.Fatalf("DeleteGeofence: %v", err)
	}
	if err := s.DeleteGeofence(ctx, "gf-1"); err == nil {
		t.Error("second DeleteGeofence should fail (not found)")
	}
}

func TestGeofence_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetGeofence(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent geofence")
	}
}

func TestGeofence_ListPagination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s.SaveGeofence(ctx, &storage.Geofence{
			ID:    "gf-" + string(rune('a'+i)),
			Name:  "fence",
			OrgID: "org-x",
		})
	}
	result, err := s.ListGeofences(ctx, storage.ListOptions{OrgID: "org-x", Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ListGeofences: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("Total = %d, want 5", result.Total)
	}
	items, ok := result.Items.([]*storage.Geofence)
	if !ok {
		t.Fatalf("Items type = %T, want []*storage.Geofence", result.Items)
	}
	if len(items) != 2 {
		t.Errorf("Items len = %d, want 2 (pageSize)", len(items))
	}
}

func TestForwardRule_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rule := &storage.ForwardRule{
		ID:               "fr-1",
		SourcePlatformID: "src-1",
		PlatformID:       "plat-1",
		DataType:         "0x1200",
		Phone:            "13800000001",
		AlarmTypes:       "1,2,3",
		MinLevel:         2,
		TimeStart:        "08:00",
		TimeEnd:          "20:00",
		Enabled:          true,
	}
	if err := s.SaveForwardRule(ctx, rule); err != nil {
		t.Fatalf("SaveForwardRule: %v", err)
	}

	got, err := s.GetForwardRule(ctx, "fr-1")
	if err != nil {
		t.Fatalf("GetForwardRule: %v", err)
	}
	if got.PlatformID != "plat-1" {
		t.Errorf("PlatformID = %q, want plat-1", got.PlatformID)
	}
	if got.SourcePlatformID != "src-1" {
		t.Errorf("SourcePlatformID = %q, want src-1", got.SourcePlatformID)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got.MinLevel != 2 {
		t.Errorf("MinLevel = %d, want 2", got.MinLevel)
	}

	list, err := s.ListForwardRules(ctx, "plat-1")
	if err != nil {
		t.Fatalf("ListForwardRules: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	if err := s.DeleteForwardRule(ctx, "fr-1"); err != nil {
		t.Fatalf("DeleteForwardRule: %v", err)
	}
	if err := s.DeleteForwardRule(ctx, "fr-1"); err == nil {
		t.Error("second DeleteForwardRule should fail (not found)")
	}
}

func TestForwardRule_ListAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.SaveForwardRule(ctx, &storage.ForwardRule{ID: "fr-a", PlatformID: "p1"})
	s.SaveForwardRule(ctx, &storage.ForwardRule{ID: "fr-b", PlatformID: "p2"})

	list, err := s.ListForwardRules(ctx, "")
	if err != nil {
		t.Fatalf("ListForwardRules: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("list len = %d, want 2 (all)", len(list))
	}
}

func TestPlatform_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p := &storage.Platform{
		ID:         "pf-1",
		Name:       "下级平台A",
		UserID:     "user-a",
		Password:   "pass-a",
		Role:       "downstream",
		Host:       "192.168.1.100",
		Port:       8899,
		LinkType:   1,
		DownLinkID: "dl-1",
		Enabled:    true,
	}
	if err := s.SavePlatform(ctx, p); err != nil {
		t.Fatalf("SavePlatform: %v", err)
	}

	got, err := s.GetPlatform(ctx, "pf-1")
	if err != nil {
		t.Fatalf("GetPlatform: %v", err)
	}
	if got.Name != "下级平台A" {
		t.Errorf("Name = %q, want 下级平台A", got.Name)
	}
	if got.Role != "downstream" {
		t.Errorf("Role = %q, want downstream", got.Role)
	}
	if got.Port != 8899 {
		t.Errorf("Port = %d, want 8899", got.Port)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}

	list, err := s.ListPlatforms(ctx, "downstream")
	if err != nil {
		t.Fatalf("ListPlatforms: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	if err := s.DeletePlatform(ctx, "pf-1"); err != nil {
		t.Fatalf("DeletePlatform: %v", err)
	}
	if err := s.DeletePlatform(ctx, "pf-1"); err == nil {
		t.Error("second DeletePlatform should fail (not found)")
	}
}

func TestPlatform_ListByRole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.SavePlatform(ctx, &storage.Platform{ID: "pf-d", Role: "downstream"})
	s.SavePlatform(ctx, &storage.Platform{ID: "pf-u", Role: "upstream"})

	downstream, _ := s.ListPlatforms(ctx, "downstream")
	if len(downstream) != 1 {
		t.Errorf("downstream len = %d, want 1", len(downstream))
	}
	all, _ := s.ListPlatforms(ctx, "")
	if len(all) != 2 {
		t.Errorf("all len = %d, want 2", len(all))
	}
}
