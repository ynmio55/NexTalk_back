package main

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ynmio55/NexTalk_back/internal/auth"
)

const maxMessageLength = 4000

type client struct {
	conn      *websocket.Conn
	userID    string
	username  string
	display   string
	voiceRoom string
	send      chan any
}

type hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
}

func newHub() *hub {
	return &hub{clients: map[*client]struct{}{}}
}

func (h *hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *hub) setVoiceRoom(c *client, room string) {
	h.mu.Lock()
	c.voiceRoom = room
	h.mu.Unlock()
}

func (h *hub) broadcast(v any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- v:
		default:
		}
	}
}

func (h *hub) sendTo(userID string, v any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.userID != userID {
			continue
		}
		select {
		case c.send <- v:
		default:
		}
	}
}

func (h *hub) presence() []map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	seen := map[string]map[string]string{}
	for c := range h.clients {
		if _, ok := seen[c.userID]; !ok {
			seen[c.userID] = map[string]string{
				"id": c.userID,
				"username": c.username,
				"display_name": c.display,
				"voice_room": c.voiceRoom,
			}
		} else if c.voiceRoom != "" {
			seen[c.userID]["voice_room"] = c.voiceRoom
		}
	}
	out := make([]map[string]string, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["display_name"] < out[j]["display_name"]
	})
	return out
}

func (a *app) ws(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, err := auth.Parse(a.cfg.JWTSecret, token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var display string
	if err := a.db.QueryRow(r.Context(),
		"SELECT display_name FROM users WHERE id=$1",
		claims.UserID,
	).Scan(&display); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 10)

	c := &client{
		conn: conn,
		userID: claims.UserID,
		username: claims.Username,
		display: display,
		send: make(chan any, 64),
	}
	a.hub.add(c)
	a.broadcastPresence()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case v, ok := <-c.send:
				if !ok {
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(v); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	c.send <- map[string]any{
		"type": "ready",
		"user": map[string]string{
			"id": c.userID,
			"username": c.username,
			"display_name": c.display,
		},
	}

	defer func() {
		a.hub.remove(c)
		close(c.send)
		_ = conn.Close()
		<-done
		a.broadcastPresence()
	}()

	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	})

	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		typ, _ := msg["type"].(string)

		switch typ {
		case "chat":
			a.handleChat(c, msg)
		case "voice_join":
			room, _ := msg["channel_id"].(string)
			if room != "" && a.canAccessChannel(r.Context(), c.userID, room) {
				a.hub.setVoiceRoom(c, room)
				a.broadcastPresence()
			}
		case "voice_leave":
			a.hub.setVoiceRoom(c, "")
			a.broadcastPresence()
		case "voice_signal":
			target, _ := msg["target"].(string)
			if target != "" {
				a.hub.sendTo(target, map[string]any{
					"type": "voice_signal",
					"from": c.userID,
					"signal": msg["signal"],
				})
			}
		}
	}
}

func (a *app) handleChat(c *client, msg map[string]any) {
	channelID, _ := msg["channel_id"].(string)
	text, _ := msg["text"].(string)
	text = strings.TrimSpace(text)

	if channelID == "" || text == "" || len([]rune(text)) > maxMessageLength {
		return
	}
	if !a.canAccessChannel(context.Background(), c.userID, channelID) {
		return
	}

	id := newID()
	now := time.Now().UTC()
	_, err := a.db.Exec(
		context.Background(),
		"INSERT INTO messages (id,channel_id,author_id,content,created_at) VALUES ($1,$2,$3,$4,$5)",
		id, channelID, c.userID, text, now,
	)
	if err != nil {
		return
	}

	a.hub.broadcast(map[string]any{
		"type": "chat",
		"id": id,
		"channel_id": channelID,
		"author_id": c.userID,
		"author": c.display,
		"text": text,
		"sent_at": now,
	})
}

func (a *app) broadcastPresence() {
	a.hub.broadcast(map[string]any{
		"type": "presence",
		"users": a.hub.presence(),
	})
}

func (a *app) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	for _, allowed := range strings.Split(a.cfg.CORSOrigin, ",") {
		allowed = strings.TrimSpace(allowed)
		if allowed == "*" || allowed == origin {
			return true
		}
		if u, err := url.Parse(allowed); err == nil && u.Scheme+"://"+u.Host == origin {
			return true
		}
	}
	return false
}
