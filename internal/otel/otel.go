// Package otel provides OpenTelemetry setup for metrics (Prometheus) and
// optional tracing (OTLP HTTP). When disabled, global OTel providers remain
// no-ops with zero overhead.
package otel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config mirrors config.OTelConfig from the TOML layer.
type Config struct {
	Enabled        bool
	TracesEndpoint string // OTLP HTTP endpoint, e.g. "http://localhost:4318" or "localhost:4318"
	ServiceName    string // defaults to "denkeeper"
}

// Setup initialises the global OTel meter and tracer providers. When
// cfg.Enabled is false it returns a no-op shutdown. The returned function
// must be called on process exit to flush pending telemetry.
func Setup(cfg Config, logger *slog.Logger) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	svcName := cfg.ServiceName
	if svcName == "" {
		svcName = "denkeeper"
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName(svcName)),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: creating resource: %w", err)
	}

	// Metrics — Prometheus pull exporter.
	promExp, err := promexporter.New()
	if err != nil {
		return nil, fmt.Errorf("otel: creating prometheus exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(promExp),
	)
	otel.SetMeterProvider(meterProvider)

	// W3C Trace Context propagation — always enabled so inbound traceparent
	// headers create child spans even when this instance is not exporting traces.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Traces — optional OTLP HTTP push exporter.
	var tracerProvider *sdktrace.TracerProvider
	if cfg.TracesEndpoint != "" {
		hostPort, secure := parseEndpoint(cfg.TracesEndpoint)
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(hostPort),
		}
		if !secure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		traceExp, traceErr := otlptracehttp.New(context.Background(), opts...)
		if traceErr != nil {
			// Non-fatal: metrics still work, just warn.
			logger.Warn("otel: failed to create trace exporter, tracing disabled", "error", traceErr)
		} else {
			tracerProvider = sdktrace.NewTracerProvider(
				sdktrace.WithResource(res),
				sdktrace.WithBatcher(traceExp),
			)
			otel.SetTracerProvider(tracerProvider)
			logger.Info("otel: tracing enabled", "endpoint", cfg.TracesEndpoint)
		}
	}

	logger.Info("otel: metrics enabled (prometheus /metrics)")

	return func(ctx context.Context) error {
		var firstErr error
		if tracerProvider != nil {
			if e := tracerProvider.Shutdown(ctx); e != nil && firstErr == nil {
				firstErr = e
			}
		}
		if e := meterProvider.Shutdown(ctx); e != nil && firstErr == nil {
			firstErr = e
		}
		return firstErr
	}, nil
}

// parseEndpoint normalises a traces endpoint value into the host:port form
// that otlptracehttp.WithEndpoint expects. Users commonly provide a full URL
// (e.g. "http://collector:4318") but the SDK treats the value as a literal
// host, producing a malformed URL like "http://http:%2F%2Fcollector:4318/...".
//
// Returns the host:port string and whether TLS should be used.
func parseEndpoint(raw string) (hostPort string, secure bool) {
	// If it looks like a URL (has a scheme), parse it.
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			secure = u.Scheme == "https"
			hostPort = u.Host
			if hostPort != "" {
				return hostPort, secure
			}
		}
	}
	// Bare host:port — pass through, assume insecure.
	return raw, false
}

// PrometheusHandler returns an HTTP handler that serves Prometheus metrics.
func PrometheusHandler() http.Handler {
	return promhttp.Handler()
}
