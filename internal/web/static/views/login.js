// views/login.js — the login screen (FR3). It loads the public realm catalog
// from GET /api/v1/tenants, submits {email, password, tenant_id} and surfaces
// a 429 by disabling the form until the Retry-After countdown elapses. On
// success it calls onSuccess so the app can route to the dashboard.
import { esc, el } from '../dom.js';

export function renderLogin(container, { api, onSuccess }) {
  container.replaceChildren();

  const banner = el('div', { class: 'banner error hidden', role: 'alert' });
  const tenantSelect = el('select', { name: 'tenant_id', required: true, 'aria-label': 'Realm' },
    el('option', { value: '' }, 'Loading realms…'));
  const emailInput = el('input', { type: 'email', name: 'email', required: true, placeholder: 'you@esperanza.coop', autocomplete: 'username', 'aria-label': 'Email' });
  const passwordInput = el('input', { type: 'password', name: 'password', required: true, placeholder: 'Password', autocomplete: 'current-password', 'aria-label': 'Password' });
  const submit = el('button', { type: 'submit', class: 'btn primary btn-block' }, 'Sign in');
  const form = el('form', { class: 'login-form' },
    el('div', { class: 'field' }, el('label', { for: 'realm' }, 'Tenant'), tenantSelect),
    el('div', { class: 'field' }, el('label', { for: 'email' }, 'Email'), emailInput),
    el('div', { class: 'field' }, el('label', { for: 'password' }, 'Password'), passwordInput),
    submit);

  const card = el('div', { class: 'card login-card' },
    el('div', { class: 'login-brand' }, 'Agro IAM'),
    el('p', { class: 'login-sub' }, 'Demo — multi-tenant RBAC'),
    banner,
    form);
  container.append(card);

  function showError(message) {
    banner.textContent = message;
    banner.classList.remove('hidden');
  }
  function hideError() {
    banner.classList.add('hidden');
  }

  // A 429 disables the form for the Retry-After window with a live countdown
  // (FR3): the submit button stays disabled until backoff elapses.
  function lockUntil(seconds) {
    let remaining = seconds;
    submit.disabled = true;
    form.classList.add('locked');
    showError(`Too many attempts — try again in ${remaining}s`);
    const timer = setInterval(() => {
      remaining -= 1;
      if (remaining <= 0) {
        clearInterval(timer);
        submit.disabled = false;
        form.classList.remove('locked');
        hideError();
      } else {
        showError(`Too many attempts — try again in ${remaining}s`);
      }
    }, 1000);
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    hideError();
    submit.disabled = true;
    try {
      const body = await api.login({
        tenantId: tenantSelect.value,
        email: emailInput.value,
        password: passwordInput.value,
      });
      onSuccess(body);
    } catch (err) {
      if (err && err.status === 429 && err.retryAfter) {
        lockUntil(err.retryAfter);
      } else {
        showError(err && err.message ? esc(err.message) : 'Network error — try again');
        submit.disabled = false;
      }
    }
  });

  // Realm catalog: public endpoint, ids read at request time (AP2 reseed-safe).
  // Failure keeps the selector disabled so a user never guesses a uuid.
  api.request('/api/v1/tenants')
    .then(({ tenants = [] }) => {
      tenantSelect.replaceChildren();
      tenantSelect.append(el('option', { value: '' }, 'Choose a tenant…'));
      for (const tenant of tenants) {
        tenantSelect.append(el('option', { value: tenant.id }, tenant.name));
      }
    })
    .catch(() => {
      showError('Could not load realms — try again');
      tenantSelect.replaceChildren(el('option', { value: '' }, 'Realms unavailable'));
    });
}