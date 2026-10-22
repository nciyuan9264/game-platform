package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nciyuan9264/game-platform-server/internal/contracts"
)

const (
	commandStream       = "GAME_COMMANDS"
	commandSubject      = "cmdlog.*"
	commandReplayBatch  = 128
	commandReplayWait   = 500 * time.Millisecond
	commandMaxAge       = 7 * 24 * time.Hour
	commandDuplicateTTL = 24 * time.Hour
)

var (
	ErrCommandLogStaleEpoch       = errors.New("command log stale epoch")
	ErrCommandLogSequenceConflict = errors.New("command log sequence conflict")
	ErrCommandLogFenceRequired    = errors.New("command log owner fence is required")
)

type Router struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

func NewRouter(url string) (*Router, error) {
	nc, err := nats.Connect(url, nats.Name("game-platform"))
	if err != nil {
		return nil, err
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Router{nc: nc, js: js}, nil
}

func (r *Router) EnsureCommandStream() error {
	return ensureCommandStream(r.js)
}

func (r *Router) Close() {
	r.nc.Close()
}

func (r *Router) SubscribeRoomCommands(ownerNode string, handler func(context.Context, contracts.RoomCommand) contracts.CommandAck) (*nats.Subscription, error) {
	commandSub, err := r.nc.Subscribe(
		fmt.Sprintf("room.cmd.%s", ownerNode),
		roomCommandHandler(false, handler),
	)
	if err != nil {
		return nil, err
	}
	if _, err := r.nc.Subscribe(
		fmt.Sprintf("room.control.%s", ownerNode),
		roomCommandHandler(true, handler),
	); err != nil {
		_ = commandSub.Unsubscribe()
		return nil, err
	}
	return commandSub, nil
}

func roomCommandHandler(controlChannel bool, handler func(context.Context, contracts.RoomCommand) contracts.CommandAck) nats.MsgHandler {
	return func(msg *nats.Msg) {
		var cmd contracts.RoomCommand
		if err := json.Unmarshal(msg.Data, &cmd); err != nil {
			_ = msg.Respond(mustJSON(contracts.CommandAck{Accepted: false, ErrorCode: "bad_command", Message: err.Error()}))
			return
		}
		if !commandAllowedOnChannel(cmd.Type, controlChannel) {
			_ = msg.Respond(mustJSON(contracts.CommandAck{
				CommandID: cmd.CommandID,
				RoomID:    cmd.RoomID,
				Accepted:  false,
				ErrorCode: "unauthorized_command",
				Message:   "command is not allowed on this NATS subject",
			}))
			return
		}
		ack := handler(context.Background(), cmd)
		_ = msg.Respond(mustJSON(ack))
	}
}

func (r *Router) SendRoomCommand(ctx context.Context, ownerNode string, cmd contracts.RoomCommand, timeout time.Duration) (contracts.CommandAck, error) {
	subject := roomCommandSubject(ownerNode, cmd.Type)
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := r.nc.RequestWithContext(ctx, subject, mustJSON(cmd))
	if err != nil {
		return contracts.CommandAck{}, err
	}
	var ack contracts.CommandAck
	if err := json.Unmarshal(resp.Data, &ack); err != nil {
		return contracts.CommandAck{}, err
	}
	return ack, nil
}

func roomCommandSubject(ownerNode, commandType string) string {
	if isRuntimeCommand(commandType) {
		return fmt.Sprintf("room.control.%s", ownerNode)
	}
	return fmt.Sprintf("room.cmd.%s", ownerNode)
}

func commandAllowedOnChannel(commandType string, controlChannel bool) bool {
	if controlChannel {
		return isRuntimeCommand(commandType)
	}
	return !strings.HasPrefix(commandType, "runtime_")
}

func isRuntimeCommand(commandType string) bool {
	return commandType == contracts.RuntimeCommandRoomCreate ||
		commandType == contracts.RuntimeCommandRoomClose ||
		commandType == contracts.RuntimeCommandMatchCreate
}

func (r *Router) SubscribeGatewayDeliveries(gatewayNode string, handler func(context.Context, contracts.OutboundMessage)) (*nats.Subscription, error) {
	subject := fmt.Sprintf("gateway.deliver.%s", gatewayNode)
	return r.nc.Subscribe(subject, func(msg *nats.Msg) {
		var out contracts.OutboundMessage
		if err := json.Unmarshal(msg.Data, &out); err != nil {
			return
		}
		handler(context.Background(), out)
	})
}

func (r *Router) Deliver(out contracts.OutboundMessage) error {
	if !out.TargetConn.Valid() {
		return errors.New("target connection reference is required")
	}
	subject := fmt.Sprintf("gateway.deliver.%s", out.TargetGatewayNodeValue())
	return r.nc.Publish(subject, mustJSON(out))
}

func (r *Router) AppendCommandLog(rec contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error) {
	if rec.MatchID == "" {
		return contracts.CommandLogAppendResult{}, errors.New("match id is required")
	}
	rec.Kind = contracts.CommandLogKindCommand
	subject := fmt.Sprintf("cmdlog.%s", rec.MatchID)
	last, err := r.js.GetLastMsg(commandStream, subject)
	var expectedLastSubjectSequence uint64
	switch {
	case err == nil:
		var previous contracts.CommandLogRecord
		if err := json.Unmarshal(last.Data, &previous); err != nil {
			return contracts.CommandLogAppendResult{}, fmt.Errorf("decode latest command log: %w", err)
		}
		normalizeCommandLogRecord(&previous)
		if previous.CommandID == rec.CommandID {
			if !sameCommandIntent(previous, rec) {
				return contracts.CommandLogAppendResult{}, errors.New("command id was reused with a different intent")
			}
			return contracts.CommandLogAppendResult{Seq: previous.Seq, Duplicate: true}, nil
		}
		if previous.Epoch != rec.Epoch ||
			(previous.OwnerNode != "" && rec.OwnerNode != "" && previous.OwnerNode != rec.OwnerNode) {
			return contracts.CommandLogAppendResult{}, fmt.Errorf(
				"%w for match %s: latest epoch %d owner %s, append epoch %d owner %s",
				ErrCommandLogStaleEpoch,
				rec.MatchID,
				previous.Epoch,
				previous.OwnerNode,
				rec.Epoch,
				rec.OwnerNode,
			)
		}
		if previous.Seq+1 != rec.Seq {
			return contracts.CommandLogAppendResult{}, fmt.Errorf("%w for match %s: latest %d, append %d", ErrCommandLogSequenceConflict, rec.MatchID, previous.Seq, rec.Seq)
		}
		expectedLastSubjectSequence = last.Sequence
	case errors.Is(err, nats.ErrMsgNotFound):
		return contracts.CommandLogAppendResult{}, fmt.Errorf("%w for match %s", ErrCommandLogFenceRequired, rec.MatchID)
	default:
		return contracts.CommandLogAppendResult{}, err
	}
	pubAck, err := r.js.Publish(
		subject,
		mustJSON(rec),
		nats.MsgId(commandMessageID(rec)),
		nats.ExpectStream(commandStream),
		nats.ExpectLastSequencePerSubject(expectedLastSubjectSequence),
	)
	if err != nil {
		if strings.Contains(err.Error(), "wrong last sequence") {
			return contracts.CommandLogAppendResult{}, fmt.Errorf("%w for match %s", ErrCommandLogSequenceConflict, rec.MatchID)
		}
		return contracts.CommandLogAppendResult{}, err
	}
	if !pubAck.Duplicate {
		return contracts.CommandLogAppendResult{Seq: rec.Seq}, nil
	}
	original, err := r.js.GetMsg(commandStream, pubAck.Sequence)
	if err != nil {
		return contracts.CommandLogAppendResult{}, fmt.Errorf("load duplicate command log: %w", err)
	}
	var previous contracts.CommandLogRecord
	if err := json.Unmarshal(original.Data, &previous); err != nil {
		return contracts.CommandLogAppendResult{}, fmt.Errorf("decode duplicate command log: %w", err)
	}
	normalizeCommandLogRecord(&previous)
	if previous.MatchID != rec.MatchID || previous.CommandID != rec.CommandID {
		return contracts.CommandLogAppendResult{}, errors.New("duplicate command log identity mismatch")
	}
	if !sameCommandIntent(previous, rec) {
		return contracts.CommandLogAppendResult{}, errors.New("command id was reused with a different intent")
	}
	return contracts.CommandLogAppendResult{Seq: previous.Seq, Duplicate: true}, nil
}

func (r *Router) AppendOwnerFence(rec contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error) {
	if rec.RoomID == "" || rec.MatchID == "" || rec.OwnerNode == "" || rec.Epoch <= 0 {
		return contracts.CommandLogAppendResult{}, errors.New("room, match, owner, and positive epoch are required")
	}
	rec.Kind = contracts.CommandLogKindOwnerFence
	rec.CommandID = fmt.Sprintf("owner-fence:%d:%s", rec.Epoch, rec.OwnerNode)
	rec.PlayerID = ""
	rec.Type = ""
	rec.Payload = nil
	rec.Operations = nil
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	subject := fmt.Sprintf("cmdlog.%s", rec.MatchID)
	for attempt := 0; attempt < 16; attempt++ {
		last, err := r.js.GetLastMsg(commandStream, subject)
		var expectedLastSubjectSequence uint64
		switch {
		case err == nil:
			var previous contracts.CommandLogRecord
			if err := json.Unmarshal(last.Data, &previous); err != nil {
				return contracts.CommandLogAppendResult{}, fmt.Errorf("decode latest command log: %w", err)
			}
			normalizeCommandLogRecord(&previous)
			if previous.Epoch > rec.Epoch {
				return contracts.CommandLogAppendResult{}, fmt.Errorf(
					"%w for match %s: latest epoch %d, fence epoch %d",
					ErrCommandLogStaleEpoch,
					rec.MatchID,
					previous.Epoch,
					rec.Epoch,
				)
			}
			if previous.Epoch == rec.Epoch &&
				previous.Kind == contracts.CommandLogKindOwnerFence &&
				previous.OwnerNode == rec.OwnerNode {
				return contracts.CommandLogAppendResult{Seq: previous.Seq, Duplicate: true}, nil
			}
			if previous.Epoch == rec.Epoch &&
				previous.OwnerNode != "" &&
				previous.OwnerNode != rec.OwnerNode {
				return contracts.CommandLogAppendResult{}, fmt.Errorf(
					"%w for match %s: epoch %d belongs to %s",
					ErrCommandLogStaleEpoch,
					rec.MatchID,
					rec.Epoch,
					previous.OwnerNode,
				)
			}
			rec.Seq = previous.Seq
			expectedLastSubjectSequence = last.Sequence
		case errors.Is(err, nats.ErrMsgNotFound):
			rec.Seq = 0
		default:
			return contracts.CommandLogAppendResult{}, err
		}
		pubAck, err := r.js.Publish(
			subject,
			mustJSON(rec),
			nats.MsgId(commandMessageID(rec)),
			nats.ExpectStream(commandStream),
			nats.ExpectLastSequencePerSubject(expectedLastSubjectSequence),
		)
		if err != nil {
			if strings.Contains(err.Error(), "wrong last sequence") {
				continue
			}
			return contracts.CommandLogAppendResult{}, err
		}
		return contracts.CommandLogAppendResult{Seq: rec.Seq, Duplicate: pubAck.Duplicate}, nil
	}
	return contracts.CommandLogAppendResult{}, fmt.Errorf("%w for match %s after repeated CAS conflicts", ErrCommandLogSequenceConflict, rec.MatchID)
}

func sameCommandIntent(previous, current contracts.CommandLogRecord) bool {
	if previous.RoomID != current.RoomID ||
		previous.MatchID != current.MatchID ||
		previous.CommandID != current.CommandID ||
		previous.Epoch != current.Epoch ||
		(previous.OwnerNode != "" && current.OwnerNode != "" && previous.OwnerNode != current.OwnerNode) ||
		previous.PlayerID != current.PlayerID ||
		previous.Type != current.Type {
		return false
	}
	previousPayload, previousErr := json.Marshal(previous.Payload)
	currentPayload, currentErr := json.Marshal(current.Payload)
	return previousErr == nil && currentErr == nil && bytes.Equal(previousPayload, currentPayload)
}

func commandMessageID(rec contracts.CommandLogRecord) string {
	if rec.CommandID != "" {
		return rec.MatchID + ":" + rec.CommandID
	}
	return fmt.Sprintf("%s:%d", rec.MatchID, rec.Seq)
}

func normalizeCommandLogRecord(rec *contracts.CommandLogRecord) {
	if rec.Kind == "" {
		rec.Kind = contracts.CommandLogKindCommand
	}
}

func (r *Router) ListCommandLog(matchID string, afterSeq int64) ([]contracts.CommandLogRecord, error) {
	if matchID == "" {
		return nil, errors.New("match id is required")
	}
	subject := fmt.Sprintf("cmdlog.%s", matchID)
	consumer := "replay_" + uuid.NewString()
	sub, err := r.js.PullSubscribe(
		subject,
		consumer,
		nats.BindStream(commandStream),
		nats.DeliverAll(),
		nats.AckExplicit(),
		nats.ReplayInstant(),
		nats.MaxDeliver(1),
		nats.MaxAckPending(commandReplayBatch*2),
		nats.InactiveThreshold(30*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = r.js.DeleteConsumer(commandStream, consumer)
	}()

	out := []contracts.CommandLogRecord{}
	for {
		msgs, err := sub.Fetch(commandReplayBatch, nats.MaxWait(commandReplayWait))
		if err != nil {
			if err == nats.ErrTimeout {
				return out, nil
			}
			return nil, err
		}
		for _, msg := range msgs {
			var rec contracts.CommandLogRecord
			if err := json.Unmarshal(msg.Data, &rec); err != nil {
				_ = msg.Ack()
				return nil, err
			}
			normalizeCommandLogRecord(&rec)
			if metadata, metadataErr := msg.Metadata(); metadataErr == nil {
				rec.StreamSequence = metadata.Sequence.Stream
			}
			if rec.Kind == contracts.CommandLogKindOwnerFence || rec.Seq > afterSeq {
				out = append(out, rec)
			}
			_ = msg.Ack()
		}
		if len(msgs) < commandReplayBatch {
			return out, nil
		}
	}
}

func ensureCommandStream(js nats.JetStreamContext) error {
	cfg := &nats.StreamConfig{
		Name:       commandStream,
		Subjects:   []string{commandSubject},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   commandReplicas(),
		MaxAge:     commandMaxAge,
		MaxMsgSize: 1 << 20,
		Duplicates: commandDuplicateTTL,
		NoAck:      false,
	}
	if _, err := js.AddStream(cfg); err == nil {
		return nil
	} else if err != nats.ErrStreamNameAlreadyInUse {
		return err
	}
	_, err := js.UpdateStream(cfg)
	return err
}

func commandReplicas() int {
	replicas, err := strconv.Atoi(os.Getenv("NATS_COMMAND_REPLICAS"))
	if err != nil || replicas < 1 || replicas > 5 {
		return 1
	}
	return replicas
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
