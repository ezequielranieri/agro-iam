// views/lots.js — the lots catalog (FS1, S5). Any authenticated role can read
// the lot list, so this screen renders no write controls. It shows the
// standard loading/empty/error states (FR6) and every dynamic value flows
// through el()/esc() (FR7) — a lot named '<img src=x onerror=alert(1)>' must
// render inert. The module never touches browser storage.
//
// Client-side polish (S5): a live search box filters rows by name or crop, the
// Name/Crop/Area headers sort the rows (▲/▼ marks the active column), crops
// render as color-coded badges, and areas format with thousands separators.
// Search and sort operate on the already-loaded array — no extra requests.
import { esc, el } from '../dom.js';
import { emptyState, errorBanner, showError, skeletonTable, tableWrap } from './table.js';

export function renderLots(container, { api }) {
  container.replaceChildren();

  const banner = errorBanner();
  const card = skeletonTable();
  container.append(banner, card);

  async function load() {
    try {
      const { lots } = await api.request('/api/v1/lots');
      card.replaceChildren(lotsTable(lots));
    } catch (err) {
      card.replaceChildren(emptyState('Could not load the lots.'));
      showError(banner, err && err.message ? esc(err.message) : 'Could not load lots — try again');
    }
  }

  load();
}

// Crop name → badge variant. The Spanish seed names ("Trigo", "Cebada") and
// their English equivalents map to the same chip; any other crop falls back to
// the default badge look. Matching is case-insensitive.
const CROP_BADGES = {
  trigo: 'crop-trigo',
  wheat: 'crop-trigo',
  cebada: 'crop-cebada',
  barley: 'crop-cebada',
};

const AREA_FORMAT = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });

// lotsTable renders the interactive catalog: a search toolbar above a sortable
// table of name, crop, area. The name and crop are hostile-input surfaces
// (FR7) — like every field they go through el() text escaping.
function lotsTable(lots) {
  if (lots.length === 0) {
    return emptyState('No lots in this tenant yet.');
  }

  let query = '';
  let sortKey = null; // 'name' | 'crop' | 'area_ha'
  let sortDir = 1;    // 1 ascending, -1 descending

  const region = el('div', null);
  const headRow = el('tr', null, header('name', 'Name'), header('crop', 'Crop'), header('area_ha', 'Area'));
  const tbody = el('tbody');
  const table = el('table', { class: 'table' },
    el('thead', null, headRow),
    tbody);
  const wrap = tableWrap(el('div', { class: 'table-card' }, table));

  const search = el('input', {
    id: 'lots-search',
    type: 'search',
    placeholder: 'Search by name or crop',
    oninput: () => { query = search.value; renderBody(); },
  });
  const toolbar = el('div', { class: 'table-toolbar' },
    el('div', { class: 'field' },
      el('label', { for: 'lots-search' }, 'Search lots'),
      search));

  function header(key, label) {
    return el('th', {
      scope: 'col',
      class: 'sortable' + (sortKey === key ? ' active' : ''),
      onclick: () => toggleSort(key),
    }, label, el('span', { class: 'sort-indicator' }, sortKey === key ? (sortDir === 1 ? '▲' : '▼') : ''));
  }

  function toggleSort(key) {
    if (sortKey === key) {
      sortDir = -sortDir;
    } else {
      sortKey = key;
      sortDir = 1;
    }
    headRow.replaceChildren(header('name', 'Name'), header('crop', 'Crop'), header('area_ha', 'Area'));
    renderBody();
  }

  function renderBody() {
    const filtered = lots.filter((lot) => matches(lot, query));
    if (filtered.length === 0) {
      region.replaceChildren(emptyState('No lots match your search.'));
      return;
    }
    const sorted = sortKey ? sortBy(filtered, sortKey, sortDir) : filtered;
    tbody.replaceChildren(...sorted.map((lot) => row(lot)));
    region.replaceChildren(wrap);
  }

  function matches(lot, q) {
    const needle = q.trim().toLowerCase();
    if (!needle) return true;
    return String(lot.name ?? '').toLowerCase().includes(needle)
        || String(lot.crop ?? '').toLowerCase().includes(needle);
  }

  function sortBy(rows, key, dir) {
    const copy = rows.slice();
    if (key === 'area_ha') {
      copy.sort((a, b) => (Number(a.area_ha) - Number(b.area_ha)) * dir);
    } else {
      copy.sort((a, b) => String(a[key] ?? '').localeCompare(String(b[key] ?? '')) * dir);
    }
    return copy;
  }

  function row(lot) {
    return el('tr', null,
      el('td', null, lot.name),
      el('td', null, cropBadge(lot.crop)),
      el('td', null, formatArea(lot.area_ha)));
  }

  function cropBadge(crop) {
    const variant = CROP_BADGES[String(crop ?? '').toLowerCase()] || '';
    return el('span', { class: 'badge' + (variant ? ' ' + variant : '') }, crop);
  }

  function formatArea(areaHa) {
    const n = Number(areaHa);
    return Number.isFinite(n) ? AREA_FORMAT.format(n) + ' ha' : '—';
  }

  const root = el('div', null, toolbar, region);
  renderBody();
  return root;
}
