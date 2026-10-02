package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relayscope/internal/linuxdo"
	"relayscope/internal/store"
)

// 免费额度服务端强制：非会员最多 3 个站点，前端限制可被直接调 API 绕过。
func TestSubscriptionCreateEnforcesFreeSiteLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := linuxdo.New(linuxdo.Config{}, db)
	handler, err := NewHandler(Options{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:   db,
		LinuxDO: service,
	})
	if err != nil {
		t.Fatal(err)
	}

	freeUser, err := db.UpsertUser(ctx, "linuxdo", "free-user", "freebie", "Freebie", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	memberUser, err := db.UpsertUser(ctx, "linuxdo", "member-user", "member", "Member", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExtendMembership(ctx, memberUser.ID, 30); err != nil {
		t.Fatal(err)
	}

	sites := make([]store.Site, 0, 6)
	for i := range 6 {
		site, err := db.CreateSite(ctx, store.Site{
			Name:       "站点" + string(rune('A'+i)),
			BaseURL:    "https://limit-test-" + strings.ToLower(string(rune('a'+i))) + ".example.invalid",
			SourceURL:  "https://limit-test-" + strings.ToLower(string(rune('a'+i))) + ".example.invalid/s",
			AdapterKey: "probe",
			Interval:   5 * time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		sites = append(sites, site)
	}

	freeToken, _, err := service.StartSession(freeUser)
	if err != nil {
		t.Fatal(err)
	}
	memberToken, _, err := service.StartSession(memberUser)
	if err != nil {
		t.Fatal(err)
	}
	post := func(token string, siteID int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/me/notification-subscriptions",
			strings.NewReader(`{"siteId":`+jsonInt(siteID)+`,"platform":"telegram","target":"1212406777"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "relayscope_user", Value: token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 非会员：前 3 个站点成功，第 4 个被 403 拦下
	for i := 0; i < 3; i++ {
		if rec := post(freeToken, sites[i].ID); rec.Code != http.StatusOK {
			t.Fatalf("free subscription #%d status = %d, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	blocked := post(freeToken, sites[3].ID)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("4th free subscription status = %d, want 403, body = %s", blocked.Code, blocked.Body.String())
	}

	// 已订阅站点换渠道重订（不同 target）不算新增站点，放行
	reSub := httptest.NewRequest(http.MethodPost, "/api/v1/me/notification-subscriptions",
		strings.NewReader(`{"siteId":`+jsonInt(sites[0].ID)+`,"platform":"bark","target":"anotherDeviceKey"}`))
	reSub.Header.Set("Content-Type", "application/json")
	reSub.AddCookie(&http.Cookie{Name: "relayscope_user", Value: freeToken})
	reRec := httptest.NewRecorder()
	handler.ServeHTTP(reRec, reSub)
	if reRec.Code != http.StatusOK {
		t.Fatalf("re-subscribing same site with new channel status = %d, body = %s", reRec.Code, reRec.Body.String())
	}

	// 会员不受限
	for i := 3; i < 6; i++ {
		if rec := post(memberToken, sites[i].ID); rec.Code != http.StatusOK {
			t.Fatalf("member subscription #%d status = %d, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
}

func jsonInt(v int64) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
