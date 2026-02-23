// Package telemetry configures OpenTelemetry for WanderSort.
//
// Signals exported over OTLP HTTP:
//   - Traces  - distributed tracing
//   - Metrics - request counts, latency histograms
//   - Logs    - slog to OTel log bridge
//
// Standard OTel env vars:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT  e.g. http://localhost:4318
//	OTEL_SERVICE_NAME             e.g. wandersort
//	OTEL_SERVICE_VERSION          e.g. 1.0.0
package telemetry

import (
	"context"
	"errors"
	"net/url"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

const defaultEndpoint = "http://localhost:4318"

// Setup initialises the OTel SDK (traces, metrics, logs) and registers global
// providers. The returned shutdown function must be deferred in main.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	var shutdownFns []func(context.Context) error

	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFns {
			errs = append(errs, fn(ctx))
		}
		return errors.Join(errs...)
	}

	res, err := buildResource(ctx)
	if err != nil {
		return shutdown, err
	}

	// Propagator: W3C TraceContext + Baggage
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	endpoint := Endpoint()
	insecure := isInsecure(endpoint)

	// ── Traces ───────────────────────────────────────────────────────────────
	traceOpts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint + "/v1/traces")}
	if insecure {
		traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
	}
	traceExporter, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return shutdown, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	shutdownFns = append(shutdownFns, tp.Shutdown)
	otel.SetTracerProvider(tp)

	// ── Metrics ──────────────────────────────────────────────────────────────
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(endpoint + "/v1/metrics")}
	if insecure {
		metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
	}
	metricExporter, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		return shutdown, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(15*time.Second)),
		),
		sdkmetric.WithResource(res),
	)
	shutdownFns = append(shutdownFns, mp.Shutdown)
	otel.SetMeterProvider(mp)

	// ── Logs ─────────────────────────────────────────────────────────────────
	logOpts := []otlploghttp.Option{otlploghttp.WithEndpointURL(endpoint + "/v1/logs")}
	if insecure {
		logOpts = append(logOpts, otlploghttp.WithInsecure())
	}
	logExporter, err := otlploghttp.New(ctx, logOpts...)
	if err != nil {
		return shutdown, err
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	shutdownFns = append(shutdownFns, lp.Shutdown)
	global.SetLoggerProvider(lp)

	return shutdown, nil
}

// buildResource constructs an OTel resource with service name/version.
func buildResource(ctx context.Context) (*resource.Resource, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "wandersort"
	}
	serviceVersion := os.Getenv("OTEL_SERVICE_VERSION")
	if serviceVersion == "" {
		serviceVersion = "dev"
	}
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion),
		),
	)
}

// Endpoint returns the configured OTLP endpoint (or the default).
func Endpoint() string {
	if ep := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); ep != "" {
		return ep
	}
	return defaultEndpoint
}

// isInsecure returns true when the endpoint URL uses the http scheme.
func isInsecure(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return true // assume insecure if we can't parse
	}
	return u.Scheme == "http"
}
