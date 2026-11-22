package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func TextTranslator(key string) func(context.Context, string, string) (string, error) {
	if key == "" {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context, text, target string) (string, error) {
		body, err := json.Marshal(map[string]string{"q": text, "target": target, "format": "text"})
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://translation.googleapis.com/language/translate/v2", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Goog-Api-Key", key)
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return "", errors.New("translation provider rejected the request")
		}
		var result struct {
			Data struct {
				Translations []struct {
					Text string `json:"translatedText"`
				} `json:"translations"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result); err != nil {
			return "", err
		}
		if len(result.Data.Translations) != 1 || strings.TrimSpace(result.Data.Translations[0].Text) == "" {
			return "", errors.New("translation provider returned no text")
		}
		return result.Data.Translations[0].Text, nil
	}
}
