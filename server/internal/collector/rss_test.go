package collector

import "testing"

func TestClampPublishedTS(t *testing.T) {
	now := int64(1795800000)
	// Normal past timestamp kept intact
	if got := clampPublishedTS(now-600, now); got != now-600 {
		t.Fatalf("expected past ts preserved, got %d", got)
	}
	// Slight clock skew within 5 min kept intact
	if got := clampPublishedTS(now+120, now); got != now+120 {
		t.Fatalf("expected slight skew preserved, got %d", got)
	}
	// Future scheduled maintenance or +8h timezone bug clamped to now
	if got := clampPublishedTS(now+8*3600, now); got != now {
		t.Fatalf("expected future ts clamped to now, got %d", got)
	}
	if got := clampPublishedTS(0, now); got != now {
		t.Fatalf("expected zero ts clamped to now, got %d", got)
	}
}

func TestIsRoutineStatusMaintenance(t *testing.T) {
	if !isRoutineStatusMaintenance("CMB (Colombo) on 2026-12-09", "We will be performing scheduled maintenance in CMB (Colombo) datacenter.") {
		t.Fatalf("expected routine Cloudflare scheduled maintenance to be filtered")
	}
	if isRoutineStatusMaintenance("Cloudflare Global Network Outage", "Major outage affecting API and edge routing across regions.") {
		t.Fatalf("expected real outage to pass")
	}
	if isRoutineStatusMaintenance("Scheduled Maintenance causing Major Outage", "Unexpected service disruption during maintenance.") {
		t.Fatalf("expected maintenance escalated to outage to pass")
	}
}
