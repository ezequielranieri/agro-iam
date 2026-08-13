// views/lots.js — the lots catalog (FS1, S5). Any authenticated role can read
// the lot list, so this screen renders no write controls. It shows the
// standard loading/empty/error states (FR6) and every dynamic value flows
// through el()/esc() (FR7) — a lot named '<img src=x onerror=alert(1)>' must
// render inert. The module never touches browser storage.
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

// lotsTable renders the rows: name, crop, area. The name is the hostile-input
// surface (FR7) — like every field it goes through el() text escaping.
function lotsTable(lots) {
  if (lots.length === 0) {
    return emptyState('No lots in this tenant yet.');
  }
  const table = el('table', { class: 'table' },
    el('thead', null,
      el('tr', null,
        el('th', { scope: 'col' }, 'Name'),
        el('th', { scope: 'col' }, 'Crop'),
        el('th', { scope: 'col' }, 'Area (ha)'))) ,
    el('tbody', null,
      ...lots.map((lot) =>
        el('tr', null,
          el('td', null, lot.name),
          el('td', null, lot.crop),
          el('td', null, String(lot.area_ha))))));
  return tableWrap(table);
}
