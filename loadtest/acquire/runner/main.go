package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type config struct {
	HTTPBase     string
	WSBase       string
	Token        string
	AdminToken   string
	Prefix       string
	Mode         string
	Rooms        int
	Duration     time.Duration
	ActionDelay  time.Duration
	AccessTokens []string
}

type metrics struct {
	connected    atomic.Int64
	disconnected atomic.Int64
	messages     atomic.Int64
	actions      atomic.Int64
	errors       atomic.Int64
	readErrors   atomic.Int64
	writeErrors  atomic.Int64
	recvBytes    atomic.Int64
	sentBytes    atomic.Int64
	startedAt    time.Time
}

type loadClient struct {
	roomID        string
	userID        string
	accessToken   string
	conn          *websocket.Conn
	cfg           config
	m             *metrics
	lastActionKey string
}

func main() {
	cfg := parseConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rooms, err := createRooms(ctx, cfg)
	if err != nil {
		log.Fatalf("prepare rooms: %v", err)
	}
	log.Printf("prepared %d rooms", len(rooms))

	m := &metrics{startedAt: time.Now()}
	go reportMetrics(ctx, m)

	if err := run(ctx, cfg, rooms, m); err != nil {
		log.Fatalf("run loadtest: %v", err)
	}
}

func parseConfig() config {
	cfg := config{}
	flag.StringVar(&cfg.HTTPBase, "http-base", getenv("ACQUIRE_HTTP_BASE", "http://127.0.0.1:8083"), "Platform API base URL")
	flag.StringVar(&cfg.WSBase, "ws-base", getenv("ACQUIRE_WS_BASE", "ws://127.0.0.1:18000/ws"), "Gateway WebSocket URL")
	flag.StringVar(&cfg.Token, "token", os.Getenv("ACQUIRE_LOADTEST_TOKEN"), "X-Loadtest-Token value")
	flag.StringVar(&cfg.AdminToken, "admin-token", os.Getenv("ACQUIRE_ADMIN_TOKEN"), "administrator access token")
	flag.StringVar(&cfg.Prefix, "prefix", "lt-local", "loadtest room/user prefix, must start with lt-")
	flag.StringVar(&cfg.Mode, "mode", "connect", "connect, ai, or ws6")
	flag.IntVar(&cfg.Rooms, "rooms", 1, "number of rooms to create")
	flag.DurationVar(&cfg.Duration, "duration", time.Minute, "test duration")
	flag.DurationVar(&cfg.ActionDelay, "action-delay", 200*time.Millisecond, "delay before sending a decided game action")
	flag.Parse()
	for _, token := range strings.Split(os.Getenv("ACQUIRE_ACCESS_TOKENS"), ",") {
		if token = strings.TrimSpace(token); token != "" {
			cfg.AccessTokens = append(cfg.AccessTokens, token)
		}
	}
	return cfg
}

func run(ctx context.Context, cfg config, rooms []string, m *metrics) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	var wg sync.WaitGroup
	for roomIndex, roomID := range rooms {
		switch cfg.Mode {
		case "connect":
			startWS6Room(ctx, &wg, cfg, roomID, roomIndex, false, m)
		case "ai":
			startAIRoom(ctx, &wg, cfg, roomID, roomIndex, m)
		case "ws6":
			startWS6Room(ctx, &wg, cfg, roomID, roomIndex, true, m)
		default:
			return fmt.Errorf("unsupported mode %q", cfg.Mode)
		}
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}

func startAIRoom(ctx context.Context, wg *sync.WaitGroup, cfg config, roomID string, roomIndex int, m *metrics) {
	client := &loadClient{
		roomID:      roomID,
		userID:      userIDForSeat(cfg, roomIndex, 1),
		accessToken: tokenForSeat(cfg, roomIndex, 1),
		cfg:         cfg,
		m:           m,
	}
	if err := client.connect(); err != nil {
		log.Printf("connect ai room failed room=%s err=%v", roomID, err)
		m.errors.Add(1)
		return
	}
	for i := 0; i < 5; i++ {
		client.writeJSON(map[string]any{"type": "match_add_ai"})
	}
	client.writeJSON(map[string]any{"type": "match_begin"})
	client.writeJSON(map[string]any{"type": "game_ready"})
	wg.Add(1)
	go client.readLoop(ctx, wg, true)
}

func startWS6Room(ctx context.Context, wg *sync.WaitGroup, cfg config, roomID string, roomIndex int, active bool, m *metrics) {
	clients := make([]*loadClient, 0, 6)
	for seat := 1; seat <= 6; seat++ {
		client := &loadClient{
			roomID:      roomID,
			userID:      userIDForSeat(cfg, roomIndex, seat),
			accessToken: tokenForSeat(cfg, roomIndex, seat),
			cfg:         cfg,
			m:           m,
		}
		if err := client.connect(); err != nil {
			log.Printf("connect ws6 room failed room=%s seat=%d err=%v", roomID, seat, err)
			m.errors.Add(1)
			continue
		}
		clients = append(clients, client)
	}

	if active && len(clients) > 0 {
		time.Sleep(500 * time.Millisecond)
		for i := 1; i < len(clients); i++ {
			clients[i].writeJSON(map[string]any{"type": "match_ready", "payload": map[string]any{"ready": true}})
		}
		time.Sleep(500 * time.Millisecond)
		clients[0].writeJSON(map[string]any{"type": "match_begin"})
		time.Sleep(500 * time.Millisecond)
		for _, client := range clients {
			client.writeJSON(map[string]any{"type": "game_ready"})
		}
	}

	for _, client := range clients {
		wg.Add(1)
		go client.readLoop(ctx, wg, active)
	}
}

func (c *loadClient) connect() error {
	if c.accessToken == "" {
		return fmt.Errorf("missing access token for room=%s user=%s", c.roomID, c.userID)
	}
	conn, _, err := websocket.DefaultDialer.Dial(webSocketURL(c.cfg.WSBase, c.roomID, c.accessToken), nil)
	if err != nil {
		return err
	}
	c.conn = conn
	c.m.connected.Add(1)
	return nil
}

func (c *loadClient) readLoop(ctx context.Context, wg *sync.WaitGroup, active bool) {
	defer wg.Done()
	defer func() {
		c.conn.Close()
		c.m.disconnected.Add(1)
	}()
	go func() {
		<-ctx.Done()
		c.conn.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				c.m.readErrors.Add(1)
				c.m.errors.Add(1)
				log.Printf("read failed room=%s user=%s close=%s err=%v",
					c.roomID, c.userID, closeDescription(err), err)
			}
			return
		}
		c.m.messages.Add(1)
		c.m.recvBytes.Add(int64(len(data)))
		if messageType != websocket.TextMessage {
			log.Printf("non-text message room=%s user=%s message_type=%d bytes=%d", c.roomID, c.userID, messageType, len(data))
		}
		if !active {
			continue
		}

		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &probe); err != nil || probe.Type != "ROOM_SYNC" {
			continue
		}
		var sync roomSync
		if err := json.Unmarshal(data, &sync); err != nil {
			c.m.errors.Add(1)
			continue
		}
		key := actionKey(sync)
		if key == "" || key == c.lastActionKey {
			continue
		}
		msg, ok := decideAction(sync)
		if !ok {
			continue
		}
		time.Sleep(c.cfg.ActionDelay)
		if ctx.Err() != nil {
			return
		}
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			c.m.writeErrors.Add(1)
			c.m.errors.Add(1)
			log.Printf("write action failed room=%s user=%s bytes=%d close=%s err=%v",
				c.roomID, c.userID, len(msg), closeDescription(err), err)
			continue
		}
		c.m.sentBytes.Add(int64(len(msg)))
		c.lastActionKey = key
		c.m.actions.Add(1)
	}
}

func (c *loadClient) writeJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("marshal write failed room=%s user=%s err=%v", c.roomID, c.userID, err)
		c.m.writeErrors.Add(1)
		c.m.errors.Add(1)
		return
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("write failed room=%s user=%s bytes=%d close=%s err=%v",
			c.roomID, c.userID, len(data), closeDescription(err), err)
		c.m.writeErrors.Add(1)
		c.m.errors.Add(1)
		return
	}
	c.m.sentBytes.Add(int64(len(data)))
}

func createRooms(ctx context.Context, cfg config) ([]string, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("missing loadtest token")
	}
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("missing administrator access token")
	}
	body := map[string]any{
		"prefix":      cfg.Prefix,
		"count":       cfg.Rooms,
		"ownerPrefix": ownerPrefixForRoom(cfg),
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, helperURL(cfg.HTTPBase, "/platform/__loadtest/acquire/rooms"), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Loadtest-Token", cfg.Token)
	req.Header.Set("Authorization", "Bearer "+cfg.AdminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create rooms status %d", resp.StatusCode)
	}
	var result struct {
		Data struct {
			Rooms []string `json:"rooms"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data.Rooms, nil
}

func reportMetrics(ctx context.Context, m *metrics) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logMetrics("final", m)
			return
		case <-ticker.C:
			logMetrics("tick", m)
		}
	}
}

func logMetrics(label string, m *metrics) {
	elapsed := time.Since(m.startedAt)
	recvBytes := m.recvBytes.Load()
	sentBytes := m.sentBytes.Load()
	log.Printf("%s connected=%d disconnected=%d messages=%d actions=%d errors=%d read_errors=%d write_errors=%d recv_bytes=%d sent_bytes=%d recv_mbps=%.3f sent_mbps=%.3f",
		label,
		m.connected.Load(),
		m.disconnected.Load(),
		m.messages.Load(),
		m.actions.Load(),
		m.errors.Load(),
		m.readErrors.Load(),
		m.writeErrors.Load(),
		recvBytes,
		sentBytes,
		throughputMbps(recvBytes, elapsed),
		throughputMbps(sentBytes, elapsed),
	)
}

func throughputMbps(bytes int64, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(bytes*8) / elapsed.Seconds() / 1_000_000
}

func closeDescription(err error) string {
	if closeErr, ok := err.(*websocket.CloseError); ok {
		return fmt.Sprintf("code=%d text=%q", closeErr.Code, closeErr.Text)
	}
	return "none"
}

func webSocketURL(base, roomID, accessToken string) string {
	values := url.Values{}
	values.Set("roomID", roomID)
	values.Set("token", accessToken)
	return strings.TrimRight(base, "/") + "?" + values.Encode()
}

func helperURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func ownerPrefixForRoom(cfg config) string {
	return cfg.Prefix + "-owner"
}

func userIDForSeat(cfg config, roomIndex int, seat int) string {
	if seat == 1 {
		return fmt.Sprintf("%s-%06d", ownerPrefixForRoom(cfg), roomIndex+1)
	}
	return fmt.Sprintf("%s-u-%06d-%d", cfg.Prefix, roomIndex+1, seat)
}

func tokenForSeat(cfg config, roomIndex int, seat int) string {
	index := roomIndex*6 + seat - 1
	if index < 0 || index >= len(cfg.AccessTokens) {
		return ""
	}
	return cfg.AccessTokens[index]
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
