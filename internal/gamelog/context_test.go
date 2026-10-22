package gamelog

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/game"
)

type recordingSink struct {
	operations []game.OperationEvent
	hints      []game.SnapshotHint
}

func (s *recordingSink) AppendOperation(ctx context.Context, event game.OperationEvent) error {
	s.operations = append(s.operations, event)
	return nil
}

func (s *recordingSink) SaveSnapshotHint(ctx context.Context, hint game.SnapshotHint) error {
	s.hints = append(s.hints, hint)
	return nil
}

func TestWithSinkExposesLogContextToPlugins(t *testing.T) {
	sink := &recordingSink{}
	ctx := WithSink(context.Background(), sink)

	if got := FromContext(ctx); got != sink {
		t.Fatalf("FromContext() did not return injected sink")
	}
	logCtx, ok := ctx.(game.LogContext)
	if !ok {
		t.Fatalf("context does not implement game.LogContext")
	}
	if logCtx.LogSink() != sink {
		t.Fatalf("LogSink() did not return injected sink")
	}
}
