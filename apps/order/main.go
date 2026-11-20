package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"mvp/shared/db"
	"mvp/shared/httpx"
	"mvp/shared/metrics"
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
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CustomerRef string  `json:"customer_ref"`
			Amount      float64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id := uuid.NewString()
		_, _ = p.Exec(ctx, `INSERT INTO order_svc.orders(id, customer_ref, status, total_amount) VALUES ($1,$2,'PENDING',$3)`, id, body.CustomerRef, body.Amount)
		metrics.IncOrder("PENDING")
		httpx.WriteJSON(w, 200, map[string]any{"order_id": id, "status": "PENDING"})
	})
	mux.HandleFunc("GET /orders/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/orders/"):]
		var st, ref string
		var amt float64
		_ = p.QueryRow(ctx, `SELECT status, customer_ref, total_amount FROM order_svc.orders WHERE id=$1`, id).Scan(&st, &ref, &amt)
		httpx.WriteJSON(w, 200, map[string]any{"order_id": id, "status": st, "customer_ref": ref, "amount": amt})
	})
	port := httpx.Env("PORT", "8081")
	s := &http.Server{Addr: ":" + port, Handler: metrics.Middleware("order-service", httpx.CORS(mux)), ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
