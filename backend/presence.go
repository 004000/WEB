package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const connectedUsersLiveKey = "connected_users_live"

type LiveConnectionInfo struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	ConnectedAt string `json:"connectedAt"`
	LastSeen    string `json:"lastSeen"`
}

// registerConnectedUser records a single SSE connection's identity (or marks
// it as a guest if not logged in) and returns a unique id for that connection,
// to be passed to unregisterConnectedUser when the connection closes.
func registerConnectedUser(r *http.Request) string {
	connectionId := generatedRandomID(20)
	if connectionId == "" {
		return ""
	}

	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	info := LiveConnectionInfo{
		Email:       user.Email,
		Name:        user.PublicName,
		ConnectedAt: time.Now().Format(time.RFC3339),
		LastSeen:    time.Now().Format(time.RFC3339),
	}
	if info.Name == "" {
		info.Name = user.Username
	}
	if info.Email == "" && info.Name == "" {
		shortId := connectionId
		if len(shortId) > 4 {
			shortId = shortId[len(shortId)-4:]
		}
		info.Name = fmt.Sprintf("אורח #%s", strings.ToUpper(shortId))
	}

	data, _ := json.Marshal(info)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb.HSet(ctx, connectedUsersLiveKey, connectionId, data)

	return connectionId
}

// refreshConnectedUser updates the last-seen timestamp for an open connection,
// called on every SSE heartbeat so stale entries (from ungraceful shutdowns)
// can be filtered out even if unregisterConnectedUser never ran.
func refreshConnectedUser(connectionId string) {
	if connectionId == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := rdb.HGet(ctx, connectedUsersLiveKey, connectionId).Result()
	if err != nil {
		return
	}
	var info LiveConnectionInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return
	}
	info.LastSeen = time.Now().Format(time.RFC3339)
	data, _ := json.Marshal(info)
	rdb.HSet(ctx, connectedUsersLiveKey, connectionId, data)
}

func unregisterConnectedUser(connectionId string) {
	if connectionId == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb.HDel(ctx, connectedUsersLiveKey, connectionId)
}

// listLiveConnections returns every currently-open connection, opportunistically
// removing any entry that hasn't sent a heartbeat in the last 90 seconds (a
// "ghost" left behind by an ungraceful server restart). This is the single
// source of truth for both the admin live list and the header live count,
// so the two can never show different numbers.
func listLiveConnections(ctx context.Context) ([]LiveConnectionInfo, error) {
	data, err := rdb.HGetAll(ctx, connectedUsersLiveKey).Result()
	if err != nil {
		return nil, err
	}

	connections := make([]LiveConnectionInfo, 0, len(data))
	staleCutoff := time.Now().Add(-30 * time.Second)
	for connectionId, raw := range data {
		var info LiveConnectionInfo
		if err := json.Unmarshal([]byte(raw), &info); err != nil {
			continue
		}
		lastSeen, err := time.Parse(time.RFC3339, info.LastSeen)
		if err != nil || lastSeen.Before(staleCutoff) {
			rdb.HDel(ctx, connectedUsersLiveKey, connectionId)
			continue
		}
		connections = append(connections, info)
	}

	return connections, nil
}

// getLiveConnectionCount is the same source of truth as listLiveConnections,
// used wherever only the number (not the full list) is needed, such as the
// header "X מחוברים" badge.
func getLiveConnectionCount(ctx context.Context) int64 {
	connections, err := listLiveConnections(ctx)
	if err != nil {
		return 0
	}
	return int64(len(connections))
}

// broadcastLiveConnectionCount re-reads the current count from Redis (the
// same data the admin panel's live list uses) and pushes it to all connected
// clients, so the header badge and the admin list can never drift apart.
func broadcastLiveConnectionCount() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count := getLiveConnectionCount(ctx)

	payload, err := json.Marshal(struct {
		Type  string `json:"type"`
		Count int64  `json:"count"`
	}{Type: "connected-users", Count: count})
	if err != nil {
		return
	}
	rdb.Publish(ctx, "events", payload)
}

// leaveConnection lets the browser proactively signal that a connection is
// closing (tab closed, page refreshed, navigated away) via sendBeacon, so it
// disappears from the live list immediately instead of waiting for the
// heartbeat-based staleness timeout. No privilege check: any client can only
// ever remove its own connection id, which carries no sensitive data.
func leaveConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Id string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Id == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	unregisterConnectedUser(body.Id)
	go broadcastLiveConnectionCount()

	w.WriteHeader(http.StatusNoContent)
}

func getConnectedUsersLive(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connections, err := listLiveConnections(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(connections)
}
