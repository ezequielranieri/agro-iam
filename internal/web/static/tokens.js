// tokens.js — the token lifecycle (FR5, D5) as pure logic, so node --test can
// cover it and the browser can import it. Storage is injected through the
// createTokenStore adapter: this module NEVER touches browser storage or the
// DOM, which is what makes the "access token lives in memory only" guarantee
// checkable (web_test.go asserts this file stays pure).
//
// Contract:
//   - access token: closure memory only, sent as Bearer, never persisted (FR5)
//   - refresh token: persisted under REFRESH_KEY, overwritten on EVERY rotation
//   - refresh: single-flight — concurrent 401s share one in-flight request
//   - role claim: decoded client-side from the JWT payload (FR4)

// REFRESH_KEY is the only storage key the app writes.
export const REFRESH_KEY = 'agro.refresh_token';

// decodePayload parses the payload segment of a JWT (base64url -> JSON).
// Returns null for any malformed input so callers can discard the session
// (FR4: an unparsable claim must not be trusted).
export function decodePayload(token) {
  if (typeof token !== 'string' || token === '') return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  try {
    const base64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
    const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);
    return JSON.parse(atob(padded));
  } catch {
    return null;
  }
}

// roleFromToken returns the role claim as a string, or null when the token or
// the claim is missing/unparsable (FR4). The nav and route guards trust only
// this value — never a /me endpoint.
export function roleFromToken(token) {
  const payload = decodePayload(token);
  if (!payload || typeof payload.role !== 'string' || payload.role === '') return null;
  return payload.role;
}

// createTokenStore builds the token holder over an injected storage adapter
// (the {getItem, setItem, removeItem} trio of a browser storage area). The
// access token lives in the closure; the refresh token goes to storage.
export function createTokenStore(storage) {
  let accessToken = null;
  return {
    getAccess() {
      return accessToken;
    },
    setAccess(token) {
      accessToken = token || null;
    },
    getRefresh() {
      try {
        return storage.getItem(REFRESH_KEY);
      } catch {
        return null;
      }
    },
    // Rotation write-back: every new refresh token REPLACES the stored one
    // (FR5 "overwritten with the rotated value on EVERY refresh"). A null
    // value removes the entry (logout / wipe).
    setRefresh(token) {
      try {
        if (token) storage.setItem(REFRESH_KEY, token);
        else storage.removeItem(REFRESH_KEY);
      } catch {
        // Storage unavailable: the session cannot be restored later, but the
        // in-memory access token still works for this page load.
      }
    },
    clear() {
      accessToken = null;
      try {
        storage.removeItem(REFRESH_KEY);
      } catch {
        // Ignore: nothing else to clean.
      }
    },
  };
}

// createSingleFlight wraps an async fn so concurrent callers await the SAME
// in-flight promise; once it settles the next call starts a fresh flight.
// This is the "one shared refresh request" guarantee (FR5, D5). Rejections
// propagate to every waiter and free the slot.
export function createSingleFlight(fn) {
  let inFlight = null;
  return (...args) => {
    if (!inFlight) {
      inFlight = Promise.resolve()
        .then(() => fn(...args))
        .finally(() => {
          inFlight = null;
        });
    }
    return inFlight;
  };
}