(function (root) {
  const request =
    typeof module !== "undefined"
      ? require("./dashboard-state.js").requestJSON
      : root.DashboardState.requestJSON;
  function createSubscriptions(fetcher) {
    const base = "/api/v1/me/notification-subscriptions";
    return {
      async list() {
        return (await request(fetcher, base)).subscriptions || [];
      },
      add(value) {
        return request(fetcher, base, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(value),
        });
      },
      remove(id) {
        return request(fetcher, `${base}/${encodeURIComponent(id)}`, {
          method: "DELETE",
        });
      },
      updateChannel(value) {
        return request(fetcher, base, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(value),
        });
      },
    };
  }
  const api = { createSubscriptions };
  if (typeof module !== "undefined") module.exports = api;
  else root.Subscriptions = api;
})(globalThis);
