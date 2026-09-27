package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ynmio55/NexTalk_back/internal/auth"
	"github.com/ynmio55/NexTalk_back/internal/config"
)

type app struct {
	db       *pgxpool.Pool
	cfg      config.Config
	upgrader websocket.Upgrader
}

type authRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
	User  any    `json:"user"`
}

func main() {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	a := &app{
		db: db,
		cfg: cfg,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("POST /api/auth/register", a.register)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/me", a.requireAuth(a.me))
	mux.HandleFunc("GET /ws", a.ws)

	log.Printf("NexTalk API listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, a.cors(mux)); err != nil {
		log.Fatal(err)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "nextalk-back"})
}

func (a *app) register(w http.ResponseWriter, r *http.Request) {
	var in authRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	in.Username = strings.TrimSpace(strings.ToLower(in.Username))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if len(in.Username) < 3 || len(in.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username must be 3+ chars and password 8+ chars"})
		return
	}
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "password error"})
		return
	}
	id := uuid.NewString()

	_, err = a.db.Exec(r.Context(),
		`INSERT INTO users (id, username, display_name, password_hash) VALUES ($1,$2,$3,$4)`,
		id, in.Username, in.DisplayName, hash,
	)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
		return
	}

	token, _ := auth.Sign(a.cfg.JWTSecret, id, in.Username)
	writeJSON(w, http.StatusCreated, authResponse{
		Token: token,
		User: map[string]string{"id": id, "username": in.Username, "display_name": in.DisplayName},
	})
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var in authRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	var id, username, displayName, hash string
	err := a.db.QueryRow(r.Context(),
		`SELECT id, username, display_name, password_hash FROM users WHERE username=$1`,
		strings.ToLower(strings.TrimSpace(in.Username)),
	).Scan(&id, &username, &displayName, &hash)
	if err != nil || auth.CheckPassword(hash, in.Password) != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	token, _ := auth.Sign(a.cfg.JWTSecret, id, username)
	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		User: map[string]string{"id": id, "username": username, "display_name": displayName},
	})
}

func (a *app) me(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	var displayName, avatarURL string
	err := a.db.QueryRow(r.Context(),
		`SELECT display_name, avatar_url FROM users WHERE id=$1`,
		claims.UserID,
	).Scan(&displayName, &avatarURL)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id": claims.UserID, "username": claims.Username,
		"display_name": displayName, "avatar_url": avatarURL,
	})
}

func (a *app) ws(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, err := auth.Parse(a.cfg.JWTSecret, token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	_ = conn.WriteJSON(map[string]any{
		"type": "ready",
		"user": map[string]string{"id": claims.UserID, "username": claims.Username},
	})

	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		msg["from"] = claims.UserID
		msg["sent_at"] = time.Now().UTC()
		if err := conn.WriteJSON(msg); err != nil {
			return
		}
	}
}

func (a *app) requireAuth(next func(http.ResponseWriter, *http.Request, *auth.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
			return
		}
		claims, err := auth.Parse(a.cfg.JWTSecret, strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next(w, r, claims)
	}
}

func (a *app) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", a.cfg.CORSOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
