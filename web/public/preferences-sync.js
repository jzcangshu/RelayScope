(function (root) {
  const request =
    typeof module !== "undefined"
      ? require("./dashboard-state.js").requestJSON
      : root.DashboardState.requestJSON;

  function createPreferencesSync(fetcher) {
    let pending = Promise.resolve();
    let timer;
    let generation = 0;

    function ensureCurrent(owner) {
      if (owner !== generation) {
        throw new DOMException("同步已取消", "AbortError");
      }
    }

    async function run(owner, options) {
      ensureCurrent(owner);
      try {
        const value = await request(fetcher, "/api/v1/me/preferences", options);
        ensureCurrent(owner);
        return value;
      } catch (error) {
        // An old HTTP error must not expire the newly signed-in account.
        ensureCurrent(owner);
        throw error;
      }
    }

    return {
      load() {
        return run(generation, { cache: "no-store" });
      },
      save(value) {
        const owner = generation;
        const body = JSON.stringify(value);
        const next = pending
          .catch(() => {})
          .then(() =>
            run(owner, {
              method: "PUT",
              headers: { "Content-Type": "application/json" },
              body,
            }),
          );
        pending = next;
        return next;
      },
      schedule(callback, delay = 800) {
        clearTimeout(timer);
        timer = setTimeout(callback, delay);
      },
      cancel() {
        generation++;
        clearTimeout(timer);
      },
    };
  }

  const api = { createPreferencesSync };
  if (typeof module !== "undefined") module.exports = api;
  else root.PreferencesSync = api;
})(globalThis);
