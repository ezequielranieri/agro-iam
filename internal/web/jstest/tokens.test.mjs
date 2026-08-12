import test from 'node:test';
import assert from 'node:assert/strict';
import {
  REFRESH_KEY,
  decodePayload,
  roleFromToken,
  createTokenStore,
  createSingleFlight,
} from '../static/tokens.js';

// Fake storage adapter — same shape as localStorage, without a browser.
function memoryStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    dump: () => map,
  };
}

// header.payload.signature — a well-formed JWT with the given claims payload.
function jwt(payload) {
  const enc = (obj) => btoa(JSON.stringify(obj)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `${enc({ alg: 'HS256', typ: 'JWT' })}.${enc(payload)}.${enc({ sig: 'x' })}`;
}

test('decodePayload returns claims for a valid JWT (FR4)', () => {
  const claims = decodePayload(jwt({ sub: 'u-1', tenant_id: 't-1', role: 'admin', exp: 9999999999 }));
  assert.equal(claims.sub, 'u-1');
  assert.equal(claims.tenant_id, 't-1');
  assert.equal(claims.role, 'admin');
});

test('decodePayload rejects malformed tokens (FR4 unparsable claim)', () => {
  assert.equal(decodePayload(''), null);
  assert.equal(decodePayload('only-two-parts'), null);
  assert.equal(decodePayload('a.b.c'), null); // payload is not JSON
  assert.equal(decodePayload('a.!!not-base64!!.c'), null);
  assert.equal(decodePayload(null), null);
});

test('roleFromToken reads the role claim and only the role claim', () => {
  assert.equal(roleFromToken(jwt({ sub: 'u', tenant_id: 't', role: 'agronomist' })), 'agronomist');
  assert.equal(roleFromToken(jwt({ sub: 'u', tenant_id: 't' })), null); // missing role
  assert.equal(roleFromToken(jwt({ sub: 'u', tenant_id: 't', role: 42 })), null); // non-string role
  assert.equal(roleFromToken('garbage'), null);
});

test('token store keeps the access token in memory only (FR5)', () => {
  const storage = memoryStorage();
  const tokens = createTokenStore(storage);
  tokens.setAccess('access-1');
  assert.equal(tokens.getAccess(), 'access-1');
  assert.equal(storage.dump().has('access-1'), false, 'access token must never reach storage');
  assert.equal(storage.dump().size, 0, 'access token must never touch storage at all');
});

test('token store persists the refresh token under the fixed key (FR5)', () => {
  const storage = memoryStorage();
  const tokens = createTokenStore(storage);
  tokens.setRefresh('refresh-1');
  assert.equal(storage.dump().get(REFRESH_KEY), 'refresh-1');
  assert.equal(tokens.getRefresh(), 'refresh-1');
});

test('rotation write-back overwrites the stored refresh token (FR5)', () => {
  const storage = memoryStorage();
  const tokens = createTokenStore(storage);
  tokens.setRefresh('refresh-old');
  tokens.setRefresh('refresh-new'); // every rotation replaces the stored value
  assert.equal(storage.dump().get(REFRESH_KEY), 'refresh-new');
});

test('clear wipes both tokens and the storage entry', () => {
  const storage = memoryStorage();
  const tokens = createTokenStore(storage);
  tokens.setAccess('access');
  tokens.setRefresh('refresh');
  tokens.clear();
  assert.equal(tokens.getAccess(), null);
  assert.equal(tokens.getRefresh(), null);
  assert.equal(storage.dump().has(REFRESH_KEY), false);
});

test('single-flight: concurrent callers share one in-flight promise (FR5)', async () => {
  let calls = 0;
  const flight = createSingleFlight(async () => {
    calls += 1;
    return 'fresh-access';
  });
  const results = await Promise.all([flight(), flight(), flight()]);
  assert.equal(calls, 1, 'exactly one refresh request must fire');
  assert.deepEqual(results, ['fresh-access', 'fresh-access', 'fresh-access']);
});

test('single-flight: a new flight starts after the previous settles', async () => {
  let calls = 0;
  const flight = createSingleFlight(async () => {
    calls += 1;
    return 'ok';
  });
  await flight();
  await flight();
  assert.equal(calls, 2, 'each settled flight must allow the next call to fire');
});

test('single-flight: rejection propagates to every waiter and frees the slot', async () => {
  let calls = 0;
  const flight = createSingleFlight(async () => {
    calls += 1;
    throw new Error('family revoked');
  });
  await assert.rejects(Promise.all([flight(), flight()]), /family revoked/);
  assert.equal(calls, 1);
  await assert.rejects(flight(), /family revoked/); // slot freed: new flight
  assert.equal(calls, 2);
});