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
)

var failing atomic.Bool

func main() {
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()
	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /check", func(w http.ResponseWriter, r *http.Request) {
		corr := httpx.CorrID(r)
		var body struct {
			OrderID string `json:"order_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if failing.Load() {
			httpx.Log("risk-service", "REJECTED (chaos) "+body.OrderID, corr)
			httpx.WriteJSON(w, 500, map[string]string{"error": "risk check failed (chaos)"})
			return
		}
		id := uuid.NewString()
		_, _ = p.Exec(ctx, `INSERT INTO risk_svc.risk_checks(id, order_id, verdict) VALUES ($1,$2,'approved')`, id, body.OrderID)
		httpx.Log("risk-service", "approved "+body.OrderID, corr)
		httpx.WriteJSON(w, 200, map[string]string{"check_id": id, "verdict": "approved"})
	})
	mux.HandleFunc("POST /chaos/fail", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		failing.Store(body.Enabled)
		httpx.WriteJSON(w, 200, map[string]bool{"failing": body.Enabled})
	})
	port := httpx.Env("PORT", "8084")
	s := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
