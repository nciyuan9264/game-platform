package gamelog

import (
	"context"

	"github.com/nciyuan9264/game-platform/internal/game"
)

type contextKey struct{}

type Context struct {
	context.Context
	sink game.LogSink
}

func WithSink(ctx context.Context, sink game.LogSink) context.Context {
	if sink == nil {
		sink = NopSink{}
	}
	return &Context{Context: ctx, sink: sink}
}

func FromContext(ctx context.Context) game.LogSink {
	if ctx == nil {
		return NopSink{}
	}
	if logCtx, ok := ctx.(game.LogContext); ok {
		return logCtx.LogSink()
	}
	if sink, ok := ctx.Value(contextKey{}).(game.LogSink); ok && sink != nil {
		return sink
	}
	return NopSink{}
}

func (c *Context) Value(key any) any {
	if key == (contextKey{}) {
		return c.sink
	}
	return c.Context.Value(key)
}

func (c *Context) LogSink() game.LogSink {
	if c == nil || c.sink == nil {
		return NopSink{}
	}
	return c.sink
}

type NopSink struct{}

func (NopSink) AppendOperation(context.Context, game.OperationEvent) error {
	return nil
}

func (NopSink) SaveSnapshotHint(context.Context, game.SnapshotHint) error {
	return nil
}
