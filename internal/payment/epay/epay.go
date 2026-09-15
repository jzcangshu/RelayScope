// Package epay 实现 LINUX DO Credit 的易支付（EasyPay）兼容协议。
// 网关 https://credit.linux.do/epay，签名 = MD5(参数 ASCII 升序 k1=v1&k2=v2… + key) 小写十六进制。
package epay

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"relayscope/internal/payment"
	"sort"
	"strings"
	"time"
)

const (
	orderExpire = 2 * time.Hour
	successBody = "success"
)

var ErrPaymentFailed = errors.New("支付平台返回错误")

type Config struct {
	Gateway string // 默认 https://credit.linux.do/epay
	PID     string // Client ID
	Key     string // Client Secret
}

type Provider struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Provider {
	gateway := strings.TrimRight(strings.TrimSpace(cfg.Gateway), "/")
	if gateway == "" {
		gateway = "https://credit.linux.do/epay"
	}
	return &Provider{cfg: Config{Gateway: gateway, PID: strings.TrimSpace(cfg.PID), Key: cfg.Key}, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (p *Provider) Configured() bool { return p.cfg.PID != "" && p.cfg.Key != "" }

// Sign 按 ASCII 升序拼接非空参数（排除 sign/sign_type）并追加密钥后取 MD5。
func Sign(params map[string]string, key string) string {
	names := make([]string, 0, len(params))
	for name, value := range params {
		if value == "" || name == "sign" || name == "sign_type" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+params[name])
	}
	sum := md5.Sum([]byte(strings.Join(pairs, "&") + key))
	return hex.EncodeToString(sum[:])
}

func verifySign(params map[string]string, key, sign string) bool {
	expected := Sign(params, key)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(strings.ToLower(sign))) == 1
}

// CreateCharge 调 submit.php 创建积分流转服务，成功时网关 302 到支付页，取其 Location 返回。
func (p *Provider) CreateCharge(ctx context.Context, req payment.ChargeRequest) (payment.Charge, error) {
	if !p.Configured() {
		return payment.Charge{}, errors.New("epay provider is not configured")
	}
	params := map[string]string{
		"pid":          p.cfg.PID,
		"type":         "epay",
		"out_trade_no": req.Ref,
		"name":         req.Description,
		"money":        fmt.Sprintf("%.2f", float64(req.AmountLDC)),
		"notify_url":   req.NotifyURL,
		"return_url":   req.ReturnURL,
	}
	params["sign"] = Sign(params, p.cfg.Key)
	params["sign_type"] = "MD5"
	form := url.Values{}
	for name, value := range params {
		form.Set(name, value)
	}
	endpoint := p.cfg.Gateway + "/pay/submit.php"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return payment.Charge{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.client.Do(request)
	if err != nil {
		return payment.Charge{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusFound || response.StatusCode == http.StatusSeeOther {
		location := response.Header.Get("Location")
		if location != "" {
			return payment.Charge{Ref: req.Ref, PayURL: location, ExpiresAt: time.Now().Add(orderExpire)}, nil
		}
		return payment.Charge{}, ErrPaymentFailed
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	return payment.Charge{}, fmt.Errorf("%w: %s", ErrPaymentFailed, strings.TrimSpace(string(body)))
}

// Query 主动查询订单状态（GET /epay/api.php）。status=1 视为已支付。
func (p *Provider) Query(ctx context.Context, ref string) (payment.Status, error) {
	if !p.Configured() {
		return payment.StatusFailed, errors.New("epay provider is not configured")
	}
	params := url.Values{"act": {"order"}, "pid": {p.cfg.PID}, "key": {p.cfg.Key}, "out_trade_no": {ref}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.Gateway+"/api.php?"+params.Encode(), nil)
	if err != nil {
		return payment.StatusFailed, err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return payment.StatusFailed, err
	}
	defer response.Body.Close()
	var payload struct {
		Code   int    `json:"code"`
		Status int    `json:"status"`
		Msg    string `json:"msg"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil {
		return payment.StatusFailed, err
	}
	if payload.Code == 1 && payload.Status == 1 {
		return payment.StatusPaid, nil
	}
	if payload.Code == -1 {
		return payment.StatusFailed, nil
	}
	return payment.StatusPending, nil
}

// Verify 满足端口：等价于 Query。
func (p *Provider) Verify(ctx context.Context, ref string) (payment.Status, error) {
	return p.Query(ctx, ref)
}

// ParseCallback 校验异步通知（HTTP GET，参数含 sign）。
// 依次校验：签名、pid、trade_status=TRADE_SUCCESS；订单号与金额由调用方比对。
func (p *Provider) ParseCallback(r *http.Request) (string, payment.Status, error) {
	query := r.URL.Query()
	params := map[string]string{}
	for name := range query {
		params[name] = query.Get(name)
	}
	sign := params["sign"]
	if sign == "" || !verifySign(params, p.cfg.Key, sign) {
		return "", payment.StatusFailed, errors.New("回调签名校验失败")
	}
	if params["pid"] != p.cfg.PID {
		return "", payment.StatusFailed, errors.New("回调 pid 不匹配")
	}
	ref := params["out_trade_no"]
	if ref == "" {
		return "", payment.StatusFailed, errors.New("回调缺少订单号")
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		return ref, payment.StatusFailed, nil
	}
	return ref, payment.StatusPaid, nil
}

// Refund 调平台退款接口对已支付订单全额退回。
func (p *Provider) Refund(ctx context.Context, ref string, amountLDC int64) error {
	if !p.Configured() {
		return errors.New("epay provider is not configured")
	}
	form := url.Values{
		"pid":          {p.cfg.PID},
		"key":          {p.cfg.Key},
		"trade_no":     {ref},
		"out_trade_no": {ref},
		"money":        {fmt.Sprintf("%.2f", float64(amountLDC))},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Gateway+"/api.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := decodeJSON(response.Body, &payload); err != nil {
		return err
	}
	if payload.Code != 1 {
		return fmt.Errorf("%w: %s", ErrPaymentFailed, payload.Msg)
	}
	return nil
}

func decodeJSON(body io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(body, 16<<10))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return ErrPaymentFailed
	}
	return nil
}
