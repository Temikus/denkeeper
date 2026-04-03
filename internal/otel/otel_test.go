package otel

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSetup_Disabled(t *testing.T) {
	shutdown, err := Setup(Config{Enabled: false}, discardLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

func TestSetup_Enabled(t *testing.T) {
	shutdown, err := Setup(Config{Enabled: true, ServiceName: "test-svc"}, discardLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	// Prometheus handler should return 200 with prometheus content type.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	PrometheusHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "application/openmetrics") {
		t.Errorf("unexpected content-type: %s", ct)
	}
}

func TestSetup_DefaultServiceName(t *testing.T) {
	shutdown, err := Setup(Config{Enabled: true}, discardLogger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = shutdown(context.Background())
}
