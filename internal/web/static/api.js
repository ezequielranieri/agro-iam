// api.js — the single fetch wrapper (FR3/FR5, D5). It injects the in-memory
// Bearer access token, transparently refreshes on a 401 through the
// single-flight gate from tokens.js, surfaces 429 Retry-After for backoff and
// parses the uniform {"error": ...} contract into ApiError. fetchImpl and
// storage are injected so the interceptor logic runs under node --test with
// fakes — no browser globals at module top level.
import { createTokenStore, createSingleFlight, decodePayload } from './tokens.js';

const LOGIN_ENDPOINT = '/api/v1/auth/login';
const REFRESH_ENDPOINT = '/api/v1/auth/refresh';

// ApiError carries the HTTP status, the {"error"} message and — for 429 — the
// Retry-After seconds so the login form can disable itself until backoff ends
// (FR3).
export class ApiError extends Error {
  constructor(status, message, retryAfter = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

async function parseError(res) {
  let message = '';
  try {
    const body = await res.json();
    if (body && typeof body.error === 'string') message = body.error;
  } catch {
    // Non-JSON error body: fall back to a generic message.
  }
  const retryAfter = res.status === 429 ? Number(res.headers.get('Retry-After')) || null : null;
  return new ApiError(res.status, message || `request failed (${res.status})`, retryAfter);
}

// createApi wires the client. storage: {getItem,setItem,removeItem} adapter
// (window.localStorage in the app). onSessionLost: called when a refresh 401
// destroys the session so the app can redirect to login (FR5).
export function createApi({ storage, fetchImpl, onSessionLost = () => {} }) {
  const tokens = createTokenStore(storage);

  // Single-flight refresh: concurrent 401s share ONE in-flight request (FR5).
  // A refresh 401 (replay / revoked family) wipes both tokens and signals the
  // app to drop to the login screen — no further API calls fire.
  const refresh = createSingleFlight(async () => {
    const refreshToken = tokens.getRefresh();
    if (!refreshToken) throw new ApiError(401, 'no refresh token');
    const res = await fetchImpl(REFRESH_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
    if (res.status === 401) {
      tokens.clear();
      onSessionLost();
      throw await parseError(res);
    }
    if (!res.ok) throw await parseError(res);
    const body = await res.json();
    tokens.setAccess(body.access_token);
    tokens.setRefresh(body.refresh_token); // rotation: write the NEW refresh back
    return tokens.getAccess();
  });

  async function request(path, { method = 'GET', body } = {}) {
    const headers = {};
    const access = tokens.getAccess();
    if (access) headers.Authorization = `Bearer ${access}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const options = { method, headers, body: body === undefined ? undefined : JSON.stringify(body) };

    let res = await fetchImpl(path, options);
    if (res.status === 401 && access) {
      // Access token expired or invalid: one refresh, then retry ONCE. If the
      // refresh itself fails, the refresh 401 path already wiped the session.
      try {
        await refresh();
      } catch {
        throw await parseError(res);
      }
      headers.Authorization = `Bearer ${tokens.getAccess()}`;
      res = await fetchImpl(path, { ...options, headers });
    }
    if (res.status === 429) throw await parseError(res); // Retry-After surfaces
    if (!res.ok) throw await parseError(res);
    if (res.status === 204) return null;
    return res.json();
  }

  // login submits the realm-scoped credentials (FR3: tenant_id required —
  // RLS means the user can only be looked up inside its own tenant).
  async function login({ tenantId, email, password }) {
    const res = await fetchImpl(LOGIN_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tenant_id: tenantId, email, password }),
    });
    if (res.status === 429) throw await parseError(res);
    if (!res.ok) throw await parseError(res);
    const body = await res.json();
    tokens.setAccess(body.access_token);
    tokens.setRefresh(body.refresh_token);
    return body;
  }

  // logout is client-side destruction only (D5: no server endpoint).
  function logout() {
    tokens.clear();
  }

  // session decodes the in-memory access token claims (FR4). Null when there
  // is no token or the payload does not parse — the app must discard the
  // session and render login in that case.
  function session() {
    const access = tokens.getAccess();
    if (!access) return null;
    const payload = decodePayload(access);
    if (!payload || typeof payload.sub !== 'string' || typeof payload.tenant_id !== 'string') {
      return null;
    }
    return { sub: payload.sub, tenantId: payload.tenant_id, role: payload.role || '' };
  }

  return { request, login, logout, session, refresh, tokens };
}