package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"relayscope/internal/admin"
	"relayscope/internal/linuxdo"
	"relayscope/internal/payment"
	"relayscope/internal/store"
)

// fakeProvider 模拟一个已配置的支付通道：CreateCharge 返回假支付页，ParseCallback 信任请求参数。
type fakeProvider struct{ created []string }

func (f *fakeProvider) Configured() bool { return true }
func (f *fakeProvider) CreateCharge(_ context.Context, req payment.ChargeRequest) (payment.Charge, error) {
	f.created = append(f.created, req.Ref)
	return payment.Charge{Ref: req.Ref, PayURL: "https://pay.example/paying?no=" + req.Ref}, nil
}
func (f *fakeProvider) Verify(_ context.Context, ref string) (payment.Status, error) {
	if strings.HasSuffix(ref, "-paid") {
		return payment.StatusPaid, nil
	}
	return payment.StatusPending, nil
}
func (f *fakeProvider) ParseCallback(r *http.Request) (string, payment.Status, error) {
	ref := r.URL.Query().Get("out_trade_no")
	if ref == "" {
		return "", payment.StatusFailed, payment.ErrNotConfigured
	}
	return ref, payment.StatusPaid, nil
}
func (f *fakeProvider) Refund(_ context.Context, _ string, _ int64) error { return nil }

// newLinuxDOService 建一个 LinuxDO 服务用于签发测试会话（配置为空不影响会话逻辑）。
func newLinuxDOService(db *store.Store) *linuxdo.Service {
	return linuxdo.New(linuxdo.Config{}, db)
}

func newMembershipTestHandler(t *testing.T, db *store.Store, provider payment.Provider, auth *admin.Auth, publicURL string) http.Handler {
	t.Helper()
	handler, err := NewHandler(Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: db, Payment: provider, Auth: auth, PublicURL: publicURL, LinuxDO: newLinuxDOService(db)})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestRedeemEndpoint(t *testing.T) {
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	handler := newMembershipTestHandler(t, db, nil, nil, "")
	// 未登录 401
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, "/api/v1/redeem", strings.NewReader(`{"code":"RS-AAAA-BBBB-CCCC"}`)))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d", unauth.Code)
	}
	user, _ := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "", 2)
	service := newLinuxDOService(db)
	token, _, _ := service.StartSession(user)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/redeem", strings.NewReader(`{"code":"bad"}`))
	request.AddCookie(&http.Cookie{Name: "relayscope_user", Value: token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad code status = %d", recorder.Code)
	}
	codes, _ := db.GenerateRedeemCodes(context.Background(), 1, 30, "")
	payload := `{"code":"` + codes[0] + `"}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/redeem", strings.NewReader(payload))
	request.AddCookie(&http.Cookie{Name: "relayscope_user", Value: token})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("redeem status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	membership, _ := db.GetMembership(context.Background(), user.ID)
	if !membership.Active {
		t.Fatal("membership should be active after redeem")
	}
	// /auth/me 应携带会员信息
	me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	me.AddCookie(&http.Cookie{Name: "relayscope_user", Value: token})
	meRecorder := httptest.NewRecorder()
	handler.ServeHTTP(meRecorder, me)
	var identity struct {
		Membership struct {
			Active bool `json:"active"`
		} `json:"membership"`
	}
	_ = json.Unmarshal(meRecorder.Body.Bytes(), &identity)
	if !identity.Membership.Active {
		t.Fatalf("auth/me should report active membership: %s", meRecorder.Body.String())
	}
}

func TestPreferencesGateAndRoundTrip(t *testing.T) {
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	handler := newMembershipTestHandler(t, db, nil, nil, "")
	user, _ := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "", 2)
	service := newLinuxDOService(db)
	token, _, _ := service.StartSession(user)
	cookie := &http.Cookie{Name: "relayscope_user", Value: token}

	get := httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences", nil)
	get.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get prefs status = %d", recorder.Code)
	}
	put := httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", strings.NewReader(`{"hidden":{"sites":["x"]},"defaultHealthy":true,"tags":{}}`))
	put.AddCookie(cookie)
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusForbidden {
		t.Fatalf("non-member PUT should be 403, got %d", putRecorder.Code)
	}
	if _, err := db.ExtendMembership(context.Background(), user.ID, 30); err != nil {
		t.Fatal(err)
	}
	put = httptest.NewRequest(http.MethodPut, "/api/v1/me/preferences", strings.NewReader(`{"hidden":{"sites":["x"],"providers":[],"models":[]},"defaultHealthy":true,"tags":{"主力":{"color":"mint","sites":["y"]}}}`))
	put.AddCookie(cookie)
	putRecorder = httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("member PUT status = %d body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	get = httptest.NewRequest(http.MethodGet, "/api/v1/me/preferences", nil)
	get.AddCookie(cookie)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, get)
	var prefs struct {
		Hidden struct {
			Sites []string `json:"sites"`
		} `json:"hidden"`
		DefaultHealthy bool `json:"defaultHealthy"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &prefs)
	if len(prefs.Hidden.Sites) != 1 || prefs.Hidden.Sites[0] != "x" || !prefs.DefaultHealthy {
		t.Fatalf("prefs round trip mismatch: %s", recorder.Body.String())
	}
}

func TestWishEndpointsAndDedupe(t *testing.T) {
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	handler := newMembershipTestHandler(t, db, nil, nil, "")
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, "/api/v1/wishes", strings.NewReader(`{"name":"x","url":"https://a.com"}`)))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d", unauth.Code)
	}
	user, _ := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "", 2)
	service := newLinuxDOService(db)
	token, _, _ := service.StartSession(user)
	cookie := &http.Cookie{Name: "relayscope_user", Value: token}

	create := httptest.NewRequest(http.MethodPost, "/api/v1/wishes", strings.NewReader(`{"name":"云雾","url":"https://www.a.com/p","inviteRequired":false}`))
	create.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Wish struct {
			ID        int64  `json:"id"`
			Domain    string `json:"domain"`
			TargetLdc *int64 `json:"targetLdc"`
		} `json:"wish"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &created)
	if created.Wish.Domain != "a.com" || created.Wish.TargetLdc == nil || *created.Wish.TargetLdc != 30 {
		t.Fatalf("non-invite wish should default 30 LDC: %s", recorder.Body.String())
	}

	dup := httptest.NewRequest(http.MethodPost, "/api/v1/wishes", strings.NewReader(`{"name":"重复","url":"https://a.com/2","inviteRequired":true}`))
	dup.AddCookie(cookie)
	dupRecorder := httptest.NewRecorder()
	handler.ServeHTTP(dupRecorder, dup)
	if dupRecorder.Code != http.StatusConflict {
		t.Fatalf("duplicate should be 409, got %d", dupRecorder.Code)
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/wishes", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"a.com"`) {
		t.Fatalf("public list should include wish: %d %s", list.Code, list.Body.String())
	}

	// 助力：支付通道未配置 → 501
	pledge := httptest.NewRequest(http.MethodPost, "/api/v1/wishes/"+strconv.FormatInt(created.Wish.ID, 10)+"/pledge", strings.NewReader(`{"amountLdc":10}`))
	pledge.AddCookie(cookie)
	pledgeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(pledgeRecorder, pledge)
	if pledgeRecorder.Code != http.StatusNotImplemented {
		t.Fatalf("pledge without payment should be 501, got %d", pledgeRecorder.Code)
	}
}

func TestPaymentFlowWithFakeProvider(t *testing.T) {
	provider := &fakeProvider{}
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	handler := newMembershipTestHandler(t, db, provider, nil, "https://watchbot.cfd")
	user, _ := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "", 2)
	service := newLinuxDOService(db)
	token, _, _ := service.StartSession(user)
	cookie := &http.Cookie{Name: "relayscope_user", Value: token}
	wish, _ := db.CreateWishSite(context.Background(), user.ID, "目标站", "https://target.example.com", false, 30)

	// 会员直充
	recharge := httptest.NewRequest(http.MethodPost, "/api/v1/membership/recharge", strings.NewReader(`{}`))
	recharge.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, recharge)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recharge status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var charge map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &charge)
	orderNo, _ := charge["orderNo"].(string)
	if charge["payUrl"] != "https://pay.example/paying?no="+orderNo {
		t.Fatalf("unexpected charge response: %s", recorder.Body.String())
	}

	// 多月直充：3 个月 → 金额 = 3 × 月价，天数 = 90
	multi := httptest.NewRequest(http.MethodPost, "/api/v1/membership/recharge", strings.NewReader(`{"months":3}`))
	multi.AddCookie(cookie)
	multiRecorder := httptest.NewRecorder()
	handler.ServeHTTP(multiRecorder, multi)
	if multiRecorder.Code != http.StatusOK {
		t.Fatalf("multi-month recharge status = %d body=%s", multiRecorder.Code, multiRecorder.Body.String())
	}
	var multiCharge map[string]any
	_ = json.Unmarshal(multiRecorder.Body.Bytes(), &multiCharge)
	if amount, _ := multiCharge["amountLdc"].(float64); amount != 45 {
		t.Fatalf("3-month recharge amount = %v, want 45", multiCharge["amountLdc"])
	}

	// 非法月数 → 400
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/membership/recharge", strings.NewReader(`{"months":40}`))
	bad.AddCookie(cookie)
	badRecorder := httptest.NewRecorder()
	handler.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("months=40 should be 400, got %d", badRecorder.Code)
	}

	// 平台异步通知 → 落账 → 会员生效
	notifyQuery := url.Values{"out_trade_no": {orderNo}, "money": {"15.00"}}
	notify := httptest.NewRequest(http.MethodGet, "/api/v1/payment/notify?"+notifyQuery.Encode(), nil)
	notifyRecorder := httptest.NewRecorder()
	handler.ServeHTTP(notifyRecorder, notify)
	if notifyRecorder.Code != http.StatusOK || strings.TrimSpace(notifyRecorder.Body.String()) != "success" {
		t.Fatalf("notify status = %d body=%s", notifyRecorder.Code, notifyRecorder.Body.String())
	}
	membership, _ := db.GetMembership(context.Background(), user.ID)
	if !membership.Active {
		t.Fatal("membership should be active after recharge payment")
	}
	// 重放通知必须幂等
	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, notify)
	if replay.Code != http.StatusOK {
		t.Fatalf("replayed notify should still answer success, got %d", replay.Code)
	}

	// 许愿助力：会员已激活，10 免费额度优先抵扣，剩余 20 走支付 → 30/30 自动 reached
	pledge := httptest.NewRequest(http.MethodPost, "/api/v1/wishes/"+strconv.FormatInt(wish.ID, 10)+"/pledge", strings.NewReader(`{"amountLdc":30}`))
	pledge.AddCookie(cookie)
	pledgeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(pledgeRecorder, pledge)
	if pledgeRecorder.Code != http.StatusOK {
		t.Fatalf("pledge status = %d body=%s", pledgeRecorder.Code, pledgeRecorder.Body.String())
	}
	var pledgeCharge struct {
		OrderNo    string `json:"orderNo"`
		CreditUsed int64  `json:"creditUsed"`
		AmountLDC  int64  `json:"amountLdc"`
	}
	_ = json.Unmarshal(pledgeRecorder.Body.Bytes(), &pledgeCharge)
	if pledgeCharge.CreditUsed != 10 || pledgeCharge.AmountLDC != 30 {
		t.Fatalf("pledge should consume 10 credit of 30: %+v", pledgeCharge)
	}
	pledgeOrderNo := pledgeCharge.OrderNo
	notifyQuery = url.Values{"out_trade_no": {pledgeOrderNo}, "money": {"20.00"}}
	notify2 := httptest.NewRecorder()
	handler.ServeHTTP(notify2, httptest.NewRequest(http.MethodGet, "/api/v1/payment/notify?"+notifyQuery.Encode(), nil))
	if notify2.Code != http.StatusOK {
		t.Fatalf("wish notify failed: %d", notify2.Code)
	}
	site, _ := db.GetWishSite(context.Background(), wish.ID)
	if site.Status != store.WishStatusReached {
		t.Fatalf("wish should auto-reach, got %s", site.Status)
	}
	// 订单状态查询（本人）
	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/payment/orders/"+pledgeOrderNo, nil)
	statusReq.AddCookie(cookie)
	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, statusReq)
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"paid"`) {
		t.Fatalf("order status: %d %s", statusRecorder.Code, statusRecorder.Body.String())
	}
}

func TestAdminMembershipEndpoints(t *testing.T) {
	provider := &fakeProvider{}
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	auth, err := admin.NewAuth("this-is-a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	handler := newMembershipTestHandler(t, db, provider, auth, "")
	login := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"this-is-a-long-test-password"}`))
	login.RemoteAddr = "127.0.0.1:12345"
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, login)
	var adminCookie, csrfCookie *http.Cookie
	for _, cookie := range loginRecorder.Result().Cookies() {
		switch cookie.Name {
		case "relayscope_admin":
			adminCookie = cookie
		case "relayscope_csrf":
			csrfCookie = cookie
		}
	}
	if adminCookie == nil || csrfCookie == nil {
		t.Fatal("admin login did not issue cookies")
	}
	adminGet := func(path string) *http.Response {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(adminCookie)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result()
	}
	adminWrite := func(method, path, body string) *http.Response {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(adminCookie)
		request.AddCookie(csrfCookie)
		request.Header.Set("X-CSRF-Token", csrfCookie.Value)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result()
	}

	// 未登录 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/redeem-codes", nil)
	unauthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthRecorder, unauthReq)
	if unauthRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("admin endpoints require auth, got %d", unauthRecorder.Code)
	}
	// 生成兑换码
	generated := adminWrite(http.MethodPost, "/api/v1/admin/redeem-codes", `{"count":3,"days":90,"note":"内测"}`)
	if generated.StatusCode != http.StatusOK {
		t.Fatalf("generate status = %d", generated.StatusCode)
	}
	var batch struct {
		Codes []string `json:"codes"`
	}
	_ = json.NewDecoder(generated.Body).Decode(&batch)
	if len(batch.Codes) != 3 {
		t.Fatalf("expected 3 codes, got %v", batch.Codes)
	}
	// 列表
	if listed := adminGet("/api/v1/admin/redeem-codes"); listed.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listed.StatusCode)
	}
	// 设置
	if patched := adminWrite(http.MethodPatch, "/api/v1/admin/settings", `{"membershipMonthlyPriceLdc":15,"wishDefaultTargetLdc":50,"wishFreeCreditLdc":10}`); patched.StatusCode != http.StatusOK {
		t.Fatalf("settings patch = %d", patched.StatusCode)
	}
	if value, _ := db.GetSetting(context.Background(), "wish_default_target_ldc", "30"); value != "50" {
		t.Fatalf("setting should persist, got %q", value)
	}
	// LD ID 预登记会员：用户未登录时先写入，同一 ID 登录后自动匹配
	const expiresAt = "2030-01-02T15:04:05Z"
	if put := adminWrite(http.MethodPut, "/api/v1/admin/members/42", `{"expiresAt":"`+expiresAt+`"}`); put.StatusCode != http.StatusOK {
		t.Fatalf("member pre-registration = %d", put.StatusCode)
	}
	preRegistered, err := db.GetMemberByExternalID(context.Background(), store.ProviderLinuxDO, "42")
	if err != nil || preRegistered.Registered || preRegistered.MembershipExpiresAt == nil {
		t.Fatalf("member should be pre-registered: %+v err=%v", preRegistered, err)
	}
	if _, err := db.UpsertUser(context.Background(), store.ProviderLinuxDO, "42", "tester", "Tester", "", 2); err != nil {
		t.Fatal(err)
	}
	registered, err := db.GetMemberByExternalID(context.Background(), store.ProviderLinuxDO, "42")
	if err != nil || !registered.Registered || registered.MembershipExpiresAt == nil {
		t.Fatalf("LD login should match pre-registration: %+v err=%v", registered, err)
	}
	if listed := adminGet("/api/v1/admin/members"); listed.StatusCode != http.StatusOK {
		t.Fatalf("member list status = %d", listed.StatusCode)
	}
	if removed := adminWrite(http.MethodDelete, "/api/v1/admin/members/42", `{}`); removed.StatusCode != http.StatusOK {
		t.Fatalf("member remove = %d", removed.StatusCode)
	}
	removedMember, err := db.GetMemberByExternalID(context.Background(), store.ProviderLinuxDO, "42")
	if err != nil || removedMember.MembershipExpiresAt != nil {
		t.Fatalf("membership should be removed: %+v err=%v", removedMember, err)
	}
	// 许愿管理
	user, _ := db.UpsertUser(context.Background(), store.ProviderLinuxDO, "42", "tester", "Tester", "", 2)
	wish, _ := db.CreateWishSite(context.Background(), user.ID, "目标站", "https://t.example.com", true, 30)
	if listed := adminGet("/api/v1/admin/wishes"); listed.StatusCode != http.StatusOK {
		t.Fatalf("admin wishes status = %d", listed.StatusCode)
	}
	target := int64(120)
	body := `{"targetLdc":` + strconv.FormatInt(target, 10) + `}`
	if patched := adminWrite(http.MethodPatch, "/api/v1/admin/wishes/"+strconv.FormatInt(wish.ID, 10), body); patched.StatusCode != http.StatusOK {
		t.Fatalf("wish patch = %d", patched.StatusCode)
	}
	if updated, _ := db.GetWishSite(context.Background(), wish.ID); updated.TargetLDC == nil || *updated.TargetLDC != 120 {
		t.Fatal("wish target should update")
	}
	// 订单退款登记
	order, _ := db.CreateOrder(context.Background(), store.LDCOrder{OrderNo: "LD-R", UserID: user.ID, Kind: store.OrderKindWish, WishSiteID: &wish.ID, AmountLDC: 120})
	if _, _, err := db.MarkOrderPaid(context.Background(), "LD-R", "T-1"); err != nil {
		t.Fatal(err)
	}
	if refunded := adminWrite(http.MethodPost, "/api/v1/admin/orders/"+strconv.FormatInt(order.ID, 10)+"/refund", `{}`); refunded.StatusCode != http.StatusOK {
		t.Fatalf("refund = %d", refunded.StatusCode)
	}
	if site, _ := db.GetWishSite(context.Background(), wish.ID); site.Status != store.WishStatusOpen {
		t.Fatalf("wish should reopen after refund, got %s", site.Status)
	}
}

func TestWishCreditPledge(t *testing.T) {
	provider := &fakeProvider{}
	db, _ := store.Open(context.Background(), t.TempDir()+"/state.db")
	defer db.Close()
	handler := newMembershipTestHandler(t, db, provider, nil, "https://watchbot.cfd")
	user, _ := db.UpsertUser(context.Background(), "linuxdo", "42", "tester", "Tester", "", 2)
	service := newLinuxDOService(db)
	token, _, _ := service.StartSession(user)
	cookie := &http.Cookie{Name: "relayscope_user", Value: token}
	wish, _ := db.CreateWishSite(context.Background(), user.ID, "目标站", "https://credit.example.com", false, 30)

	// 非会员：额度端点不合格，助力走全额支付
	creditReq := httptest.NewRequest(http.MethodGet, "/api/v1/me/wish-credit", nil)
	creditReq.AddCookie(cookie)
	creditRecorder := httptest.NewRecorder()
	handler.ServeHTTP(creditRecorder, creditReq)
	if !strings.Contains(creditRecorder.Body.String(), `"eligible":false`) {
		t.Fatalf("non-member should be ineligible: %s", creditRecorder.Body.String())
	}
	pledge := httptest.NewRequest(http.MethodPost, "/api/v1/wishes/"+strconv.FormatInt(wish.ID, 10)+"/pledge", strings.NewReader(`{"amountLdc":30}`))
	pledge.AddCookie(cookie)
	pledgeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(pledgeRecorder, pledge)
	if pledgeRecorder.Code != http.StatusOK || strings.Contains(pledgeRecorder.Body.String(), `"creditUsed":`) && !strings.Contains(pledgeRecorder.Body.String(), `"creditUsed":0`) {
		t.Fatalf("non-member pledge should use no credit: %s", pledgeRecorder.Body.String())
	}

	// 会员：10 额度优先抵扣，剩余 20 走支付
	if _, err := db.ExtendMembership(context.Background(), user.ID, 30); err != nil {
		t.Fatal(err)
	}
	creditReq = httptest.NewRequest(http.MethodGet, "/api/v1/me/wish-credit", nil)
	creditReq.AddCookie(cookie)
	creditRecorder = httptest.NewRecorder()
	handler.ServeHTTP(creditRecorder, creditReq)
	if !strings.Contains(creditRecorder.Body.String(), `"available":10`) {
		t.Fatalf("member should have 10 credit: %s", creditRecorder.Body.String())
	}
	pledge = httptest.NewRequest(http.MethodPost, "/api/v1/wishes/"+strconv.FormatInt(wish.ID, 10)+"/pledge", strings.NewReader(`{"amountLdc":30}`))
	pledge.AddCookie(cookie)
	pledgeRecorder = httptest.NewRecorder()
	handler.ServeHTTP(pledgeRecorder, pledge)
	if pledgeRecorder.Code != http.StatusOK {
		t.Fatalf("member pledge status = %d body=%s", pledgeRecorder.Code, pledgeRecorder.Body.String())
	}
	var charge struct {
		CreditUsed int64  `json:"creditUsed"`
		AmountLDC  int64  `json:"amountLdc"`
		PayURL     string `json:"payUrl"`
	}
	_ = json.Unmarshal(pledgeRecorder.Body.Bytes(), &charge)
	if charge.CreditUsed != 10 || charge.AmountLDC != 30 || charge.PayURL == "" {
		t.Fatalf("mixed pledge mismatch: %+v", charge)
	}
	orders, _ := db.ListOrders(context.Background(), user.ID, store.OrderKindWish, "", 10)
	creditOrders := 0
	paidSum := int64(0)
	for _, order := range orders {
		if order.Funding == store.OrderFundingCredit && order.Status == store.OrderStatusPaid {
			creditOrders++
			paidSum += order.AmountLDC
		}
	}
	if creditOrders != 1 || paidSum != 10 {
		t.Fatalf("credit order should be paid immediately: %d orders sum=%d", creditOrders, paidSum)
	}
	// 助力 8 LDC：全额由剩余 2 额度 + 6 支付？剩余额度只有 0（30-10 已耗尽后剩 0）→ 全额支付
	// 先验证额度已耗尽
	creditReq = httptest.NewRequest(http.MethodGet, "/api/v1/me/wish-credit", nil)
	creditReq.AddCookie(cookie)
	creditRecorder = httptest.NewRecorder()
	handler.ServeHTTP(creditRecorder, creditReq)
	if !strings.Contains(creditRecorder.Body.String(), `"available":0`) {
		t.Fatalf("credit should be exhausted: %s", creditRecorder.Body.String())
	}
}
