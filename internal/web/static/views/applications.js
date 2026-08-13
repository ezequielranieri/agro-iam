// views/applications.js — the applications catalog (FS1, S5) with
// matrix-gated write controls (FS2). Any authenticated role reads the
// application list (loading/empty/error states, FR6). The "New application"
// form renders ONLY for roles allowed by the write matrix (admin | agronomist
// | producer); its lot and campaign selectors come from the same tenant list
// endpoints — reads any authenticated role can make — so it never widens
// privileges. A stale-token 403 surfaces the uniform {"error"} message in the
// shared banner and the form stays open (FR6). All dynamic text flows through
// el()/esc() (FR7); the module never touches browser storage.
import { esc, el } from '../dom.js';
import { canWrite, validateApplication } from '../matrix.js';
import { emptyState, errorBanner, showError, skeletonTable, tableWrap } from './table.js';

export function renderApplications(container, { api, role }) {
  container.replaceChildren();

  const banner = errorBanner();
  const card = skeletonTable();
  container.append(banner, card);

  let formCard = null;
  let hideForm = () => {};

  function renderForm() {
    if (formCard) return;
    const lotSelect = el('select', { name: 'lot_id', required: true, 'aria-label': 'Lot' },
      el('option', { value: '' }, 'Loading lots…'));
    const campaignSelect = el('select', { name: 'campaign_id', required: true, 'aria-label': 'Campaign' },
      el('option', { value: '' }, 'Loading campaigns…'));
    const productInput = el('input', { type: 'text', name: 'product_name', required: true, placeholder: 'Urea 46%', 'aria-label': 'Product name' });
    const doseInput = el('input', { type: 'text', name: 'dose', placeholder: '150 kg/ha', 'aria-label': 'Dose' });
    const appliedInput = el('input', { type: 'datetime-local', name: 'applied_at', 'aria-label': 'Applied at' });
    const notesInput = el('input', { type: 'text', name: 'notes', placeholder: 'Notes (optional)', 'aria-label': 'Notes' });
    const submit = el('button', { type: 'submit', class: 'btn primary btn-sm' }, 'Create application');
    const cancel = el('button', { type: 'button', class: 'btn btn-sm', 'aria-label': 'Cancel' }, 'Cancel');
    const form = el('form', { class: 'create-form' },
      el('div', { class: 'field' }, el('label', { for: 'app-lot' }, 'Lot'), lotSelect),
      el('div', { class: 'field' }, el('label', { for: 'app-campaign' }, 'Campaign'), campaignSelect),
      el('div', { class: 'field' }, el('label', { for: 'app-product' }, 'Product'), productInput),
      el('div', { class: 'field' }, el('label', { for: 'app-dose' }, 'Dose'), doseInput),
      el('div', { class: 'field' }, el('label', { for: 'app-applied' }, 'Applied at'), appliedInput),
      el('div', { class: 'field' }, el('label', { for: 'app-notes' }, 'Notes'), notesInput),
      el('div', { class: 'form-actions' }, submit, cancel));

    hideForm = () => {
      formCard.remove();
      formCard = null;
    };
    cancel.addEventListener('click', hideForm);

    // Selector sources: the same tenant-scoped list endpoints the dashboard
    // uses. Reads are open to any authenticated role (FS1), so loading them
    // never widens privileges beyond what the write form already implies.
    Promise.all([
      api.request('/api/v1/lots'),
      api.request('/api/v1/campaigns'),
    ]).then(([lotsRes, campaignsRes]) => {
      lotSelect.replaceChildren(el('option', { value: '' }, 'Choose a lot…'));
      for (const lot of lotsRes.lots) {
        lotSelect.append(el('option', { value: lot.id }, lot.name));
      }
      campaignSelect.replaceChildren(el('option', { value: '' }, 'Choose a campaign…'));
      for (const campaign of campaignsRes.campaigns) {
        campaignSelect.append(el('option', { value: campaign.id }, campaign.name));
      }
    }).catch(() => {
      showError(banner, 'Could not load lots or campaigns for the form — try again');
    });

    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const input = {
        lot_id: lotSelect.value,
        campaign_id: campaignSelect.value,
        product_name: productInput.value,
        dose: doseInput.value,
        applied_at: appliedInput.value ? new Date(appliedInput.value).toISOString() : null,
        operator_id: '',
        notes: notesInput.value,
      };
      const check = validateApplication(input);
      if (!check.valid) {
        showError(banner, `Missing required: ${check.missing.join(', ')}`);
        return;
      }
      submit.disabled = true;
      try {
        await api.request('/api/v1/applications', { method: 'POST', body: input });
        hideForm();
        await load();
      } catch (err) {
        // A stale-token 403 lands here: the uniform {error} shows in the
        // banner and the form stays open (FR6).
        showError(banner, err && err.message ? esc(err.message) : 'Could not create the application — try again');
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
      'aria-label': 'New application',
      onclick: () => renderForm(),
    }, 'New application'));

  async function load() {
    try {
      const { applications } = await api.request('/api/v1/applications');
      card.replaceChildren(applicationsTable(applications));
    } catch (err) {
      card.replaceChildren(emptyState('Could not load the applications.'));
      showError(banner, err && err.message ? esc(err.message) : 'Could not load applications — try again');
    }
  }

  if (canWrite(role, 'applications')) {
    container.append(writeToolbar);
    writeToolbar.before(card);
  }

  load();
}

// applicationsTable renders the rows: product, lot, campaign, operator and
// applied date. OperatorName is the server-resolved display name (R6) — the
// client never needs the users endpoint. Every value flows through el() text
// children (FR7).
function applicationsTable(applications) {
  if (applications.length === 0) {
    return emptyState('No applications in this tenant yet.');
  }
  const table = el('table', { class: 'table' },
    el('thead', null,
      el('tr', null,
        el('th', { scope: 'col' }, 'Product'),
        el('th', { scope: 'col' }, 'Lot'),
        el('th', { scope: 'col' }, 'Campaign'),
        el('th', { scope: 'col' }, 'Operator'),
        el('th', { scope: 'col' }, 'Applied'))),
    el('tbody', null,
      ...applications.map((app) =>
        el('tr', null,
          el('td', null, app.product_name),
          el('td', null, app.lot_id),
          el('td', null, app.campaign_id),
          el('td', null, app.operator_name || '—'),
          el('td', null, app.applied_at ? formatDate(app.applied_at) : '—')))));
  return tableWrap(table);
}

// formatDate renders RFC3339 timestamps as date-only for the table.
function formatDate(iso) {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? '' : date.toISOString().slice(0, 10);
}