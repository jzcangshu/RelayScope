# RelayPulse Access Token Sync Extension

1. Open `chrome://extensions` and enable developer mode.
2. Choose **Load unpacked** and select this directory.
3. Reach RelayPulse through HTTPS or an SSH tunnel such as `http://127.0.0.1:8080`.
4. Copy `config.example.js` to `config.js` and set only the RelayPulse server origin; never put the administrator password in the extension.
5. In the administrator console choose **浏览器同步**, copy the ten-minute pairing code, paste it into the extension, and click **连接**.
6. Click **打开需浏览器登录的页面** to grant access only to the listed site origins, then select an All API Hub account backup. Only exact-origin matches from the server's pending list are prepared for upload.
7. For Sub2API sites, keep the matching logged-in site page open so the extension can read its refreshable token set. Choose **同步并逐站验证**.

NewAPI sites receive their access token and user ID from the selected backup. Sub2API sites receive the access token, refresh token, and expiry from that site's local storage. Accounts absent from the pending list are never uploaded. A missing credential skips only that site, so other exact matches are still synchronized. RelayPulse stores the values encrypted and performs a real collection check for each imported site.
