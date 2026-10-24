package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// sendNotify виконує GET-запит на URL (апі ключ вже зашитий в URL конфігом).
// Успіхом вважається будь-яка відповідь 2xx/3xx.
func sendNotify(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "svitlobot-client/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// читаємо і викидаємо тіло, щоб keep-alive з'єднання можна було пере-використати
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}
