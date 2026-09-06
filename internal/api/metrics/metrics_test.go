package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daggix02-m/event_nu/backend/internal/api/metrics"
)

func TestRegistryCountsByMethodAndStatus(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.Hit("GET", 200)
	reg.Hit("GET", 200)
	reg.Hit("POST", 201)
	reg.Hit("GET", 404)

	var sb strings.Builder
	reg.WritePrometheus(&sb)
	body := sb.String()

	for _, want := range []string{
		"# HELP http_requests_total",
		"# TYPE http_requests_total counter",
		`http_requests_total{method="GET",status="200"} 2`,
		`http_requests_total{method="POST",status="201"} 1`,
		`http_requests_total{method="GET",status="404"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}

func TestWritePrometheusIsDeterministic(t *testing.T) {
	reg := metrics.NewRegistry()
	reg.Hit("POST", 201)
	reg.Hit("GET", 200)
	reg.Hit("DELETE", 204)

	var a, b strings.Builder
	reg.WritePrometheus(&a)
	reg.WritePrometheus(&b)

	// Runtime gauges (memstats, GC count) legitimately differ between scrapes;
	// the additive request counters must be byte-identical.
	reqA := strings.Split(a.String(), "# HELP process_alloc_bytes")[0]
	reqB := strings.Split(b.String(), "# HELP process_alloc_bytes")[0]
	if reqA != reqB {
		t.Fatalf("request counters not deterministic:\n%s\nvs\n%s", reqA, reqB)
	}

	// Buckets sorted by method then status; the request samples must be in order.
	di := strings.Index(reqA, `method="DELETE"`)
	gi := strings.Index(reqA, `method="GET"`)
	pi := strings.Index(reqA, `method="POST"`)
	if di > gi || gi > pi {
		t.Fatalf("buckets not sorted by method (DELETE=%d GET=%d POST=%d)", di, gi, pi)
	}
}

func TestWritePrometheusIncludesRuntimeGauges(t *testing.T) {
	reg := metrics.NewRegistry()
	var sb strings.Builder
	reg.WritePrometheus(&sb)
	body := sb.String()

	for _, family := range []string{
		"process_alloc_bytes",
		"process_total_alloc_bytes",
		"process_gc_count",
		"process_goroutines",
	} {
		if !strings.Contains(body, "# HELP "+family) || !strings.Contains(body, "# TYPE "+family+" gauge") {
			t.Fatalf("output missing gauge family %q:\n%s", family, body)
		}
		if !strings.Contains(body, family+" ") {
			t.Fatalf("output missing sample for %q:\n%s", family, body)
		}
	}
}

func TestCountMiddlewareRecordsFinalStatus(t *testing.T) {
	reg := metrics.NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fine"))
	})
	mux.HandleFunc("POST /created", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	h := metrics.Count(reg)(
		// RecoverPanic-like layer so the panic path exercises a written 500.
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/boom" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			mux.ServeHTTP(w, r)
		}))

	serve := func(method, path string) {
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	serve("GET", "/ok") // implicit 200
	serve("POST", "/created")
	serve("GET", "/boom") // explicit 500
	serve("GET", "/ok")   // second 200

	var sb strings.Builder
	reg.WritePrometheus(&sb)
	body := sb.String()
	for _, want := range []string{
		`http_requests_total{method="GET",status="200"} 2`,
		`http_requests_total{method="POST",status="201"} 1`,
		`http_requests_total{method="GET",status="500"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q:\n%s", want, body)
		}
	}
}

func TestContentType(t *testing.T) {
	if metrics.ContentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("unexpected Content-Type: %q", metrics.ContentType)
	}
}
