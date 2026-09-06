package collector

import (
	"encoding/json"
	"strings"
)

// parseBlockedKeywords reads the optional "blockedKeywords" string array from
// a site's adapter config. Model names containing any keyword are dropped
// from the collection and removed from the dashboard immediately, regardless
// of their observed availability.
func parseBlockedKeywords(configJSON string) []string {
	var config struct {
		BlockedKeywords []string `json:"blockedKeywords"`
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil
	}
	keywords := make([]string, 0, len(config.BlockedKeywords))
	for _, keyword := range config.BlockedKeywords {
		if keyword = strings.TrimSpace(keyword); keyword != "" {
			keywords = append(keywords, strings.ToLower(keyword))
		}
	}
	if len(keywords) == 0 {
		return nil
	}
	return keywords
}

func containsAnyKeyword(name string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	name = strings.ToLower(name)
	for _, keyword := range keywords {
		if strings.Contains(name, keyword) {
			return true
		}
	}
	return false
}
