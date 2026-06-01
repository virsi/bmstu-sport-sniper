package oidc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WatchdogStatus — статус ответа /portal4/cookie/watchdog.
type WatchdogStatus struct {
	// Status — обычно "OK" или "FAIL".
	Status string `json:"status"`
	// Interval — рекомендуемый интервал следующего health-check, сек.
	Interval int `json:"interval"`
}

// IsAlive возвращает true, если portal4/cookie/watchdog ответил status=OK.
//
// Передавать *http.Client с настроенным cookiejar (тем самым, что после Login).
// Если httpClient == nil — используется внутренний клиент *Client (см. New).
func (c *Client) IsAlive(ctx context.Context, httpClient *http.Client) bool {
	st, err := c.Watchdog(ctx, httpClient)
	if err != nil {
		return false
	}
	return strings.EqualFold(st.Status, "OK")
}

// Watchdog возвращает декодированный ответ watchdog endpoint.
func (c *Client) Watchdog(ctx context.Context, httpClient *http.Client) (*WatchdogStatus, error) {
	if httpClient == nil {
		httpClient = c.http
	}
	wdURL := strings.TrimRight(c.baseURL, "/") + WatchdogPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wdURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("oidc: build watchdog request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: watchdog request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return nil, fmt.Errorf("oidc: read watchdog body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: watchdog status %d", ErrUnexpectedResponse, resp.StatusCode)
	}

	var st WatchdogStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("oidc: decode watchdog: %w", err)
	}
	return &st, nil
}
