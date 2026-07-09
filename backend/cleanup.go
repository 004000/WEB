package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var githubBackupToken = os.Getenv("GITHUB_BACKUP_TOKEN")
var githubBackupRepo = os.Getenv("GITHUB_BACKUP_REPO") // format: "owner/repo"

func getRetentionDays() int {
	days, err := strconv.Atoi(os.Getenv("CLEANUP_RETENTION_DAYS"))
	if err != nil || days <= 0 {
		return 90
	}
	return days
}

type CleanupResult struct {
	RetentionDays  int    `json:"retentionDays"`
	MessagesFound  int    `json:"messagesFound"`
	BackedUp       bool   `json:"backedUp"`
	BackupSkipped  string `json:"backupSkipped,omitempty"`
	MessagesPurged int    `json:"messagesPurged"`
}

// runCleanup finds messages older than the retention period, optionally backs them up
// to a GitHub repo, and only then permanently deletes them from Redis.
// If a backup is configured but fails, deletion is aborted for safety.
func runCleanup(ctx context.Context) (*CleanupResult, error) {
	retentionDays := getRetentionDays()
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	oldKeys, err := rdb.ZRangeByScore(ctx, "m_times:1", &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(cutoff.Unix(), 10),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list old messages: %v", err)
	}

	result := &CleanupResult{RetentionDays: retentionDays, MessagesFound: len(oldKeys)}

	if len(oldKeys) == 0 {
		return result, nil
	}

	messages := make([]Message, 0, len(oldKeys))
	for _, key := range oldKeys {
		data, err := rdb.HGetAll(ctx, key).Result()
		if err != nil || len(data) == 0 {
			continue
		}
		messages = append(messages, messageFromHash(data))
	}

	if githubBackupToken == "" || githubBackupRepo == "" {
		result.BackupSkipped = "GITHUB_BACKUP_TOKEN or GITHUB_BACKUP_REPO not configured"
		return result, nil
	}

	if err := backupToGithub(ctx, messages, retentionDays); err != nil {
		return result, fmt.Errorf("backup failed, deletion aborted for safety: %v", err)
	}
	result.BackedUp = true

	pipe := rdb.Pipeline()
	for _, key := range oldKeys {
		idStr := strings.TrimPrefix(key, "messages:")
		pipe.Del(ctx, key)
		pipe.Del(ctx, fmt.Sprintf("message:%s:reactions", idStr))
		pipe.ZRem(ctx, "m_times:1", key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return result, fmt.Errorf("backup succeeded but deletion failed: %v", err)
	}
	result.MessagesPurged = len(oldKeys)

	return result, nil
}

func messageFromHash(data map[string]string) Message {
	var m Message
	m.ID, _ = strconv.Atoi(data["id"])
	m.Type = data["type"]
	m.Text = data["text"]
	m.Author = data["author"]
	m.AuthorId = data["authorId"]
	if ts, err := time.Parse(time.RFC3339Nano, data["timestamp"]); err == nil {
		m.Timestamp = ts
	}
	m.Deleted = data["deleted"] == "1" || data["deleted"] == "true"
	views, _ := strconv.Atoi(data["views"])
	m.Views = views
	m.IsAds = data["is_ads"] == "1" || data["is_ads"] == "true"
	if data["reactions"] != "" {
		_ = json.Unmarshal([]byte(data["reactions"]), &m.Reactions)
	}
	return m
}

// backupToGithub creates a new file in the configured backup repo containing the
// given messages as JSON, using the GitHub Contents API.
func backupToGithub(ctx context.Context, messages []Message, retentionDays int) error {
	payload, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("backups/messages_%s.json", time.Now().Format("2006-01-02_150405"))
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", githubBackupRepo, path)

	body := map[string]string{
		"message": fmt.Sprintf("Backup %d messages older than %d days", len(messages), retentionDays),
		"content": base64.StdEncoding.EncodeToString(payload),
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+githubBackupToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	return nil
}

func triggerCleanup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := runCleanup(ctx)
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		log.Printf("Manual cleanup failed: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(result)
}

func getStorageUsage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")

	info, err := rdb.Info(ctx, "memory").Result()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	usedBytes := int64(0)
	maxBytes := int64(0)
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "used_memory:") {
			usedBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "used_memory:"), 10, 64)
		}
		if strings.HasPrefix(line, "maxmemory:") {
			maxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "maxmemory:"), 10, 64)
		}
	}

	messageCount, _ := rdb.ZCard(ctx, "m_times:1").Result()

	response := map[string]interface{}{
		"usedBytes":     usedBytes,
		"maxBytes":      maxBytes,
		"messageCount":  messageCount,
		"retentionDays": getRetentionDays(),
		"backupEnabled": githubBackupToken != "" && githubBackupRepo != "",
	}
	if maxBytes > 0 {
		response["percentUsed"] = float64(usedBytes) / float64(maxBytes) * 100
	}

	json.NewEncoder(w).Encode(response)
}

// startCleanupScheduler runs the cleanup job once per day in the background.
func startCleanupScheduler() {
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			result, err := runCleanup(ctx)
			cancel()
			if err != nil {
				log.Printf("Scheduled cleanup failed: %v\n", err)
			} else if result.MessagesPurged > 0 {
				log.Printf("Scheduled cleanup: backed up and purged %d messages older than %d days\n", result.MessagesPurged, result.RetentionDays)
			}
		}
	}()
}
