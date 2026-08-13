// views/table.js — shared table states for the S5 catalog screens (FR6): the
// loading skeleton, the empty state and the error banner are identical across
// lots/campaigns/applications (and the users/audit grids reuse them), so they
// live here once instead of being copied into every view. All text flows
// through el() (FR7); this module never touches browser storage.
import { el } from '../dom.js';

// skeletonTable renders the FR6 loading state: a card with shimmer blocks
// where the table rows will land.
export function skeletonTable() {
  return el('div', { class: 'card' },
    el('div', { class: 'skeleton block-skeleton' }),
    el('div', { class: 'skeleton block-skeleton' }),
    el('div', { class: 'skeleton block-skeleton' }));
}

// emptyState renders the FR6 empty state — a message, never a blank table.
export function emptyState(message) {
  return el('div', { class: 'empty' }, message);
}

// errorBanner is the shared FR6 banner slot: hidden until showError reveals it.
export function errorBanner() {
  return el('div', { class: 'banner error hidden', role: 'alert' });
}

// showError reveals the banner with the uniform {"error"} message text.
export function showError(banner, message) {
  banner.textContent = message;
  banner.classList.remove('hidden');
}

// tableWrap gives wide tables a scroll container so narrow viewports do not
// break the layout.
export function tableWrap(...children) {
  return el('div', { class: 'table-wrap' }, ...children);
}
