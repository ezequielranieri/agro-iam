// charts.js — client-side dashboard math (FD1, D1 hand-rolled SVG). Every
// widget aggregates the applications/campaigns/lots data fetched from the list
// endpoints. This module holds the aggregation layer (KPI cards via
// aggregateKpis and the series fed to each widget via the group* helpers) and
// the SVG geometry that turns those series into paths: a smooth line
// (buildLinePath), a donut with legend (buildDonutSegments) and an illustrated
// map with circular markers (mapMarkers). All functions are pure and
// deterministic so they run under node --test; empty datasets resolve to
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

// ---- SVG geometry ---------------------------------------------------------

const LINE_PAD = 10;

// buildLinePath turns monthly points into one smooth SVG path. The curve is a
// Catmull-Rom spline converted to cubic Béziers, so the chart reads as a
// smooth trend line (FD1) instead of a jagged polyline. Fewer than two points
// return null — the dashboard renders the empty state instead of a bare axis.
// An all-zero series is a flat baseline at the bottom, never a NaN.
export function buildLinePath(points, width, height) {
  if (points.length < 2) return null;
  const max = Math.max(...points.map((p) => p.count));
  const innerW = width - 2 * LINE_PAD;
  const innerH = height - 2 * LINE_PAD;
  const scaled = points.map((p, i) => ({
    x: LINE_PAD + (i * innerW) / (points.length - 1),
    y: height - LINE_PAD - (max === 0 ? 0 : (p.count / max) * innerH),
  }));
  // Doubled endpoints keep the spline tangent at the first/last point.
  const pts = [scaled[0], ...scaled, scaled[scaled.length - 1]];
  let path = `M ${scaled[0].x} ${scaled[0].y}`;
  for (let i = 1; i <= scaled.length - 1; i += 1) {
    const p0 = pts[i - 1];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[i + 2];
    const c1 = { x: p1.x + (p2.x - p0.x) / 6, y: p1.y + (p2.y - p0.y) / 6 };
    const c2 = { x: p2.x - (p3.x - p1.x) / 6, y: p2.y - (p3.y - p1.y) / 6 };
    path += ` C ${c1.x} ${c1.y}, ${c2.x} ${c2.y}, ${p2.x} ${p2.y}`;
  }
  return path;
}

// buildDonutSegments turns product shares into annular SVG paths, one per
// part, starting at 12 o'clock and sweeping clockwise. Each path is the outer
// arc, a line to the inner edge, the inner arc back and a closing line — the
// classic donut ring. A single part is the full circle, which SVG cannot draw
// as one arc command, so it is split into two half arcs per edge.
export function buildDonutSegments(parts, innerRadius, outerRadius) {
  const total = parts.reduce((sum, p) => sum + p.count, 0);
  if (total === 0) return [];
  let angle = 0;
  return parts.filter((p) => p.count > 0).map((part) => {
    const fraction = part.count / total;
    const start = angle;
    const end = angle + fraction * 2 * Math.PI;
    angle = end;
    return {
      label: part.label,
      count: part.count,
      fraction,
      startAngle: start,
      path: ringPath(start, end, innerRadius, outerRadius, fraction >= 1 - 1e-9),
    };
  });
}

function ringPath(start, end, innerRadius, outerRadius, fullCircle) {
  const arcTo = (radius, a) => {
    // The point must sit ON the requested radius: using the outer radius here
    // would put the inner edge of the ring on the wrong circle.
    const x = radius * Math.sin(a);
    const y = -radius * Math.cos(a);
    return { x, y, radius };
  };
  const outerStart = arcTo(outerRadius, start);
  const outerEnd = arcTo(outerRadius, end);
  const innerStart = arcTo(innerRadius, start);
  const innerEnd = arcTo(innerRadius, end);
  const largeArc = end - start > Math.PI ? 1 : 0;

  if (!fullCircle) {
    return [
      `M ${outerStart.x} ${outerStart.y}`,
      `A ${outerRadius} ${outerRadius} 0 ${largeArc} 1 ${outerEnd.x} ${outerEnd.y}`,
      `L ${innerEnd.x} ${innerEnd.y}`,
      `A ${innerRadius} ${innerRadius} 0 ${largeArc} 0 ${innerStart.x} ${innerStart.y}`,
      'Z',
    ].join(' ');
  }

  // Full ring: two half arcs per edge (start === end for a closed circle).
  const midOuter = arcTo(outerRadius, start + Math.PI);
  const midInner = arcTo(innerRadius, start + Math.PI);
  return [
    `M ${outerStart.x} ${outerStart.y}`,
    `A ${outerRadius} ${outerRadius} 0 1 1 ${midOuter.x} ${midOuter.y}`,
    `A ${outerRadius} ${outerRadius} 0 1 1 ${outerStart.x} ${outerStart.y}`,
    `L ${innerStart.x} ${innerStart.y}`,
    `A ${innerRadius} ${innerRadius} 0 1 0 ${midInner.x} ${midInner.y}`,
    `A ${innerRadius} ${innerRadius} 0 1 0 ${innerStart.x} ${innerStart.y}`,
    'Z',
  ].join(' ');
}

const MAP_PAD = 16;

// mapMarkers positions one circular marker per lot that received applications.
// Positions come from a stable hash of the lot id, so the illustrated field
// map is deterministic across renders (and testable); the marker radius grows
// with the application count so busy lots read at a glance. Empty data yields
// no markers — the dashboard shows the zero state.
export function mapMarkers(lots, countsByLot, width, height) {
  const counts = new Map(countsByLot.map((entry) => [entry.lotId, entry.count]));
  const markers = [];
  for (const lot of lots) {
    const count = counts.get(lot.id) || 0;
    if (count <= 0) continue;
    markers.push({
      id: lot.id,
      name: lot.name,
      count,
      cx: MAP_PAD + hashU32(lot.id) * (width - 2 * MAP_PAD),
      cy: MAP_PAD + hashU32(`${lot.id}:y`) * (height - 2 * MAP_PAD),
      r: Math.max(5, Math.min(18, Math.round(4 + 3 * Math.sqrt(count)))),
    });
  }
  return markers;
}

// hashU32 — FNV-1a over a string, normalized to [0,1). Stable across engines
// and sessions so marker positions never shuffle between renders.
function hashU32(str) {
  let h = 0x811c9dc5;
  for (let i = 0; i < str.length; i += 1) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0) / 0x100000000;
}
