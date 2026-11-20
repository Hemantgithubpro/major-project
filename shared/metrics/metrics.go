package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by service, route and status code.",
		},
		[]string{"service", "route", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "route"},
	)
	dbQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database operation latency in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"service", "op"},
	)
	dbPoolAcquired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_pool_acquired", Help: "Connections currently acquired from the pool."},
		[]string{"service"},
	)
	dbPoolIdle = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_pool_idle", Help: "Idle connections in the pool."},
		[]string{"service"},
	)
	dbPoolWaiting = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_pool_waiting", Help: "Approximate DB operations waiting for a connection."},
		[]string{"service"},
	)
	dbPoolMax = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_pool_max", Help: "Maximum connections allowed in the pool."},
		[]string{"service"},
	)
	dbLatencyInjected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "db_latency_injected_ms", Help: "Artificial DB latency injected by chaos, in milliseconds."},
		[]string{"service"},
	)
	sagaStepsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "saga_steps_total", Help: "Saga steps by step name and status."},
		[]string{"step", "status"},
	)
	ordersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "orders_total", Help: "Orders created by status."},
		[]string{"status"},
	)
	riskChecksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "risk_checks_total", Help: "Risk checks by verdict."},
		[]string{"verdict"},
	)

	registerOnce sync.Once
)

func init() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			httpRequestsTotal,
			httpRequestDuration,
			dbQueryDuration,
			dbPoolAcquired,
			dbPoolIdle,
			dbPoolWaiting,
			dbPoolMax,
			dbLatencyInjected,
			sagaStepsTotal,
			ordersTotal,
			riskChecksTotal,
		)
	})
}

// Handler exposes the default Prometheus registry in text exposition format.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Middleware records request count and latency for every request.
func Middleware(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.URL.Path
		httpRequestsTotal.WithLabelValues(service, route, strconv.Itoa(rec.status)).Inc()
		httpRequestDuration.WithLabelValues(service, route).Observe(time.Since(start).Seconds())
	})
}

// ObserveDB records one database operation.
func ObserveDB(service, op string, start time.Time) {
	dbQueryDuration.WithLabelValues(service, op).Observe(time.Since(start).Seconds())
}

// SetDBPool updates the pool gauges for a service.
func SetDBPool(service string, acquired, idle, waiting, max int) {
	dbPoolAcquired.WithLabelValues(service).Set(float64(acquired))
	dbPoolIdle.WithLabelValues(service).Set(float64(idle))
	dbPoolWaiting.WithLabelValues(service).Set(float64(waiting))
	dbPoolMax.WithLabelValues(service).Set(float64(max))
}

// SetDBLatencyInjected records the current artificial DB latency.
func SetDBLatencyInjected(service string, ms float64) {
	dbLatencyInjected.WithLabelValues(service).Set(ms)
}

// IncSagaStep records a saga step outcome.
func IncSagaStep(step, status string) {
	sagaStepsTotal.WithLabelValues(step, status).Inc()
}

// IncOrder records an order created with the given status.
func IncOrder(status string) {
	ordersTotal.WithLabelValues(status).Inc()
}

// IncRiskCheck records a risk check outcome.
func IncRiskCheck(verdict string) {
	riskChecksTotal.WithLabelValues(verdict).Inc()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	return r.ResponseWriter.Write(b)
}
