// app.js — boot + hash router (FR2, D1). On load it restores the session with
// one single-flight refresh when a refresh token exists, then renders the
// shell; without one it renders the login screen. The router knows the 7
// routes, guards them by the decoded role claim (FR4), renders the role-aware
// nav and never fires API calls for unknown routes or denied ones.
import { esc, el } from './dom.js';
import { createApi } from './api.js';
import { renderLogin } from './views/login.js';
import { renderDashboard } from './views/dashboard.js';
import { renderLots } from './views/lots.js';

// The 7 routes (FR2). login is the unauthenticated surface; the rest render
// inside the shell. The dashboard is live (S4); the lots catalog is live now
// (S5); the remaining data routes wire in the later S5 PRs.
const NAV = [
  { route: 'dashboard', label: 'Dashboard', roles: null, note: 'KPI cards and charts.' },
  { route: 'lots', label: 'Lots', roles: null, note: 'The lots table lands in S5.' },
  { route: 'campaigns', label: 'Campaigns', roles: null, note: 'The campaigns table lands in S5.' },
  { route: 'applications', label: 'Applications', roles: null, note: 'The applications table lands in S5.' },
  { route: 'users', label: 'Users', roles: ['admin'], note: 'The users grid lands in S5.' },
  { route: 'audit', label: 'Audit', roles: ['admin', 'auditor'], note: 'The audit trail lands in S5.' },
];

const VIEW_TITLES = Object.fromEntries(NAV.map((item) => [item.route, item.label]));

// RENDERERS maps a route to its view renderer. Every live screen is an
// (container, {api, role}) renderer; routes not in the map still land on the
// placeholder card until the later S5 PRs wire them.
const RENDERERS = {
  dashboard: renderDashboard,
  lots: renderLots,
};

let api;

const shellEl = document.querySelector('#app');
const authEl = document.querySelector('#auth');
const navEl = document.querySelector('#nav');
const viewEl = document.querySelector('#view');
const titleEl = document.querySelector('#view-title');
const roleBadgeEl = document.querySelector('#role-badge');
const logoutBtn = document.querySelector('#logout');

function currentRoute() {
  const name = location.hash.replace(/^#\/?/, '');
  return name === '' ? 'dashboard' : name;
}

// ---- surfaces --------------------------------------------------------------

function showAuth() {
  shellEl.classList.add('hidden');
  authEl.classList.remove('hidden');
  renderLogin(authEl, { api, onSuccess: () => { location.hash = '#/dashboard'; } });
}

function showShell() {
  authEl.classList.add('hidden');
  shellEl.classList.remove('hidden');
}

// ---- router ----------------------------------------------------------------

function route() {
  // No session -> the login screen owns the page, whatever the hash says.
  if (!api.tokens.getRefresh()) {
    showAuth();
    return;
  }
  const name = currentRoute();
  const item = NAV.find((entry) => entry.route === name);
  const role = api.session()?.role || '';

  // Unknown fragment: fallback view, and DELIBERATELY no API call (FR2).
  if (!item) {
    renderFallback(name);
    return;
  }
  // Route guard from the decoded role claim (FR4): denied roles get a
  // permission view and no API call fires.
  if (item.roles && !item.roles.includes(role)) {
    renderPermission(item);
    return;
  }
  renderShell(name, role);
  titleEl.textContent = VIEW_TITLES[name];
  roleBadgeEl.textContent = role;
  // Dispatch through RENDERERS: every live screen is a (container, {api,
  // role}) renderer. Screens without an entry land on the placeholder card
  // until a later S5 PR wires them.
  const render = RENDERERS[name];
  if (render) {
    render(viewEl, { api, role });
    return;
  }
  renderView(el('div', { class: 'card empty ' + name }, item.note));
}

function renderShell(activeRoute, role) {
  navEl.replaceChildren();
  for (const item of NAV) {
    if (item.roles && !item.roles.includes(role)) continue;
    const link = el('a', {
      href: `#/${item.route}`,
      class: activeRoute === item.route ? 'active' : '',
    }, item.label);
    navEl.append(link);
  }
}

function renderView(node) {
  viewEl.replaceChildren(node);
}

function renderFallback(name) {
  showShell();
  renderView(el('div', { class: 'card empty' },
    `Unknown route `, el('code', null, `#/${esc(name)}`),
    ' — nothing was fetched.'));
}

function renderPermission(item) {
  showShell();
  renderView(el('div', { class: 'card empty' },
    `Your role does not allow `, esc(item.label), '.'));
}

// ---- boot ------------------------------------------------------------------

async function boot() {
  api = createApi({
    storage: window.localStorage,
    fetchImpl: (...args) => fetch(...args),
    onSessionLost: () => { location.hash = '#/login'; },
  });

  window.addEventListener('hashchange', route);
  logoutBtn.addEventListener('click', () => {
    api.logout(); // client-side destruction only (D5); no server endpoint
    location.hash = '#/login';
  });

  // Session restore: no refresh token -> login; token -> one single-flight
  // refresh, then the shell (FR5 on-load contract).
  if (!api.tokens.getRefresh()) {
    showAuth();
    return;
  }
  try {
    await api.refresh();
  } catch {
    showAuth(); // refresh 401 already cleared both tokens (api.js)
    return;
  }
  // FR4: a token whose claims do not decode is discarded outright.
  const session = api.session();
  if (!session || !session.role) {
    api.logout();
    showAuth();
    return;
  }
  showShell();
  route();
}

boot().catch(() => {
  // Boot must never leave a silent blank page: fall back to login.
  showAuth();
});