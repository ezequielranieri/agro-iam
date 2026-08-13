// views/campaigns.js — the campaigns catalog (FS1, S5) with matrix-gated
// write controls (FS2). Any authenticated role reads the campaign list with
// the standard loading/empty/error states (FR6). The "New campaign" form
// renders ONLY for roles allowed by the write matrix (admin | agronomist);
// every dynamic value flows through el()/esc() (FR7) and the module never
// touches browser storage. The server stays the authority: a stale-token 403
// surfaces the uniform {"error"} message in the shared banner and the form
// stays open (FR6).
import { esc, el } from '../dom.js';
import { canWrite, validateCampaign } from '../matrix.js';
import { emptyState, errorBanner, showError, skeletonTable, tableWrap } from './table.js';

export function renderCampaigns(container, { api, role }) {
  container.replaceChildren();

  const banner = errorBanner();
  const card = skeletonTable();
  container.append(banner, card);

  // A "New campaign" button owns an inline card form. It is rendered only
  // when the role may write (FS2); non-write roles see no write affordance.
  let formCard = null;
  const model = { campaigns: [] };

  function renderForm() {
    if (formCard) return;
    const nameInput = el('input', { type: 'text', name: 'name', required: true, placeholder: 'Campaña 2025/2026', 'aria-label': 'Campaign name' });
    const seasonInput = el('input', { type: 'text', name: 'season', required: true, placeholder: '2025/2026', 'aria-label': 'Season' });
    const startedInput = el('input', { type: 'date', name: 'started_at', 'aria-label': 'Start date' });
    const endedInput = el('input', { type: 'date', name: 'ended_at', 'aria-label': 'End date' });
    const submit = el('button', { type: 'submit', class: 'btn primary btn-sm' }, 'Create campaign');
    const cancel = el('button', { type: 'button', class: 'btn btn-sm', 'aria-label': 'Cancel' }, 'Cancel');
    const form = el('form', { class: 'create-form' },
      el('div', { class: 'field' }, el('label', { for: 'campaign-name' }, 'Name'), nameInput),
      el('div', { class: 'field' }, el('label', { for: 'campaign-season' }, 'Season'), seasonInput),
      el('div', { class: 'field' }, el('label', { for: 'campaign-started' }, 'Started'), startedInput),
      el('div', { class: 'field' }, el('label', { for: 'campaign-ended' }, 'Ended'), endedInput),
      el('div', { class: 'form-actions' }, submit, cancel));

    hideForm = () => {
      formCard.remove();
      formCard = null;
    };
    cancel.addEventListener('click', hideForm);

    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const input = {
        name: nameInput.value,
        season: seasonInput.value,
        started_at: startedInput.value || null,
        ended_at: endedInput.value || null,
      };
      const check = validateCampaign(input);
      if (!check.valid) {
        showError(banner, `Missing required: ${check.missing.join(', ')}`);
        return;
      }
      submit.disabled = true;
      try {
        // Full ISO date strings are valid RFC3339 timestamps (T00:00:00Z).
        await api.request('/api/v1/campaigns', { method: 'POST', body: input });
        hideForm();
        await load();
      } catch (err) {
        // A 403 lands here: the {error} message shows in the banner and the
        // form stays open with the user's input (FR6 defense in depth).
        showError(banner, err && err.message ? esc(err.message) : 'Could not create the campaign — try again');
      } finally {
        submit.disabled = false;
      }
    });

    formCard = el('div', { class: 'card form-card' }, form);
    card.before(formCard);
  }

  let hideForm = () => {};

  const writeToolbar = el('div', { class: 'toolbar' },
    el('button', {
      type: 'button',
      class: 'btn primary btn-sm',
      'aria-label': 'New campaign',
      onclick: () => renderForm(),
    }, 'New campaign'));

  async function load() {
    try {
      const { campaigns } = await api.request('/api/v1/campaigns');
      model.campaigns = campaigns;
      card.replaceChildren(campaignsTable(campaigns));
    } catch (err) {
      card.replaceChildren(emptyState('Could not load the campaigns.'));
      showError(banner, err && err.message ? esc(err.message) : 'Could not load campaigns — try again');
    }
  }

  if (canWrite(role, 'campaigns')) {
    // Write controls render only for matrix roles (FS2). They live in their
    // own row, not inside the table card, so the states stay interchangeable.
    container.append(writeToolbar);
    writeToolbar.before(card);
  }

  load();
}

// campaignsTable renders the rows: name, season and the optional dates. Like
// every screen, all dynamic text flows through el() text children (FR7).
function campaignsTable(campaigns) {
  if (campaigns.length === 0) {
    return emptyState('No campaigns in this tenant yet.');
  }
  const table = el('table', { class: 'table' },
    el('thead', null,
      el('tr', null,
        el('th', { scope: 'col' }, 'Name'),
        el('th', { scope: 'col' }, 'Season'),
        el('th', { scope: 'col' }, 'Started'),
        el('th', { scope: 'col' }, 'Ended'))),
    el('tbody', null,
      ...campaigns.map((campaign) =>
        el('tr', null,
          el('td', null, campaign.name),
          el('td', null, campaign.season),
          el('td', null, campaign.started_at ? formatDate(campaign.started_at) : '—'),
          el('td', null, campaign.ended_at ? formatDate(campaign.ended_at) : '—')))));
  return tableWrap(table);
}

// formatDate renders RFC3339 timestamps as date-only for the table.
function formatDate(iso) {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? '' : date.toISOString().slice(0, 10);
}