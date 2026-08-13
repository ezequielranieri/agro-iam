import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ACCEPTED_ROLES,
  canWrite,
  requiredField,
  validateApplication,
  validateUser,
  WRITE_ROLES,
} from '../static/matrix.js';

// The RBAC write matrix (FS2) must mirror the route guards in router.go —
// campaigns writes {admin, agronomist}, applications writes {admin,
// agronomist, producer} — so the UI renders write controls only for roles the
// server will actually accept (defense in depth: a stale-token 403 still
// surfaces uniformly through the shared banner, FR6).

// ---- WRITE_ROLES / canWrite (FS2) ----------------------------------------

test('write matrix matches the route guards (FS2)', () => {
  assert.deepEqual(WRITE_ROLES.campaigns, ['admin', 'agronomist']);
  assert.deepEqual(WRITE_ROLES.applications, ['admin', 'agronomist', 'producer']);
});

test('canWrite allows only matrix roles per resource (FS2 agronomist writes both)', () => {
  assert.equal(canWrite('agronomist', 'campaigns'), true);
  assert.equal(canWrite('agronomist', 'applications'), true);
});

test('producer is scoped: applications only, never campaigns (FS2 producer scoped)', () => {
  assert.equal(canWrite('producer', 'applications'), true);
  assert.equal(canWrite('producer', 'campaigns'), false);
});

test('admin writes every write-matrix resource (FS2)', () => {
  assert.equal(canWrite('admin', 'campaigns'), true);
  assert.equal(canWrite('admin', 'applications'), true);
});

test('non-write roles never render write controls (FS2)', () => {
  for (const role of ['auditor', 'hauler']) {
    assert.equal(canWrite(role, 'campaigns'), false);
    assert.equal(canWrite(role, 'applications'), false);
  }
  assert.equal(canWrite('hauler', 'lots'), false); // unknown resource: never
  assert.equal(canWrite(undefined, 'applications'), false);
});

// ---- requiredField (R6) --------------------------------------------------

test('requiredField accepts non-blank strings and rejects blank/absent values', () => {
  assert.equal(requiredField('Urea 46%'), true);
  assert.equal(requiredField('   '), false);
  assert.equal(requiredField(''), false);
  assert.equal(requiredField(null), false);
  assert.equal(requiredField(undefined), false);
  assert.equal(requiredField(42), false);
});

// ---- validateApplication (R6) ---------------------------------------------

test('validateApplication passes a complete payload (R6)', () => {
  const result = validateApplication({
    lot_id: 'l-1',
    campaign_id: 'c-1',
    product_name: 'Urea 46%',
    dose: '150 kg/ha',
  });
  assert.equal(result.valid, true);
  assert.deepEqual(result.missing, []);
});

test('validateApplication flags every missing R6 field (R6 invalid payload)', () => {
  const result = validateApplication({});
  assert.equal(result.valid, false);
  assert.deepEqual(result.missing, ['lot', 'campaign', 'product']);
});

test('validateApplication treats a whitespace-only product name as missing', () => {
  const result = validateApplication({ lot_id: 'l-1', campaign_id: 'c-1', product_name: '   ' });
  assert.equal(result.valid, false);
  assert.deepEqual(result.missing, ['product']);
});

test('validateApplication tolerates null/undefined input', () => {
  for (const input of [null, undefined]) {
    const result = validateApplication(input);
    assert.equal(result.valid, false);
    assert.deepEqual(result.missing, ['lot', 'campaign', 'product']);
  }
});

// ---- validateUser (FA1) ----------------------------------------------------

test('validateUser passes a complete create-user payload (FA1)', () => {
  const result = validateUser({
    email: 'nueva@esperanza.coop',
    password: 'test123',
    full_name: 'Nueva Operaria',
    role: 'producer',
  });
  assert.equal(result.valid, true);
  assert.deepEqual(result.missing, []);
});

test('validateUser rejects missing fields and unknown roles (FA1)', () => {
  const empty = validateUser({});
  assert.equal(empty.valid, false);
  assert.deepEqual(empty.missing, ['email', 'password', 'name', 'role']);

  const badRole = validateUser({ email: 'x@y.coop', password: 'p', full_name: 'X', role: 'superuser' });
  assert.equal(badRole.valid, false);
  assert.deepEqual(badRole.missing, ['role']);
});

test('ACCEPTED_ROLES mirrors the app.roles catalog (FA1 role picker)', () => {
  assert.deepEqual(ACCEPTED_ROLES, ['admin', 'agronomist', 'producer', 'auditor', 'hauler']);
});
