(function (root) {
  async function requestJSON(fetcher, url, options) {
    const response = await fetcher(url, options);
    if (!response.ok) {
      const body = await response.json?.().catch(() => ({}));
      const error = new Error(body?.message || "请求失败，请重试");
      error.status = response.status;
      throw error;
    }
    if (response.status === 204) return {};
    return response.json();
  }
  function createLatestRequest(fetcher) {
    let sequence = 0,
      controller;
    return {
      cancel() {
        sequence++;
        controller?.abort();
      },
      async run(url) {
        const id = ++sequence;
        controller?.abort();
        controller = new AbortController();
        try {
          const result = await requestJSON(fetcher, url, {
            cache: "no-store",
            signal: controller.signal,
          });
          return id === sequence ? result : undefined;
        } catch (error) {
          if (id === sequence) throw error;
        }
      },
    };
  }
  function createPoller({
    fetch: fetcher,
    apply,
    onMeta = async () => {},
    onSuccess = () => {},
    onError = () => {},
  }) {
    let revision = null,
      inFlight = null;
    async function refresh() {
      try {
        const meta = await requestJSON(fetcher, "/api/v1/meta", {
          cache: "no-store",
        });
        await onMeta(meta);
        if (revision === null || meta.revision !== revision) {
          const snapshot = await requestJSON(
            fetcher,
            "/api/v1/public/dashboard",
            { cache: "no-store" },
          );
          await apply(snapshot, meta);
          revision = snapshot.revision ?? meta.revision ?? null;
        }
        onSuccess();
      } catch (error) {
        onError(error);
      }
    }
    return {
      refresh() {
        if (!inFlight)
          inFlight = refresh().finally(() => {
            inFlight = null;
          });
        return inFlight;
      },
    };
  }
  function updateNotificationPanel({
    visible,
    open,
    compact,
    background,
    header,
    panel,
    backdrop,
    body,
  }) {
    const modal = visible && open && compact;
    backdrop.hidden = !modal;
    background.inert = modal;
    header.inert = modal;
    body.classList.toggle("nc-modal-open", modal);
    panel.setAttribute("role", modal ? "dialog" : "complementary");
    if (modal) panel.setAttribute("aria-modal", "true");
    else panel.removeAttribute("aria-modal");
    return modal;
  }
  const api = {
    requestJSON,
    createLatestRequest,
    createPoller,
    updateNotificationPanel,
  };
  if (typeof module !== "undefined") module.exports = api;
  else root.DashboardState = api;
})(globalThis);
