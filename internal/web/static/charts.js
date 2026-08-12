// charts.js — client-side dashboard math (FD1, D1 hand-rolled SVG). Every
// widget aggregates the applications/campaigns/lots data fetched from the list
// endpoints. This module holds the aggregation layer: KPI cards
// (aggregateKpis) and the series fed to the line, donut and map widgets
// (groupApplicationsByMonth/Product/Lot). The SVG geometry that turns the
// series into paths lives alongside in later slices. All functions are pure
// and deterministic so they run under node --test; empty datasets resolve to
// well-defined zero states instead of errors (FD1 empty tenant).

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const UNKNOWN_SEASON = 'Unknown';

// ---- KPI aggregation (FD1: KPI cards with zero states) -------------------

// aggregateKpis computes the four dashboard KPIs from the applications list
// and the campaigns it references: total applications, distinct active
// operators (a null operator_id is not an operator), distinct products used,
// and per-season application totals with the share each season holds — the
// progress bar data. An application whose campaign_id matches no campaign
// buckets under "Unknown" so the totals always reconcile.
export function aggregateKpis(applications, campaigns) {
  const seasonById = new Map(campaigns.map((c) => [c.id, c.season]));
  const operators = new Set();
  const products = new Set();
  const seasonCounts = new Map();

  for (const app of applications) {
    if (app.operator_id) operators.add(app.operator_id);
    if (app.product_name) products.add(app.product_name);
    const season = seasonById.get(app.campaign_id) || UNKNOWN_SEASON;
    seasonCounts.set(season, (seasonCounts.get(season) || 0) + 1);
  }

  const total = applications.length;
  const seasonTotals = [...seasonCounts.entries()]
    .map(([season, count]) => ({ season, count, fraction: total === 0 ? 0 : count / total }))
    .sort((a, b) => b.count - a.count || a.season.localeCompare(b.season));

  return {
    totalApplications: total,
    activeOperators: operators.size,
    productsUsed: products.size,
    seasonTotals,
  };
}

// ---- Series grouping ------------------------------------------------------

// groupApplicationsByMonth buckets applications by their applied_at month and
// returns ascending points [{label, count}] for the line chart. Unparsable
// timestamps are skipped — a bad date cannot draw on the time axis.
export function groupApplicationsByMonth(applications) {
  const counts = new Map();
  for (const app of applications) {
    const month = parseMonth(app.applied_at);
    if (!month) continue;
    counts.set(month.key, { key: month.key, label: month.label, count: (counts.get(month.key)?.count || 0) + 1 });
  }
  return [...counts.values()]
    .sort((a, b) => a.key.localeCompare(b.key))
    .map(({ label, count }) => ({ label, count }));
}

// groupApplicationsByProduct counts applications per product for the donut,
// descending by count with alphabetical ties, so the biggest share starts at
// the top and the legend order is stable. Empty names are skipped.
export function groupApplicationsByProduct(applications) {
  const counts = new Map();
  for (const app of applications) {
    if (!app.product_name) continue;
    counts.set(app.product_name, (counts.get(app.product_name) || 0) + 1);
  }
  return [...counts.entries()]
    .map(([label, count]) => ({ label, count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

// groupApplicationsByLot counts applications per lot for the map card,
// descending. The dashboard joins the ids with the lots list for names.
export function groupApplicationsByLot(applications) {
  const counts = new Map();
  for (const app of applications) {
    counts.set(app.lot_id, (counts.get(app.lot_id) || 0) + 1);
  }
  return [...counts.entries()]
    .map(([lotId, count]) => ({ lotId, count }))
    .sort((a, b) => b.count - a.count);
}

function parseMonth(value) {
  // Only real RFC3339 strings are plotted: Date(null) would coerce to the
  // 1970 epoch and draw a phantom January point.
  if (typeof value !== 'string' || value === '') return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  const key = `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}`;
  return { key, label: MONTHS[date.getUTCMonth()] };
}
