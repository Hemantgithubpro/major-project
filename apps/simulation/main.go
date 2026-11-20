// Simulation service for the e-commerce storefront branch.
// It drives the existing testbed (never mocks it), collects real metrics and
// ledger rows, and asks the advisory GenAI service to explain the run.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mvp/shared/httpx"
)

type sample struct {
	name   string
	labels map[string]string
	value  float64
}

type orderResult struct {
	status  int
	body    []byte
	latency time.Duration
	err     error
}

type runStats struct {
	Scenario             string   `json:"scenario"`
	StartedAt            string   `json:"started_at"`
	DurationS            float64  `json:"duration_s"`
	OrdersPlaced         int      `json:"orders_placed"`
	OrdersOK             int      `json:"orders_ok"`
	OrdersFailed         int      `json:"orders_failed"`
	Shedded503           int      `json:"shedded_503"`
	P95BeforeMS          float64  `json:"p95_before_ms"`
	P95AfterMS           float64  `json:"p95_after_ms"`
	DBConnPctBefore      float64  `json:"db_conn_pct_before"`
	DBConnPctAfter       float64  `json:"db_conn_pct_after"`
	DBPoolWaiting        float64  `json:"db_pool_waiting"`
	DBLatencyInjectedMS  float64  `json:"db_latency_injected_ms"`
	RiskScore            float64  `json:"risk_score"`
	RiskLevel            string   `json:"risk_level"`
	PredictedCause       string   `json:"predicted_cause"`
	NewDecisions         int      `json:"new_decisions"`
	VetoInvariant        string   `json:"veto_invariant"`
	ShieldAction         string   `json:"shield_action"`
	VerificationImproved *bool    `json:"verification_improved"`
	IncidentID           string   `json:"incident_id,omitempty"`
	Scenarios            []string `json:"available_scenarios,omitempty"`
}

type server struct {
	gateway    string
	payment    string
	controller string
	genai      string
	client     *http.Client
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	s := &server{
		gateway:    env("GATEWAY_URL", "http://gateway:8080"),
		payment:    env("PAYMENT_URL", "http://payment:8083"),
		controller: env("CONTROLLER_URL", "http://reliability-controller:8086"),
		genai:      env("GENAI_URL", "http://genai:9001"),
		client:     &http.Client{Timeout: 120 * time.Second},
	}
	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("GET /scenarios", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"scenarios": []string{"happy", "risk_outage", "db_bottleneck", "traffic_spike"}})
	})
	mux.HandleFunc("POST /run", s.run)
	port := env("PORT", "8088")
	_ = http.ListenAndServe(":"+port, httpx.CORS(mux))
}

func (s *server) postJSON(url string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Correlation-ID", "sim-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("%s: %d: %s", url, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (s *server) getJSON(url string, out any) error {
	resp, err := s.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func scrape(url string) ([]sample, error) {
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []sample
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexAny(line, "{ ")
		if idx < 0 {
			continue
		}
		s := sample{name: line[:idx], labels: map[string]string{}}
		rest := line[idx:]
		if line[idx] == '{' {
			end := strings.LastIndex(line, "}")
			if end < 0 {
				continue
			}
			for _, part := range strings.Split(line[idx+1:end], ",") {
				kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(kv) == 2 {
					s.labels[kv[0]] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
				}
			}
			rest = line[end+1:]
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		s.value = v
		out = append(out, s)
	}
	return out, scanner.Err()
}

func matches(labels, want map[string]string) bool {
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func gauge(samples []sample, name string, want map[string]string) float64 {
	for _, s := range samples {
		if s.name == name && matches(s.labels, want) {
			return s.value
		}
	}
	return 0
}

func delta(start, end []sample, name string, want map[string]string) float64 {
	var a, b float64
	for _, s := range start {
		if s.name == name && matches(s.labels, want) {
			a += s.value
		}
	}
	for _, s := range end {
		if s.name == name && matches(s.labels, want) {
			b += s.value
		}
	}
	if b < a {
		return 0
	}
	return b - a
}

func p95Delta(start, end []sample, name string, want map[string]string) float64 {
	type bucket struct {
		le    float64
		count float64
	}
	var buckets []bucket
	total := 0.0
	for _, s := range end {
		if s.name != name+"_bucket" || !matches(s.labels, want) {
			continue
		}
		le, err := strconv.ParseFloat(s.labels["le"], 64)
		if err != nil {
			continue
		}
		before := 0.0
		for _, p := range start {
			if p.name == s.name && p.labels["le"] == s.labels["le"] && matches(p.labels, want) {
				before = p.value
				break
			}
		}
		d := math.Max(0, s.value-before)
		if math.IsInf(le, 1) {
			total = d
			continue
		}
		buckets = append(buckets, bucket{le: le, count: d})
	}
	if total <= 0 {
		return 0
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].le < buckets[j].le })
	max := 0.0
	for _, b := range buckets {
		max = b.le
		if b.count >= 0.95*total {
			return b.le * 1000
		}
	}
	return max * 1000
}

func (s *server) metrics() ([]sample, error) {
	var all []sample
	for _, url := range []string{s.gateway + "/metrics", s.payment + "/metrics"} {
		ss, err := scrape(url)
		if err != nil {
			return nil, err
		}
		all = append(all, ss...)
	}
	return all, nil
}

func dbConnPct(samples []sample) float64 {
	acquired := gauge(samples, "db_pool_acquired", map[string]string{"service": "payment-service"})
	max := gauge(samples, "db_pool_max", map[string]string{"service": "payment-service"})
	if max <= 0 {
		return 0
	}
	return acquired / max
}

func (s *server) placeOrder(customer string) orderResult {
	start := time.Now()
	data, err := s.postJSON(s.gateway+"/api/orders", map[string]any{
		"customer_ref": customer,
		"amount":       129.5,
	})
	if err != nil {
		// postJSON returns the body in the error string for HTTP failures; recover status via a direct call.
		return orderResult{status: 0, body: data, latency: time.Since(start), err: err}
	}
	return orderResult{status: 200, body: data, latency: time.Since(start)}
}

// placeOrderStatus preserves the real HTTP status (including 503 from load shedding).
func (s *server) placeOrderStatus(customer string) orderResult {
	b, _ := json.Marshal(map[string]any{"customer_ref": customer, "amount": 129.5})
	req, _ := http.NewRequest(http.MethodPost, s.gateway+"/api/orders", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		return orderResult{latency: time.Since(start), err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return orderResult{status: resp.StatusCode, body: body, latency: time.Since(start)}
}

func fireLoad(s *server, rps float64, duration time.Duration, customer string) []orderResult {
	results := make(chan orderResult, 4096)
	var wg sync.WaitGroup
	ticker := time.NewTicker(time.Duration(float64(time.Second) / rps))
	defer ticker.Stop()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		<-ticker.C
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.placeOrderStatus(customer)
		}()
	}
	wg.Wait()
	close(results)
	var out []orderResult
	for r := range results {
		out = append(out, r)
	}
	return out
}

func (s *server) reset() {
	_, _ = s.postJSON(s.gateway+"/api/chaos/risk-outage", map[string]any{"enabled": false})
	_, _ = s.postJSON(s.gateway+"/api/chaos/db-latency", map[string]any{"enabled": false})
	_, _ = s.postJSON(s.gateway+"/admin/shed", map[string]any{"enabled": false})
	time.Sleep(500 * time.Millisecond)
}

func (s *server) decisionUIDs() map[string]bool {
	var list []map[string]any
	if err := s.getJSON(s.controller+"/decisions", &list); err != nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, d := range list {
		if uid, ok := d["decision_uid"].(string); ok {
			out[uid] = true
		}
	}
	return out
}

func (s *server) newDecisions(known map[string]bool) []map[string]any {
	var list []map[string]any
	if err := s.getJSON(s.controller+"/decisions", &list); err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, d := range list {
		uid, _ := d["decision_uid"].(string)
		if !known[uid] {
			out = append(out, d)
		}
	}
	return out
}

func (s *server) prediction() (float64, string, string) {
	var p struct {
		FailureProbability float64 `json:"failure_probability"`
		Prediction         struct {
			RiskLevel      string  `json:"risk_level"`
			PredictedCause *string `json:"predicted_cause"`
		} `json:"prediction"`
	}
	if err := s.getJSON(s.controller+"/predictions", &p); err != nil {
		return 0, "", ""
	}
	cause := ""
	if p.Prediction.PredictedCause != nil {
		cause = *p.Prediction.PredictedCause
	}
	return p.FailureProbability, p.Prediction.RiskLevel, cause
}

func (s *server) waitForVeto(known map[string]bool, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, d := range s.newDecisions(known) {
			if d["blocked_by"] == "INV-O2" {
				veto, _ := d["blocked_by"].(string)
				action, _ := d["shield_action"].(string)
				return veto, action
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", ""
}

func (s *server) waitForVerification(known map[string]bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, d := range s.newDecisions(known) {
			if vs, ok := d["verifications"].([]any); ok && len(vs) > 0 {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
}

func (s *server) explain(st runStats, decisions []map[string]any) map[string]any {
	body := map[string]any{"scenario": st.Scenario, "stats": st, "decisions": decisions}
	data, err := s.postJSON(s.genai+"/explain-simulation", body)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"error": "malformed AI response"}
	}
	return out
}

func (s *server) postmortem(incidentID string) map[string]any {
	if incidentID == "" {
		return nil
	}
	data, err := s.postJSON(s.genai+"/generate-postmortem", map[string]any{"incident_id": incidentID})
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"error": "malformed postmortem"}
	}
	return out
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Scenario == "" {
		httpx.WriteJSON(w, 400, map[string]string{"error": "scenario required"})
		return
	}
	switch req.Scenario {
	case "happy", "risk_outage", "db_bottleneck", "traffic_spike":
	default:
		httpx.WriteJSON(w, 400, map[string]string{"error": "unknown scenario " + req.Scenario})
		return
	}

	s.reset()
	before0, err := s.metrics()
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	time.Sleep(2 * time.Second)
	before, err := s.metrics()
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	known := s.decisionUIDs()
	start := time.Now()

	var peak struct {
		sync.Mutex
		conn    float64
		waiting float64
	}
	stopPeaks := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPeaks:
				return
			case <-t.C:
				ss, err := s.metrics()
				if err != nil {
					continue
				}
				c := dbConnPct(ss)
				w := gauge(ss, "db_pool_waiting", map[string]string{"service": "payment-service"})
				peak.Lock()
				if c > peak.conn {
					peak.conn = c
				}
				if w > peak.waiting {
					peak.waiting = w
				}
				peak.Unlock()
			}
		}
	}()

	st := runStats{Scenario: req.Scenario, StartedAt: start.UTC().Format(time.RFC3339),
		Scenarios: []string{"happy", "risk_outage", "db_bottleneck", "traffic_spike"}}

	switch req.Scenario {
	case "happy":
		for i := 0; i < 10; i++ {
			res := s.placeOrderStatus("storefront-happy")
			countResult(&st, res)
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(2 * time.Second)
	case "risk_outage":
		_, _ = s.postJSON(s.gateway+"/api/chaos/risk-outage", map[string]any{"enabled": true})
		res := s.placeOrderStatus("storefront-risk")
		countResult(&st, res)
		_, _ = s.postJSON(s.gateway+"/api/chaos/risk-outage", map[string]any{"enabled": false})
		var body map[string]any
		_ = json.Unmarshal(res.body, &body)
		st.IncidentID, _ = body["incident_id"].(string)
		time.Sleep(1 * time.Second)
	case "db_bottleneck":
		_, _ = s.postJSON(s.gateway+"/api/chaos/db-latency", map[string]any{"enabled": true, "added_ms": 300})
		for _, res := range fireLoad(s, 20, 18*time.Second, "storefront-db") {
			countResult(&st, res)
		}
		st.VetoInvariant, st.ShieldAction = s.waitForVeto(known, 25*time.Second)
	case "traffic_spike":
		for _, res := range fireLoad(s, 35, 12*time.Second, "storefront-spike") {
			countResult(&st, res)
		}
	}
	close(stopPeaks)
	if req.Scenario == "db_bottleneck" {
		s.waitForVerification(known, 40*time.Second)
	}

	after, err := s.metrics()
	if err != nil {
		httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	st.DurationS = time.Since(start).Seconds()
	st.P95BeforeMS = p95Delta(before0, before, "http_request_duration_seconds", map[string]string{"service": "gateway", "route": "/api/orders"})
	st.P95AfterMS = p95Delta(before, after, "http_request_duration_seconds", map[string]string{"service": "gateway", "route": "/api/orders"})
	st.DBConnPctBefore = dbConnPct(before)
	st.DBConnPctAfter = dbConnPct(after)
	st.DBPoolWaiting = gauge(after, "db_pool_waiting", map[string]string{"service": "payment-service"})
	peak.Lock()
	if peak.conn > st.DBConnPctAfter {
		st.DBConnPctAfter = peak.conn
	}
	if peak.waiting > st.DBPoolWaiting {
		st.DBPoolWaiting = peak.waiting
	}
	peak.Unlock()
	st.DBLatencyInjectedMS = gauge(after, "db_latency_injected_ms", map[string]string{"service": "payment-service"})
	st.RiskScore, st.RiskLevel, st.PredictedCause = s.prediction()

	decisions := s.newDecisions(known)
	st.NewDecisions = len(decisions)
	for _, d := range decisions {
		if st.VetoInvariant == "" {
			st.VetoInvariant, _ = d["blocked_by"].(string)
		}
		if st.ShieldAction == "" {
			st.ShieldAction, _ = d["shield_action"].(string)
		}
		if vs, ok := d["verifications"].([]any); ok && len(vs) > 0 {
			if last, ok := vs[len(vs)-1].(map[string]any); ok {
				if imp, ok := last["improved"].(bool); ok {
					st.VerificationImproved = &imp
				}
			}
		}
	}

	// Restore the testbed before answering.
	_, _ = s.postJSON(s.gateway+"/api/chaos/db-latency", map[string]any{"enabled": false})
	_, _ = s.postJSON(s.gateway+"/admin/shed", map[string]any{"enabled": false})

	ai := s.explain(st, decisions)
	pm := s.postmortem(st.IncidentID)
	httpx.WriteJSON(w, 200, map[string]any{
		"scenario":   req.Scenario,
		"stats":      st,
		"decisions":  decisions,
		"ai":         ai,
		"postmortem": pm,
	})
}

func countResult(st *runStats, res orderResult) {
	st.OrdersPlaced++
	switch {
	case res.status == 200:
		var body map[string]any
		if json.Unmarshal(res.body, &body) == nil && body["status"] == "FAILED" {
			st.OrdersFailed++ // compensated saga still returns HTTP 200
		} else {
			st.OrdersOK++
		}
	case res.status == 503:
		st.Shedded503++
	default:
		st.OrdersFailed++
	}
}
