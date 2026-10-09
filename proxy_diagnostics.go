package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type proxyAccountLimit struct {
	retryAt  time.Time
	cooldown bool
}

func (e *proxyAccountLimit) Error() string {
	message := "CLIProxyAPI account is rate limited"
	if e.cooldown {
		message = "CLIProxyAPI account is in quota cooldown"
	}
	if !e.retryAt.IsZero() {
		message += "; gateway retry time " + e.retryAt.Local().Format("Mon 02 Jan 2006 15:04:05 MST")
	}
	return message
}

func missingProxyModel(client *http.Client, p proxyConfig, key, model string) error {
	missing := fmt.Errorf("configured proxy model %q is not advertised; check the gateway account route and model settings", model)
	if p.Provider != "cliproxyapi" {
		return missing
	}
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "ccenv preflight"}}})
	req, err := http.NewRequest(http.MethodPost, p.URL+"/v1/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return missing
	}
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w; CLIProxyAPI cooldown diagnostic unavailable", missing)
	}
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if response.StatusCode != http.StatusTooManyRequests || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope) != nil || envelope.Error.Type != "rate_limit_error" {
		return fmt.Errorf("%w; CLIProxyAPI can also hide models during account cooldown", missing)
	}
	return &proxyAccountLimit{retryAt: proxyRetryAt(response.Header.Get("Retry-After"), time.Now()), cooldown: strings.HasPrefix(envelope.Error.Message, "All credentials for model ") && strings.HasSuffix(envelope.Error.Message, " are cooling down")}
}

func proxyRetryAt(value string, now time.Time) time.Time {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds >= 0 && seconds <= 365*24*60*60 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
		return time.Time{}
	}
	if date, err := http.ParseTime(value); err == nil && !date.Before(now) {
		return date
	}
	return time.Time{}
}
