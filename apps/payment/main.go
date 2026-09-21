package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"mvp/shared/db"
	"mvp/shared/httpx"
)

func main() {
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()
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
		_, err := p.Exec(ctx, `INSERT INTO payment_svc.payments(id, order_id, amount) VALUES ($1,$2,$3)`, id, body.OrderID, body.Amount)
		if err != nil {
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
		_, _ = p.Exec(ctx, `UPDATE payment_svc.payments SET status='refunded' WHERE order_id=$1`, body.OrderID)
		httpx.WriteJSON(w, 200, map[string]string{"status": "refunded"})
	})
	port := httpx.Env("PORT", "8083")
	s := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
