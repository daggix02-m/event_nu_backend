// Package metrics provides a dependency-free Prometheus-text-format registry
// exposing lean process gauges and an HTTP request counter for the /metrics
// endpoint. Kept deliberately small: no external metrics client, and the
// request counter is a simple additive counter, not a histogram.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"sync"
)

const ContentType = "text/plain; version=0.0.4; charset=utf-8"

type reqKey struct {
	method string
	status string
}

type Registry struct {
	mu       sync.Mutex
	requests map[reqKey]uint64
}

func NewRegistry() *Registry {
	return &Registry{requests: make(map[reqKey]uint64)}
}

func (r *Registry) Hit(method string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := reqKey{method: method, status: fmt.Sprintf("%d", status)}
	r.requests[k]++
}

func Count(reg *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			reg.Hit(r.Method, rec.status)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	WroteHeader bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if !s.WroteHeader {
		s.WroteHeader = true
		s.status = status
		s.ResponseWriter.WriteHeader(status)
	}
}

func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.Lock()
	keys := make([]reqKey, 0, len(r.requests))
	for k := range r.requests {
		keys = append(keys, k)
	}
	counts := make(map[reqKey]uint64, len(r.requests))
	for k, n := range r.requests {
		counts[k] = n
	}
	r.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})

	fmt.Fprintln(w, "# HELP http_requests_total Total number of HTTP requests processed, by method and status.")
	fmt.Fprintln(w, "# TYPE http_requests_total counter")
	for _, k := range keys {
		fmt.Fprintf(w, "http_requests_total{method=%q,status=%q} %d\n", k.method, k.status, counts[k])
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	fmt.Fprintln(w, "# HELP process_alloc_bytes Heap bytes currently allocated.")
	fmt.Fprintln(w, "# TYPE process_alloc_bytes gauge")
	fmt.Fprintf(w, "process_alloc_bytes %d\n", ms.Alloc)

	fmt.Fprintln(w, "# HELP process_total_alloc_bytes Cumulative heap bytes allocated over the process lifetime.")
	fmt.Fprintln(w, "# TYPE process_total_alloc_bytes gauge")
	fmt.Fprintf(w, "process_total_alloc_bytes %d\n", ms.TotalAlloc)

	fmt.Fprintln(w, "# HELP process_gc_count Number of completed GC cycles.")
	fmt.Fprintln(w, "# TYPE process_gc_count gauge")
	fmt.Fprintf(w, "process_gc_count %d\n", ms.NumGC)

	fmt.Fprintln(w, "# HELP process_goroutines Number of goroutines currently running.")
	fmt.Fprintln(w, "# TYPE process_goroutines gauge")
	fmt.Fprintf(w, "process_goroutines %d\n", runtime.NumGoroutine())
}
