"""本地前端验证用的模拟服务：静态托管 web/public，并模拟看板 API。

用法: python mock_server.py [端口]
环境变量 MOCK_LOGGED_IN=1 模拟已登录会员，便于联调登录态 UI。
"""
import json
import mimetypes
import os
import sys
import time
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PUBLIC = Path(__file__).resolve().parent.parent / "web" / "public"
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8099
LOGGED_IN = bool(os.environ.get("MOCK_LOGGED_IN"))
# MOCK_MEMBER_EXPIRED=1：已登录但会员已过期（用于验证定制页门禁），需配合 MOCK_LOGGED_IN=1
MEMBER_EXPIRED = bool(os.environ.get("MOCK_MEMBER_EXPIRED"))

NOW = datetime.now(timezone.utc).isoformat()
HOURS_AGO = lambda h: datetime.now(timezone.utc).timestamp() * 1000 - h * 3600 * 1000

MOCK_USER = {"id": 1, "provider": "linuxdo", "externalId": "42", "username": "tester", "name": "Tester", "avatarUrl": "", "trustLevel": 2, "createdAt": NOW}
MOCK_MEMBERSHIP_ACTIVE = {"expiresAt": (datetime.now(timezone.utc) + timedelta(days=90)).isoformat(), "active": True}
MOCK_MEMBERSHIP = ({"expiresAt": (datetime.now(timezone.utc) - timedelta(days=3)).isoformat(), "active": False} if MEMBER_EXPIRED else MOCK_MEMBERSHIP_ACTIVE)
MOCK_PREFERENCES = {"hidden": {"sites": [], "providers": [], "models": []}, "defaultHealthy": False, "tags": {"主力": {"color": "rose", "sites": ["星云中转"]}, "观望": {"color": "amber", "sites": ["紫电API"]}}, "sorting": {"model": "default", "site": "default"}, "updatedAt": NOW}
MOCK_WISHES = [
    {"id": 1, "domain": "example.com", "name": "示例中转", "url": "https://example.com", "inviteRequired": False, "targetLdc": 30, "status": "open", "pledgedLdc": 12, "pledgers": 3, "myPledgedLdc": 5, "myPending": False},
    {"id": 2, "domain": "secret.example.org", "name": "神秘站点", "url": "https://secret.example.org", "inviteRequired": True, "targetLdc": None, "status": "open", "pledgedLdc": 45, "pledgers": 6, "myPledgedLdc": 0, "myPending": False},
    {"id": 3, "domain": "done.example.net", "name": "已达成站点", "url": "https://done.example.net", "inviteRequired": False, "targetLdc": 30, "status": "reached", "pledgedLdc": 32, "pledgers": 5, "myPledgedLdc": 0, "myPending": False},
]
MOCK_ORDERS = [
    {"id": 1, "orderNo": "LDmock0001", "userId": 1, "kind": "membership", "wishSiteId": None, "days": 30, "amountLdc": 30, "status": "paid", "platformTradeNo": "T1", "username": "tester", "createdAt": NOW, "paidAt": NOW},
]
MOCK_REDEEM_CODES = [
    {"id": 1, "code": "RS-AAAA-BBBB-CCCC", "days": 30, "note": "mock", "status": "unused", "redeemedBy": 0, "redeemedByName": "", "redeemedAt": None, "createdAt": NOW},
]

PRICE = {
    "available": True, "mode": "token",
    "currency": "CNY", "currencySymbol": "¥",
    "inputPerMillion": 12.5, "outputPerMillion": 25.0,
    "cacheReadPerMillion": 3.0, "cacheWritePerMillion": 6.0,
    "fixedPerRequest": None, "groupMultiplier": None,
}

def row(site_id, site, provider, rule, raw, group, state, acq, ratio, latency, price=None, offset_h=0.5):
    ts = NOW if offset_h == 0 else datetime.now(timezone.utc).timestamp() * 1000 - offset_h * 3600 * 1000
    return {
        "provider": provider, "ruleName": rule, "siteId": site_id, "siteName": site,
        "siteUrl": f"https://{site}.example.com", "rawModelName": raw, "groupName": group,
        "serviceState": state, "acquisitionState": acq,
        "observedAt": ts, "collectedAt": ts,
        "successRatio": ratio, "averageLatencyMs": latency,
        "firstTokenMs": 320, "tokensPerSecond": 142.3,
        "price": price or PRICE,
    }

ROWS = [
    # 星云中转：OpenAI 系，健康
    row(1, "星云中转", "OpenAI", "gpt-4o", "gpt-4o-2024-11-20", "官方", "healthy", "fresh", 0.998, 214),
    row(1, "星云中转", "OpenAI", "gpt-4o-mini", "gpt-4o-mini", "官方", "healthy", "fresh", 0.996, 183),
    row(1, "星云中转", "OpenAI", "gpt-4o", "gpt-4o-2024-11-20", "备用-1", "degraded", "stale", 0.91, 480),
    # 紫电API：混合状态
    row(2, "紫电API", "GLM", "glm-4-plus", "glm-4-plus", "国内", "healthy", "fresh", 0.99, 260),
    row(2, "紫电API", "GLM", "glm-4-flash", "glm-4-flash", "国内", "degraded", "fresh", 0.87, 512),
    row(2, "紫电API", "OpenAI", "gpt-4o", "gpt-4o-main", "主链路", "failed", "collection_failed", 0.42, 900),
    # 蓝光通道：失败 + 未归类
    row(3, "蓝光通道", "Anthropic", "claude-3-5-sonnet", "claude-3-5-sonnet-20241022", "直连", "healthy", "fresh", 0.995, 198),
    row(3, "蓝光通道", "", "", "nova-pro-v2", "实验线", "no_samples", "unknown", None, None, {"available": False, "mode": "token", "currency": "CNY", "currencySymbol": "¥", "inputPerMillion": None, "outputPerMillion": None, "cacheReadPerMillion": None, "cacheWritePerMillion": None, "fixedPerRequest": None, "groupMultiplier": None}),
]

BUCKETS = []
for _ in range(48):
    BUCKETS.append({"siteId": 1, "rawModelName": "gpt-4o-2024-11-20", "groupName": "官方", "serviceState": "healthy", "start": NOW, "end": NOW})

# 通知订阅（内存态，重启即清空）
SUBSCRIPTIONS = []
NEXT_SUB_ID = 1

def _site_name(site_id):
    for r in ROWS:
        if r["siteId"] == site_id:
            return r["siteName"]
    return ""

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def end_headers(self):
        # 与生产 securityHeaders 保持一致：本地也要有 CSP，
        # 否则「CSP 静默拦截内联 style」这类只在生产爆发的问题本地永远测不出来。
        self.send_header("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
        super().end_headers()

    def _json(self, payload, status=200):
        body = json.dumps(payload, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = self.path.split("?")[0]
        if path == "/api/v1/meta":
            return self._json({"revision": "mock-1", "serverTime": NOW, "membershipMonthlyPriceLdc": "15", "wishDefaultTargetLdc": "30", "authProviders": ["linuxdo"]})
        if path == "/api/v1/public/dashboard":
            return self._json({"revision": "mock-1", "rows": ROWS, "buckets": BUCKETS, "hours": 24})
        if path == "/api/v1/public/announcements":
            fail_anns = [
                {"siteId": 3, "siteName": "蓝光通道", "failureCode": "challenge_failed", "reason": "Cloudflare 验证持续失败，FlareSolverr 无法通过 challenge"},
                {"siteId": 2, "siteName": "紫电API", "failureCode": "collection_failed", "reason": "请求超时（7 分钟内未响应）"},
            ]
            site_ann_ids = [1, 2, 3]
            if LOGGED_IN:
                notice = {"markdown": "# 欢迎使用 RelayScope\n\n- 数据每 **5 分钟** 自动刷新\n\n- 问题请通过反馈提交", "updatedAt": NOW}
                return self._json({"announcements": fail_anns, "revision": "mock-1", "notice": notice, "siteAnnouncementSiteIds": site_ann_ids})
            return self._json({"announcements": fail_anns, "revision": "mock-1", "siteAnnouncementSiteIds": site_ann_ids})
        if path.startswith("/api/v1/public/site-announcements"):
            site_id = int(self.path.split("site_id=")[-1].split("&")[0]) if "site_id=" in self.path else 0
            now_ts = int(datetime.now(timezone.utc).timestamp() * 1000)
            anns_by_site = {
                1: [
                    {"id": 1, "siteId": 1, "externalId": "a1", "title": "上线 glm-5.3-flash", "content": "Translate 分组扩容，现已支持 GLM-5.3-flash 模型，倍率 0.5。请勿高并发使用。", "annType": "success", "extra": "", "publishedAt": now_ts - 3600000 * 2, "firstSeenAt": now_ts - 3600000 * 2, "lastSeenAt": now_ts},
                    {"id": 2, "siteId": 1, "externalId": "a2", "title": "", "content": "由于学业繁重，且本人为住宿生，故维护频率会降低。GLM5.2 空回复/429 稍等重试即可。", "annType": "warning", "extra": "", "publishedAt": now_ts - 86400000 * 2, "firstSeenAt": now_ts - 86400000 * 2, "lastSeenAt": now_ts},
                    {"id": 9, "siteId": 1, "externalId": "a9", "title": "", "content": "站点上线一周年，感谢大家支持。历史公告比 24h/7d 都旧，用于验证默认范围自动适配。", "annType": "default", "extra": "", "publishedAt": now_ts - 86400000 * 45, "firstSeenAt": now_ts - 86400000 * 45, "lastSeenAt": now_ts},
                ],
                2: [
                    {"id": 3, "siteId": 2, "externalId": "a3", "title": "关于账号封禁问题的说明", "content": "由于目前资源紧张，当天token资源分配完毕后将不再继续分配，请求也不会被处理。请大家留意以下几点：正常使用者请自查，若发现大量 429 错误建议立即停止使用。", "annType": "warning", "extra": "", "publishedAt": now_ts - 7200000, "firstSeenAt": now_ts - 7200000, "lastSeenAt": now_ts},
                ],
                3: [
                    {"id": 4, "siteId": 3, "externalId": "a4", "title": "", "content": "complimentary分组glm-5.2模型目前有几率降级路由至glm-5.1，glm-5以提升可用性。", "annType": "default", "extra": "", "publishedAt": now_ts - 86400000, "firstSeenAt": now_ts - 86400000, "lastSeenAt": now_ts},
                ],
            }
            return self._json({"announcements": anns_by_site.get(site_id, [])})
        if path == "/api/v1/public/details":
            return self._json({"buckets": BUCKETS, "groups": [g for g in ROWS if g["siteName"] == "星云中转" or g["groupName"] == "官方"]})
        if path == "/api/v1/auth/me":
            if LOGGED_IN:
                return self._json({"authenticated": True, "user": MOCK_USER, "membership": MOCK_MEMBERSHIP})
            return self._json({"authenticated": False})
        if path == "/api/v1/me/preferences":
            return self._json(MOCK_PREFERENCES)
        if path == "/api/v1/me/wish-credit":
            if LOGGED_IN:
                return self._json({"eligible": True, "available": 10})
            return self._json({"eligible": False, "available": 0})
        if path == "/api/v1/me/notification-subscriptions":
            if not LOGGED_IN:
                return self._json({"error": "请先登录"}, status=401)
            return self._json({"subscriptions": SUBSCRIPTIONS})
        if path == "/api/v1/wishes":
            return self._json({"wishes": MOCK_WISHES})
        if path.startswith("/api/v1/payment/orders/"):
            if LOGGED_IN:
                return self._json({"order": {"orderNo": path.rsplit("/", 1)[-1], "kind": "wish", "status": "paid", "amountLdc": 10}, "membership": MOCK_MEMBERSHIP})
            return self._json({"error": "请先登录"}, status=401)
        if path == "/api/v1/admin/redeem-codes":
            return self._json({"codes": MOCK_REDEEM_CODES})
        if path == "/api/v1/admin/wishes":
            return self._json({"wishes": MOCK_WISHES})
        if path == "/api/v1/admin/orders":
            return self._json({"orders": MOCK_ORDERS})
        if path == "/api/v1/admin/settings":
            return self._json({"membershipMonthlyPriceLdc": 15, "wishDefaultTargetLdc": 30, "wishFreeCreditLdc": 10, "siteNotice": "", "siteNoticeUpdatedAt": ""})
        requested = path.lstrip("/")
        if requested.startswith("assets/"):
            requested = requested[len("assets/"):]
        requested = requested or "index.html"
        target = (PUBLIC / requested).resolve()
        if not str(target).startswith(str(PUBLIC)) or not target.is_file():
            self.send_error(404)
            return
        body = target.read_bytes()
        ctype = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
        self.send_response(200)
        self.send_header("Content-Type", f"{ctype}; charset=utf-8" if ctype.startswith("text/") else ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        path = self.path.split("?")[0]
        if path == "/api/v1/redeem":
            return self._json({"status": "ok", "membership": MOCK_MEMBERSHIP})
        if path in ("/api/v1/membership/recharge",) or path.endswith("/pledge"):
            return self._json({"orderNo": "LDmock" + str(int(time.time())), "payUrl": "https://credit.linux.do/paying?order_no=mock", "amountLdc": 30})
        if path == "/api/v1/wishes":
            return self._json({"wish": MOCK_WISHES[0]})
        if path == "/api/v1/me/notification-subscriptions":
            if not LOGGED_IN:
                return self._json({"error": "请先登录"}, status=401)
            global NEXT_SUB_ID
            payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
            platform = (payload.get("platform") or "").strip()
            target = (payload.get("target") or "").strip()
            site_id = payload.get("siteId")
            if not site_id or platform not in ("telegram", "feishu", "bark") or not target:
                return self._json({"message": "siteId, platform, and target are required"}, status=400)
            sub = {"id": NEXT_SUB_ID, "userId": 1, "siteId": site_id, "siteName": _site_name(site_id), "platform": platform, "target": target, "config": payload.get("config") or "{}", "enabled": True, "createdAt": NOW, "updatedAt": NOW}
            NEXT_SUB_ID += 1
            SUBSCRIPTIONS.append(sub)
            return self._json({"status": "ok", "subscription": sub})
        if path == "/api/v1/me/notification-test":
            if not LOGGED_IN:
                return self._json({"error": "请先登录"}, status=401)
            return self._json({"status": "ok"})
        if path == "/api/v1/feedback":
            return self._json({"status": "ok"})
        if path == "/api/v1/admin/session-import":
            return self._json({"imported": 2, "noMatch": 1, "results": [
                {"siteName": "星云中转", "siteUrl": "https://example.com", "status": "imported"},
                {"siteName": "紫电API", "siteUrl": "https://zi.example.org", "status": "imported"},
                {"siteName": "神秘站点", "siteUrl": "https://secret.example.org", "status": "no_match", "detail": "未在站点列表中找到该站点"},
            ]})
        if path.startswith("/api/v1/admin/"):
            return self._json({"status": "ok", "revoked": 1, "codes": ["RS-MOCK-0000-0000"]})
        return self._json({}, status=404)

    def do_PATCH(self):
        if self.path.split("?")[0].startswith("/api/v1/admin/"):
            return self._json({"status": "ok"})
        return self._json({}, status=404)

    def do_DELETE(self):
        path = self.path.split("?")[0]
        prefix = "/api/v1/me/notification-subscriptions/"
        if path.startswith(prefix):
            if not LOGGED_IN:
                return self._json({"error": "请先登录"}, status=401)
            sub_id = int(path[len(prefix):])
            global SUBSCRIPTIONS
            SUBSCRIPTIONS = [s for s in SUBSCRIPTIONS if s["id"] != sub_id]
            return self._json({"status": "ok"})
        if path.startswith("/api/v1/admin/"):
            return self._json({"status": "ok"})
        return self._json({}, status=404)

    def do_PUT(self):
        path = self.path.split("?")[0]
        if path == "/api/v1/me/preferences":
            return self._json({"status": "ok"})
        if path == "/api/v1/me/notification-subscriptions":
            if not LOGGED_IN:
                return self._json({"error": "请先登录"}, status=401)
            payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
            platform = (payload.get("platform") or "").strip()
            target = (payload.get("target") or "").strip()
            if platform not in ("telegram", "feishu", "bark") or not target:
                return self._json({"message": "platform and target are required"}, status=400)
            updated = 0
            for sub in SUBSCRIPTIONS:
                sub["platform"] = platform
                sub["target"] = target
                sub["updatedAt"] = NOW
                updated += 1
            return self._json({"status": "ok", "updated": updated})
        return self._json({}, status=404)

    def do_OPTIONS(self):
        self.send_response(204)
        self.end_headers()

if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"mock serving {PUBLIC} on http://127.0.0.1:{PORT}")
    server.serve_forever()