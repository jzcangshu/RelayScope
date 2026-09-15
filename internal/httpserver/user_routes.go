package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"relayscope/internal/payment"
	"relayscope/internal/store"
)

const (
	settingMembershipLdcPerDay = "membership_ldc_per_day"
	settingWishDefaultTarget   = "wish_default_target_ldc"

	defaultMembershipLdcPerDay = "1"
	defaultWishDefaultTarget   = "30"
)

// registerUserRoutes 注册面向登录用户的会员/偏好/许愿/支付路由。
func registerUserRoutes(mux *http.ServeMux, options Options) {
	if options.Store == nil {
		return
	}
	redeemLimiter := newRateLimiter(10, time.Minute)
	wishCreateLimiter := newRateLimiter(5, 24*time.Hour)
	pledgeLimiter := newRateLimiter(20, time.Hour)

	// GET/PUT /api/v1/me/preferences —— 定制页云端同步（PUT 需要有效会员）
	mux.HandleFunc("GET /api/v1/me/preferences", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		prefs, err := options.Store.GetUserPreferences(request.Context(), user.ID)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "读取偏好失败")
			return
		}
		writeJSON(writer, prefs)
	})
	mux.HandleFunc("PUT /api/v1/me/preferences", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		if !requireMembership(options, writer, request, user.ID) {
			return
		}
		var payload store.Preferences
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "偏好格式错误")
			return
		}
		saved, err := options.Store.PutUserPreferences(request.Context(), user.ID, payload)
		if err != nil {
			if errors.Is(err, store.ErrPreferencesTooLarge) {
				writeError(writer, http.StatusBadRequest, "偏好数据超出限制")
				return
			}
			writeError(writer, http.StatusBadRequest, "偏好保存失败")
			return
		}
		writeJSON(writer, saved)
	})

	// POST /api/v1/redeem —— 兑换码核销，延长会员
	mux.HandleFunc("POST /api/v1/redeem", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		if !redeemLimiter.allow("u" + strconv.FormatInt(user.ID, 10)) {
			writeError(writer, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
			return
		}
		var payload struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "兑换码格式错误")
			return
		}
		membership, err := options.Store.RedeemCode(request.Context(), user.ID, payload.Code)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"status": "ok", "membership": membership})
	})

	// POST /api/v1/membership/recharge —— LDC 直充会员
	mux.HandleFunc("POST /api/v1/membership/recharge", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		if !pledgeLimiter.allow("u" + strconv.FormatInt(user.ID, 10)) {
			writeError(writer, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
			return
		}
		var payload struct {
			Days int64 `json:"days"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "参数格式错误")
			return
		}
		if payload.Days < 1 || payload.Days > 3650 {
			writeError(writer, http.StatusBadRequest, "会员天数需在 1-3650 之间")
			return
		}
		chargeOrder, err := createChargeOrder(options, request.Context(), user.ID, store.LDCOrder{
			UserID: user.ID, Kind: store.OrderKindMembership, Days: &payload.Days,
			AmountLDC: payload.Days * membershipLdcPerDay(request.Context(), options.Store),
		}, "会员 "+strconv.FormatInt(payload.Days, 10)+" 天")
		if err != nil {
			emitOrderError(writer, err)
			return
		}
		writeJSON(writer, chargeOrder)
	})

	// GET /api/v1/wishes —— 公开许愿池（登录用户附带本人认领额）
	mux.HandleFunc("GET /api/v1/wishes", func(writer http.ResponseWriter, request *http.Request) {
		viewerID := int64(0)
		if user, ok := linuxDOUser(options.LinuxDO, request); ok {
			viewerID = user.ID
		}
		items, err := options.Store.ListWishSites(request.Context(), viewerID)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "读取许愿池失败")
			return
		}
		writeJSON(writer, map[string]any{"wishes": items})
	})

	// POST /api/v1/wishes —— 新增许愿（按域名去重）
	mux.HandleFunc("POST /api/v1/wishes", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		if !wishCreateLimiter.allow("u" + strconv.FormatInt(user.ID, 10)) {
			writeError(writer, http.StatusTooManyRequests, "今天的许愿次数已用完，明天再来吧")
			return
		}
		var payload struct {
			Name           string `json:"name"`
			URL            string `json:"url"`
			InviteRequired bool   `json:"inviteRequired"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "许愿格式错误")
			return
		}
		defaultTarget := wishDefaultTarget(request.Context(), options.Store)
		wish, err := options.Store.CreateWishSite(request.Context(), user.ID, payload.Name, payload.URL, payload.InviteRequired, defaultTarget)
		if err != nil {
			if errors.Is(err, store.ErrWishDuplicate) {
				writeError(writer, http.StatusConflict, err.Error())
				return
			}
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"wish": wish})
	})

	// POST /api/v1/wishes/{id}/pledge —— 为许愿站点助力（LDC 支付）
	mux.HandleFunc("POST /api/v1/wishes/{id}/pledge", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		if !pledgeLimiter.allow("u" + strconv.FormatInt(user.ID, 10)) {
			writeError(writer, http.StatusTooManyRequests, "操作过于频繁，请稍后再试")
			return
		}
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的许愿站点")
			return
		}
		site, err := options.Store.GetWishSite(request.Context(), id)
		if err != nil {
			writeError(writer, http.StatusNotFound, "许愿站点不存在")
			return
		}
		if site.Status != store.WishStatusOpen {
			writeError(writer, http.StatusBadRequest, "该站点不在可助力状态")
			return
		}
		var payload struct {
			AmountLDC int64 `json:"amountLdc"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "助力金额格式错误")
			return
		}
		if payload.AmountLDC < 1 || payload.AmountLDC > 10000 {
			writeError(writer, http.StatusBadRequest, "助力金额需在 1-10000 LDC 之间")
			return
		}
		chargeOrder, err := createChargeOrder(options, request.Context(), user.ID, store.LDCOrder{
			UserID: user.ID, Kind: store.OrderKindWish, WishSiteID: &site.ID, AmountLDC: payload.AmountLDC,
		}, "许愿助力 · "+site.Name)
		if err != nil {
			emitOrderError(writer, err)
			return
		}
		writeJSON(writer, chargeOrder)
	})

	// GET /api/v1/payment/orders/{orderNo} —— 本人订单状态；pending 时顺带向平台查单兜底
	mux.HandleFunc("GET /api/v1/payment/orders/{orderNo}", func(writer http.ResponseWriter, request *http.Request) {
		user, ok := requireUser(options, writer, request)
		if !ok {
			return
		}
		order, err := options.Store.GetOrderByNo(request.Context(), request.PathValue("orderNo"))
		if err != nil || order.UserID != user.ID {
			writeError(writer, http.StatusNotFound, "订单不存在")
			return
		}
		if order.Status == store.OrderStatusPending && options.Payment.Configured() {
			if status, err := options.Payment.Verify(request.Context(), order.OrderNo); err == nil && status == payment.StatusPaid {
				if _, _, err := options.Store.MarkOrderPaid(request.Context(), order.OrderNo, order.OrderNo); err != nil {
					writeError(writer, http.StatusInternalServerError, "订单状态更新失败")
					return
				}
				order, _ = options.Store.GetOrderByNo(request.Context(), order.OrderNo)
			}
		}
		response := map[string]any{"order": order}
		if order.Kind == store.OrderKindMembership && order.Status == store.OrderStatusPaid {
			if membership, err := options.Store.GetMembership(request.Context(), user.ID); err == nil {
				response["membership"] = membership
			}
		}
		writeJSON(writer, response)
	})

	// GET /api/v1/payment/notify —— 平台异步通知（签名校验后落账，回复 success）
	mux.HandleFunc("GET /api/v1/payment/notify", func(writer http.ResponseWriter, request *http.Request) {
		if !options.Payment.Configured() {
			writeError(writer, http.StatusNotImplemented, "支付通道未配置")
			return
		}
		ref, status, err := options.Payment.ParseCallback(request)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "通知校验失败")
			return
		}
		if status != payment.StatusPaid {
			writeError(writer, http.StatusBadRequest, "通知状态无效")
			return
		}
		order, err := options.Store.GetOrderByNo(request.Context(), ref)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "订单不存在")
			return
		}
		// 签名之外再核一遍金额，双保险
		if money := moneyToLDC(request.URL.Query().Get("money")); money != order.AmountLDC {
			writeError(writer, http.StatusBadRequest, "通知金额不匹配")
			return
		}
		tradeNo := request.URL.Query().Get("trade_no")
		if _, _, err := options.Store.MarkOrderPaid(request.Context(), order.OrderNo, tradeNo); err != nil {
			writeError(writer, http.StatusInternalServerError, "订单状态更新失败")
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("success"))
	})

	// GET /api/v1/payment/return —— 支付完成回跳，重定向回对应页面并携带订单号供前端轮询确认
	mux.HandleFunc("GET /api/v1/payment/return", func(writer http.ResponseWriter, request *http.Request) {
		target := "/"
		if orderNo := request.URL.Query().Get("no"); orderNo != "" {
			if order, err := options.Store.GetOrderByNo(request.Context(), orderNo); err == nil && order.Kind == store.OrderKindWish {
				target = "/#wishes?paid=" + url.QueryEscape(orderNo)
			} else {
				target = "/?paid=" + url.QueryEscape(orderNo)
			}
		}
		base := strings.TrimSpace(options.PublicURL)
		http.Redirect(writer, request, base+target, http.StatusFound)
	})
}

// createChargeOrder 建单并创建支付；返回 orderNo 与 payUrl。
func createChargeOrder(options Options, ctx context.Context, userID int64, order store.LDCOrder, description string) (map[string]any, error) {
	if !options.Payment.Configured() {
		return nil, payment.ErrNotConfigured
	}
	orderNo, err := newOrderNo()
	if err != nil {
		return nil, err
	}
	order.OrderNo = orderNo
	if _, err := options.Store.CreateOrder(ctx, order); err != nil {
		return nil, err
	}
	charge, err := options.Payment.CreateCharge(ctx, payment.ChargeRequest{
		Ref:         orderNo,
		AmountLDC:   order.AmountLDC,
		Description: description,
		NotifyURL:   strings.TrimRight(options.PublicURL, "/") + "/api/v1/payment/notify",
		ReturnURL:   strings.TrimRight(options.PublicURL, "/") + "/api/v1/payment/return?no=" + orderNo,
	})
	if err != nil {
		// 建单成功但平台下单失败：作废这张订单，避免悬挂 pending
		_ = options.Store.CancelOrder(ctx, orderNo)
		return nil, err
	}
	return map[string]any{"orderNo": orderNo, "payUrl": charge.PayURL, "amountLdc": order.AmountLDC}, nil
}

func emitOrderError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrNotConfigured):
		writeError(writer, http.StatusNotImplemented, "LDC 支付通道即将开通")
	default:
		writeError(writer, http.StatusBadGateway, "支付下单失败，请稍后再试")
	}
}

func moneyToLDC(raw string) int64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 {
		return -1
	}
	return int64(value + 0.5)
}

func membershipLdcPerDay(ctx context.Context, db *store.Store) int64 {
	return settingInt64(ctx, db, settingMembershipLdcPerDay, defaultMembershipLdcPerDay)
}

func wishDefaultTarget(ctx context.Context, db *store.Store) int64 {
	return settingInt64(ctx, db, settingWishDefaultTarget, defaultWishDefaultTarget)
}

func settingInt64(ctx context.Context, db *store.Store, key, fallback string) int64 {
	raw, err := db.GetSetting(ctx, key, fallback)
	if err != nil {
		return parseClampedInt(fallback)
	}
	return parseClampedInt(raw)
}

func parseClampedInt(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 1 {
		return 1
	}
	if value > 10000 {
		return 10000
	}
	return value
}

func newOrderNo() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "LD" + base64.RawURLEncoding.EncodeToString(b), nil
}

func requireUser(options Options, writer http.ResponseWriter, request *http.Request) (store.User, bool) {
	user, ok := linuxDOUser(options.LinuxDO, request)
	if !ok {
		writeError(writer, http.StatusUnauthorized, "请先登录")
		return store.User{}, false
	}
	return user, true
}

func requireMembership(options Options, writer http.ResponseWriter, request *http.Request, userID int64) bool {
	membership, err := options.Store.GetMembership(request.Context(), userID)
	if err != nil || !membership.Active {
		writeError(writer, http.StatusForbidden, "该功能需要有效会员")
		return false
	}
	return true
}

// rateLimiter 是内存滑动窗口限速器，按 key 独立计数；进程重启即重置，够用且零依赖。
type rateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	events map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, events: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.events[key][:0]
	for _, at := range l.events[key] {
		if now.Sub(at) < l.window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= l.limit {
		l.events[key] = recent
		return false
	}
	l.events[key] = append(recent, now)
	return true
}
