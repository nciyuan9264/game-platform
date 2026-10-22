package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

const DefaultRoomLease = 30 * time.Second

var ErrNotOwner = errors.New("not room owner")

type RoomOwner struct {
	RoomID         string    `json:"roomID"`
	OwnerNode      string    `json:"ownerNode"`
	Epoch          int64     `json:"epoch"`
	LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type Coordinator struct {
	client *clientv3.Client
}

func Open(ctx context.Context, endpoints string) (*Coordinator, error) {
	parts := strings.Split(endpoints, ",")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) == 0 {
		clean = []string{"localhost:2379"}
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   clean,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	coord := &Coordinator{client: client}
	if err := coord.waitReady(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return coord, nil
}

func (c *Coordinator) Close() error {
	return c.client.Close()
}

func (c *Coordinator) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := c.client.Get(pingCtx, "/game-platform/health")
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastErr
}

func (c *Coordinator) InitRoomOwner(ctx context.Context, rec contracts.RoomRecord, ttl time.Duration) (RoomOwner, error) {
	owner := RoomOwner{
		RoomID:         rec.RoomID,
		OwnerNode:      rec.OwnerNode,
		Epoch:          rec.Epoch,
		LeaseExpiresAt: time.Now().UTC().Add(ttl),
		UpdatedAt:      time.Now().UTC(),
	}
	key := roomOwnerKey(rec.RoomID)
	lease, err := c.client.Grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return RoomOwner{}, err
	}
	txn, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, mustJSON(owner), clientv3.WithLease(lease.ID))).
		Else(clientv3.OpGet(key)).
		Commit()
	if err != nil {
		c.revokeLease(lease.ID)
		return RoomOwner{}, err
	}
	if txn.Succeeded {
		return owner, nil
	}
	c.revokeLease(lease.ID)
	if len(txn.Responses) == 0 || len(txn.Responses[0].GetResponseRange().Kvs) == 0 {
		return RoomOwner{}, errors.New("room owner init conflict")
	}
	return decodeOwner(txn.Responses[0].GetResponseRange().Kvs[0].Value)
}

func (c *Coordinator) DeleteRoomOwner(ctx context.Context, roomID, ownerNode string, epoch int64) error {
	key := roomOwnerKey(roomID)
	resp, err := c.client.Get(ctx, key)
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return nil
	}
	kv := resp.Kvs[0]
	owner, err := decodeOwner(kv.Value)
	if err != nil {
		return err
	}
	if owner.OwnerNode != ownerNode || owner.Epoch != epoch {
		return nil
	}
	txn, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", kv.ModRevision)).
		Then(
			clientv3.OpDelete(key),
			clientv3.OpDelete(roomReadyKey(roomID)),
		).
		Commit()
	if err != nil {
		return err
	}
	if !txn.Succeeded {
		return ErrNotOwner
	}
	if kv.Lease != 0 {
		_, err = c.client.Revoke(ctx, clientv3.LeaseID(kv.Lease))
	}
	return err
}

func (c *Coordinator) EnsureRoomOwner(ctx context.Context, rec contracts.RoomRecord, nodeID string, ttl time.Duration) (RoomOwner, error) {
	if rec.OwnerNode != nodeID {
		return RoomOwner{}, ErrNotOwner
	}
	key := roomOwnerKey(rec.RoomID)
	resp, err := c.client.Get(ctx, key)
	if err != nil {
		return RoomOwner{}, err
	}
	if len(resp.Kvs) == 0 {
		owner := RoomOwner{RoomID: rec.RoomID, OwnerNode: nodeID, Epoch: rec.Epoch, LeaseExpiresAt: time.Now().UTC().Add(ttl), UpdatedAt: time.Now().UTC()}
		return c.putOwnerIfMissing(ctx, key, owner, ttl)
	}
	owner, err := decodeOwner(resp.Kvs[0].Value)
	if err != nil {
		return RoomOwner{}, err
	}
	if owner.OwnerNode != nodeID || owner.Epoch != rec.Epoch {
		return RoomOwner{}, ErrNotOwner
	}
	return c.renewOwnerAtRevision(ctx, key, owner, resp.Kvs[0].ModRevision, ttl)
}

func (c *Coordinator) RenewRoomOwner(ctx context.Context, roomID, nodeID string, epoch int64, ttl time.Duration) (RoomOwner, error) {
	key := roomOwnerKey(roomID)
	resp, err := c.client.Get(ctx, key)
	if err != nil {
		return RoomOwner{}, err
	}
	if len(resp.Kvs) == 0 {
		return RoomOwner{}, ErrNotOwner
	}
	owner, err := decodeOwner(resp.Kvs[0].Value)
	if err != nil {
		return RoomOwner{}, err
	}
	if owner.OwnerNode != nodeID || owner.Epoch != epoch {
		return RoomOwner{}, ErrNotOwner
	}
	return c.renewOwnerAtRevision(ctx, key, owner, resp.Kvs[0].ModRevision, ttl)
}

func (c *Coordinator) VerifyRoomOwner(ctx context.Context, roomID, nodeID string, epoch int64) error {
	resp, err := c.client.Get(ctx, roomOwnerKey(roomID))
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return ErrNotOwner
	}
	owner, err := decodeOwner(resp.Kvs[0].Value)
	if err != nil {
		return err
	}
	if owner.OwnerNode != nodeID || owner.Epoch != epoch {
		return ErrNotOwner
	}
	return nil
}

func (c *Coordinator) ClaimExpiredRoom(ctx context.Context, rec contracts.RoomRecord, nodeID string, ttl time.Duration) (RoomOwner, bool, error) {
	if contracts.IsTerminalRoomStatus(rec.Status) || rec.OwnerNode != nodeID {
		return RoomOwner{}, false, nil
	}
	key := roomOwnerKey(rec.RoomID)
	owner := RoomOwner{
		RoomID:         rec.RoomID,
		OwnerNode:      nodeID,
		Epoch:          rec.Epoch,
		LeaseExpiresAt: time.Now().UTC().Add(ttl),
		UpdatedAt:      time.Now().UTC(),
	}
	lease, err := c.client.Grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return RoomOwner{}, false, err
	}
	txn, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, mustJSON(owner), clientv3.WithLease(lease.ID))).
		Commit()
	if err != nil {
		c.revokeLease(lease.ID)
		return RoomOwner{}, false, err
	}
	if !txn.Succeeded {
		c.revokeLease(lease.ID)
	}
	return owner, txn.Succeeded, nil
}

func (c *Coordinator) PublishRoomReady(ctx context.Context, roomID, nodeID string, epoch int64) error {
	key := roomOwnerKey(roomID)
	resp, err := c.client.Get(ctx, key)
	if err != nil {
		return err
	}
	if len(resp.Kvs) == 0 {
		return ErrNotOwner
	}
	kv := resp.Kvs[0]
	owner, err := decodeOwner(kv.Value)
	if err != nil {
		return err
	}
	if owner.OwnerNode != nodeID || owner.Epoch != epoch || kv.Lease == 0 {
		return ErrNotOwner
	}
	txn, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", kv.ModRevision)).
		Then(clientv3.OpPut(roomReadyKey(roomID), mustJSON(owner), clientv3.WithLease(clientv3.LeaseID(kv.Lease)))).
		Commit()
	if err != nil {
		return err
	}
	if !txn.Succeeded {
		return ErrNotOwner
	}
	return nil
}

func (c *Coordinator) ReadyRoomOwner(ctx context.Context, roomID string) (RoomOwner, error) {
	resp, err := c.client.Get(ctx, roomReadyKey(roomID))
	if err != nil {
		return RoomOwner{}, err
	}
	if len(resp.Kvs) == 0 {
		return RoomOwner{}, ErrNotOwner
	}
	return decodeOwner(resp.Kvs[0].Value)
}

func (c *Coordinator) putOwnerIfMissing(ctx context.Context, key string, owner RoomOwner, ttl time.Duration) (RoomOwner, error) {
	lease, err := c.client.Grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return RoomOwner{}, err
	}
	txn, err := c.client.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, mustJSON(owner), clientv3.WithLease(lease.ID))).
		Else(clientv3.OpGet(key)).
		Commit()
	if err != nil {
		c.revokeLease(lease.ID)
		return RoomOwner{}, err
	}
	if txn.Succeeded {
		return owner, nil
	}
	c.revokeLease(lease.ID)
	if len(txn.Responses) == 0 || len(txn.Responses[0].GetResponseRange().Kvs) == 0 {
		return RoomOwner{}, errors.New("room owner race")
	}
	current, err := decodeOwner(txn.Responses[0].GetResponseRange().Kvs[0].Value)
	if err != nil {
		return RoomOwner{}, err
	}
	if current.OwnerNode != owner.OwnerNode || current.Epoch != owner.Epoch {
		return RoomOwner{}, ErrNotOwner
	}
	return current, nil
}

func (c *Coordinator) renewOwnerAtRevision(ctx context.Context, key string, owner RoomOwner, revision int64, ttl time.Duration) (RoomOwner, error) {
	owner.LeaseExpiresAt = time.Now().UTC().Add(ttl)
	owner.UpdatedAt = time.Now().UTC()
	lease, err := c.client.Grant(ctx, leaseSeconds(ttl))
	if err != nil {
		return RoomOwner{}, err
	}
	readyKey := roomReadyKey(owner.RoomID)
	readyResp, err := c.client.Get(ctx, readyKey)
	if err != nil {
		c.revokeLease(lease.ID)
		return RoomOwner{}, err
	}
	compares := []clientv3.Cmp{clientv3.Compare(clientv3.ModRevision(key), "=", revision)}
	operations := []clientv3.Op{clientv3.OpPut(key, mustJSON(owner), clientv3.WithLease(lease.ID))}
	if len(readyResp.Kvs) > 0 {
		readyKV := readyResp.Kvs[0]
		readyOwner, decodeErr := decodeOwner(readyKV.Value)
		if decodeErr != nil {
			c.revokeLease(lease.ID)
			return RoomOwner{}, decodeErr
		}
		if readyOwner.OwnerNode != owner.OwnerNode || readyOwner.Epoch != owner.Epoch {
			c.revokeLease(lease.ID)
			return RoomOwner{}, ErrNotOwner
		}
		compares = append(compares, clientv3.Compare(clientv3.ModRevision(readyKey), "=", readyKV.ModRevision))
		operations = append(operations, clientv3.OpPut(readyKey, mustJSON(owner), clientv3.WithLease(lease.ID)))
	}
	txn, err := c.client.Txn(ctx).
		If(compares...).
		Then(operations...).
		Commit()
	if err != nil {
		c.revokeLease(lease.ID)
		return RoomOwner{}, err
	}
	if !txn.Succeeded {
		c.revokeLease(lease.ID)
		return RoomOwner{}, ErrNotOwner
	}
	return owner, nil
}

func (c *Coordinator) revokeLease(leaseID clientv3.LeaseID) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = c.client.Revoke(ctx, leaseID)
}

func roomOwnerKey(roomID string) string {
	return fmt.Sprintf("/game-platform/rooms/%s/owner", roomID)
}

func roomReadyKey(roomID string) string {
	return fmt.Sprintf("/game-platform/rooms/%s/ready", roomID)
}

func leaseSeconds(ttl time.Duration) int64 {
	seconds := int64(ttl / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func decodeOwner(raw []byte) (RoomOwner, error) {
	var owner RoomOwner
	err := json.Unmarshal(raw, &owner)
	return owner, err
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
