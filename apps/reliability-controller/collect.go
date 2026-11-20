package main

import (
	"bufio"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sample is one parsed line of Prometheus text exposition.
type sample struct {
	name   string
	labels map[string]string
	value  float64
}

type p95Point struct {
	at  time.Time
	p95 float64
}

// features are the exact eight feature names the ML model was trained on.
// Keep this in sync with ai/ml/features.py.
type features struct {
	RequestsPerMin      float64 `json:"requests_per_min"`
	P95LatencyMS        float64 `json:"p95_latency_ms"`
	ErrorRate           float64 `json:"error_rate"`
	DBConnPct           float64 `json:"db_conn_pct"`
	DBPoolWaiting       float64 `json:"db_pool_waiting"`
	SagaFailureRate     float64 `json:"saga_failure_rate"`
	DBLatencyInjectedMS float64 `json:"db_latency_injected_ms"`
	RiskSlope5M         float64 `json:"risk_slope_5m"`
}

type snapshot struct {
	Features   features       `json:"features"`
	Raw        map[string]any `json:"raw"`
	CapturedAt time.Time      `json:"captured_at"`
}

type collector struct {
	urls     map[string]string
	interval time.Duration

	mu            sync.Mutex
	prev          map[string]float64
	primed        bool
	lastScrapeAt  time.Time
	lastTrafficAt time.Time
	p95           float64
	hist          []p95Point
}

func newCollector(urls map[string]string, interval time.Duration) *collector {
	return &collector{urls: urls, interval: interval, prev: map[string]float64{}}
}

func scrape(url string) ([]sample, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape %s: status %d", url, resp.StatusCode)
	}
	var out []sample
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, ok := parseLine(line)
		if ok {
			out = append(out, s)
		}
	}
	return out, scanner.Err()
}

func parseLine(line string) (sample, bool) {
	idx := strings.IndexAny(line, "{ ")
	if idx < 0 {
		return sample{}, false
	}
	s := sample{name: line[:idx], labels: map[string]string{}}
	rest := line[idx:]
	if line[idx] == '{' {
		end := strings.LastIndex(line, "}")
		if end < 0 {
			return sample{}, false
		}
		for _, part := range strings.Split(line[idx+1:end], ",") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 {
				continue
			}
			s.labels[kv[0]] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
		}
		rest = line[end+1:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return sample{}, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return sample{}, false
	}
	s.value = v
	return s, true
}

func keyOf(s sample) string {
	keys := make([]string, 0, len(s.labels))
	for k := range s.labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(s.name)
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(s.labels[k])
	}
	return b.String()
}

func matches(labels map[string]string, want map[string]string) bool {
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// counterDelta sums the increase of a counter family over one scrape interval.
func counterDelta(samples []sample, prev map[string]float64, name string, want map[string]string) float64 {
	var total, before float64
	for _, s := range samples {
		if s.name != name || !matches(s.labels, want) {
			continue
		}
		total += s.value
		before += prev[keyOf(s)]
	}
	d := total - before
	if d < 0 {
		return 0
	}
	return d
}

func gaugeValue(samples []sample, name string, want map[string]string) (float64, bool) {
	for _, s := range samples {
		if s.name == name && matches(s.labels, want) {
			return s.value, true
		}
	}
	return 0, false
}

// histogramP95 reads the 95th percentile from Prometheus cumulative buckets.
func histogramP95(samples []sample, prev map[string]float64, name string, want map[string]string, delta bool) float64 {
	type bucket struct {
		le    float64
		count float64
	}
	var buckets []bucket
	var total float64
	for _, s := range samples {
		if s.name != name+"_bucket" || !matches(s.labels, want) {
			continue
		}
		le, err := strconv.ParseFloat(s.labels["le"], 64)
		if err != nil {
			continue
		}
		count := s.value
		if delta {
			count -= prev[keyOf(s)]
			if count < 0 {
				count = 0
			}
		}
		if math.IsInf(le, 1) {
			total = count
			continue
		}
		buckets = append(buckets, bucket{le: le, count: count})
	}
	if total <= 0 {
		return 0
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].le < buckets[j].le })
	target := 0.95 * total
	maxLe := 0.0
	for _, b := range buckets {
		maxLe = b.le
		if b.count >= target {
			return b.le * 1000
		}
	}
	return maxLe * 1000
}

func (c *collector) collect() (snapshot, error) {
	var all []sample
	for name, url := range c.urls {
		ss, err := scrape(url)
		if err != nil {
			return snapshot{}, fmt.Errorf("scrape %s: %w", name, err)
		}
		all = append(all, ss...)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.prev
	delta := c.primed
	now := time.Now().UTC()
	elapsed := 0.0
	if !c.lastScrapeAt.IsZero() {
		elapsed = now.Sub(c.lastScrapeAt).Seconds()
	}
	c.lastScrapeAt = now

	ordersTotal := counterDelta(all, prev, "http_requests_total", map[string]string{"service": "gateway", "route": "/api/orders"})
	errors := 0.0
	for _, s := range all {
		if s.name != "http_requests_total" || s.labels["service"] != "gateway" || s.labels["route"] != "/api/orders" {
			continue
		}
		if s.labels["status"] == "200" {
			continue
		}
		errors += math.Max(0, s.value-prev[keyOf(s)])
	}

	if ordersTotal > 0 {
		c.lastTrafficAt = now
	}
	p95 := histogramP95(all, prev, "http_request_duration_seconds", map[string]string{"service": "gateway", "route": "/api/orders"}, delta)
	if p95 == 0 && c.p95 > 0 && now.Sub(c.lastTrafficAt) <= 30*time.Second {
		p95 = c.p95 // no traffic in this window; carry briefly, then expire
	} else {
		c.p95 = p95
	}

	acquired, _ := gaugeValue(all, "db_pool_acquired", map[string]string{"service": "payment-service"})
	dbMax, ok := gaugeValue(all, "db_pool_max", map[string]string{"service": "payment-service"})
	connPct := 0.0
	if ok && dbMax > 0 {
		connPct = acquired / dbMax
	}
	waiting, _ := gaugeValue(all, "db_pool_waiting", map[string]string{"service": "payment-service"})
	injected, _ := gaugeValue(all, "db_latency_injected_ms", map[string]string{"service": "payment-service"})

	sagaTotal := counterDelta(all, prev, "saga_steps_total", map[string]string{})
	sagaFailed := counterDelta(all, prev, "saga_steps_total", map[string]string{"status": "FAILED"})
	sagaFailRate := 0.0
	if sagaTotal > 0 {
		sagaFailRate = sagaFailed / sagaTotal
	}

	c.hist = append(c.hist, p95Point{at: now, p95: p95})
	cutoff := now.Add(-10 * time.Minute)
	kept := c.hist[:0]
	for _, h := range c.hist {
		if h.at.After(cutoff) {
			kept = append(kept, h)
		}
	}
	c.hist = kept
	slope := 0.0
	if len(c.hist) >= 2 {
		oldest := c.hist[0]
		mins := now.Sub(oldest.at).Minutes()
		if mins >= 1 {
			slope = (p95 - oldest.p95) / mins
		}
	}

	rpm := 0.0
	errRate := 0.0
	if delta && elapsed > 0 {
		rpm = ordersTotal * 60 / elapsed
		if ordersTotal+errors > 0 {
			errRate = errors / (ordersTotal + errors)
		}
	}

	// Refresh the previous-value map for the next interval.
	fresh := make(map[string]float64, len(all))
	for _, s := range all {
		fresh[keyOf(s)] = s.value
	}
	c.prev = fresh
	c.primed = true

	return snapshot{
		Features: features{
			RequestsPerMin:      rpm,
			P95LatencyMS:        p95,
			ErrorRate:           errRate,
			DBConnPct:           connPct,
			DBPoolWaiting:       waiting,
			SagaFailureRate:     sagaFailRate,
			DBLatencyInjectedMS: injected,
			RiskSlope5M:         slope,
		},
		Raw: map[string]any{
			"orders_in_window": ordersTotal,
			"db_pool_acquired": acquired,
			"db_pool_max":      dbMax,
			"saga_failed":      sagaFailed,
			"last_p95_ms":      p95,
		},
		CapturedAt: now,
	}, nil
}
