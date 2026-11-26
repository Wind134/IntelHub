package api

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"

	"intel-hub/internal/model"
)

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub broadcasts realtime events (new alerts, 9+ verifications, bookmark updates) to all connected clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[*wsClient]struct{}),
	}
}

func (h *Hub) ActiveCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) Broadcast(msg model.StreamMessage) {
	if msg.SentAt == 0 {
		msg.SentAt = time.Now().Unix()
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Slow consumer dropped frame; client will backfill on reconnect via after_id.
		}
	}
}

func (h *Hub) ServeConn(ctx context.Context, conn *websocket.Conn, replay []model.IntelEvent) {
	c := &wsClient{
		conn: conn,
		send: make(chan []byte, 64),
	}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	// Replay missed events if client reconnected with after_id cursor.
	for i := range replay {
		ev := replay[i]
		frame, _ := json.Marshal(model.StreamMessage{
			Type:   "event.created",
			SeqID:  ev.ID,
			Event:  &ev,
			SentAt: time.Now().Unix(),
		})
		if err := writeWithTimeout(ctx, conn, frame); err != nil {
			return
		}
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case data := <-c.send:
			if err := writeWithTimeout(ctx, conn, data); err != nil {
				return
			}
		case <-pingTicker.C:
			hb, _ := json.Marshal(model.StreamMessage{
				Type:   "heartbeat",
				SentAt: time.Now().Unix(),
			})
			if err := writeWithTimeout(ctx, conn, hb); err != nil {
				log.Printf("[ws] heartbeat failed: %v", err)
				return
			}
		}
	}
}

func writeWithTimeout(ctx context.Context, conn *websocket.Conn, payload []byte) error {
	wctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, payload)
}
