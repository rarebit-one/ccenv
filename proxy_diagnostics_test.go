package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCLIProxyDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, catalog, provider string
		status                  int
		body                    string
		limited, probe          bool
	}{
		{"cooldown", `{"data":[]}`, "cliproxyapi", 429, `{"error":{"type":"rate_limit_error","message":"All credentials for model work/opus are cooling down"}}`, true, true},
		{"generic", `{"data":[]}`, "", 429, `{}`, false, false},
		{"healthy", `{"data":[{"id":"work/opus"}]}`, "cliproxyapi", 429, `{}`, false, false},
		{"missing route", `{"data":[]}`, "cliproxyapi", 404, `{}`, false, true},
		{"invalid catalog", `{}`, "cliproxyapi", 429, `{}`, false, false},
		{"unstructured limit", `{"data":[]}`, "cliproxyapi", 429, `{"error":"secret-client-key"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					fmt.Fprint(w, tc.catalog)
					return
				}
				probed = true
				var body struct {
					Model string `json:"model"`
				}
				if r.Method != "POST" || r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("X-Api-Key") != "secret-client-key" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "work/opus" {
					t.Error("unexpected diagnostic request")
				}
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			err := checkProxy(proxyConfig{URL: server.URL, Provider: tc.provider, Model: "work/opus", TimeoutSeconds: 1}, "secret-client-key")
			var limit *proxyAccountLimit
			if errors.As(err, &limit) != tc.limited || probed != tc.probe {
				t.Fatalf("err=%v probed=%v", err, probed)
			}
			if tc.name == "healthy" && err != nil {
				t.Fatal(err)
			}
			if tc.limited && (!strings.Contains(err.Error(), "quota cooldown") || !strings.Contains(err.Error(), "gateway retry time")) {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-client-key") {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestProxyRetryAt(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, value := range []string{"-1", "9223372036854775807", "garbage", now.Add(-time.Hour).Format(http.TimeFormat)} {
		if !proxyRetryAt(value, now).IsZero() {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, value := range []string{"3600", now.Add(time.Hour).Format(http.TimeFormat)} {
		if !proxyRetryAt(value, now).Equal(now.Add(time.Hour)) {
			t.Fatalf("bad retry time for %q", value)
		}
	}
}

func TestCLIProxyDiagnosticDoesNotFollowRedirects(t *testing.T) {
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHit = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	err := checkProxy(proxyConfig{URL: server.URL, Provider: "cliproxyapi", DefaultModels: map[string]string{"opus": "work/opus"}, TimeoutSeconds: 1}, "secret-client-key")
	if err == nil || targetHit {
		t.Fatalf("err=%v targetHit=%v", err, targetHit)
	}
}
