package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// exportMessagesCSV streams every message in the channel as a CSV file for
// the admin to download. Reads directly from Redis, independent of pagination.
func exportMessagesCSV(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	keys, err := rdb.ZRevRange(ctx, "m_times:1", 0, -1).Result()
	if err != nil {
		http.Error(w, "error listing messages", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("channel-export-%s.csv", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	// UTF-8 BOM so Excel opens Hebrew text correctly instead of garbling it.
	w.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(w)
	defer writer.Flush()

	writer.Write([]string{"id", "timestamp", "author", "text", "views", "deleted", "is_ads"})

	for _, key := range keys {
		data, err := rdb.HGetAll(ctx, key).Result()
		if err != nil || len(data) == 0 {
			continue
		}
		m := messageFromHash(data)
		writer.Write([]string{
			strconv.Itoa(m.ID),
			m.Timestamp.Format(time.RFC3339),
			m.Author,
			m.Text,
			strconv.Itoa(m.Views),
			strconv.FormatBool(m.Deleted),
			strconv.FormatBool(m.IsAds),
		})
	}
}
