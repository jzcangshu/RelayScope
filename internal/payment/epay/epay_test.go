package epay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	payment "relayscope/internal/payment"
)

func TestSignMatchesDocumentExample(t *testing.T) {
	// 文档示例：payload="money=10&name=Test&out_trade_no=M20250101&pid=001&type=epay"，密钥拼接后取小写 MD5
	sign := Sign(map[string]string{"pid": "001", "type": "epay", "out_trade_no": "M20250101", "name": "Test", "money": "10", "sign_type": "MD5"}, "SECRET")
	if sign != Sign(map[string]string{"money": "10", "name": "Test", "out_trade_no": "M20250101", "pid": "001", "type": "epay"}, "SECRET") {
		t.Fatal("sign must ignore sign_type and empty values deterministically")
	}
	if len(sign) != 32 || strings.ToLower(sign) != sign {
		t.Fatalf("sign must be lowercase md5 hex, got %q", sign)
	}
}

func TestCreateChargeFollowsGatewayRedirect(t *testing.T) {
	var received url.Values
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		received = r.Form
		http.Redirect(w, r, "https://credit.linux.do/paying?order_no=X", http.StatusFound)
	}))
	defer gateway.Close()
	provider := New(Config{Gateway: gateway.URL, PID: "001", Key: "SECRET"})
	charge, err := provider.CreateCharge(context.Background(), payment.ChargeRequest{Ref: "LD-1", AmountLDC: 30, Description: "许愿助力", NotifyURL: "https://x/notify", ReturnURL: "https://x/return"})
	if err != nil {
		t.Fatal(err)
	}
	if charge.PayURL != "https://credit.linux.do/paying?order_no=X" {
		t.Fatalf("unexpected pay url %q", charge.PayURL)
	}
	if received.Get("money") != "30.00" || received.Get("type") != "epay" || received.Get("out_trade_no") != "LD-1" {
		t.Fatalf("submit params mismatch: %v", received)
	}
	if Sign(map[string]string{"a": "1"}, "SECRET") == Sign(map[string]string{"a": "2"}, "SECRET") {
		t.Fatal("different payloads must yield different signs")
	}
}

func TestCreateChargeSurfacesGatewayError(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"error_msg": "签名验证失败", "data": nil})
	}))
	defer gateway.Close()
	provider := New(Config{Gateway: gateway.URL, PID: "001", Key: "SECRET"})
	if _, err := provider.CreateCharge(context.Background(), payment.ChargeRequest{Ref: "LD-1", AmountLDC: 30, Description: "x"}); err == nil || !strings.Contains(err.Error(), "签名验证失败") {
		t.Fatalf("gateway error should surface, got %v", err)
	}
}

func TestParseCallbackValidatesSignature(t *testing.T) {
	provider := New(Config{PID: "001", Key: "SECRET"})
	params := map[string]string{
		"pid": "001", "trade_no": "T1", "out_trade_no": "LD-1", "type": "epay",
		"name": "许愿", "money": "30.00", "trade_status": "TRADE_SUCCESS",
	}
	sign := Sign(params, "SECRET")
	query := url.Values{}
	for name, value := range params {
		query.Set(name, value)
	}
	query.Set("sign", sign)
	request := httptest.NewRequest(http.MethodGet, "/notify?"+query.Encode(), nil)

	ref, status, err := provider.ParseCallback(request)
	if err != nil || ref != "LD-1" || status != payment.StatusPaid {
		t.Fatalf("callback parse: ref=%q status=%v err=%v", ref, status, err)
	}

	tampered := query
	tampered.Set("money", "9999.00")
	if _, _, err := provider.ParseCallback(httptest.NewRequest(http.MethodGet, "/notify?"+tampered.Encode(), nil)); err == nil {
		t.Fatal("tampered callback must fail signature check")
	}
	query.Set("money", "30.00")
	params["trade_status"] = "TRADE_CLOSED"
	query.Set("trade_status", "TRADE_CLOSED")
	query.Set("sign", Sign(params, "SECRET"))
	if _, status, err := provider.ParseCallback(httptest.NewRequest(http.MethodGet, "/notify?"+query.Encode(), nil)); err != nil || status != payment.StatusFailed {
		t.Fatalf("non-success trade status should map to failed, got %v err=%v", status, err)
	}
}

func TestQueryInterpretsPlatformStatus(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("out_trade_no") == "LD-PAID" {
			_, _ = w.Write([]byte(`{"code":1,"msg":"ok","status":1}`))
			return
		}
		if r.URL.Query().Get("out_trade_no") == "LD-MISSING" {
			_, _ = w.Write([]byte(`{"code":-1,"msg":"服务不存在或已完成"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok","status":0}`))
	}))
	defer gateway.Close()
	provider := New(Config{Gateway: gateway.URL, PID: "001", Key: "SECRET"})
	if status, _ := provider.Query(context.Background(), "LD-PAID"); status != payment.StatusPaid {
		t.Fatal("status 1 must map to paid")
	}
	if status, _ := provider.Query(context.Background(), "LD-MISSING"); status != payment.StatusFailed {
		t.Fatal("code -1 must map to failed")
	}
	if status, _ := provider.Query(context.Background(), "LD-WAIT"); status != payment.StatusPending {
		t.Fatal("status 0 must map to pending")
	}
}
