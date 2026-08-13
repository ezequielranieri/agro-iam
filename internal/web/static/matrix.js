// matrix.js — the RBAC write matrix (FS2) and write-form validation (R6, FA1).
// Pure ESM with no DOM or storage, exercised by node --test through the Go
// test binary (D6). canWrite drives which write controls a view renders:
// campaigns writes {admin, agronomist}, applications writes {admin, agronomist,
// producer} — mirroring the route guards in router.go. The server stays the
// authority (defense in depth): a stale-token 403 must still surface uniformly
// through the shared banner, and the form must stay open (FR6).

export const WRITE_ROLES = {
  campaigns: ['admin', 'agronomist'],
  applications: ['admin', 'agronomist', 'producer'],
};

// ACCEPTED_ROLES mirrors the app.roles catalog (domain/role.go) so the users
// create form (FA1) can offer a safe role picker instead of a free-text field.
export const ACCEPTED_ROLES = ['admin', 'agronomist', 'producer', 'auditor', 'hauler'];

// canWrite answers "may this role open write controls for this resource?" —
// the client-side half of FS2. Unknown resources are always false: a future
// screen must opt into the matrix explicitly.
export function canWrite(role, resource) {
  return (WRITE_ROLES[resource] || []).includes(role);
}

// requiredField: a value counts as present only when it is a non-blank string,
// so a whitespace-only product name is still a missing field.
export function requiredField(value) {
  return typeof value === 'string' && value.trim() !== '';
}

// validateApplication checks the R6 required fields — lot_id, campaign_id and
// product_name — and returns {valid, missing}. The missing list drives inline
// hints on the form so the client never ships a payload the service would
// reject with 400; domain.Application.IsValid still enforces it server-side.
export function validateApplication(input = {}) {
  const value = input || {};
  const missing = [];
  if (!requiredField(value.lot_id)) missing.push('lot');
  if (!requiredField(value.campaign_id)) missing.push('campaign');
  if (!requiredField(value.product_name)) missing.push('product');
  return { valid: missing.length === 0, missing };
}

// validateUser checks the FA1 create form: email, password, full name and a
// known role are all required.
export function validateUser(input = {}) {
  const value = input || {};
  const missing = [];
  if (!requiredField(value.email)) missing.push('email');
  if (!requiredField(value.password)) missing.push('password');
  if (!requiredField(value.full_name)) missing.push('name');
  if (!ACCEPTED_ROLES.includes(value.role)) missing.push('role');
  return { valid: missing.length === 0, missing };
}
