// views/audit.js — the audit trail (FA2, S5). The nav exposes audit to
// {admin, auditor}; for the auditor the server-side route is admin-only (AP1),
// so the fetch resolves to a 403 that surfaces the uniform {"error"} message
// in the shared banner — defense in depth, the screen never crashes (FR6).
// The table shows the latest-N events with their severity as a badge and the
// standard loading/empty/error states. All dynamic text flows through
// el()/esc() (FR7); the module never touches browser storage.
import { esc, el } from '../dom.js';
import { emptyState, errorBanner, showError, skeletonTable, tableWrap } from './table.js';

export function renderAudit(container, { api }) {
  container.replaceChildren();

  const banner = errorBanner();
  const card = skeletonTable();
  container.append(banner, card);

  async function load() {
    try {
      const { events } = await api.request('/api/v1/audit');
      card.replaceChildren(eventsTable(events));
    } catch (err) {
      card.replaceChildren(emptyState('Could not load the audit trail.'));
      showError(banner, err && err.message ? esc(err.message) : 'Could not load the audit trail — try again');
    }
  }

  load();
}

// eventsTable renders the latest events: action, entity, severity badge and
// timestamp. The payload blob is deliberately not part of the response (AP1)
// and not rendered.
function eventsTable(events) {
  if (events.length === 0) {
    return emptyState('No audit events in this tenant yet.');
  }
  const table = el('table', { class: 'table' },
    el('thead', null,
      el('tr', null,
        el('th', { scope: 'col' }, 'Action'),
        el('th', { scope: 'col' }, 'Entity'),
        el('th', { scope: 'col' }, 'Severity'),
        el('th', { scope: 'col' }, 'When'))),
    el('tbody', null,
      ...events.map((event) =>
        el('tr', null,
          el('td', null, event.action),
          el('td', null, event.entity_type ? `${event.entity_type} ${event.entity_id}` : '—'),
          el('td', null, el('span', { class: 'badge severity-' + event.severity }, event.severity)),
          el('td', null, formatDateTime(event.created_at))))));
  return tableWrap(table);
}

// formatDateTime renders RFC3339 timestamps as local-ish readable text.
function formatDateTime(iso) {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return date.toISOString().replace('T', ' ').slice(0, 19) + 'Z';
}