package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"mvp/shared/db"
	"mvp/shared/httpx"
	"mvp/shared/metrics"
)

// This controller is a singleton by design. There is no leader election and no
// multi-replica coordination: one process owns the decision loop for the testbed.

type config struct {
	port            string
	gatewayURL      string
	riskURL         string
	shieldURL       string
	mlURL           string
	interval        time.Duration
	verifyAfter     time.Duration
	riskThreshold   float64
	dbConnThreshold float64
	cooldown        time.Duration
	budget          int
	baseline        bool
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func loadConfig() config {
	return config{
		port:            httpx.Env("PORT", "8086"),
		gatewayURL:      httpx.Env("GATEWAY_URL", "http://gateway:8080"),
		riskURL:         httpx.Env("RISK_URL", "http://risk:8084"),
		shieldURL:       httpx.Env("SHIELD_URL", "http://safety-shield:8087"),
		mlURL:           httpx.Env("ML_URL", "http://ml-service:9000"),
		interval:        envDur("CONTROLLER_INTERVAL", 15*time.Second),
		verifyAfter:     envDur("VERIFY_AFTER", 30*time.Second),
		riskThreshold:   envFloat("RISK_THRESHOLD", 0.60),
		dbConnThreshold: envFloat("DB_CONN_THRESHOLD", 0.70),
		cooldown:        envDur("COOLDOWN", 60*time.Second),
		budget:          envInt("ACTION_BUDGET", 3),
		baseline:        strings.EqualFold(httpx.Env("BASELINE_MODE", "false"), "true"),
	}
}

type shieldState struct {
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

type prediction struct {
	FailureProbability float64          `json:"failure_probability"`
	RiskLevel          string           `json:"risk_level"`
	HorizonMinutes     int              `json:"horizon_minutes"`
	PredictedCause     *string          `json:"predicted_cause"`
	TopFeatures        []map[string]any `json:"top_features"`
	ModelVersion       string           `json:"model_version"`
	TS                 string           `json:"ts"`
}

type verdict struct {
	Allow            bool   `json:"allow"`
	InvariantID      string `json:"invariant_id"`
	Reason           string `json:"reason"`
	InvariantVersion int    `json:"invariant_version"`
}

type decision struct {
	DecisionUID   string           `json:"decision_uid"`
	Service       string           `json:"service"`
	PolicyAction  string           `json:"policy_action"`
	ShieldAction  string           `json:"shield_action"`
	Verdict       string           `json:"verdict"`
	BlockedBy     string           `json:"blocked_by"`
	Reason        string           `json:"reason"`
	Score         float64          `json:"score"`
	State         map[string]any   `json:"state_snapshot"`
	Prediction    map[string]any   `json:"prediction"`
	CreatedAt     time.Time        `json:"created_at"`
	Actions       []map[string]any `json:"actions,omitempty"`
	Verifications []map[string]any `json:"verifications,omitempty"`
}

type controller struct {
	cfg       config
	pool      *pgxpool.Pool
	collector *collector
	client    *http.Client
}

func postJSON(ctx context.Context, url string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Correlation-ID", "ctrl-"+uuid.NewString())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *controller) predict(ctx context.Context, snap snapshot) (prediction, error) {
	b, err := postJSON(ctx, c.cfg.mlURL+"/risk", map[string]any{
		"service":  "payment-service",
		"features": snap.Features,
	})
	if err != nil {
		return prediction{}, err
	}
	var p prediction
	if err := json.Unmarshal(b, &p); err != nil {
		return prediction{}, fmt.Errorf("malformed prediction: %w", err)
	}
	if p.FailureProbability < 0 || p.FailureProbability > 1 {
		return prediction{}, fmt.Errorf("prediction out of range: %f", p.FailureProbability)
	}
	p.TS = time.Now().UTC().Format(time.RFC3339)
	return p, nil
}

func (c *controller) buildState(ctx context.Context, snap snapshot, predTS string) (shieldState, error) {
	var lastAt sql.NullTime
	var count int
	if err := c.pool.QueryRow(ctx, `
		SELECT max(executed_at), count(*)::int
		FROM reliability_svc.actions
		WHERE status = 'EXECUTED' AND action <> 'NO_ACTION'
		  AND executed_at > now() - interval '5 minutes'
	`).Scan(&lastAt, &count); err != nil {
		return shieldState{}, err
	}
	pt, err := time.Parse(time.RFC3339, predTS)
	if err != nil {
		return shieldState{}, err
	}
	s := shieldState{
		Service:               "payment-service",
		Replicas:              1,
		MinReplicas:           1,
		MaxReplicas:           4,
		DBConnPct:             snap.Features.DBConnPct,
		DBConnThreshold:       c.cfg.dbConnThreshold,
		PredictionTS:          pt.UTC().Format(time.RFC3339),
		Now:                   time.Now().UTC().Format(time.RFC3339),
		PredictionMaxAgeS:     60,
		CooldownS:             c.cfg.cooldown.Seconds(),
		ActionsInWindow:       count,
		ActionBudgetPerWindow: c.cfg.budget,
	}
	if lastAt.Valid {
		s.LastActionAt = lastAt.Time.UTC().Format(time.RFC3339)
	}
	return s, nil
}

// evaluate asks the shield. Any transport error or malformed reply fails closed.
func (c *controller) evaluate(ctx context.Context, uid, action string, s shieldState) (verdict, error) {
	b, err := postJSON(ctx, c.cfg.shieldURL+"/evaluate", map[string]any{
		"decision_uid": uid,
		"action":       action,
		"state":        s,
	})
	if err != nil {
		return verdict{}, err
	}
	var v verdict
	if err := json.Unmarshal(b, &v); err != nil {
		return verdict{}, fmt.Errorf("malformed verdict: %w", err)
	}
	if v.InvariantVersion <= 0 || v.Reason == "" || (!v.Allow && v.InvariantID == "") {
		return verdict{}, fmt.Errorf("malformed verdict payload: %s", strings.TrimSpace(string(b)))
	}
	return v, nil
}

func (c *controller) execute(ctx context.Context, uid, action string) (map[string]any, error) {
	switch action {
	case "NO_ACTION":
		return map[string]any{"executed": false}, nil
	case "SCALE_UP", "SCALE_DOWN":
		// Recorded only: real cloud scaling is out of scope for the testbed.
		return map[string]any{"executed": false, "simulated": true, "note": "recorded only; no real replicas changed"}, nil
	case "SHED_LOAD":
		data, err := postJSON(ctx, c.cfg.gatewayURL+"/admin/shed", map[string]any{"enabled": true})
		return map[string]any{"response": strings.TrimSpace(string(data))}, err
	case "ENABLE_CIRCUIT_BREAKER":
		data, err := postJSON(ctx, c.cfg.riskURL+"/chaos/fail", map[string]any{"enabled": true})
		return map[string]any{"response": strings.TrimSpace(string(data))}, err
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

func (c *controller) insertDecision(ctx context.Context, d decision) (bool, error) {
	state, _ := json.Marshal(d.State)
	pred, _ := json.Marshal(d.Prediction)
	tag, err := c.pool.Exec(ctx, `
		INSERT INTO reliability_svc.decisions
		  (decision_uid, service, policy_action, shield_action, verdict, blocked_by, reason, score, state_snapshot, prediction)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (decision_uid) DO NOTHING
	`, d.DecisionUID, d.Service, d.PolicyAction, d.ShieldAction, d.Verdict, nullable(d.BlockedBy), d.Reason, d.Score, state, pred)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (c *controller) verify(ctx context.Context, uid string, before snapshot, action string) {
	select {
	case <-time.After(c.cfg.verifyAfter):
	case <-ctx.Done():
		return
	}
	after, err := c.collector.collect()
	if err != nil {
		_, _ = c.pool.Exec(ctx, `
			INSERT INTO reliability_svc.verifications(decision_uid, metrics_before, improved, invariants_ok)
			VALUES ($1,$2,false,false)
		`, uid, mustJSON(before.Features))
		return
	}
	b, a := before.Features, after.Features
	improved := a.P95LatencyMS < b.P95LatencyMS*0.9 && a.DBConnPct <= b.DBConnPct

	// Post-action invariant check: the shield must still be reachable and healthy.
	state, err := c.buildState(ctx, after, time.Now().UTC().Format(time.RFC3339))
	invariantsOK := false
	if err == nil {
		if v, err := c.evaluate(ctx, uid, "NO_ACTION", state); err == nil && v.Allow {
			invariantsOK = true
		}
	}
	_, _ = c.pool.Exec(ctx, `
		INSERT INTO reliability_svc.verifications(decision_uid, metrics_before, metrics_after, improved, invariants_ok)
		VALUES ($1,$2,$3,$4,$5)
	`, uid, mustJSON(b), mustJSON(a), improved, invariantsOK)
	httpx.Log("reliability-controller", fmt.Sprintf("verified %s action=%s improved=%v invariants_ok=%v", uid, action, improved, invariantsOK), "")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (c *controller) cycle(ctx context.Context) (decision, error) {
	uid := uuid.NewString()
	snap, err := c.collector.collect()
	if err != nil {
		return c.failClosed(ctx, uid, "metric collection failed: "+err.Error())
	}
	pred, err := c.predict(ctx, snap)
	if err != nil {
		return c.failClosed(ctx, uid, "ML prediction failed: "+err.Error())
	}
	state, err := c.buildState(ctx, snap, pred.TS)
	if err != nil {
		return c.failClosed(ctx, uid, "state snapshot failed: "+err.Error())
	}

	stateJSON := map[string]any{
		"service":           state.Service,
		"features":          snap.Features,
		"raw":               snap.Raw,
		"replicas":          state.Replicas,
		"min_replicas":      state.MinReplicas,
		"max_replicas":      state.MaxReplicas,
		"db_conn_pct":       state.DBConnPct,
		"db_conn_threshold": state.DBConnThreshold,
		"prediction_ts":     state.PredictionTS,
		"now":               state.Now,
		"last_action_at":    state.LastActionAt,
		"cooldown_s":        state.CooldownS,
		"actions_in_window": state.ActionsInWindow,
		"action_budget":     state.ActionBudgetPerWindow,
	}
	predJSON := map[string]any{
		"failure_probability": pred.FailureProbability,
		"risk_level":          pred.RiskLevel,
		"predicted_cause":     pred.PredictedCause,
		"top_features":        pred.TopFeatures,
		"model_version":       pred.ModelVersion,
		"ts":                  pred.TS,
	}

	d := decision{
		DecisionUID: uid,
		Service:     "payment-service",
		Score:       pred.FailureProbability,
		State:       stateJSON,
		Prediction:  predJSON,
	}

	var chosen string
	var blockedBy string
	var reason string
	verdictName := "ALLOWED"

	if pred.FailureProbability < c.cfg.riskThreshold {
		chosen = "NO_ACTION"
		reason = fmt.Sprintf("risk %.3f below threshold %.2f", pred.FailureProbability, c.cfg.riskThreshold)
		if v, err := c.evaluate(ctx, uid, chosen, state); err != nil {
			return c.failClosed(ctx, uid, "shield unavailable on NO_ACTION: "+err.Error())
		} else if !v.Allow {
			return c.failClosed(ctx, uid, "NO_ACTION vetoed by "+v.InvariantID+": "+v.Reason)
		}
	} else if c.cfg.baseline {
		// Baseline mode: never call the shield. Demonstrates unconstrained harm.
		chosen = "SCALE_UP"
		verdictName = "BASELINE"
		reason = "baseline mode: shield not consulted; scale-up proposed on risk alone"
	} else {
		candidates := []string{"SCALE_UP", "SHED_LOAD", "ENABLE_CIRCUIT_BREAKER", "NO_ACTION"}
		for _, candidate := range candidates {
			v, err := c.evaluate(ctx, uid, candidate, state)
			if err != nil {
				return c.failClosed(ctx, uid, "shield unavailable on "+candidate+": "+err.Error())
			}
			if v.Allow {
				chosen = candidate
				reason = v.Reason
				break
			}
			if blockedBy == "" {
				blockedBy = v.InvariantID
				verdictName = "BLOCKED"
				reason = v.Reason
			}
		}
		if chosen == "" {
			return c.failClosed(ctx, uid, "all candidate actions vetoed; first veto "+blockedBy+": "+reason)
		}
	}

	d.PolicyAction = func() string {
		if verdictName == "BLOCKED" {
			return "SCALE_UP"
		}
		return chosen
	}()
	d.ShieldAction = chosen
	d.Verdict = verdictName
	d.BlockedBy = blockedBy
	d.Reason = reason

	inserted, err := c.insertDecision(ctx, d)
	if err != nil {
		return decision{}, err
	}
	if !inserted {
		return c.getDecision(ctx, uid)
	}

	// Ledger before action: the row above is committed before anything executes.
	_, err = c.pool.Exec(ctx, `
		INSERT INTO reliability_svc.actions(decision_uid, action, status, result)
		VALUES ($1,$2,'PLANNED',$3)
	`, uid, chosen, mustJSON(map[string]any{"proposed": d.PolicyAction, "risk": pred.FailureProbability}))
	if err != nil {
		return d, err
	}

	result, execErr := c.execute(ctx, uid, chosen)
	status := "EXECUTED"
	if execErr != nil {
		status = "FAILED"
	}
	if execErr != nil {
		_, _ = c.pool.Exec(ctx, `
			UPDATE reliability_svc.actions SET status=$1, error=$2, executed_at=now()
			WHERE decision_uid=$3 AND action=$4
		`, status, execErr.Error(), uid, chosen)
	} else {
		_, _ = c.pool.Exec(ctx, `
			UPDATE reliability_svc.actions SET status=$1, result=$2, executed_at=now()
			WHERE decision_uid=$3 AND action=$4
		`, status, mustJSON(result), uid, chosen)
	}

	go c.verify(context.Background(), uid, snap, chosen)
	return c.getDecision(ctx, uid)
}

func (c *controller) failClosed(ctx context.Context, uid, why string) (decision, error) {
	d := decision{
		DecisionUID:  uid,
		Service:      "payment-service",
		PolicyAction: "NO_ACTION",
		ShieldAction: "NO_ACTION",
		Verdict:      "FAIL_CLOSED",
		Reason:       why,
		State:        map[string]any{},
		Prediction:   map[string]any{},
	}
	if _, err := c.insertDecision(ctx, d); err != nil {
		return decision{}, err
	}
	_, _ = c.pool.Exec(ctx, `
		INSERT INTO reliability_svc.actions(decision_uid, action, status, result)
		VALUES ($1,'NO_ACTION','EXECUTED',$2)
	`, uid, mustJSON(map[string]any{"note": "fail closed; nothing executed"}))
	return d, nil
}

func (c *controller) getDecision(ctx context.Context, uid string) (decision, error) {
	var d decision
	var blocked sql.NullString
	var stateJSON, predJSON []byte
	err := c.pool.QueryRow(ctx, `
		SELECT decision_uid, service, policy_action, shield_action, verdict,
		       blocked_by, reason, score, state_snapshot, prediction, created_at
		FROM reliability_svc.decisions WHERE decision_uid=$1
	`, uid).Scan(&d.DecisionUID, &d.Service, &d.PolicyAction, &d.ShieldAction, &d.Verdict,
		&blocked, &d.Reason, &d.Score, &stateJSON, &predJSON, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	if blocked.Valid {
		d.BlockedBy = blocked.String
	}
	_ = json.Unmarshal(stateJSON, &d.State)
	_ = json.Unmarshal(predJSON, &d.Prediction)

	rows, err := c.pool.Query(ctx, `
		SELECT action, status, executed_at, coalesce(result,'{}'::jsonb), coalesce(error,'')
		FROM reliability_svc.actions WHERE decision_uid=$1 ORDER BY id
	`, uid)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var a map[string]any
		var action, status, errMsg string
		var at time.Time
		var result []byte
		if err := rows.Scan(&action, &status, &at, &result, &errMsg); err != nil {
			rows.Close()
			return d, err
		}
		a = map[string]any{"action": action, "status": status, "executed_at": at, "error": errMsg}
		var resultObj map[string]any
		if err := json.Unmarshal(result, &resultObj); err == nil {
			a["result"] = resultObj
		}
		d.Actions = append(d.Actions, a)
	}
	rows.Close()

	rows, err = c.pool.Query(ctx, `
		SELECT coalesce(metrics_after,'{}'::jsonb), coalesce(improved,false),
		       coalesce(invariants_ok,false), created_at
		FROM reliability_svc.verifications WHERE decision_uid=$1 ORDER BY id
	`, uid)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var after []byte
		var improved, ok bool
		var at time.Time
		if err := rows.Scan(&after, &improved, &ok, &at); err != nil {
			rows.Close()
			return d, err
		}
		v := map[string]any{"metrics_after": json.RawMessage(after), "improved": improved, "invariants_ok": ok, "created_at": at}
		d.Verifications = append(d.Verifications, v)
	}
	rows.Close()
	return d, nil
}

func (c *controller) listDecisions(ctx context.Context, limit int) ([]decision, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT decision_uid FROM reliability_svc.decisions ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	var uids []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return nil, err
		}
		uids = append(uids, uid)
	}
	rows.Close()
	out := make([]decision, 0, len(uids))
	for _, uid := range uids {
		d, err := c.getDecision(ctx, uid)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (c *controller) latestPrediction(ctx context.Context) (map[string]any, error) {
	var pred, state []byte
	var score float64
	var at time.Time
	err := c.pool.QueryRow(ctx, `
		SELECT prediction, state_snapshot, score, created_at
		FROM reliability_svc.decisions
		WHERE service='payment-service' AND prediction <> '{}'::jsonb
		ORDER BY created_at DESC LIMIT 1
	`).Scan(&pred, &state, &score, &at)
	if err != nil {
		return nil, err
	}
	var p, s map[string]any
	_ = json.Unmarshal(pred, &p)
	_ = json.Unmarshal(state, &s)
	return map[string]any{
		"failure_probability": score,
		"prediction":          p,
		"features":            s["features"],
		"observed_at":         at,
	}, nil
}

func (c *controller) audit(ctx context.Context) (map[string]any, error) {
	resp, err := c.client.Get(c.cfg.shieldURL + "/invariants")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var shieldCatalog map[string]any
	if err := json.Unmarshal(body, &shieldCatalog); err != nil {
		return nil, fmt.Errorf("malformed shield catalog: %w", err)
	}
	rows, err := c.pool.Query(ctx, `
		SELECT blocked_by, count(*)::int
		FROM reliability_svc.decisions
		WHERE blocked_by IS NOT NULL
		GROUP BY blocked_by ORDER BY 2 DESC
	`)
	if err != nil {
		return nil, err
	}
	blocks := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		blocks[id] = n
	}
	rows.Close()
	var total, allowed, blocked, failClosed int
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE verdict='ALLOWED')::int,
		       count(*) FILTER (WHERE verdict='BLOCKED')::int,
		       count(*) FILTER (WHERE verdict='FAIL_CLOSED')::int
		FROM reliability_svc.decisions
	`).Scan(&total, &allowed, &blocked, &failClosed); err != nil {
		return nil, err
	}
	return map[string]any{
		"shield":              shieldCatalog,
		"blocks_by_invariant": blocks,
		"decisions": map[string]any{
			"total": total, "allowed": allowed, "blocked": blocked, "fail_closed": failClosed,
		},
		"singleton":     true,
		"baseline_mode": c.cfg.baseline,
	}, nil
}

func main() {
	cfg := loadConfig()
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()
	c := &controller{
		cfg:  cfg,
		pool: p,
		collector: newCollector(map[string]string{
			"gateway": "http://gateway:8080/metrics",
			"payment": "http://payment:8083/metrics",
			"saga":    "http://saga:8085/metrics",
			"risk":    "http://risk:8084/metrics",
		}, 5*time.Second),
		client: &http.Client{Timeout: 5 * time.Second},
	}

	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /control/cycle", func(w http.ResponseWriter, r *http.Request) {
		d, err := c.cycle(r.Context())
		if err != nil {
			httpx.WriteJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, 200, d)
	})
	mux.HandleFunc("GET /decisions", func(w http.ResponseWriter, r *http.Request) {
		n := envInt("DECISION_LIMIT", 50)
		list, err := c.listDecisions(r.Context(), n)
		if err != nil {
			httpx.WriteJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, 200, list)
	})
	mux.HandleFunc("GET /predictions", func(w http.ResponseWriter, r *http.Request) {
		pred, err := c.latestPrediction(r.Context())
		if err != nil {
			httpx.WriteJSON(w, 404, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, 200, pred)
	})
	mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		a, err := c.audit(r.Context())
		if err != nil {
			httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, 200, a)
	})

	go func() {
		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := c.cycle(ctx); err != nil {
				httpx.Log("reliability-controller", "cycle error: "+err.Error(), "")
			}
		}
	}()

	httpx.Log("reliability-controller", fmt.Sprintf("started baseline=%v interval=%s risk_threshold=%.2f db_conn_threshold=%.2f", cfg.baseline, cfg.interval, cfg.riskThreshold, cfg.dbConnThreshold), "")
	s := &http.Server{Addr: ":" + cfg.port, Handler: metrics.Middleware("reliability-controller", httpx.CORS(mux)), ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
