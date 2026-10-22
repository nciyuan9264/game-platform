package telemetry

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	pammeta "github.com/nciyuan9264/pam-protocol/metadata"
	"go.opentelemetry.io/otel/trace"
)

var accessLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

func AccessLogMiddleware(service string) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req, resp any) error {
			startedAt := time.Now()
			err := next(ctx, req, resp)
			attributes := []any{
				"service", service,
				"rpc_method", rpcMethod(ctx),
				"duration_ms", time.Since(startedAt).Milliseconds(),
				"status", status(err),
			}
			if info, metadataErr := pammeta.RequestInfoFromContext(ctx); metadataErr != nil {
				attributes = append(attributes, "request_metadata_invalid", true)
			} else if info.RequestID != "" {
				attributes = append(attributes, "request_id", info.RequestID)
			}
			span := trace.SpanContextFromContext(ctx)
			if span.IsValid() {
				attributes = append(attributes, "trace_id", span.TraceID().String(), "span_id", span.SpanID().String())
			}
			accessLogger.InfoContext(ctx, "rpc request completed", attributes...)
			return err
		}
	}
}

func rpcMethod(ctx context.Context) string {
	if info := rpcinfo.GetRPCInfo(ctx); info != nil && info.Invocation() != nil {
		return info.Invocation().MethodName()
	}
	return ""
}

func status(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
