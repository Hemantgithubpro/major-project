package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"mvp/shared/httpx"
)

// invariantVersion is bumped whenever the catalog or its priority order changes.
const invariantVersion = 1

type evaluateRequest struct {
	DecisionUID string `json:"decision_uid"`
	Action      string `json:"action"`
	State       state  `json:"state"`
}

type state struct {
	Service               string   `json:"service"`
	Replicas              int      `json:"replicas"`
	MinReplicas           int      `json:"min_replicas"`
	MaxReplicas           int      `json:"max_replicas"`
	DBConnPct             float64  `json:"db_conn_pct"`
	DBConnThreshold       float64  `json:"db_conn_threshold"`
	PredictionTS          string   `json:"prediction_ts"`
	Now                   string   `json:"now"`
	PredictionMaxAgeS     float64  `json:"prediction_max_age_s"`
	LastActionAt          string   `json:"last_action_at"`
	CooldownS             float64  `json:"cooldown_s"`
	ActionsInWindow       int      `json:"actions_in_window"`
	ActionBudgetPerWindow int      `json:"action_budget_per_window"`
	BudgetRemainingUSD    *float64 `json:"budget_remaining_usd"`
	TransactionStateOK    *bool    `json:"transaction_state_ok"`
}

type evaluateResponse struct {
	Allow            bool   `json:"allow"`
	InvariantID      string `json:"invariant_id"`
	Reason           string `json:"reason"`
	InvariantVersion int    `json:"invariant_version"`
}

type invariant struct {
	ID          string `json:"id"`
	Family      string `json:"family"`
	Description string `json:"description"`
	Predicate   string `json:"predicate"`
	Enforcement string `json:"enforcement"`
}

var catalog = []invariant{
	{ID: "INV-O0", Family: "input", Description: "Unknown or unsupported action", Predicate: "action ∈ {NO_ACTION, SCALE_UP, SCALE_DOWN, SHED_LOAD, ENABLE_CIRCUIT_BREAKER}", Enforcement: "shield"},
	{ID: "INV-O5", Family: "freshness", Description: "Prediction must be fresh", Predicate: "now - prediction_ts <= prediction_max_age_s", Enforcement: "shield"},
	{ID: "INV-O3", Family: "idempotency", Description: "Decision must carry an id; uniqueness is enforced by the controller ledger", Predicate: "decision_uid != ''", Enforcement: "shield+controller"},
	{ID: "INV-O1", Family: "operational", Description: "Replica bounds", Predicate: "min_replicas <= replicas <= max_replicas; SCALE_UP only if replicas < max; SCALE_DOWN only if replicas > min", Enforcement: "shield"},
	{ID: "INV-O2", Family: "operational", Description: "DB connection ceiling", Predicate: "not (action == SCALE_UP and db_conn_pct >= db_conn_threshold)", Enforcement: "shield"},
	{ID: "INV-O4", Family: "operational", Description: "Cooldown and action budget", Predicate: "now - last_action_at >= cooldown_s and actions_in_window < action_budget_per_window", Enforcement: "shield"},
	{ID: "INV-O6", Family: "operational", Description: "Budget ceiling", Predicate: "budget_remaining_usd > 0 (when supplied)", Enforcement: "shield"},
	{ID: "INV-O7", Family: "transaction", Description: "Transaction non-regression", Predicate: "transaction_state_ok != false (when supplied)", Enforcement: "shield"},
}

type rule struct {
	ID    string
	check func(req evaluateRequest, s state) (bool, string)
}

// rules are evaluated in this exact priority order; the first violation is returned.
var rules = []rule{
	{ID: "INV-O0", check: checkKnownAction},
	{ID: "INV-O5", check: checkFreshness},
	{ID: "INV-O3", check: checkDecisionID},
	{ID: "INV-O1", check: checkReplicaBounds},
	{ID: "INV-O2", check: checkDBConnCeiling},
	{ID: "INV-O4", check: checkCooldown},
	{ID: "INV-O6", check: checkBudget},
	{ID: "INV-O7", check: checkTransactionState},
}

func checkKnownAction(req evaluateRequest, s state) (bool, string) {
	switch req.Action {
	case "NO_ACTION", "SCALE_UP", "SCALE_DOWN", "SHED_LOAD", "ENABLE_CIRCUIT_BREAKER":
		return false, ""
	}
	return true, fmt.Sprintf("unknown action %q", req.Action)
}

func parseTS(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func checkFreshness(req evaluateRequest, s state) (bool, string) {
	if s.PredictionTS == "" {
		return true, "missing prediction timestamp"
	}
	pt, err := parseTS(s.PredictionTS)
	if err != nil {
		return true, "invalid prediction timestamp: " + err.Error()
	}
	if s.Now == "" {
		return true, "missing now timestamp"
	}
	now, err := parseTS(s.Now)
	if err != nil {
		return true, "invalid now timestamp: " + err.Error()
	}
	age := now.Sub(pt).Seconds()
	if age < 0 {
		return true, fmt.Sprintf("prediction timestamp is %.1fs in the future", -age)
	}
	if age > s.PredictionMaxAgeS {
		return true, fmt.Sprintf("prediction is %.1fs old (max %.1fs)", age, s.PredictionMaxAgeS)
	}
	return false, ""
}

func checkDecisionID(req evaluateRequest, s state) (bool, string) {
	if req.DecisionUID == "" {
		return true, "missing decision_uid"
	}
	return false, ""
}

func checkReplicaBounds(req evaluateRequest, s state) (bool, string) {
	switch req.Action {
	case "SCALE_UP":
		if s.Replicas >= s.MaxReplicas {
			return true, fmt.Sprintf("replicas %d already at max %d", s.Replicas, s.MaxReplicas)
		}
	case "SCALE_DOWN":
		if s.Replicas <= s.MinReplicas {
			return true, fmt.Sprintf("replicas %d already at min %d", s.Replicas, s.MinReplicas)
		}
	}
	if s.Replicas < s.MinReplicas || s.Replicas > s.MaxReplicas {
		return true, fmt.Sprintf("replicas %d outside bounds [%d,%d]", s.Replicas, s.MinReplicas, s.MaxReplicas)
	}
	return false, ""
}

func checkDBConnCeiling(req evaluateRequest, s state) (bool, string) {
	if req.Action == "SCALE_UP" && s.DBConnPct >= s.DBConnThreshold {
		return true, fmt.Sprintf("db_conn_pct %.2f >= threshold %.2f; scale-up would add DB connections", s.DBConnPct, s.DBConnThreshold)
	}
	return false, ""
}

func checkCooldown(req evaluateRequest, s state) (bool, string) {
	if s.ActionsInWindow >= s.ActionBudgetPerWindow {
		return true, fmt.Sprintf("action budget reached (%d/%d) in this window", s.ActionsInWindow, s.ActionBudgetPerWindow)
	}
	if s.LastActionAt == "" {
		return false, ""
	}
	la, err := parseTS(s.LastActionAt)
	if err != nil {
		return true, "invalid last_action_at: " + err.Error()
	}
	now, err := parseTS(s.Now)
	if err != nil {
		return true, "invalid now timestamp: " + err.Error()
	}
	elapsed := now.Sub(la).Seconds()
	if elapsed < s.CooldownS {
		return true, fmt.Sprintf("cooldown active: %.1fs since last action (need %.1fs)", elapsed, s.CooldownS)
	}
	return false, ""
}

func checkBudget(req evaluateRequest, s state) (bool, string) {
	if s.BudgetRemainingUSD != nil && *s.BudgetRemainingUSD <= 0 {
		return true, fmt.Sprintf("budget exhausted (remaining %.4f USD)", *s.BudgetRemainingUSD)
	}
	return false, ""
}

func checkTransactionState(req evaluateRequest, s state) (bool, string) {
	if s.TransactionStateOK != nil && !*s.TransactionStateOK {
		return true, "transaction invariants are not satisfied"
	}
	return false, ""
}

// evaluate is the pure decision function. Same input -> same output.
func evaluate(req evaluateRequest) (bool, string, string) {
	for _, r := range rules {
		if violated, reason := r.check(req, req.State); violated {
			return false, r.ID, reason
		}
	}
	return true, "", "all invariants satisfied"
}

func main() {
	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /evaluate", func(w http.ResponseWriter, r *http.Request) {
		var req evaluateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJSON(w, 400, evaluateResponse{
				Allow: false, InvariantID: "INV-O0", Reason: "invalid request: " + err.Error(), InvariantVersion: invariantVersion,
			})
			return
		}
		allow, id, reason := evaluate(req)
		httpx.WriteJSON(w, 200, evaluateResponse{Allow: allow, InvariantID: id, Reason: reason, InvariantVersion: invariantVersion})
	})
	mux.HandleFunc("GET /invariants", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"version": invariantVersion, "invariants": catalog})
	})
	port := httpx.Env("PORT", "8087")
	s := &http.Server{Addr: ":" + port, Handler: httpx.CORS(mux), ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
