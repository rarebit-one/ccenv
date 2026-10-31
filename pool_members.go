package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// poolMembers reads the gateway's live route membership for menu previews.
// A failed status lookup only removes the annotations; routing remains explicit.
func poolMembers(p proxyConfig) map[string][]string {
	key, err := readProxyKey(p.APIKeyFile)
	if err != nil {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(p.URL, "/")+"/v1/pool-status", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	var status struct {
		Pools map[string]map[string]struct {
			Members []string `json:"members"`
		} `json:"pools"`
		Accounts map[string]struct {
			Label string `json:"label"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return nil
	}
	result := make(map[string][]string)
	for workspace, tiers := range status.Pools {
		for _, tier := range []string{"pool", "fallback"} {
			entry, exists := tiers[tier]
			if !exists {
				continue
			}
			members := make([]string, 0, len(entry.Members))
			for _, id := range entry.Members {
				if !validName.MatchString(id) {
					continue
				}
				label := status.Accounts[id].Label
				if !validName.MatchString(label) {
					label = id
				}
				members = append(members, label)
			}
			result[workspace+"-"+tier] = members
		}
	}
	return result
}
