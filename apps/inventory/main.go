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

var pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(dest ...any) error }
} = nil

func main() {
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()
	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /reserve", func(w http.ResponseWriter, r *http.Request) {
		corr := httpx.CorrID(r)
		var body struct {
			OrderID string `json:"order_id"`
			TxID    string `json:"tx_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := uuid.NewString()
		_, err := p.Exec(ctx, `INSERT INTO inventory_svc.reservations(id, order_id) VALUES ($1, $2)`, id, body.OrderID)
		if err != nil {
			httpx.WriteJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		httpx.Log("inventory-service", "reserved "+body.OrderID, corr)
		httpx.WriteJSON(w, 200, map[string]string{"reservation_id": id, "status": "reserved"})
	})
	mux.HandleFunc("POST /release", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderID string `json:"order_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = p.Exec(ctx, `UPDATE inventory_svc.reservations SET status='released' WHERE order_id=$1`, body.OrderID)
		httpx.WriteJSON(w, 200, map[string]string{"status": "released"})
	})
	port := httpx.Env("PORT", "8082")
	s := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
