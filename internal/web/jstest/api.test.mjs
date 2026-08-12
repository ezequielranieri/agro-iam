import test from 'node:test';
import assert from 'node:assert/strict';
import { createApi, ApiError } from '../static/api.js';
import { REFRESH_KEY } from '../static/tokens.js';

// Fake Response shape — the only surface api.js reads from fetch.
function fakeRes(status, body, headers = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    headers: { get: (name) => headers[name.toLowerCase()] ?? null },
    json: async () => (typeof body === 'string' ? JSON.parse(body) : body),
  };
}

function memoryStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    dump: () => map,
  };
}

// fetchRecorder returns a fake fetchImpl and a record of every call it made.
function fetchRecorder(routes) {
  const calls = [];
  const fetchImpl = async (url, options = {}) => {
    calls.push({ url, options });
    const handler = routes[url];
    if (!handler) return fakeRes(404, { error: 'not found' });
    return handler(options, url);
  };
  return { calls, fetchImpl };
}

const okSession = { access_token: 'access-2', refresh_token: 'refresh-2', expires_in: 900 };
const refreshBody = JSON.stringify({ refresh_token: 'refresh-1' });

test('request attaches the Bearer access token from memory', async () => {
  const storage = memoryStorage();
  const { calls, fetchImpl } = fetchRecorder({
    '/api/v1/lots': () => fakeRes(200, { lots: [] }),
  });
  const api = createApi({ storage, fetchImpl });
  api.tokens.setAccess('access-1');
  await api.request('/api/v1/lots');
  assert.equal(calls[0].options.headers.Authorization, 'Bearer access-1');
});

test('401 with a stored access token triggers one refresh and retries once', async () => {
  const storage = memoryStorage();
  storage.setItem(REFRESH_KEY, 'refresh-1');
  let first = true;
  const { calls, fetchImpl } = fetchRecorder({
    '/api/v1/lots': (opts) => {
      if (first) { first = false; return fakeRes(401, { error: 'unauthorized' }); }
      assert.equal(opts.headers.Authorization, 'Bearer access-2', 'retry must carry the NEW access token');
      return fakeRes(200, { lots: [{ id: 'l-1' }] });
    },
    '/api/v1/auth/refresh': (opts) => {
      assert.equal(opts.body, refreshBody, 'refresh sends the stored refresh token');
      return fakeRes(200, okSession);
    },
  });
  const api = createApi({ storage, fetchImpl });
  api.tokens.setAccess('access-1');
  const body = await api.request('/api/v1/lots');
  assert.equal(body.lots[0].id, 'l-1');
  assert.equal(calls.filter((c) => c.url === '/api/v1/auth/refresh').length, 1);
});

test('concurrent 401s share one single-flight refresh (FR5)', async () => {
  const storage = memoryStorage();
  storage.setItem(REFRESH_KEY, 'refresh-1');
  // Each endpoint fails its FIRST call with a 401 (expired access token) and
  // succeeds on the retry — so both parallel requests hit the 401 path and
  // must share exactly one refresh flight.
  const failOnce = (okBody) => {
    let n = 0;
    return (opts) => {
      n += 1;
      if (n === 1) return fakeRes(401, { error: 'unauthorized' });
      assert.equal(opts.headers.Authorization, 'Bearer access-2', 'retry must carry the rotated access token');
      return fakeRes(200, okBody);
    };
  };
  const { calls, fetchImpl } = fetchRecorder({
    '/api/v1/lots': failOnce({ lots: [] }),
    '/api/v1/campaigns': failOnce({ campaigns: [] }),
    '/api/v1/auth/refresh': () => {
      return new Promise((resolve) => setTimeout(() => resolve(fakeRes(200, okSession)), 20));
    },
  });
  const api = createApi({ storage, fetchImpl });
  api.tokens.setAccess('access-1');
  await Promise.all([api.request('/api/v1/lots'), api.request('/api/v1/campaigns')]);
  const refreshes = calls.filter((c) => c.url === '/api/v1/auth/refresh');
  assert.equal(refreshes.length, 1, 'two parallel 401s must fire exactly ONE refresh');
  assert.equal(storage.dump().get(REFRESH_KEY), 'refresh-2', 'rotation must write back the NEW refresh token');
});

test('refresh 401 wipes both tokens, notifies and surfaces the error (FR5)', async () => {
  const storage = memoryStorage();
  storage.setItem(REFRESH_KEY, 'refresh-1');
  let lost = false;
  const { fetchImpl } = fetchRecorder({
    '/api/v1/lots': () => fakeRes(401, { error: 'unauthorized' }),
    '/api/v1/auth/refresh': () => fakeRes(401, { error: 'invalid or expired refresh token' }),
  });
  const api = createApi({ storage, fetchImpl, onSessionLost: () => { lost = true; } });
  api.tokens.setAccess('access-1');
  await assert.rejects(api.request('/api/v1/lots'), (err) => err instanceof ApiError && err.status === 401);
  assert.equal(api.tokens.getAccess(), null);
  assert.equal(api.tokens.getRefresh(), null);
  assert.equal(storage.dump().has(REFRESH_KEY), false, 'refresh token must be wiped on 401');
  assert.equal(lost, true, 'onSessionLost must fire so the app redirects to login');
});

test('429 surfaces Retry-After and a usable error', async () => {
  const { fetchImpl } = fetchRecorder({
    '/api/v1/auth/login': () => fakeRes(429, { error: 'rate limited' }, { 'retry-after': '42' }),
  });
  const api = createApi({ storage: memoryStorage(), fetchImpl });
  await assert.rejects(
    api.login({ tenantId: 't-1', email: 'a@b.c', password: 'x' }),
    (err) => err instanceof ApiError && err.status === 429 && err.retryAfter === 42 && err.message === 'rate limited',
  );
});

test('login stores the fresh token pair and writes the refresh back', async () => {
  const storage = memoryStorage();
  const { fetchImpl } = fetchRecorder({
    '/api/v1/auth/login': (opts) => {
      assert.equal(opts.body, JSON.stringify({ tenant_id: 't-1', email: 'a@b.c', password: 'x' }));
      return fakeRes(200, okSession);
    },
  });
  const api = createApi({ storage, fetchImpl });
  const body = await api.login({ tenantId: 't-1', email: 'a@b.c', password: 'x' });
  assert.equal(body.access_token, 'access-2');
  assert.equal(api.tokens.getAccess(), 'access-2', 'access token held in memory');
  assert.equal(storage.dump().get(REFRESH_KEY), 'refresh-2');
});

test('non-2xx bodies parse through the {error} contract', async () => {
  const { fetchImpl } = fetchRecorder({
    '/api/v1/audit': () => fakeRes(403, { error: 'forbidden' }),
  });
  const api = createApi({ storage: memoryStorage(), fetchImpl });
  api.tokens.setAccess('access-1');
  await assert.rejects(api.request('/api/v1/audit'), (err) => err instanceof ApiError && err.status === 403 && err.message === 'forbidden');
});

test('session() exposes decoded claims or null for unparsable tokens (FR4)', async () => {
  const storage = memoryStorage();
  const { fetchImpl } = fetchRecorder({});
  const api = createApi({ storage, fetchImpl });
  assert.equal(api.session(), null, 'no access token -> no session');
  api.tokens.setAccess('not-a-jwt');
  assert.equal(api.session(), null, 'unparsable access token -> no session (FR4 discard)');
  const enc = (obj) => btoa(JSON.stringify(obj)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  api.tokens.setAccess(`${enc({ alg: 'HS256' })}.${enc({ sub: 'u-1', tenant_id: 't-1', role: 'producer' })}.x`);
  assert.deepEqual(api.session(), { sub: 'u-1', tenantId: 't-1', role: 'producer' });
});

test('logout clears the access token and the stored refresh token', async () => {
  const storage = memoryStorage();
  const { fetchImpl } = fetchRecorder({});
  const api = createApi({ storage, fetchImpl });
  api.tokens.setAccess('access-1');
  api.tokens.setRefresh('refresh-1');
  api.logout();
  assert.equal(api.tokens.getAccess(), null);
  assert.equal(storage.dump().has(REFRESH_KEY), false);
});