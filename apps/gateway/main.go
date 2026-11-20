package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"mvp/shared/httpx"
	"mvp/shared/metrics"
)

func proxy(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b []byte
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		req, _ := http.NewRequest(r.Method, target, bytes.NewReader(b))
		for k, v := range r.Header {
			req.Header[k] = v
		}
		if req.Header.Get("X-Correlation-ID") == "" {
			req.Header.Set("X-Correlation-ID", httpx.CorrID(r))
		}
		c := &http.Client{Timeout: 8 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

func main() {
	sagaURL := httpx.Env("SAGA_URL", "http://saga:8085")
	riskURL := httpx.Env("RISK_URL", "http://risk:8084")
	mux := http.NewServeMux()
	httpx.Healthz(mux)
	mux.HandleFunc("POST /api/orders", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CustomerRef string  `json:"customer_ref"`
			Amount      float64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		corr := httpx.CorrID(r)
		b, _ := json.Marshal(map[string]any{"order_id": "", "amount": body.Amount})
		_ = b
		oidReq, _ := json.Marshal(map[string]any{"customer_ref": body.CustomerRef, "amount": body.Amount})
		req, _ := http.NewRequest("POST", "http://order:8081/orders", bytes.NewReader(oidReq))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Correlation-ID", corr)
		c := &http.Client{Timeout: 5 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		var created map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&created)
		resp.Body.Close()
		orderID, _ := created["order_id"].(string)
		sagaBody, _ := json.Marshal(map[string]any{"order_id": orderID, "amount": body.Amount})
		sreq, _ := http.NewRequest("POST", sagaURL+"/saga/run", bytes.NewReader(sagaBody))
		sreq.Header.Set("Content-Type", "application/json")
		sreq.Header.Set("X-Correlation-ID", corr)
		sresp, err := c.Do(sreq)
		if err != nil {
			httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		defer sresp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(sresp.Body).Decode(&out)
		out["order_id"] = orderID
		httpx.WriteJSON(w, sresp.StatusCode, out)
	})
	mux.HandleFunc("GET /api/transactions/recent", func(w http.ResponseWriter, r *http.Request) {
		proxy(sagaURL+"/sagas/recent")(w, r)
	})
	mux.HandleFunc("GET /api/transactions/", func(w http.ResponseWriter, r *http.Request) {
		tx := r.URL.Path[len("/api/transactions/"):]
		proxy(sagaURL+"/saga/"+tx)(w, r)
	})
	mux.HandleFunc("POST /api/chaos/risk-outage", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequest("POST", riskURL+"/chaos/fail", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Correlation-ID", httpx.CorrID(r))
		c := &http.Client{Timeout: 5 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			httpx.WriteJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	mux.HandleFunc("GET /api/system/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	port := httpx.Env("PORT", "8080")
	s := &http.Server{Addr: ":" + port, Handler: metrics.Middleware("gateway", httpx.CORS(mux)), ReadHeaderTimeout: 5 * time.Second}
	_ = s.ListenAndServe()
}
