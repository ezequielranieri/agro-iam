// views/users.js — the users grid (FA1, S5). The route is admin-only (NAV
// roles + app.js guard), so this screen only ever mounts for an admin: listing
// (with the server-resolved role column), creating a user and toggling
// is_active. The role picker uses ACCEPTED_ROLES from the shared matrix so the
// client never ships a role the catalog rejects. All dynamic text flows
// through el()/esc() (FR7); the module never touches browser storage. A write
// that fails server-side surfaces the uniform {"error"} message in the shared
// banner and the form stays open (FR6).
import { esc, el } from '../dom.js';
import { ACCEPTED_ROLES, validateUser } from '../matrix.js';
import { emptyState, errorBanner, showError, skeletonTable, tableWrap } from './table.js';

export function renderUsers(container, { api }) {
  container.replaceChildren();

  const banner = errorBanner();
  const card = skeletonTable();
  container.append(banner, card);

  let formCard = null;
  let hideForm = () => {};

  function renderForm() {
    if (formCard) return;
    const emailInput = el('input', { type: 'email', name: 'email', required: true, placeholder: 'nueva@esperanza.coop', 'aria-label': 'Email' });
    const passwordInput = el('input', { type: 'password', name: 'password', required: true, placeholder: 'test123', autocomplete: 'new-password', 'aria-label': 'Password' });
    const nameInput = el('input', { type: 'text', name: 'full_name', required: true, placeholder: 'Full name', 'aria-label': 'Full name' });
    const roleSelect = el('select', { name: 'role', required: true, 'aria-label': 'Role' },
      el('option', { value: '' }, 'Choose a role…'),
      ...ACCEPTED_ROLES.map((role) => el('option', { value: role }, role)));
    const submit = el('button', { type: 'submit', class: 'btn primary btn-sm' }, 'Create user');
    const cancel = el('button', { type: 'button', class: 'btn btn-sm', 'aria-label': 'Cancel' }, 'Cancel');
    const form = el('form', { class: 'create-form' },
      el('div', { class: 'field' }, el('label', { for: 'user-email' }, 'Email'), emailInput),
      el('div', { class: 'field' }, el('label', { for: 'user-password' }, 'Password'), passwordInput),
      el('div', { class: 'field' }, el('label', { for: 'user-name' }, 'Full name'), nameInput),
      el('div', { class: 'field' }, el('label', { for: 'user-role' }, 'Role'), roleSelect),
      el('div', { class: 'form-actions' }, submit, cancel));

    hideForm = () => {
      formCard.remove();
      formCard = null;
    };
    cancel.addEventListener('click', hideForm);

    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const input = {
        email: emailInput.value,
        password: passwordInput.value,
        full_name: nameInput.value,
        role: roleSelect.value,
      };
      const check = validateUser(input);
      if (!check.valid) {
        showError(banner, `Missing required: ${check.missing.join(', ')}`);
        return;
      }
      submit.disabled = true;
      try {
        await api.request('/api/v1/users', { method: 'POST', body: input });
        hideForm();
        await load();
      } catch (err) {
        // Any server rejection (conflict 409, invalid input 400, stale 403)
        // shows the uniform {error} and keeps the form open (FR6).
        showError(banner, err && err.message ? esc(err.message) : 'Could not create the user — try again');
      } finally {
        submit.disabled = false;
      }
    });

    formCard = el('div', { class: 'card form-card' }, form);
    card.before(formCard);
  }

  const writeToolbar = el('div', { class: 'toolbar' },
    el('button', {
      type: 'button',
      class: 'btn primary btn-sm',
      'aria-label': 'New user',
      onclick: () => renderForm(),
    }, 'New user'));

  async function load() {
    try {
      const { users } = await api.request('/api/v1/users');
      card.replaceChildren(usersTable(users, (user) => row(user, banner, api, load)));
    } catch (err) {
      card.replaceChildren(emptyState('Could not load the users.'));
      showError(banner, err && err.message ? esc(err.message) : 'Could not load users — try again');
    }
  }

  container.append(writeToolbar);
  writeToolbar.before(card);
  load();
}

// usersTable renders the grid: email, full name, role and active status, plus
// the active toggle for every row (FA1). The row builder is injected so the
// toggle can close over the live api/banner/load.
function usersTable(users, buildRow) {
  if (users.length === 0) {
    return emptyState('No users in this tenant yet.');
  }
  const table = el('table', { class: 'table' },
    el('thead', null,
      el('tr', null,
        el('th', { scope: 'col' }, 'Email'),
        el('th', { scope: 'col' }, 'Full name'),
        el('th', { scope: 'col' }, 'Role'),
        el('th', { scope: 'col' }, 'Status'),
        el('th', { scope: 'col' }, 'Actions'))),
    el('tbody', null,
      ...users.map(buildRow)));
  return tableWrap(table);
}

// row is one user row: the server-resolved role badge (R9), the active badge
// and an aria-labelled Activate/Deactivate button. The toggle is a full-row
// replace (PATCH semantics in the handler): it sends the current full_name
// with the flipped is_active.
function row(user, banner, api, load) {
  const button = el('button', {
    type: 'button',
    class: 'btn btn-sm',
    'aria-label': `${user.is_active ? 'Deactivate' : 'Activate'} ${user.email}`,
    onclick: async (event) => {
      const target = event.currentTarget;
      target.disabled = true;
      try {
        await api.request(`/api/v1/users/${user.id}`, {
          method: 'PATCH',
          body: { full_name: user.full_name, is_active: !user.is_active },
        });
        await load();
      } catch (err) {
        showError(banner, err && err.message ? esc(err.message) : 'Could not update the user — try again');
        target.disabled = false;
      }
    },
  }, user.is_active ? 'Deactivate' : 'Activate');
  return el('tr', null,
    el('td', null, user.email),
    el('td', null, user.full_name),
    el('td', null, el('span', { class: 'badge' }, user.role)),
    el('td', null, user.is_active ? 'Active' : 'Inactive'),
    el('td', null, button));
}