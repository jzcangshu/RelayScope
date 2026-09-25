package store

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// 临时文件库而非 :memory:：modernc/sqlite 的内存库是每连接独立的，
// 池化多连接（并发用例、多语句事务）会各自看到空库。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigration004CreatesNewTables(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	for _, table := range []string{"user_sessions", "user_preferences", "redeem_codes", "wish_sites", "ldc_orders"} {
		if _, err := db.db.ExecContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1"); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	// 重开库（同 schema 重放迁移路径）不应报错
	if _, err := db.db.ExecContext(ctx, `SELECT trust_level, membership_expires_at FROM users LIMIT 1`); err != nil {
		t.Fatalf("users columns missing: %v", err)
	}
}

func TestExtendMembershipSemantics(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if membership, err := db.GetMembership(ctx, user.ID); err != nil || membership.Active {
		t.Fatalf("new user should have no membership: %+v err=%v", membership, err)
	}
	first, err := db.ExtendMembership(ctx, user.ID, 30)
	if err != nil || !first.Active {
		t.Fatalf("extend failed: %+v err=%v", first, err)
	}
	if got := time.Until(*first.ExpiresAt); got < 29*24*time.Hour || got > 31*24*time.Hour {
		t.Fatalf("first extension should be ~30 days, got %v", got)
	}
	// 未过期时再次延长：从原有效期起算
	second, err := db.ExtendMembership(ctx, user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if delta := second.ExpiresAt.Sub(*first.ExpiresAt); delta < 9*24*time.Hour || delta > 11*24*time.Hour {
		t.Fatalf("extension should stack from expiry, delta = %v", delta)
	}
	// 过期后续费：从现在起算
	past := now.Add(-48 * time.Hour)
	if _, err := db.db.ExecContext(ctx, `UPDATE users SET membership_expires_at = ? WHERE id = ?`, unixMilli(past), user.ID); err != nil {
		t.Fatal(err)
	}
	third, err := db.ExtendMembership(ctx, user.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Until(*third.ExpiresAt); got < 6*24*time.Hour || got > 8*24*time.Hour {
		t.Fatalf("extension after expiry should start from now, got %v", got)
	}
}

func TestUserSessionPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/sessions.db"
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserSession(ctx, "token-abc", user.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, ok, _ := reopened.UserSessionUser(ctx, "token-abc", time.Now().UTC()); !ok || got.Username != "tester" {
		t.Fatal("session must survive process restart")
	}
}

func TestSetMembershipByUsernamePreRegistersUser(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Millisecond)

	member, err := db.SetMembershipByUsername(ctx, ProviderLinuxDO, "@Tester", &expiry)
	if err != nil {
		t.Fatal(err)
	}
	if member.Username != "Tester" || member.Registered || !member.PreRegistered {
		t.Fatalf("member should be pre-registered by username: %+v", member)
	}
	if member.MembershipExpiresAt == nil || !member.MembershipExpiresAt.Equal(expiry) {
		t.Fatalf("pre-registered membership should keep exact expiry, got %+v", member)
	}

	members, err := db.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Username != "Tester" || members[0].Registered || !members[0].PreRegistered {
		t.Fatalf("members list should show pre-registration: %+v", members)
	}

	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "tester" || user.RegisteredAt == nil {
		t.Fatalf("LD login should complete pre-registration: %+v", user)
	}
	membership, err := db.GetMembership(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !membership.Active || membership.ExpiresAt == nil || !membership.ExpiresAt.Equal(expiry) {
		t.Fatalf("LD login should preserve membership expiry: %+v", membership)
	}

	members, err = db.ListMembers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Username != "tester" || !members[0].Registered || members[0].PreRegistered {
		t.Fatalf("members list should describe matched user: %+v", members)
	}

	member, err = db.SetMembershipByUsername(ctx, ProviderLinuxDO, "@tester", nil)
	if err != nil {
		t.Fatal(err)
	}
	if member.MembershipExpiresAt != nil || member.PreRegistered {
		t.Fatalf("membership should be removable, got %+v", member)
	}
}

func TestUserSchemaReconciliationWhenSchemaVersionIsHigh(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, ProviderLinuxDO, "42", "tester", "Tester", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `ALTER TABLE users DROP COLUMN registered_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE app_meta SET value = '999' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	reconciled, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RegisteredAt == nil {
		t.Fatalf("existing users should be marked registered, got %+v", reconciled)
	}
}

func TestUserSessionLifecycle(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUserSession(ctx, "token-abc", user.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.UserSessionUser(ctx, "token-abc", time.Now().UTC())
	if err != nil || !ok || got.Username != "tester" {
		t.Fatalf("session lookup failed: ok=%v user=%+v err=%v", ok, got, err)
	}
	if _, ok, _ := db.UserSessionUser(ctx, "token-other", time.Now().UTC()); ok {
		t.Fatal("unknown token must not resolve")
	}
	if err := db.DeleteUserSession(ctx, "token-abc"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.UserSessionUser(ctx, "token-abc", time.Now().UTC()); ok {
		t.Fatal("deleted session must not resolve")
	}
}

func TestRedeemCodeFormat(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	codes, err := db.GenerateRedeemCodes(ctx, 3, 30, "首批内测")
	if err != nil || len(codes) != 3 {
		t.Fatalf("generate: %v %v", codes, err)
	}
	seen := map[string]bool{}
	for _, code := range codes {
		if !strings.HasPrefix(code, "RS-") || len(code) != len("RS-XXXX-XXXX-XXXX") {
			t.Fatalf("unexpected code format %q", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code generated %q", code)
		}
		seen[code] = true
	}
}

func TestRedeemCodeRedemption(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := db.GenerateRedeemCodes(ctx, 1, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	// 大小写、分隔符、混淆字符都能归一
	messy := "rs-" + strings.ReplaceAll(codes[0][3:], "0", "O")
	membership, err := db.RedeemCode(ctx, user.ID, messy)
	if err != nil || !membership.Active {
		t.Fatalf("redeem normalized code: %+v err=%v", membership, err)
	}
	// 同码二次兑换必须失败
	if _, err := db.RedeemCode(ctx, user.ID, codes[0]); err == nil {
		t.Fatal("second redemption must fail")
	}
	// 未知码失败
	if _, err := db.RedeemCode(ctx, user.ID, "RS-ZZZZ-ZZZZ-ZZZZ"); err == nil {
		t.Fatal("unknown code must fail")
	}
	list, err := db.ListRedeemCodes(ctx, RedeemStatusUsed, 10, 0)
	if err != nil || len(list) != 1 || list[0].RedeemedByName != "tester" {
		t.Fatalf("list used codes: %+v err=%v", list, err)
	}
}

func TestRedeemCodeConcurrentRedemption(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := db.GenerateRedeemCodes(ctx, 1, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	successes := make(chan int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.RedeemCode(ctx, user.ID, codes[0]); err == nil {
				successes <- 1
			}
		}()
	}
	wg.Wait()
	close(successes)
	if got := len(successes); got != 1 {
		t.Fatalf("exactly one concurrent redemption should win, got %d", got)
	}
}

func TestRevokeRedeemCodes(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	codes, err := db.GenerateRedeemCodes(ctx, 2, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListRedeemCodes(ctx, RedeemStatusUnused, 10, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	ids := []int64{list[0].ID, list[1].ID}
	revoked, err := db.RevokeRedeemCodes(ctx, ids)
	if err != nil || revoked != 2 {
		t.Fatalf("revoke: %v %v", revoked, err)
	}
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RedeemCode(ctx, user.ID, codes[0]); err == nil {
		t.Fatal("revoked code must not redeem")
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil || empty.DefaultHealthy || len(empty.Hidden.Sites) != 0 {
		t.Fatalf("default prefs: %+v err=%v", empty, err)
	}
	prefs := Preferences{
		Hidden:         PreferencesHidden{Sites: []string{"站点A"}, Providers: []string{"OpenAI"}, Models: []string{"gpt-4o"}},
		DefaultHealthy: true,
		Tags:           map[string]PreferencesTag{"主力": {Color: "mint", Sites: []string{"站点A"}}},
	}
	saved, err := db.PutUserPreferences(ctx, user.ID, prefs)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Hidden.Models) != 1 || loaded.Hidden.Models[0] != "gpt-4o" || !loaded.DefaultHealthy {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.UpdatedAt.Before(saved.UpdatedAt.Add(-time.Second)) {
		t.Fatal("updated_at should be set")
	}
	tooMany := Preferences{Hidden: PreferencesHidden{Sites: make([]string, 501)}}
	if _, err := db.PutUserPreferences(ctx, user.ID, tooMany); err != ErrPreferencesTooLarge {
		t.Fatalf("oversized prefs must be rejected, got %v", err)
	}
}

func TestSortingPreferencesRoundTrip(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Put with sorting
	prefs := Preferences{
		Hidden:         PreferencesHidden{Sites: []string{}, Providers: []string{}, Models: []string{}},
		DefaultHealthy: false,
		Tags:           map[string]PreferencesTag{},
		Sorting:        &SortingPrefs{Model: "smart", Site: "latency"},
	}
	if _, err := db.PutUserPreferences(ctx, user.ID, prefs); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sorting == nil || loaded.Sorting.Model != "smart" || loaded.Sorting.Site != "latency" {
		t.Fatalf("sorting round trip mismatch: %+v", loaded.Sorting)
	}
}

func TestSortingNilPreservesExisting(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	// First put with sorting set
	_, err = db.PutUserPreferences(ctx, user.ID, Preferences{
		Hidden:  PreferencesHidden{Sites: []string{}, Providers: []string{}, Models: []string{}},
		Tags:    map[string]PreferencesTag{},
		Sorting: &SortingPrefs{Model: "price", Site: "healthy-count"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Second put with nil Sorting → should preserve existing
	_, err = db.PutUserPreferences(ctx, user.ID, Preferences{
		Hidden: PreferencesHidden{Sites: []string{"a"}, Providers: []string{}, Models: []string{}},
		Tags:   map[string]PreferencesTag{},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sorting == nil || loaded.Sorting.Model != "price" || loaded.Sorting.Site != "healthy-count" {
		t.Fatalf("nil sorting should preserve existing: %+v", loaded.Sorting)
	}
	if len(loaded.Hidden.Sites) != 1 {
		t.Fatalf("other fields should update: %+v", loaded.Hidden)
	}
}

func TestSortingInvalidNormalizesToDefault(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.PutUserPreferences(ctx, user.ID, Preferences{
		Hidden:  PreferencesHidden{Sites: []string{}, Providers: []string{}, Models: []string{}},
		Tags:    map[string]PreferencesTag{},
		Sorting: &SortingPrefs{Model: "bogus", Site: "invalid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sorting == nil || loaded.Sorting.Model != "default" || loaded.Sorting.Site != "default" {
		t.Fatalf("invalid sorting should normalize to default: %+v", loaded.Sorting)
	}
}

func TestSortingDefaultValuesForOldRows(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Put without Sorting (simulates old row)
	_, err = db.PutUserPreferences(ctx, user.ID, Preferences{
		Hidden:         PreferencesHidden{Sites: []string{}, Providers: []string{}, Models: []string{}},
		DefaultHealthy: true,
		Tags:           map[string]PreferencesTag{},
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := db.GetUserPreferences(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should get default sorting, not nil
	if loaded.Sorting == nil || loaded.Sorting.Model != "default" || loaded.Sorting.Site != "default" {
		t.Fatalf("old row should return default sorting: %+v", loaded.Sorting)
	}
}

func TestNormalizeWishDomain(t *testing.T) {
	cases := []struct {
		raw    string
		domain string
		url    string
		fails  bool
	}{
		{raw: "https://www.Example.com/pricing", domain: "example.com", url: "https://example.com"},
		{raw: "example.com", domain: "example.com", url: "https://example.com"},
		{raw: "http://api.foo.cn:8443/x", domain: "api.foo.cn", url: "https://api.foo.cn"},
		{raw: "", fails: true},
		{raw: "ftp://x.com", fails: true},
		{raw: "https://", fails: true},
	}
	for _, c := range cases {
		domain, normalized, err := NormalizeWishDomain(c.raw)
		if c.fails {
			if err == nil {
				t.Fatalf("%q should fail", c.raw)
			}
			continue
		}
		if err != nil || domain != c.domain || normalized != c.url {
			t.Fatalf("%q => (%q, %q) err=%v, want (%q, %q)", c.raw, domain, normalized, err, c.domain, c.url)
		}
	}
}

func TestWishSiteLifecycle(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	noInvite, err := db.CreateWishSite(ctx, user.ID, "云雾API", "https://www.example.com/pricing", false, 30)
	if err != nil {
		t.Fatal(err)
	}
	if noInvite.Domain != "example.com" || noInvite.TargetLDC == nil || *noInvite.TargetLDC != 30 {
		t.Fatalf("non-invite wish should default to 30 LDC: %+v", noInvite)
	}
	if _, err := db.CreateWishSite(ctx, user.ID, "重复站", "https://example.com/other", false, 30); err != ErrWishDuplicate {
		t.Fatalf("duplicate domain must fail, got %v", err)
	}
	inviteWish, err := db.CreateWishSite(ctx, user.ID, "神秘站", "https://secret.example.org", true, 30)
	if err != nil {
		t.Fatal(err)
	}
	if inviteWish.TargetLDC != nil {
		t.Fatal("invite-required wish must start with undecided target")
	}
	// 管理端定价
	target := int64(100)
	if _, err := db.UpdateWishSite(ctx, inviteWish.ID, &target, ""); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetWishSite(ctx, inviteWish.ID)
	if err != nil || updated.TargetLDC == nil || *updated.TargetLDC != 100 {
		t.Fatalf("target update failed: %+v err=%v", updated, err)
	}
	list, err := db.ListWishSites(ctx, user.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list wishes: %+v err=%v", list, err)
	}
}

func TestOrderPaymentFlowMembership(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	order, err := db.CreateOrder(ctx, LDCOrder{OrderNo: "LD-1", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30})
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != OrderStatusPending {
		t.Fatalf("new order should be pending: %+v", order)
	}
	changed := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ { // 回调重放幂等
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := db.MarkOrderPaid(ctx, "LD-1", "T-1"); err == nil && ok {
				changed <- 1
			}
		}()
	}
	wg.Wait()
	if got := len(changed); got > 1 {
		t.Fatalf("idempotent payment triggered membership extension %d times", got)
	}
	membership, err := db.GetMembership(ctx, user.ID)
	if err != nil || !membership.Active {
		t.Fatalf("membership should be active after payment: %+v err=%v", membership, err)
	}
}

func TestOrderPaymentFlowWishReachesTarget(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	wish, err := db.CreateWishSite(ctx, user.ID, "目标站", "https://target.example.com", false, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateOrder(ctx, LDCOrder{OrderNo: "LD-W1", UserID: user.ID, Kind: OrderKindWish, WishSiteID: &wish.ID, AmountLDC: 12}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateOrder(ctx, LDCOrder{OrderNo: "LD-W2", UserID: user.ID, Kind: OrderKindWish, WishSiteID: &wish.ID, AmountLDC: 20}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.MarkOrderPaid(ctx, "LD-W1", "T-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetWishSite(ctx, wish.ID); err != nil {
		t.Fatal(err)
	}
	if site, _ := db.GetWishSite(ctx, wish.ID); site.Status != WishStatusOpen {
		t.Fatalf("12/30 should stay open, got %s", site.Status)
	}
	if _, _, err := db.MarkOrderPaid(ctx, "LD-W2", "T-2"); err != nil {
		t.Fatal(err)
	}
	if site, _ := db.GetWishSite(ctx, wish.ID); site.Status != WishStatusReached {
		t.Fatalf("32/30 should auto-reach, got %s", site.Status)
	}
	summary, err := db.ListWishSites(ctx, user.ID)
	if err != nil || len(summary) != 1 || summary[0].PledgedLDC != 32 || summary[0].MyPledgedLDC != 32 || summary[0].Pledgers != 1 {
		t.Fatalf("progress summary: %+v err=%v", summary, err)
	}
	// 退款回落到 open
	orders, err := db.ListOrders(ctx, 0, OrderKindWish, OrderStatusPaid, 10)
	if err != nil || len(orders) != 2 {
		t.Fatalf("list orders: %+v err=%v", orders, err)
	}
	if _, err := db.MarkOrderRefunded(ctx, orders[0].ID); err != nil {
		t.Fatal(err)
	}
	if site, _ := db.GetWishSite(ctx, wish.ID); site.Status != WishStatusOpen {
		t.Fatalf("after refund below target should reopen, got %s", site.Status)
	}
}

func TestCancelExpiredOrders(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateOrder(ctx, LDCOrder{OrderNo: "LD-X", UserID: user.ID, Kind: OrderKindMembership, Days: int64Ptr(30), AmountLDC: 30}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := db.CancelExpiredOrders(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || cancelled != 1 {
		t.Fatalf("cancel expired: %v %v", cancelled, err)
	}
	if order, _ := db.GetOrderByNo(ctx, "LD-X"); order.Status != OrderStatusCancelled {
		t.Fatalf("order should be cancelled: %+v", order)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	if value, err := db.GetSetting(ctx, "membership_ldc_per_day", "1"); err != nil || value != "1" {
		t.Fatalf("fallback setting: %q err=%v", value, err)
	}
	if err := db.SetSetting(ctx, "membership_ldc_per_day", "2"); err != nil {
		t.Fatal(err)
	}
	if value, _ := db.GetSetting(ctx, "membership_ldc_per_day", "1"); value != "2" {
		t.Fatalf("setting should persist, got %q", value)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestMonthlyWishCredit(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "42", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// 首次发放 10，重复发放不叠加（每人每月一次）
	if available, err := db.EnsureMonthlyCredit(ctx, user.ID, 10, now); err != nil || available != 10 {
		t.Fatalf("first grant: available=%d err=%v", available, err)
	}
	if available, _ := db.EnsureMonthlyCredit(ctx, user.ID, 10, now); available != 10 {
		t.Fatalf("second grant must not stack, available=%d", available)
	}
	// 部分消耗
	consumed, err := db.ConsumeMonthlyCredit(ctx, user.ID, 4, now)
	if err != nil || consumed != 4 {
		t.Fatalf("consume 4: consumed=%d err=%v", consumed, err)
	}
	if available, _ := db.EnsureMonthlyCredit(ctx, user.ID, 10, now); available != 6 {
		t.Fatalf("available after consume = %d, want 6", available)
	}
	// 超额消耗自动截断
	consumed, err = db.ConsumeMonthlyCredit(ctx, user.ID, 100, now)
	if err != nil || consumed != 6 {
		t.Fatalf("over-consume clamps: consumed=%d err=%v", consumed, err)
	}
	if consumed, _ := db.ConsumeMonthlyCredit(ctx, user.ID, 1, now); consumed != 0 {
		t.Fatalf("exhausted credit must return 0, got %d", consumed)
	}
	// 并发消耗不超发：先重置到 10 可用，再 8 个并发各抢 2
	future := now.AddDate(0, 1, 0)
	if _, err := db.EnsureMonthlyCredit(ctx, user.ID, 10, future); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	granted := make(chan int, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if used, err := db.ConsumeMonthlyCredit(ctx, user.ID, 2, future); err == nil {
				granted <- int(used)
			}
		}()
	}
	wg.Wait()
	close(granted)
	total := 0
	for used := range granted {
		total += used
	}
	if total != 10 {
		t.Fatalf("concurrent consumption must clamp to granted 10, got %d", total)
	}
	// 只读查询不发放
	other, err := db.UpsertUser(ctx, "linuxdo", "43", "other", "Other", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := db.GetMonthlyCredit(ctx, other.ID, now); exists {
		t.Fatal("GetMonthlyCredit must not grant")
	}
}

// TestWishCreditExpiry 锁定"当月未用完的赠额自动过期"语义：
// 额度按 (user_id, period) 分月记账，消费只触碰当月行，跨月后上月余额既不结转也不可再消费。
func TestWishCreditExpiry(t *testing.T) {
	db := newTestStore(t)
	ctx := context.Background()
	user, err := db.UpsertUser(ctx, "linuxdo", "44", "tester", "Tester", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	next := now.AddDate(0, 1, 0)
	// 当月发放 10，只用 3，剩 7
	if available, err := db.EnsureMonthlyCredit(ctx, user.ID, 10, now); err != nil || available != 10 {
		t.Fatalf("grant: available=%d err=%v", available, err)
	}
	if consumed, err := db.ConsumeMonthlyCredit(ctx, user.ID, 3, now); err != nil || consumed != 3 {
		t.Fatalf("consume 3: consumed=%d err=%v", consumed, err)
	}
	// 次月：剩余 7 不结转，只发放新月度的 10
	if available, err := db.EnsureMonthlyCredit(ctx, user.ID, 10, next); err != nil || available != 10 {
		t.Fatalf("next month available = %d, want 10 (leftover 7 must expire)", available)
	}
	// 次月额度用尽后，上月剩余 7 不可再消费
	if consumed, err := db.ConsumeMonthlyCredit(ctx, user.ID, 10, next); err != nil || consumed != 10 {
		t.Fatalf("consume 10 next month: consumed=%d err=%v", consumed, err)
	}
	if consumed, _ := db.ConsumeMonthlyCredit(ctx, user.ID, 1, next); consumed != 0 {
		t.Fatalf("expired leftover must not be spendable, got %d", consumed)
	}
}
