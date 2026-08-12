package main

import (
	"fmt"
	"testing"
	"time"
)

// TestDemoPlanTenants asserts the byte-stable anchor of the demo dataset (SD1):
// the two original tenants and their admins keep their exact names, emails,
// passwords and display names so existing documentation and demos never churn.
// Each tenant must carry a user for every role in the vocabulary.
func TestDemoPlanTenants(t *testing.T) {
	plan := demoPlan()

	if len(plan) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(plan))
	}
	if plan[0].name != "Coop La Esperanza" || plan[1].name != "Estancia El Algarrobo" {
		t.Fatalf("tenant names drifted: %q, %q", plan[0].name, plan[1].name)
	}

	wantAdmin := []demoUser{
		{email: "admin@esperanza.coop", pass: "test123", fullName: "Admin Coop La Esperanza", role: "admin"},
		{email: "admin@algarrobo.campo", pass: "test123", fullName: "Admin Estancia El Algarrobo", role: "admin"},
	}
	for i, want := range wantAdmin {
		got := plan[i].users[0]
		if got != want {
			t.Errorf("tenant %d admin changed: got %+v want %+v", i, got, want)
		}
	}

	for _, tenant := range plan {
		if len(tenant.users) != 5 {
			t.Errorf("%s: expected 5 users (one per role), got %d", tenant.name, len(tenant.users))
		}
		roles := map[string]bool{}
		emails := map[string]bool{}
		for _, u := range tenant.users {
			roles[u.role] = true
			if emails[u.email] {
				t.Errorf("%s: duplicate user email %q", tenant.name, u.email)
			}
			emails[u.email] = true
		}
		for _, role := range []string{"admin", "agronomist", "producer", "auditor", "hauler"} {
			if !roles[role] {
				t.Errorf("%s: missing role %q in users", tenant.name, role)
			}
		}
	}
}

// TestDemoPlanCampaigns asserts each tenant seeds 2-3 season-spanning campaigns
// whose date windows are well formed (SD1: "2-3 campaigns per tenant").
func TestDemoPlanCampaigns(t *testing.T) {
	for _, tenant := range demoPlan() {
		if len(tenant.campaigns) < 2 || len(tenant.campaigns) > 3 {
			t.Errorf("%s: expected 2-3 campaigns, got %d", tenant.name, len(tenant.campaigns))
		}
		names := map[string]bool{}
		for _, c := range tenant.campaigns {
			if names[c.name] {
				t.Errorf("%s: duplicate campaign %q", tenant.name, c.name)
			}
			names[c.name] = true
			start, err := time.Parse("2006-01-02", c.start)
			if err != nil {
				t.Errorf("%s: campaign %q bad start %q: %v", tenant.name, c.name, c.start, err)
				continue
			}
			end, err := time.Parse("2006-01-02", c.end)
			if err != nil {
				t.Errorf("%s: campaign %q bad end %q: %v", tenant.name, c.name, c.end, err)
				continue
			}
			if end.Before(start) {
				t.Errorf("%s: campaign %q ends before it starts", tenant.name, c.name)
			}
		}
	}
}

// TestDemoPlanApplications asserts each tenant seeds 8-15 applications spread
// across lots, products, doses and dates, with mixed operator assignment
// (including NULL operator_id) to drive the dashboard charts (SD1).
func TestDemoPlanApplications(t *testing.T) {
	for _, tenant := range demoPlan() {
		apps := tenant.applications
		if len(apps) < 8 || len(apps) > 15 {
			t.Errorf("%s: expected 8-15 applications, got %d", tenant.name, len(apps))
		}
		if err := assertApplicationVariation(tenant); err != nil {
			t.Errorf("%s: %v", tenant.name, err)
		}
	}
}

// assertApplicationVariation checks breadth (lots/products/doses/dates),
// operator mix (NULL + assigned) and referential integrity of every
// application against the tenant's own plan data.
func assertApplicationVariation(tenant demoTenant) error {
	lotNames := map[string]bool{}
	campaignNames := map[string]bool{}
	campaignDates := map[string]struct{ start, end time.Time }{}
	emailToUser := map[string]bool{}

	for _, l := range tenant.lots {
		lotNames[l] = true
	}
	for _, c := range tenant.campaigns {
		campaignNames[c.name] = true
		start, _ := time.Parse("2006-01-02", c.start)
		end, _ := time.Parse("2006-01-02", c.end)
		campaignDates[c.name] = struct{ start, end time.Time }{start, end}
	}
	for _, u := range tenant.users {
		emailToUser[u.email] = true
	}

	products := map[string]bool{}
	doses := map[string]bool{}
	dates := map[string]bool{}
	lotsUsed := map[string]bool{}
	var nullOps, assignedOps int

	for _, a := range tenant.applications {
		products[a.product] = true
		doses[a.dose] = true
		dates[a.appliedAt] = true
		lotsUsed[a.lotName] = true

		if a.operatorEmail == "" {
			nullOps++
		} else {
			assignedOps++
			if !emailToUser[a.operatorEmail] {
				return fmt.Errorf("application on %q references unknown operator %q", a.lotName, a.operatorEmail)
			}
		}
		if !lotNames[a.lotName] {
			return fmt.Errorf("application references unknown lot %q", a.lotName)
		}
		if !campaignNames[a.campaignName] {
			return fmt.Errorf("application references unknown campaign %q", a.campaignName)
		}
		applied, err := time.Parse(time.RFC3339, a.appliedAt)
		if err != nil {
			return fmt.Errorf("application on %q has bad applied_at %q", a.lotName, a.appliedAt)
		}
		window := campaignDates[a.campaignName]
		if applied.Before(window.start) || applied.After(window.end) {
			return fmt.Errorf("application on %q outside campaign %q window", a.lotName, a.campaignName)
		}
	}

	if len(products) < 5 {
		return fmt.Errorf("only %d distinct products; need >= 5 for chart variety", len(products))
	}
	if len(doses) < 5 {
		return fmt.Errorf("only %d distinct doses; need >= 5", len(doses))
	}
	if len(dates) < 5 {
		return fmt.Errorf("only %d distinct dates; need >= 5 spread across the season", len(dates))
	}
	if len(lotsUsed) < 2 {
		return fmt.Errorf("applications use %d lot(s); want >= 2", len(lotsUsed))
	}
	if nullOps == 0 || assignedOps == 0 {
		return fmt.Errorf("operator mix broken: NULL=%d assigned=%d; want both present", nullOps, assignedOps)
	}
	return nil
}
