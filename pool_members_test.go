package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPoolMembersUsesLiveLabelsAndNeverEmails(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "key")
	if err := os.WriteFile(key, []byte("client-token"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pool-status" || r.Header.Get("Authorization") != "Bearer client-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"pools":{"family":{"pool":{"members":["family-kate","rarebit"]},"fallback":{"members":["luminality"]}}},"accounts":{"family-kate":{"label":"family-kate","email":"secret@example.test"},"rarebit":{"label":"other.water"},"luminality":{"label":"tiny.bulb"}}}`)
	}))
	defer server.Close()
	got := poolMembers(proxyConfig{URL: server.URL, APIKeyFile: key})
	if !reflect.DeepEqual(got["family-pool"], []string{"family-kate", "other.water"}) || !reflect.DeepEqual(got["family-fallback"], []string{"tiny.bulb"}) {
		t.Fatalf("wrong route membership: %#v", got)
	}
}
