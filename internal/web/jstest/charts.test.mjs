import test from 'node:test';
import assert from 'node:assert/strict';
import {
  aggregateKpis,
  groupApplicationsByMonth,
  groupApplicationsByProduct,
  groupApplicationsByLot,
} from '../static/charts.js';

// Fixtures mirror the seed (cmd/seed/main.go): applications carry
// {product_name, applied_at (RFC3339), operator_id, campaign_id}, campaigns
// carry {id, season}. The aggregation layer must compute every dashboard
// widget from these client-side aggregates (FD1).

function app({ product, appliedAt, operator = null, campaign = 'c-1', lot = 'lot-north' }) {
  return {
    id: `app-${Math.random()}`,
    lot_id: lot,
    campaign_id: campaign,
    product_name: product,
    dose: '3 L/ha',
    applied_at: appliedAt,
    operator_id: operator,
    operator_name: operator ? `Op ${operator}` : '',
    notes: '',
    created_at: appliedAt,
  };
}

const CAMPAIGNS = [
  { id: 'c-1', name: 'Campaña 2025/2026', season: '2025/2026' },
  { id: 'c-2', name: 'Campaña 2026/2027', season: '2026/2027' },
];

// ---- aggregateKpis (FD1: KPI cards + zero states) ------------------------

test('aggregateKpis returns zero-state KPIs for no data (FD1 empty tenant)', () => {
  const kpis = aggregateKpis([], []);
  assert.equal(kpis.totalApplications, 0);
  assert.equal(kpis.activeOperators, 0);
  assert.equal(kpis.productsUsed, 0);
  assert.deepEqual(kpis.seasonTotals, []);
});

test('aggregateKpis counts applications, distinct operators/products and season totals (FD1 charts render)', () => {
  const apps = [
    app({ product: 'Glifosato 48%', appliedAt: '2025-10-08T09:30:00Z', operator: 'agronomo', campaign: 'c-1' }),
    app({ product: 'Tebuconazol', appliedAt: '2025-12-12T11:00:00Z', operator: null, campaign: 'c-1' }),
    app({ product: 'Glifosato 48%', appliedAt: '2026-02-03T16:45:00Z', operator: 'productor', campaign: 'c-1' }),
    app({ product: 'Urea 46%', appliedAt: '2027-01-14T10:30:00Z', operator: 'agronomo', campaign: 'c-2' }),
  ];
  const kpis = aggregateKpis(apps, CAMPAIGNS);
  assert.equal(kpis.totalApplications, 4);
  assert.equal(kpis.activeOperators, 2); // agronomo, productor — the null operator is not one
  assert.equal(kpis.productsUsed, 3); // Glifosato 48% twice still counts once
  assert.deepEqual(kpis.seasonTotals, [
    { season: '2025/2026', count: 3, fraction: 0.75 },
    { season: '2026/2027', count: 1, fraction: 0.25 },
  ]);
});

test('aggregateKpis buckets applications whose campaign is missing under Unknown', () => {
  const apps = [app({ product: 'Atrazina', appliedAt: '2026-11-05T09:00:00Z', campaign: 'gone' })];
  const kpis = aggregateKpis(apps, CAMPAIGNS);
  assert.deepEqual(kpis.seasonTotals, [{ season: 'Unknown', count: 1, fraction: 1 }]);
});

// ---- groupApplicationsByMonth (FD1: line chart series) -------------------

test('groupApplicationsByMonth returns an empty series for no data', () => {
  assert.deepEqual(groupApplicationsByMonth([]), []);
});

test('groupApplicationsByMonth groups by month ascending and labels with the month name', () => {
  const apps = [
    app({ product: 'A', appliedAt: '2026-01-25T10:00:00Z' }),
    app({ product: 'B', appliedAt: '2025-10-08T09:30:00Z' }),
    app({ product: 'C', appliedAt: '2026-01-08T11:00:00Z' }),
    app({ product: 'D', appliedAt: '2026-03-18T07:30:00Z' }),
  ];
  assert.deepEqual(groupApplicationsByMonth(apps), [
    { label: 'Oct', count: 1 },
    { label: 'Jan', count: 2 },
    { label: 'Mar', count: 1 },
  ]);
});

test('groupApplicationsByMonth skips applications with an unparsable applied_at', () => {
  const apps = [
    app({ product: 'A', appliedAt: '2026-01-08T11:00:00Z' }),
    app({ product: 'B', appliedAt: 'not-a-date' }),
    app({ product: 'C', appliedAt: null }),
  ];
  assert.deepEqual(groupApplicationsByMonth(apps), [{ label: 'Jan', count: 1 }]);
});

// ---- groupApplicationsByProduct (FD1: donut shares) ----------------------

test('groupApplicationsByProduct returns an empty share for no data', () => {
  assert.deepEqual(groupApplicationsByProduct([]), []);
});

test('groupApplicationsByProduct counts per product, descending, ties alphabetical', () => {
  const apps = [
    app({ product: 'Urea 46%' }),
    app({ product: 'Atrazina' }),
    app({ product: 'Glifosato 48%' }),
    app({ product: 'Urea 46%' }),
    app({ product: 'Glifosato 48%' }),
  ];
  assert.deepEqual(groupApplicationsByProduct(apps), [
    { label: 'Glifosato 48%', count: 2 },
    { label: 'Urea 46%', count: 2 },
    { label: 'Atrazina', count: 1 },
  ]);
});

test('groupApplicationsByProduct skips empty product names', () => {
  assert.deepEqual(groupApplicationsByProduct([app({ product: '' })]), []);
});

// ---- groupApplicationsByLot (FD1: map breakdown) -------------------------

test('groupApplicationsByLot returns an empty breakdown for no data', () => {
  assert.deepEqual(groupApplicationsByLot([]), []);
});

test('groupApplicationsByLot counts applications per lot, descending', () => {
  const apps = [
    app({ product: 'A', lot: 'lot-north' }),
    app({ product: 'B', lot: 'lot-south' }),
    app({ product: 'C', lot: 'lot-north' }),
  ];
  assert.deepEqual(groupApplicationsByLot(apps), [
    { lotId: 'lot-north', count: 2 },
    { lotId: 'lot-south', count: 1 },
  ]);
});
