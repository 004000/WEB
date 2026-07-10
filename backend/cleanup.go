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

// getEmergencyThresholdPercent is the storage usage percentage above which the
// emergency cleanup starts purging the oldest messages, regardless of their age.
// Preference order: value saved by an admin in the settings panel, then the
// STORAGE_EMERGENCY_THRESHOLD_PERCENT env var, then a default of 80%.
func getEmergencyThresholdPercent() float64 {
	if settingConfig != nil && settingConfig.StorageEmergencyThresholdPercent > 0 && settingConfig.StorageEmergencyThresholdPercent <= 100 {
		return settingConfig.StorageEmergencyThresholdPercent
	}
	if v, err := strconv.ParseFloat(os.Getenv("STORAGE_EMERGENCY_THRESHOLD_PERCENT"), 64); err == nil && v > 0 && v <= 100 {
		return v
	}
	return 80
}

type CleanupResult struct {
	Reason         string `json:"reason"`
	RetentionDays  int    `json:"retentionDays,omitempty"`
	MessagesFound  int    `json:"messagesFound"`
	BackedUp       bool   `json:"backedUp"`
	BackupSkipped  string `json:"backupSkipped,omitempty"`
	MessagesPurged int    `json:"messagesPurged"`
}

type StorageUsageResult struct {
	UsedBytes          int64   `json:"usedBytes"`
	MaxBytes           int64   `json:"maxBytes"`
	PercentUsed        float64 `json:"percentUsed"`
	MessageCount       int64   `json:"messageCount"`
	RetentionDays      int     `json:"retentionDays"`
	EmergencyThreshold float64 `json:"emergencyThreshold"`
	BackupEnabled      bool    `json:"backupEnabled"`
}

func fetchStorageUsage(ctx context.Context) (*StorageUsageResult, error) {
	info, err := rdb.Info(ctx, "memory").Result()
	if err != nil {
		return nil, err
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

	result := &StorageUsageResult{
		UsedBytes:          usedBytes,
		MaxBytes:           maxBytes,
		MessageCount:       messageCount,
		RetentionDays:      getRetentionDays(),
		EmergencyThreshold: getEmergencyThresholdPercent(),
		BackupEnabled:      githubBackupToken != "" && githubBackupRepo != "",
	}
	if maxBytes > 0 {
		result.PercentUsed = float64(usedBytes) / float64(maxBytes) * 100
	}
	return result, nil
}

// runCleanup finds messages older than the retention period, backs them up to
// GitHub, and only then permanently deletes them from Redis. If backup is not
// configured or fails, no deletion happens.
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

	result := &CleanupResult{Reason: "age", RetentionDays: retentionDays, MessagesFound: len(oldKeys)}
	if len(oldKeys) == 0 {
		return result, nil
	}

	return backupAndPurgeKeys(ctx, oldKeys, result, fmt.Sprintf("messages older than %d days", retentionDays))
}

// runEmergencyCleanup purges the oldest messages (regardless of age) whenever
// storage usage is above the configured threshold, freeing roughly enough
// space to drop back under it. It repeats in batches until usage is back
// under the threshold, there are no more messages, or a backup fails.
func runEmergencyCleanup(ctx context.Context) (*CleanupResult, error) {
	usage, err := fetchStorageUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage usage: %v", err)
	}

	result := &CleanupResult{Reason: "emergency-storage-threshold"}

	if usage.MaxBytes == 0 || usage.PercentUsed < usage.EmergencyThreshold {
		return result, nil
	}

	const batchSize = 100
	const maxBatches = 20 // safety cap so we never loop forever in one run

	for i := 0; i < maxBatches; i++ {
		oldestKeys, err := rdb.ZRange(ctx, "m_times:1", 0, batchSize-1).Result()
		if err != nil {
			return result, fmt.Errorf("failed to list oldest messages: %v", err)
		}
		if len(oldestKeys) == 0 {
			break
		}

		result.MessagesFound += len(oldestKeys)
		batchResult := &CleanupResult{Reason: result.Reason}
		if _, err := backupAndPurgeKeys(ctx, oldestKeys, batchResult, "oldest messages (storage near capacity)"); err != nil {
			result.BackupSkipped = batchResult.BackupSkipped
			return result, err
		}
		result.BackedUp = result.BackedUp || batchResult.BackedUp
		result.MessagesPurged += batchResult.MessagesPurged
		result.BackupSkipped = batchResult.BackupSkipped

		if batchResult.MessagesPurged == 0 {
			// Backup not configured, nothing more we can safely do.
			break
		}

		usage, err = fetchStorageUsage(ctx)
		if err != nil || usage.PercentUsed < usage.EmergencyThreshold {
			break
		}
	}

	return result, nil
}

// backupAndPurgeKeys backs up the messages for the given Redis keys to GitHub
// (if configured) and, only on backup success, permanently deletes them.
func backupAndPurgeKeys(ctx context.Context, keys []string, result *CleanupResult, reasonLabel string) (*CleanupResult, error) {
	messages := make([]Message, 0, len(keys))
	for _, key := range keys {
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

	if len(messages) == 0 {
		return result, nil
	}

	if err := backupToGithub(ctx, messages, reasonLabel); err != nil {
		return result, fmt.Errorf("backup failed, deletion aborted for safety: %v", err)
	}
	result.BackedUp = true

	pipe := rdb.Pipeline()
	for _, key := range keys {
		idStr := strings.TrimPrefix(key, "messages:")
		pipe.Del(ctx, key)
		pipe.Del(ctx, fmt.Sprintf("message:%s:reactions", idStr))
		pipe.ZRem(ctx, "m_times:1", key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return result, fmt.Errorf("backup succeeded but deletion failed: %v", err)
	}
	result.MessagesPurged += len(keys)

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
func backupToGithub(ctx context.Context, messages []Message, reasonLabel string) error {
	payload, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}

	path := fmt.Sprintf("backups/messages_%s.json", time.Now().Format("2006-01-02_150405"))
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", githubBackupRepo, path)

	body := map[string]string{
		"message": fmt.Sprintf("Backup %d %s", len(messages), reasonLabel),
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

func setEmergencyThreshold(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var body struct {
		Value float64 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Value <= 0 || body.Value > 100 {
		http.Error(w, "invalid value, expected a number between 1 and 100", http.StatusBadRequest)
		return
	}

	existing, err := dbGetSettings(ctx)
	if err != nil {
		http.Error(w, "error loading settings", http.StatusInternalServerError)
		return
	}

	found := false
	for i := range existing {
		if existing[i].Key == "storage_emergency_threshold" {
			existing[i].Value = body.Value
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, Setting{Key: "storage_emergency_threshold", Value: body.Value})
	}

	if err := dbSetSettings(ctx, &existing); err != nil {
		http.Error(w, "error saving settings", http.StatusInternalServerError)
		return
	}

	settingConfig = existing.ToConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]float64{"value": body.Value})
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

func triggerEmergencyCleanup(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := runEmergencyCleanup(ctx)
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		log.Printf("Manual emergency cleanup failed: %v\n", err)
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

	usage, err := fetchStorageUsage(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(usage)
}

// startCleanupScheduler runs the age-based cleanup once per day, and checks
// storage usage every hour to trigger an emergency cleanup if needed.
func startCleanupScheduler() {
	dailyTicker := time.NewTicker(24 * time.Hour)
	go func() {
		for range dailyTicker.C {
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

	hourlyTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for range hourlyTicker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			result, err := runEmergencyCleanup(ctx)
			cancel()
			if err != nil {
				log.Printf("Emergency cleanup failed: %v\n", err)
			} else if result.MessagesPurged > 0 {
				log.Printf("Emergency cleanup: storage was near capacity, backed up and purged %d oldest messages\n", result.MessagesPurged)
			}
		}
	}()
}
