package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relayscope/internal/payment"
	"relayscope/internal/store"
)

// registerAdminMembershipRoutes 注册管理台的兑换码 / 许愿池 / LDC 订单 / 运营设置路由。
func registerAdminMembershipRoutes(mux *http.ServeMux, options Options) {
	if options.Auth == nil || options.Store == nil {
		return
	}
	adminJSON := options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		notice, _ := options.Store.GetSetting(request.Context(), settingSiteNotice, "")
		updatedAt, _ := options.Store.GetSetting(request.Context(), settingSiteNoticeUpdatedAt, "")
		writeJSON(writer, map[string]any{
			"membershipMonthlyPriceLdc": settingInt64(request.Context(), options.Store, settingMembershipMonthlyPrice, defaultMembershipMonthlyPrice),
			"wishDefaultTargetLdc":      settingInt64(request.Context(), options.Store, settingWishDefaultTarget, defaultWishDefaultTarget),
			"wishFreeCreditLdc":         settingInt64(request.Context(), options.Store, settingWishFreeCredit, defaultWishFreeCredit),
			"siteNotice":                notice,
			"siteNoticeUpdatedAt":       updatedAt,
		})
	}))
	mux.Handle("GET /api/v1/admin/settings", adminJSON)
	mux.Handle("PATCH /api/v1/admin/settings", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			MembershipMonthlyPriceLdc *int64  `json:"membershipMonthlyPriceLdc"`
			WishDefaultTargetLdc      *int64  `json:"wishDefaultTargetLdc"`
			WishFreeCreditLdc         *int64  `json:"wishFreeCreditLdc"`
			SiteNotice                *string `json:"siteNotice"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "参数格式错误")
			return
		}
		for _, field := range []struct {
			name  string
			value *int64
			key   string
		}{
			{"会员月费", payload.MembershipMonthlyPriceLdc, settingMembershipMonthlyPrice},
			{"默认许愿目标", payload.WishDefaultTargetLdc, settingWishDefaultTarget},
			{"每月免费许愿额度", payload.WishFreeCreditLdc, settingWishFreeCredit},
		} {
			if field.value == nil {
				continue
			}
			if *field.value < 1 || *field.value > 10000 {
				writeError(writer, http.StatusBadRequest, field.name+"需在 1-10000 LDC 之间")
				return
			}
			if err := options.Store.SetSetting(request.Context(), field.key, strconv.FormatInt(*field.value, 10)); err != nil {
				writeError(writer, http.StatusInternalServerError, "设置保存失败")
				return
			}
		}
		if payload.SiteNotice != nil {
			notice := strings.TrimSpace(*payload.SiteNotice)
			if len(notice) > 20000 {
				writeError(writer, http.StatusBadRequest, "公告内容过长（上限 20000 字符）")
				return
			}
			if err := options.Store.SetSetting(request.Context(), settingSiteNotice, notice); err != nil {
				writeError(writer, http.StatusInternalServerError, "设置保存失败")
				return
			}
			if err := options.Store.SetSetting(request.Context(), settingSiteNoticeUpdatedAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
				writeError(writer, http.StatusInternalServerError, "设置保存失败")
				return
			}
		}
		writeJSON(writer, map[string]string{"status": "ok"})
	}))))

	// 会员列表：列出所有曾开通会员的用户（含已过期），按到期时间倒序
	mux.Handle("GET /api/v1/admin/members", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		members, err := options.Store.ListMembers(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "查询会员列表失败")
			return
		}
		writeJSON(writer, map[string]any{"members": members})
	})))

	// 会员身份：支持按 LinuxDO ID 预登记；用户登录时通过同一 ID 自动匹配
	mux.Handle("PUT /api/v1/admin/members/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的 LinuxDO ID")
			return
		}
		var payload struct {
			ExpiresAt string `json:"expiresAt"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil || strings.TrimSpace(payload.ExpiresAt) == "" {
			writeError(writer, http.StatusBadRequest, "请填写会员到期时间")
			return
		}
		expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ExpiresAt))
		if err != nil {
			writeError(writer, http.StatusBadRequest, "到期时间格式错误")
			return
		}
		member, err := options.Store.SetMembershipByExternalID(request.Context(), store.ProviderLinuxDO, strconv.FormatInt(id, 10), &expiresAt)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"member": member})
	}))))

	// 会员身份：清除有效期但保留用户/预登记记录
	mux.Handle("DELETE /api/v1/admin/members/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的 LinuxDO ID")
			return
		}
		member, err := options.Store.SetMembershipByExternalID(request.Context(), store.ProviderLinuxDO, strconv.FormatInt(id, 10), nil)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"member": member})
	}))))

	// 兑换码：批量生成
	mux.Handle("POST /api/v1/admin/redeem-codes", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Count int64  `json:"count"`
			Days  int64  `json:"days"`
			Note  string `json:"note"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "参数格式错误")
			return
		}
		codes, err := options.Store.GenerateRedeemCodes(request.Context(), payload.Count, payload.Days, payload.Note)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		display := make([]string, 0, len(codes))
		for _, canonical := range codes {
			display = append(display, store.FormatRedeemCode(canonical))
		}
		writeJSON(writer, map[string]any{"codes": display})
	}))))

	// 兑换码：列表
	mux.Handle("GET /api/v1/admin/redeem-codes", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
		codes, err := options.Store.ListRedeemCodes(request.Context(), request.URL.Query().Get("status"), limit, offset)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "读取兑换码失败")
			return
		}
		writeJSON(writer, map[string]any{"codes": codes})
	})))

	// 兑换码：撤销未使用
	mux.Handle("POST /api/v1/admin/redeem-codes/revoke", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			IDs []int64 `json:"ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "参数格式错误")
			return
		}
		revoked, err := options.Store.RevokeRedeemCodes(request.Context(), payload.IDs)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"revoked": revoked})
	}))))

	// 许愿池：全量列表（含进度）
	mux.Handle("GET /api/v1/admin/wishes", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		items, err := options.Store.ListAllWishSites(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "读取许愿池失败")
			return
		}
		writeJSON(writer, map[string]any{"wishes": items})
	})))

	// 许愿池：更新目标额度 / 状态
	mux.Handle("PATCH /api/v1/admin/wishes/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的许愿站点")
			return
		}
		var payload struct {
			TargetLDC *int64 `json:"targetLdc"`
			Status    string `json:"status"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 8<<10)).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "参数格式错误")
			return
		}
		wish, err := options.Store.UpdateWishSite(request.Context(), id, payload.TargetLDC, payload.Status)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"wish": wish})
	}))))

	// 许愿池：删除（级联删除其订单）
	mux.Handle("DELETE /api/v1/admin/wishes/{id}", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的许愿站点")
			return
		}
		if err := options.Store.DeleteWishSite(request.Context(), id); err != nil {
			writeError(writer, http.StatusNotFound, "许愿站点不存在")
			return
		}
		writeJSON(writer, map[string]string{"status": "ok"})
	}))))

	// LDC 订单流水
	mux.Handle("GET /api/v1/admin/orders", options.Auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		userID, _ := strconv.ParseInt(request.URL.Query().Get("userId"), 10, 64)
		orders, err := options.Store.ListOrders(request.Context(), userID, request.URL.Query().Get("kind"), request.URL.Query().Get("status"), limit)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "读取订单失败")
			return
		}
		writeJSON(writer, map[string]any{"orders": orders})
	})))

	// 订单退款：先调平台退款（如已配置），再落 refunded（进度随之回落）
	mux.Handle("POST /api/v1/admin/orders/{id}/refund", options.Auth.Middleware(csrfMiddleware(options.Auth, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(writer, http.StatusBadRequest, "无效的订单")
			return
		}
		var payload struct {
			Platform *bool `json:"platform"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10)).Decode(&payload)
		callPlatform := payload.Platform == nil || *payload.Platform
		if callPlatform && options.Payment.Configured() {
			target, err := options.Store.GetOrder(request.Context(), id)
			if err != nil || target.Status != store.OrderStatusPaid {
				writeError(writer, http.StatusBadRequest, "订单不存在或不在可退款状态")
				return
			}
			ref := target.PlatformTradeNo
			if ref == "" {
				ref = target.OrderNo
			}
			if err := options.Payment.Refund(request.Context(), ref, target.AmountLDC); err != nil {
				if !errors.Is(err, payment.ErrNotConfigured) {
					writeError(writer, http.StatusBadGateway, "平台退款失败："+err.Error())
					return
				}
			}
		}
		order, err := options.Store.MarkOrderRefunded(request.Context(), id)
		if err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"order": order})
	}))))
}
