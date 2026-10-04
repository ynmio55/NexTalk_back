package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ynmio55/NexTalk_back/internal/auth"
)

func newID() string {
	return uuid.NewString()
}

func (a *app) ensureDefaultWorkspace(ctx context.Context, userID string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var serverID string
	err = tx.QueryRow(ctx, "SELECT id FROM servers ORDER BY created_at LIMIT 1").Scan(&serverID)
	if errors.Is(err, pgx.ErrNoRows) {
		serverID = newID()
		_, err = tx.Exec(ctx,
			"INSERT INTO servers (id,name,owner_id) VALUES ($1,'NexTalk',$2)",
			serverID, userID,
		)
		if err != nil {
			return err
		}

		_, err = tx.Exec(ctx,
			"INSERT INTO channels (id,server_id,name,type,position) VALUES ($1,$2,'general','text',0),($3,$2,'random','text',1),($4,$2,'General Voice','voice',2),($5,$2,'Gaming','voice',3)",
			newID(), serverID, newID(), newID(), newID(),
		)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	_, err = tx.Exec(ctx,
		"INSERT INTO server_members (server_id,user_id,role) VALUES ($1,$2,'member') ON CONFLICT (server_id,user_id) DO NOTHING",
		serverID, userID,
	)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *app) state(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	if err := a.ensureDefaultWorkspace(r.Context(), claims.UserID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "workspace unavailable"})
		return
	}

	var serverID, serverName string
	err := a.db.QueryRow(r.Context(),
		"SELECT s.id,s.name FROM servers s JOIN server_members sm ON sm.server_id=s.id WHERE sm.user_id=$1 ORDER BY s.created_at LIMIT 1",
		claims.UserID,
	).Scan(&serverID, &serverName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}

	rows, err := a.db.Query(r.Context(),
		"SELECT id,name,type,position FROM channels WHERE server_id=$1 ORDER BY position,name",
		serverID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "channel query failed"})
		return
	}
	defer rows.Close()

	channels := []map[string]any{}
	for rows.Next() {
		var id, name, typ string
		var position int
		if err := rows.Scan(&id, &name, &typ, &position); err == nil {
			channels = append(channels, map[string]any{
				"id": id,
				"name": name,
				"type": typ,
				"position": position,
			})
		}
	}

	memberRows, err := a.db.Query(r.Context(),
		"SELECT u.id,u.username,u.display_name,u.avatar_url,sm.role FROM server_members sm JOIN users u ON u.id=sm.user_id WHERE sm.server_id=$1 ORDER BY u.display_name",
		serverID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "member query failed"})
		return
	}
	defer memberRows.Close()

	members := []map[string]string{}
	for memberRows.Next() {
		var id, username, displayName, avatarURL, role string
		if err := memberRows.Scan(&id, &username, &displayName, &avatarURL, &role); err == nil {
			members = append(members, map[string]string{
				"id": id,
				"username": username,
				"display_name": displayName,
				"avatar_url": avatarURL,
				"role": role,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]string{"id": serverID, "name": serverName},
		"channels": channels,
		"members": members,
		"online": a.hub.presence(),
	})
}

func (a *app) channelMessages(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	channelID := r.PathValue("id")
	if !a.canAccessChannel(r.Context(), claims.UserID, channelID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	rows, err := a.db.Query(r.Context(),
		"SELECT m.id,m.channel_id,m.author_id,u.display_name,m.content,m.created_at FROM messages m JOIN users u ON u.id=m.author_id WHERE m.channel_id=$1 ORDER BY m.created_at DESC LIMIT 100",
		channelID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "message query failed"})
		return
	}
	defer rows.Close()

	messages := []map[string]any{}
	for rows.Next() {
		var id, cid, authorID, display, content string
		var created time.Time
		if err := rows.Scan(&id, &cid, &authorID, &display, &content, &created); err == nil {
			messages = append(messages, map[string]any{
				"id": id,
				"channel_id": cid,
				"author_id": authorID,
				"author": display,
				"text": content,
				"sent_at": created.UTC(),
			})
		}
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
}

func (a *app) canAccessChannel(ctx context.Context, userID, channelID string) bool {
	var ok bool
	err := a.db.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM channels c JOIN server_members sm ON sm.server_id=c.server_id WHERE c.id=$1 AND sm.user_id=$2)",
		channelID, userID,
	).Scan(&ok)
	return err == nil && ok
}
