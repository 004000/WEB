package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// getStatus is a lightweight, API-key-protected endpoint (same key as message
// import) that exposes basic health/usage info for an external multi-channel
// dashboard. CORS is enabled so a browser-hosted dashboard can call it
// directly from any origin.
func getStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	key := r.Header.Get("X-API-Key")
	if key != settingConfig.ApiSecretKey {
		http.Error(w, "error", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	usage, err := fetchStorageUsage(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	liveCount := getLiveConnectionCount(ctx)

	channelName := ""
	if details, err := getChannelDetails(ctx); err == nil {
		channelName = details["name"]
	}

	response := map[string]interface{}{
		"channelName":        channelName,
		"messageCount":       usage.MessageCount,
		"usedBytes":          usage.UsedBytes,
		"maxBytes":           usage.MaxBytes,
		"percentUsed":        usage.PercentUsed,
		"connectedUsersLive": liveCount,
		"backupEnabled":      usage.BackupEnabled,
		"estimatedDaysLeft":  usage.EstimatedDaysRemaining,
		"version":            serverStartTime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func addNewPost(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-API-Key")
	if key != settingConfig.ApiSecretKey {
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	var message Message
	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	message.ID = getMessageNextId(ctx)
	message.Type = "md" //body.Type
	message.Author = body.Author
	message.Timestamp = body.Timestamp
	message.Text = body.Text
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
