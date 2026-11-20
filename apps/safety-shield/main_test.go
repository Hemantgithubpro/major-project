package main

import (
	"testing"
	"time"
)

func fptr(f float64) *float64 { return &f }
func bptr(b bool) *bool       { return &b }

func baseReq() evaluateRequest {
	now := time.Date(2026, 11, 20, 18, 0, 5, 0, time.UTC)
	return evaluateRequest{
		DecisionUID: "d-1",
		Action:      "SCALE_UP",
		State: state{
			Service:               "payment-service",
			Replicas:              1,
			MinReplicas:           1,
			MaxReplicas:           4,
			DBConnPct:             0.50,
			DBConnThreshold:       0.70,
			PredictionTS:          now.Add(-5 * time.Second).Format(time.RFC3339),
			Now:                   now.Format(time.RFC3339),
			PredictionMaxAgeS:     60,
			LastActionAt:          now.Add(-120 * time.Second).Format(time.RFC3339),
			CooldownS:             60,
			ActionsInWindow:       0,
			ActionBudgetPerWindow: 3,
			BudgetRemainingUSD:    fptr(8.5),
			TransactionStateOK:    bptr(true),
		},
	}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*evaluateRequest)
		wantAllow bool
		wantID    string
	}{
		{
			name:      "allow scale up",
			mutate:    func(r *evaluateRequest) {},
			wantAllow: true,
		},
		{
			name:      "block O2 db ceiling",
			mutate:    func(r *evaluateRequest) { r.State.DBConnPct = 0.85 },
			wantAllow: false,
			wantID:    "INV-O2",
		},
		{
			name:      "allow fresh prediction",
			mutate:    func(r *evaluateRequest) { r.State.PredictionTS = r.State.Now },
			wantAllow: true,
		},
		{
			name: "block O5 old prediction",
			mutate: func(r *evaluateRequest) {
				ts, _ := time.Parse(time.RFC3339, r.State.Now)
				r.State.PredictionTS = ts.Add(-120 * time.Second).Format(time.RFC3339)
			},
			wantAllow: false,
			wantID:    "INV-O5",
		},
		{
			name:      "block O5 invalid prediction ts",
			mutate:    func(r *evaluateRequest) { r.State.PredictionTS = "not-a-time" },
			wantAllow: false,
			wantID:    "INV-O5",
		},
		{
			name:      "block O3 missing decision id",
			mutate:    func(r *evaluateRequest) { r.DecisionUID = "" },
			wantAllow: false,
			wantID:    "INV-O3",
		},
		{
			name: "block O1 scale up at max",
			mutate: func(r *evaluateRequest) {
				r.State.Replicas = 4
				r.State.MaxReplicas = 4
			},
			wantAllow: false,
			wantID:    "INV-O1",
		},
		{
			name: "block O1 scale down at min",
			mutate: func(r *evaluateRequest) {
				r.Action = "SCALE_DOWN"
				r.State.Replicas = 1
				r.State.MinReplicas = 1
			},
			wantAllow: false,
			wantID:    "INV-O1",
		},
		{
			name: "block O4 cooldown",
			mutate: func(r *evaluateRequest) {
				ts, _ := time.Parse(time.RFC3339, r.State.Now)
				r.State.LastActionAt = ts.Add(-10 * time.Second).Format(time.RFC3339)
			},
			wantAllow: false,
			wantID:    "INV-O4",
		},
		{
			name: "block O4 action budget",
			mutate: func(r *evaluateRequest) {
				r.State.ActionsInWindow = 3
				r.State.ActionBudgetPerWindow = 3
			},
			wantAllow: false,
			wantID:    "INV-O4",
		},
		{
			name:      "block O6 budget exhausted",
			mutate:    func(r *evaluateRequest) { r.State.BudgetRemainingUSD = fptr(0) },
			wantAllow: false,
			wantID:    "INV-O6",
		},
		{
			name:      "block O7 transaction state",
			mutate:    func(r *evaluateRequest) { r.State.TransactionStateOK = bptr(false) },
			wantAllow: false,
			wantID:    "INV-O7",
		},
		{
			name:      "block O0 unknown action",
			mutate:    func(r *evaluateRequest) { r.Action = "RESTART" },
			wantAllow: false,
			wantID:    "INV-O0",
		},
		{
			name:      "allow NO_ACTION",
			mutate:    func(r *evaluateRequest) { r.Action = "NO_ACTION" },
			wantAllow: true,
		},
		{
			name: "NO_ACTION exempt from cooldown and budget",
			mutate: func(r *evaluateRequest) {
				r.Action = "NO_ACTION"
				r.State.ActionsInWindow = 99
				r.State.BudgetRemainingUSD = fptr(0)
				ts, _ := time.Parse(time.RFC3339, r.State.Now)
				r.State.LastActionAt = ts.Add(-1 * time.Second).Format(time.RFC3339)
			},
			wantAllow: true,
		},
		{
			name: "allow SCALE_DOWN",
			mutate: func(r *evaluateRequest) {
				r.Action = "SCALE_DOWN"
				r.State.Replicas = 2
			},
			wantAllow: true,
		},
		{
			name: "priority O5 before O2",
			mutate: func(r *evaluateRequest) {
				ts, _ := time.Parse(time.RFC3339, r.State.Now)
				r.State.PredictionTS = ts.Add(-120 * time.Second).Format(time.RFC3339)
				r.State.DBConnPct = 0.95
			},
			wantAllow: false,
			wantID:    "INV-O5",
		},
		{
			name: "priority O2 before O4",
			mutate: func(r *evaluateRequest) {
				r.State.DBConnPct = 0.95
				ts, _ := time.Parse(time.RFC3339, r.State.Now)
				r.State.LastActionAt = ts.Add(-10 * time.Second).Format(time.RFC3339)
			},
			wantAllow: false,
			wantID:    "INV-O2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			tc.mutate(&req)
			allow, id, reason := evaluate(req)
			if allow != tc.wantAllow {
				t.Fatalf("allow = %v, want %v (id=%s reason=%s)", allow, tc.wantAllow, id, reason)
			}
			if !tc.wantAllow && id != tc.wantID {
				t.Fatalf("invariant = %s, want %s (reason=%s)", id, tc.wantID, reason)
			}
			if tc.wantAllow && reason == "" {
				t.Fatalf("expected a reason for allow")
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	req := baseReq()
	req.State.DBConnPct = 0.85
	a1, id1, r1 := evaluate(req)
	a2, id2, r2 := evaluate(req)
	if a1 != a2 || id1 != id2 || r1 != r2 {
		t.Fatalf("evaluate is not deterministic: (%v,%s,%s) vs (%v,%s,%s)", a1, id1, r1, a2, id2, r2)
	}
}

func TestCatalogCoversRules(t *testing.T) {
	catalogIDs := map[string]bool{}
	for _, inv := range catalog {
		catalogIDs[inv.ID] = true
	}
	for _, r := range rules {
		if !catalogIDs[r.ID] {
			t.Fatalf("rule %s missing from catalog", r.ID)
		}
	}
}
