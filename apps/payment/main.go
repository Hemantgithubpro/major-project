package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"mvp/shared/db"
	"mvp/shared/httpx"
	"mvp/shared/metrics"
)

var (
	dbLatencyMs atomic.Int64
	dbInFlight  atomic.Int64
)

func main() {
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()

	// Publish real pool gauges: the controller's DB-bottleneck signal.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			st := p.Stat()
			acquired := int(st.AcquiredConns())
			idle := int(st.IdleConns())
			max := int(st.MaxConns())
			waiting := int(dbInFlight.Load()) - acquired
			if waiting < 0 {
				waiting = 0
			}
			metrics.SetDBPool("payment-service", acquired, idle, waiting, max)
		}
	}()

	dbExec := func(op, sql string, args ...any) error {
		dbInFlight.Add(1)
		defer dbInFlight.Add(-1)
		if ms := dbLatencyMs.Load(); ms > 0 {
			// Hold a real connection while sleeping, exactly like a slow query would.
			start := time.Now()
			_, _ = p.Exec(ctx, `SELECT pg_sleep($1)`, float64(ms)/1000.0)
			metrics.ObserveDB("payment-service", "sleep", start)
		}
		start := time.Now()
		_, err := p.Exec(ctx, sql, args...)
		metrics.ObserveDB("payment-service", op, start)
		return err
	}

	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /authorize", func(w http.ResponseWriter, r *http.Request) {
		corr := httpx.CorrID(r)
		var body struct {
			OrderID string  `json:"order_id"`
			Amount  float64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := uuid.NewString()
		if err := dbExec("authorize", `INSERT INTO payment_svc.payments(id, order_id, amount) VALUES ($1,$2,$3)`, id, body.OrderID, body.Amount); err != nil {
			httpx.WriteJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		httpx.Log("payment-service", "authorized "+body.OrderID, corr)
		httpx.WriteJSON(w, 200, map[string]string{"payment_id": id, "status": "authorized"})
	})
	mux.HandleFunc("POST /refund", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderID string `json:"order_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = dbExec("refund", `UPDATE payment_svc.payments SET status='refunded' WHERE order_id=$1`, body.OrderID)
		httpx.WriteJSON(w, 200, map[string]string{"status": "refunded"})
	})
	mux.HandleFunc("POST /chaos/db-latency", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
			AddedMS int  `json:"added_ms"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ms := 0
		if body.Enabled {
			ms = body.AddedMS
			if ms <= 0 {
				ms = 250
			}
		}
		dbLatencyMs.Store(int64(ms))
		metrics.SetDBLatencyInjected("payment-service", float64(ms))
		httpx.WriteJSON(w, 200, map[string]any{"enabled": ms > 0, "added_ms": ms})
	})
	port := httpx.Env("PORT", "8083")
	s := &http.Server{Addr: ":" + port, Handler: metrics.Middleware("payment-service", httpx.CORS(mux)), ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
