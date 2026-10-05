const test = require("node:test");
const assert = require("node:assert/strict");
const { createPoller, createLatestRequest } = require("./dashboard-state.js");
const { createSubscriptions } = require("./subscriptions.js");
const { createPreferencesSync } = require("./preferences-sync.js");
const response = (value) => ({ ok: true, json: async () => value });
test("same revision retries a failed snapshot and retains the last successful data", async () => {
  let calls = 0,
    fail = true,
    applied = [];
  const poll = createPoller({
    fetch: async (url) =>
      url.endsWith("/meta")
        ? response({ revision: 7 })
        : (calls++,
          fail ? { ok: false } : response({ revision: 7, rows: ["new"] })),
    apply: async (data) => applied.push(data.rows),
    onError: () => {},
  });
  await poll.refresh();
  fail = false;
  await poll.refresh();
  await poll.refresh();
  assert.equal(calls, 2);
  assert.deepEqual(applied, [["new"]]);
});
test("latest request discards an older response and close invalidates pending work", async () => {
  const pending = [];
  const latest = createLatestRequest(
    async () => new Promise((resolve) => pending.push(resolve)),
  );
  const a = latest.run("a"),
    b = latest.run("b");
  pending[1](response("b"));
  assert.equal(await b, "b");
  pending[0](response("a"));
  assert.equal(await a, undefined);
  const c = latest.run("c");
  latest.cancel();
  pending[2](response("c"));
  assert.equal(await c, undefined);
});
test("unsubscribe rejects failed HTTP responses", async () => {
  const subs = createSubscriptions(async () => ({
    ok: false,
    status: 500,
    json: async () => ({ message: "未取消" }),
  }));
  await assert.rejects(subs.remove(4), /未取消/);
});
test("preferences saves serialize so slow old updates cannot overwrite newer ones", async () => {
  const writes = [],
    pending = [];
  const sync = createPreferencesSync(async (url, init) => {
    writes.push(JSON.parse(init.body));
    return new Promise((resolve) => pending.push(resolve));
  });
  const a = sync.save({ n: 1 }),
    b = sync.save({ n: 2 });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(writes, [{ n: 1 }]);
  pending.shift()(response({}));
  await a;
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(writes, [{ n: 1 }, { n: 2 }]);
  pending.shift()(response({}));
  await b;
});

test("snapshot application failure cannot commit a revision", async () => {
  let attempts = 0,
    snapshots = 0;
  const poll = createPoller({
    fetch: async (url) =>
      url.endsWith("/meta")
        ? response({ revision: 3 })
        : (snapshots++, response({ revision: 3 })),
    apply: async () => {
      if (++attempts === 1) throw new Error("render");
    },
  });
  await poll.refresh();
  await poll.refresh();
  assert.equal(snapshots, 2);
});
test("overlapping polls share one in-flight request", async () => {
  let finish,
    metaCalls = 0;
  const poll = createPoller({
    fetch: async (url) => {
      if (url.endsWith("/meta")) {
        metaCalls++;
        return new Promise((resolve) => (finish = resolve));
      }
      return response({ revision: 1 });
    },
    apply: async () => {},
  });
  const a = poll.refresh(),
    b = poll.refresh();
  assert.equal(a, b);
  finish(response({ revision: 1 }));
  await a;
  assert.equal(metaCalls, 1);
});
test("latest detail request exposes network errors instead of leaving loading forever", async () => {
  const request = createLatestRequest(async () => {
    throw new Error("offline");
  });
  await assert.rejects(request.run("/details"), /offline/);
});
test("subscriptions preserve server failure and channel updates use the collection endpoint", async () => {
  let call;
  const api = createSubscriptions(async (url, init) => {
    call = { url, init };
    return response({ updated: 2 });
  });
  assert.deepEqual(
    await api.updateChannel({ platform: "bark", target: "mock" }),
    { updated: 2 },
  );
  assert.equal(call.url, "/api/v1/me/notification-subscriptions");
  assert.equal(call.init.method, "PUT");
});
test("cancelled preferences queue cannot upload another account’s pending settings", async () => {
  let finish,
    calls = 0;
  const sync = createPreferencesSync(async () => {
    calls++;
    return new Promise((resolve) => (finish = resolve));
  });
  const first = sync.save({ n: 1 });
  const firstRejected = assert.rejects(first, { name: "AbortError" });
  const queued = sync.save({ n: 2 });
  const rejected = assert.rejects(queued, { name: "AbortError" });
  await new Promise((resolve) => setImmediate(resolve));
  sync.cancel();
  finish(response({}));
  await firstRejected;
  await rejected;
  assert.equal(calls, 1);
});

for (const method of ["load", "save"]) {
  for (const status of [200, 401, 403]) {
    test(`cancelled preference ${method} ignores late ${status} response`, async () => {
      let finish;
      const sync = createPreferencesSync(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      );
      const pending =
        method === "load" ? sync.load() : sync.save({ account: "old" });
      const rejected = assert.rejects(pending, { name: "AbortError" });
      await new Promise((resolve) => setImmediate(resolve));
      sync.cancel();
      finish({
        ok: status === 200,
        status,
        json: async () => ({ account: "old", message: "expired" }),
      });
      await rejected;
    });
  }
}

test("notification panel releases page interaction when route hides it before compact resize", () => {
  const { updateNotificationPanel } = require("./dashboard-state.js");
  const attributes = new Map();
  const state = {
    visible: true,
    open: true,
    compact: false,
    background: { inert: false },
    header: { inert: false },
    backdrop: { hidden: true },
    panel: {
      setAttribute: (key, value) => attributes.set(key, value),
      removeAttribute: (key) => attributes.delete(key),
    },
    body: { classList: { toggle: (key, value) => attributes.set(key, value) } },
  };
  assert.equal(updateNotificationPanel(state), false);
  state.visible = false;
  state.compact = true;
  assert.equal(updateNotificationPanel(state), false);
  assert.equal(state.header.inert, false);
  assert.equal(state.background.inert, false);
  assert.equal(state.backdrop.hidden, true);
  assert.equal(attributes.get("nc-modal-open"), false);
  assert.equal(attributes.has("aria-modal"), false);
  state.visible = true;
  assert.equal(updateNotificationPanel(state), true);
  state.open = false;
  assert.equal(updateNotificationPanel(state), false);
  assert.equal(state.header.inert, false);
});
