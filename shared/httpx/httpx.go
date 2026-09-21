package httpx

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func CorrID(r *http.Request) string {
	if v := r.Header.Get("X-Correlation-ID"); v != "" {
		return v
	}
	return "corr-" + time.Now().Format("150405.000000")
}

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func Log(service, msg, corr string) {
	log.Printf(`{"service":%q,"msg":%q,"correlation_id":%q}`, service, msg, corr)
}

func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Healthz(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("# mvp metrics stub\nup 1\n"))
	})
}
