package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOtelHTTPMiddleware_CreatesSpans(t *testing.T) {
	// Set up an in-memory span exporter so we can inspect created spans.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)

	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Force flush to ensure spans are exported.
	_ = tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span from otelhttp middleware, got none")
	}

	// The outermost span should be the otelhttp handler span.
	found := false
	for _, s := range spans {
		if s.Name == "GET" || s.Name == "denkeeper.http" || s.Name == "GET /api/v1/health" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no HTTP span found in exported spans: %v", names)
	}
}

func TestOtelHTTPMiddleware_PropagatesTraceContext(t *testing.T) {
	// Set up an in-memory span exporter.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	cfg := testConfig(allScopesKey())
	srv := New(cfg, testDeps(), testLogger())

	// Send a request with a W3C traceparent header.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	_ = tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected spans, got none")
	}

	// The span should have the trace ID from the traceparent header.
	expectedTraceID := "0af7651916cd43dd8448eb211c80319c"
	found := false
	for _, s := range spans {
		if s.SpanContext.TraceID().String() == expectedTraceID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no span found with trace ID %s; spans have trace IDs: ", expectedTraceID)
		for _, s := range spans {
			t.Logf("  span %q: traceID=%s", s.Name, s.SpanContext.TraceID())
		}
	}
}
