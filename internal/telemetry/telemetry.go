package telemetry

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/kitex-contrib/obs-opentelemetry/provider"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Provider struct {
	provider provider.OtelProvider
}

func Init(serviceName, environment string) *Provider {
	if disabled, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED"))); disabled {
		return &Provider{}
	}
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return &Provider{}
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	options := []provider.Option{
		provider.WithServiceName(serviceName),
		provider.WithDeploymentEnvironment(environment),
		provider.WithEnableMetrics(false),
		provider.WithExportEndpoint(endpoint),
		provider.WithSampler(samplerFromEnvironment()),
		provider.WithTextMapPropagator(propagation.TraceContext{}),
	}
	insecure, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")))
	if insecure {
		options = append(options, provider.WithInsecure())
	}
	return &Provider{provider: provider.NewOpenTelemetryProvider(options...)}
}

func samplerFromEnvironment() sdktrace.Sampler {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	switch name {
	case "always_off":
		return sdktrace.NeverSample()
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "always_on":
		return sdktrace.AlwaysSample()
	case "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	ratio := 1.0
	if value := strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed >= 0 && parsed <= 1 {
			ratio = parsed
		}
	}
	ratioSampler := sdktrace.TraceIDRatioBased(ratio)
	if name == "traceidratio" {
		return ratioSampler
	}
	return sdktrace.ParentBased(ratioSampler)
}

func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.provider == nil {
		return nil
	}
	return p.provider.Shutdown(ctx)
}
