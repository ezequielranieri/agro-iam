// views/dashboard.js — the dashboard (FD1, S4). It fetches the three list
// endpoints (applications, campaigns, lots — reads any authenticated role can
// make), aggregates them client-side with the pure helpers from charts.js and
// renders the four widgets: KPI cards with progress bars, a smooth line chart
// of applications per month, a product-share donut with legend and an
// illustrated field map with circular markers per lot. Every widget degrades
// to a zero-value state when its data is empty, and a fetch failure surfaces
// the uniform {"error"} message in the shared banner (FR6). All dynamic text
// flows through el()/esc() (FR7); the module never touches browser storage.
import { esc, el } from '../dom.js';
import {
  aggregateKpis,
  buildDonutSegments,
  buildLinePath,
  groupApplicationsByLot,
  groupApplicationsByMonth,
  groupApplicationsByProduct,
  mapMarkers,
} from '../charts.js';

const LINE_W = 600;
const LINE_H = 220;
const DONUT_INNER = 26;
const DONUT_OUTER = 40;
const MAP_W = 300;
const MAP_H = 160;

// Segment palette (D2-adjacent): greens first, then accents for later slices.
const SEGMENT_FILLS = ['#2E7D32', '#66BB6A', '#A5D6A7', '#FF8F00', '#5C6BC0', '#8D6E63'];

export function renderDashboard(container, { api }) {
  container.replaceChildren();

  const banner = el('div', { class: 'banner error hidden', role: 'alert' });
  const kpiRow = el('div', { class: 'kpi' });
  const widgets = el('div', { class: 'widgets' });
  container.append(banner, kpiRow, widgets);

  function showError(message) {
    banner.textContent = message;
    banner.classList.remove('hidden');
  }

  // Loading state: skeleton cards where the widgets will land (FR6 loading).
  kpiRow.append(
    skeletonCard(), skeletonCard(), skeletonCard(), skeletonCard(),
  );
  widgets.append(
    el('div', { class: 'card widget' }, skeletonBlock(), skeletonBlock()),
    el('div', { class: 'card widget' }, skeletonBlock(), skeletonBlock()),
    el('div', { class: 'card widget wide' }, skeletonBlock(), skeletonBlock()),
  );

  async function load() {
    try {
      const [appsRes, campsRes, lotsRes] = await Promise.all([
        api.request('/api/v1/applications'),
        api.request('/api/v1/campaigns'),
        api.request('/api/v1/lots'),
      ]);
      render(appsRes.applications, campsRes.campaigns, lotsRes.lots);
    } catch (err) {
      kpiRow.replaceChildren();
      widgets.replaceChildren();
      showError(err && err.message ? esc(err.message) : 'Could not load the dashboard — try again');
    }
  }

  function render(applications, campaigns, lots) {
    const kpis = aggregateKpis(applications, campaigns);
    const byMonth = groupApplicationsByMonth(applications);
    const byProduct = groupApplicationsByProduct(applications);
    const byLot = groupApplicationsByLot(applications);

    kpiRow.replaceChildren(
      kpiCard(kpis.totalApplications, 'Applications'),
      kpiCard(kpis.activeOperators, 'Active operators'),
      kpiCard(kpis.productsUsed, 'Products used'),
      seasonCard(kpis.seasonTotals),
    );

    widgets.replaceChildren(
      lineWidget(byMonth),
      donutWidget(byProduct),
      mapWidget(lots, byLot),
    );
  }

  load();
}

// ---- KPI cards ------------------------------------------------------------

function kpiCard(value, label) {
  return el('div', { class: 'card kpi-card' },
    el('div', { class: 'kpi-value' }, String(value)),
    el('div', { class: 'kpi-label' }, label));
}

// seasonCard shows the top season as the headline value plus one progress bar
// per season sized by its share of all applications (FD1: progress bars).
function seasonCard(seasonTotals) {
  const top = seasonTotals[0];
  return el('div', { class: 'card kpi-card' },
    el('div', { class: 'kpi-value' }, top ? esc(top.season) : '—'),
    el('div', { class: 'kpi-label' }, top ? `${top.count} applications` : 'Season totals'),
    seasonTotals.length === 0
      ? el('div', { class: 'kpi-sub' }, 'No applications yet')
      : el('div', { class: 'seasons' }, ...seasonTotals.map((s) =>
          el('div', { class: 'season-row' },
            el('span', { class: 'season-name' }, s.season),
            el('span', { class: 'season-count' }, String(s.count)),
            el('div', { class: 'progress' }, el('div', {
              class: 'bar',
              style: `width: ${Math.round(s.fraction * 100)}%`,
            }))))),
  );
}

// ---- Widgets --------------------------------------------------------------

function lineWidget(byMonth) {
  const points = byMonth.length >= 2 ? byMonth : null;
  const title = el('h3', { class: 'widget-title' }, 'Applications over time');
  if (!points) {
    return el('div', { class: 'card widget' }, title,
      el('div', { class: 'empty compact' }, 'No applications to chart yet — apply something to draw the trend.'));
  }

  const path = buildLinePath(points, LINE_W, LINE_H);
  const labels = points.map((p, i) =>
    el('text', { x: 10 + (i * (LINE_W - 20)) / (points.length - 1), y: LINE_H - 4, class: 'chart-label' }, p.label));

  return el('div', { class: 'card widget' }, title,
    el('svg', {
      viewBox: `0 0 ${LINE_W} ${LINE_H}`,
      class: 'chart', role: 'img', 'aria-label': `Line chart of applications over time`,
    },
      el('line', { x1: 10, y1: LINE_H - 20, x2: LINE_W - 10, y2: LINE_H - 20, class: 'chart-axis' }),
      el('line', { x1: 10, y1: 10, x2: LINE_W - 10, y2: 10, class: 'chart-axis' }),
      el('path', { d: path, class: 'chart-line' }),
      ...labels));
}

function donutWidget(byProduct) {
  const title = el('h3', { class: 'widget-title' }, 'Product share');
  if (byProduct.length === 0) {
    return el('div', { class: 'card widget' }, title,
      el('div', { class: 'empty compact' }, 'No products applied yet.'));
  }

  const segments = buildDonutSegments(byProduct, DONUT_INNER, DONUT_OUTER);
  const total = byProduct.reduce((sum, p) => sum + p.count, 0);
  const legend = el('ul', { class: 'legend' }, ...byProduct.map((part, i) =>
    el('li', null,
      el('span', { class: 'swatch', style: `background: ${SEGMENT_FILLS[i % SEGMENT_FILLS.length]}` }),
      el('span', { class: 'legend-label' }, part.label),
      el('span', { class: 'legend-count' }, String(part.count)))));

  return el('div', { class: 'card widget' }, title,
    el('div', { class: 'donut-wrap' },
      el('div', { class: 'donut-col' },
        el('svg', {
          viewBox: '-50 -50 100 100', class: 'chart donut',
          role: 'img', 'aria-label': `Donut chart of product share, ${total} applications`,
        },
          ...segments.map((seg, i) =>
            el('path', {
              d: seg.path,
              class: 'chart-segment',
              style: `fill: ${SEGMENT_FILLS[i % SEGMENT_FILLS.length]}`,
            }))),
        el('div', { class: 'donut-total' }, String(total), ' applications')),
      legend));
}

// mapWidget draws the illustrated field map (FD1: circular markers, no real
// tiles): one marker per lot with applications, positioned deterministically
// by mapMarkers and sized by application count.
function mapWidget(lots, byLot) {
  const title = el('h3', { class: 'widget-title' }, 'Lots with applications');
  const markers = mapMarkers(lots, byLot, MAP_W, MAP_H);
  if (markers.length === 0) {
    return el('div', { class: 'card widget wide' }, title,
      el('div', { class: 'empty compact' }, 'No lots have applications yet.'));
  }

  return el('div', { class: 'card widget wide' }, title,
    el('svg', {
      viewBox: `0 0 ${MAP_W} ${MAP_H}`, class: 'chart map',
      role: 'img', 'aria-label': `Map of lots with ${markers.length} markers`,
    },
      el('rect', { x: 0, y: 0, width: MAP_W, height: MAP_H, class: 'map-field' }),
      ...markers.map((m) => [
        el('circle', { cx: m.cx, cy: m.cy, r: m.r, class: 'map-marker' }),
        el('text', { x: m.cx, y: m.cy, class: 'map-count' }, String(m.count)),
        el('text', { x: m.cx, y: Math.min(m.cy + m.r + 14, MAP_H - 4), class: 'map-lot' }, m.name),
      ]).flat()));
}

// ---- Loading skeletons ----------------------------------------------------

function skeletonCard() {
  return el('div', { class: 'card kpi-card' }, el('div', { class: 'skeleton kpi-skeleton' }));
}

function skeletonBlock() {
  return el('div', { class: 'skeleton block-skeleton' });
}
