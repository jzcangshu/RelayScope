"""本地前端验证用的模拟服务：静态托管 web/public，并模拟看板 API。

用法: python mock_server.py [端口]
"""
import json
import mimetypes
import sys
import time
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PUBLIC = Path(__file__).resolve().parent.parent / "web" / "public"
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8099

NOW = datetime.now(timezone.utc).isoformat()
HOURS_AGO = lambda h: datetime.now(timezone.utc).timestamp() * 1000 - h * 3600 * 1000

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

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

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
            return self._json({"revision": "mock-1", "serverTime": NOW})
        if path == "/api/v1/public/dashboard":
            return self._json({"revision": "mock-1", "rows": ROWS, "buckets": BUCKETS, "hours": 24})
        if path == "/api/v1/public/announcements":
            return self._json({"announcements": []})
        if path == "/api/v1/public/details":
            return self._json({"buckets": BUCKETS, "groups": [g for g in ROWS if g["siteName"] == "星云中转" or g["groupName"] == "官方"]})
        if path == "/api/v1/auth/me":
            return self._json({"authenticated": False}, status=404)
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
        self._json({}, status=404)

    def do_OPTIONS(self):
        self.send_response(204)
        self.end_headers()

if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"mock serving {PUBLIC} on http://127.0.0.1:{PORT}")
    server.serve_forever()