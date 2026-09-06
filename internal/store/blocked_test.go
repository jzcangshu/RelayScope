package store

import (
	"context"
	"testing"
	"time"
)

func TestSiteWritePathsRejectInvalidAdapterConfig(t *testing.T) {
	dbStore := openTestStore(t)
	if _, err := dbStore.CreateSite(context.Background(), Site{Name: "bad-config", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", AdapterConfig: `{"broken":`, Enabled: true, Interval: 15 * time.Minute}); err == nil {
		t.Fatal("CreateSite accepted invalid adapter config JSON")
	}
	site, err := dbStore.CreateSite(context.Background(), Site{Name: "valid-site", BaseURL: "https://example.test", SourceURL: "https://example.test/pricing", AdapterKey: "newapi-pricing", AdapterConfig: `{"blockedKeywords":["只会喵喵叫"]}`, Enabled: true, Interval: 15 * time.Minute})
	if err != nil {
		t.Fatalf("CreateSite rejected valid config: %v", err)
	}
	if err := dbStore.UpdateSite(context.Background(), site.ID, site.Name, site.AdapterKey, `{"broken":`, true, nil, site.Interval, site.Jitter); err == nil {
		t.Fatal("UpdateSite accepted invalid adapter config JSON")
	}
	if err := dbStore.UpdateSiteDetails(context.Background(), site.ID, site.Name, site.BaseURL, site.SourceURL, site.AdapterKey, "not-json", true, false, site.Interval, site.Jitter, ""); err == nil {
		t.Fatal("UpdateSiteDetails accepted invalid adapter config JSON")
	}
	updated, err := dbStore.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatalf("get site: %v", err)
	}
	if updated.AdapterConfig != `{"blockedKeywords":["只会喵喵叫"]}` {
		t.Fatalf("failed updates mutated adapter config: %q", updated.AdapterConfig)
	}
}
