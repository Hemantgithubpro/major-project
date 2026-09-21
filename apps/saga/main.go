package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"mvp/shared/db"
	"mvp/shared/httpx"
	"mvp/shared/kafkax"
)

func postJSON(url string, body any, corr string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Correlation-ID", corr)
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func step(pool interface {
	Exec(ctx context.Context, sql string, args ...any) (any, error)
}, sagaID, name string, order int, status, errMsg string) {
}

func main() {
	ctx := context.Background()
	p, err := db.Connect(ctx)
	if err != nil {
		panic(err)
	}
	defer p.Close()
	invURL := httpx.Env("INVENTORY_URL", "http://inventory:8082")
	payURL := httpx.Env("PAYMENT_URL", "http://payment:8083")
	riskURL := httpx.Env("RISK_URL", "http://risk:8084")
	broker := httpx.Env("KAFKA_BROKER", "kafka:9092")
	mux := http.NewServeMux()
	httpx.Healthz(mux)

	runSaga := func(w http.ResponseWriter, r *http.Request, orderID string, amount float64) {
		corr := httpx.CorrID(r)
		tx := uuid.NewString()
		sagaID := uuid.NewString()
		_, _ = p.Exec(ctx, `INSERT INTO saga_svc.sagas(id, transaction_id, order_id, status, current_step) VALUES ($1,$2,$3,'RUNNING','reserve')`, sagaID, tx, orderID)
		record := func(name string, ord int, status, errMsg string) {
			_, _ = p.Exec(ctx, `INSERT INTO saga_svc.saga_steps(saga_id, step_name, step_order, status, finished_at, error) VALUES ($1,$2,$3,$4,now(),$5)`, sagaID, name, ord, status, errMsg)
		}
		emit := func(typ string) {
			_ = kafkax.Publish(broker, "transaction.events", kafkax.New("saga-orchestrator", typ, tx, orderID, corr, map[string]any{"saga_id": sagaID}))
		}
		incident := func(scenario, status string) string {
			id := uuid.NewString()
			_, _ = p.Exec(ctx, `INSERT INTO ai_svc.incidents(id, scenario, transaction_id, status, resolved_at) VALUES ($1,$2,$3,$4, CASE WHEN $4='closed' THEN now() END)`, id, scenario, tx, status)
			_, _ = p.Exec(ctx, `INSERT INTO ai_svc.incident_evidence(incident_id, kind, data) VALUES ($1,'saga', $2)`, id, fmt.Sprintf(`{"transaction_id":%q,"order_id":%q}`, tx, orderID))
			return id
		}

		st, _ := postJSON(invURL+"/reserve", map[string]any{"order_id": orderID, "tx_id": tx}, corr)
		if st != 200 {
			record("reserve", 1, "FAILED", "inventory error")
			_, _ = p.Exec(ctx, `UPDATE saga_svc.sagas SET status='FAILED' WHERE id=$1`, sagaID)
			_, _ = p.Exec(ctx, `UPDATE order_svc.orders SET status='FAILED' WHERE id=$1`, orderID)
			emit("SagaStepFailed")
			httpx.WriteJSON(w, 500, map[string]string{"error": "reserve failed"})
			return
		}
		record("reserve", 1, "SUCCEEDED", "")
		emit("SagaStepSucceeded")

		st, _ = postJSON(payURL+"/authorize", map[string]any{"order_id": orderID, "amount": amount}, corr)
		if st != 200 {
			_, _ = postJSON(invURL+"/release", map[string]any{"order_id": orderID}, corr)
			record("authorize", 2, "FAILED", "payment error")
			record("release-inventory", 3, "COMPENSATED", "")
			_, _ = p.Exec(ctx, `UPDATE saga_svc.sagas SET status='FAILED' WHERE id=$1`, sagaID)
			_, _ = p.Exec(ctx, `UPDATE order_svc.orders SET status='FAILED' WHERE id=$1`, orderID)
			emit("SagaCompensated")
			httpx.WriteJSON(w, 500, map[string]string{"error": "payment failed, compensated"})
			return
		}
		record("authorize", 2, "SUCCEEDED", "")
		emit("SagaStepSucceeded")

		st, _ = postJSON(riskURL+"/check", map[string]any{"order_id": orderID}, corr)
		if st != 200 {
			_, _ = postJSON(payURL+"/refund", map[string]any{"order_id": orderID}, corr)
			_, _ = postJSON(invURL+"/release", map[string]any{"order_id": orderID}, corr)
			record("risk", 3, "FAILED", "risk rejected")
			record("refund-payment", 4, "COMPENSATED", "")
			record("release-inventory", 5, "COMPENSATED", "")
			_, _ = p.Exec(ctx, `UPDATE saga_svc.sagas SET status='FAILED' WHERE id=$1`, sagaID)
			_, _ = p.Exec(ctx, `UPDATE order_svc.orders SET status='FAILED' WHERE id=$1`, orderID)
			emit("SagaCompensated")
			iid := incident("risk-outage", "closed")
			httpx.WriteJSON(w, 200, map[string]any{"transaction_id": tx, "status": "FAILED", "compensated": true, "incident_id": iid})
			return
		}
		record("risk", 3, "SUCCEEDED", "")
		_, _ = p.Exec(ctx, `UPDATE saga_svc.sagas SET status='COMPLETED' WHERE id=$1`, sagaID)
		_, _ = p.Exec(ctx, `UPDATE order_svc.orders SET status='COMPLETED' WHERE id=$1`, orderID)
		emit("SagaStepSucceeded")
		httpx.WriteJSON(w, 200, map[string]any{"transaction_id": tx, "status": "COMPLETED"})
	}

	mux.HandleFunc("POST /saga/run", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderID string  `json:"order_id"`
			Amount  float64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		runSaga(w, r, body.OrderID, body.Amount)
	})
	mux.HandleFunc("GET /saga/", func(w http.ResponseWriter, r *http.Request) {
		tx := r.URL.Path[len("/saga/"):]
		var status, orderID string
		_ = p.QueryRow(ctx, `SELECT status, order_id::text FROM saga_svc.sagas WHERE transaction_id=$1`, tx).Scan(&status, &orderID)
		rows, _ := p.Query(ctx, `SELECT step_name, step_order, status, error FROM saga_svc.saga_steps WHERE saga_id=(SELECT id FROM saga_svc.sagas WHERE transaction_id=$1) ORDER BY step_order`, tx)
		steps := []map[string]any{}
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var n, s, e string
				var o int
				_ = rows.Scan(&n, &o, &s, &e)
				steps = append(steps, map[string]any{"step": n, "order": o, "status": s, "error": e})
			}
		}
		httpx.WriteJSON(w, 200, map[string]any{"transaction_id": tx, "status": status, "order_id": orderID, "steps": steps})
	})
	port := httpx.Env("PORT", "8085")
	s := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
