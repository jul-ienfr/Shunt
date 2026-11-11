import { t, applyStatic, detectLanguage, setLanguage, currentLanguage, otherLanguage } from './i18n.js';

const tokenMeta = document.querySelector('meta[name="shunt-ui-token"]');
const token = tokenMeta ? tokenMeta.getAttribute('content') : '';
let overview = null;
let pendingEntry = null;
let lastModels = null;

const $ = (id) => document.getElementById(id);

const THEMES = ['auto', 'dark', 'light'];
const themeButton = $('theme');
const languageButton = $('language');
let theme = window.__shuntTheme;
if (!THEMES.includes(theme)) theme = 'auto';

function themeLabel(next) {
  return t('theme.' + next);
}

function applyTheme(next) {
  theme = next;
  document.documentElement.setAttribute('data-theme', next);
  try { window.localStorage.setItem('shunt-theme', next); } catch { /* storage may be disabled */ }
  if (themeButton) {
    const label = themeLabel(next);
    themeButton.textContent = label;
    themeButton.setAttribute('aria-label', t('theme.aria', { theme: label }));
    themeButton.title = t('theme.aria', { theme: label });
  }
}

if (themeButton) {
  themeButton.addEventListener('click', () => {
    applyTheme(THEMES[(THEMES.indexOf(theme) + 1) % THEMES.length]);
  });
  applyTheme(theme);
}

function languageLabel(code) {
  return t(code === 'fr' ? 'language.fr' : 'language.en');
}

function updateLanguageButton() {
  if (!languageButton) return;
  const next = otherLanguage();
  languageButton.textContent = currentLanguage.toUpperCase();
  languageButton.setAttribute('aria-label', t('language.aria', { lang: languageLabel(currentLanguage), next: languageLabel(next) }));
  languageButton.title = t('language.switch', { lang: languageLabel(next) });
}

function refreshDynamic() {
  applyTheme(theme);
  updateLanguageButton();
  if (overview) renderOverview(overview);
  if (clientState.loaded) {
    renderClients();
  }
  renderModels();
}

function applyLanguage(next) {
  setLanguage(next);
  try { window.localStorage.setItem('shunt-lang', currentLanguage); } catch { /* storage may be disabled */ }
  document.documentElement.setAttribute('lang', currentLanguage);
  applyStatic(document);
  refreshDynamic();
}

if (languageButton) {
  languageButton.addEventListener('click', () => applyLanguage(otherLanguage()));
}

function showError(message) {
  const box = $('error');
  box.textContent = message;
  box.hidden = !message;
}

function apiErrorMessage(err) {
  if (err && err.code) {
    const key = 'error.' + err.code;
    const text = t(key);
    if (text !== key) {
      if (err.code === 'probe_failed' && err.message) return text + ' ' + err.message;
      return text;
    }
  }
  if (err instanceof TypeError) return t('error.network');
  return (err && err.message) || t('error.generic');
}

function translatedValue(group, value) {
  const key = `${group}.${value}`;
  const text = t(key);
  return text === key ? String(value == null ? '' : value) : text;
}

function freeLabel(value) {
  return value === 'free' ? t('models.freeValue') : value === 'paid' ? t('models.paidValue') : t('models.unknownValue');
}

async function api(path, options = {}) {
  const headers = Object.assign({ 'Accept': 'application/json' }, options.headers || {});
  let body = options.body;
  if (body && typeof body !== 'string') {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  if (options.method && options.method !== 'GET') {
    headers['X-Shunt-UI-Token'] = token;
  }
  const res = await fetch(path, { method: options.method || 'GET', headers, body, credentials: 'same-origin' });
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = null; }
  }
  if (!res.ok) {
    const err = new Error((data && data.error && data.error.message) || text || ('HTTP ' + res.status));
    err.code = data && data.error && data.error.code;
    err.status = res.status;
    throw err;
  }
  return data;
}

function setText(id, value) {
  const el = $(id);
  if (el) el.textContent = value == null || value === '' ? '—' : String(value);
}

function row(cells) {
  const tr = document.createElement('tr');
  for (const cell of cells) {
    const td = document.createElement('td');
    if (cell instanceof Node) td.appendChild(cell); else td.textContent = cell == null || cell === '' ? '—' : String(cell);
    tr.appendChild(td);
  }
  return tr;
}

function profileURL(profile) {
  if (profile.base_url) return profile.base_url;
  const port = profile.port ? `:${profile.port}` : '';
  return `${profile.scheme}://${profile.host}${port}`;
}

function renderUpstreamWarning(data) {
  const box = $('upstream-warning');
  if (!box) return;
  const profiles = data.profiles || [];
  const effective = profiles.find((p) => p.id === data.effective);
  if (!effective) {
    $('upstream-warning-text').textContent = t('upstream.noneText');
    box.hidden = false;
    return;
  }
  if (effective.health !== 'unhealthy') {
    box.hidden = true;
    return;
  }
  $('upstream-warning-text').textContent = t('upstream.downText', {
    profile: effective.label || effective.id,
    url: profileURL(effective),
    error: effective.last_error || t('health.unknown')
  });
  box.hidden = false;
}

function renderOverview(data) {
  overview = data;
  setText('relay-summary', `v${data.relay.version} · ${data.relay.listen} · pid ${data.relay.pid}`);
  const grid = $('status-grid');
  grid.textContent = '';
  const pairs = [
    [t('status.mode'), translatedValue('mode', data.mode)],
    [t('status.effective'), data.effective],
    [t('status.selected'), data.selected],
    [t('status.preferred'), data.preferred],
    [t('status.inflight'), data.inflight],
    [t('status.lastSwitch'), data.last_switch ? `${data.last_switch.from} → ${data.last_switch.to} (${data.last_switch.reason})` : null]
  ];
  for (const [label, value] of pairs) {
    const dt = document.createElement('dt');
    dt.textContent = label;
    const dd = document.createElement('dd');
    dd.textContent = value == null ? '—' : String(value);
    grid.append(dt, dd);
  }

  const lan = $('lan-warning');
  if (!data.relay.is_loopback || data.relay.allow_non_loopback) {
    lan.hidden = false;
    const nonLoop = (data.relay.interfaces || []).filter((i) => !i.loopback).map((i) => `${i.name} (${i.address})`).join(', ');
    $('lan-warning-text').textContent = t('lan.warning', {
      interfaces: nonLoop ? t('lan.warningOn', { interfaces: nonLoop }) : ''
    });
  } else {
    lan.hidden = true;
  }

  renderUpstreamWarning(data);

  const profileOptions = data.profiles.map((p) => p.id);
  fillSelect($('switch-profile'), profileOptions, data.effective);
  fillSelect($('mode-preferred'), profileOptions, data.preferred || data.effective);
  $('mode-select').value = data.mode || 'manual';

  const body = $('profiles-body');
  body.textContent = '';
  for (const p of data.profiles) {
    body.appendChild(row([
      p.id, p.label, `${p.scheme}://${p.host}:${p.port}`, translatedValue('health', p.health),
      p.latency_ms == null ? '—' : `${p.latency_ms} ms`, p.last_error || '—'
    ]));
  }

  const forms = $('endpoint-forms');
  forms.textContent = '';
  for (const p of data.profiles) {
    forms.appendChild(endpointForm(p));
  }

  const entryHost = (data.relay.listen || '').startsWith('[') ? data.relay.listen.slice(1, data.relay.listen.indexOf(']')) : (data.relay.listen.split(':')[0] || '');
  $('entry-ip').value = entryHost;
  $('entry-port').value = p_port(data.relay.listen);
}

function p_port(listen) {
  const m = /:(\d+)$/.exec(listen || '');
  return m ? m[1] : '';
}

function fillSelect(select, values, current) {
  select.textContent = '';
  for (const value of values) {
    const opt = document.createElement('option');
    opt.value = value;
    opt.textContent = value;
    if (value === current) opt.selected = true;
    select.appendChild(opt);
  }
}

function endpointForm(profile) {
  const form = document.createElement('form');
  form.className = 'inline-form endpoint-form';
  form.dataset.profile = profile.id;
  const title = document.createElement('strong');
  title.textContent = `${profile.id} — ${profile.label}`;
  form.appendChild(title);
  const status = document.createElement('p');
  status.className = 'muted endpoint-status';
  const health = translatedValue('health', profile.health);
  status.textContent = `${t('profiles.health')}: ${health}` + (profile.last_error ? ` · ${t('profiles.lastError')}: ${profile.last_error}` : '');
  form.appendChild(status);
  const scheme = document.createElement('select');
  for (const value of ['http', 'https']) {
    const opt = document.createElement('option');
    opt.value = value; opt.textContent = value;
    if (value === profile.scheme) opt.selected = true;
    scheme.appendChild(opt);
  }
  scheme.name = 'scheme';
  const host = document.createElement('input');
  host.name = 'host'; host.required = true; host.value = profile.host || '';
  const port = document.createElement('input');
  port.name = 'port'; port.type = 'number'; port.min = '1'; port.max = '65535'; port.value = profile.port || '';
  const submit = document.createElement('button');
  submit.type = 'submit'; submit.textContent = t('endpoints.apply');
  const use = document.createElement('button');
  use.type = 'button';
  const isActive = overview && overview.effective === profile.id;
  use.textContent = isActive ? t('endpoints.active') : t('endpoints.use');
  use.disabled = isActive;
  use.addEventListener('click', async () => {
    showError('');
    use.disabled = true;
    use.textContent = t('endpoints.switching');
    try {
      await api('/_shunt/api/switch', { method: 'POST', body: { profile: profile.id, probe: true } });
      await loadOverview();
    } catch (err) {
      showError(apiErrorMessage(err));
      use.disabled = false;
      use.textContent = t('endpoints.use');
    }
  });
  form.append(scheme, host, port, submit, use);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    showError('');
    try {
      await api(`/_shunt/api/profiles/${encodeURIComponent(profile.id)}/endpoint`, {
        method: 'POST',
        body: { scheme: scheme.value, host: host.value, port: Number(port.value) }
      });
      await loadOverview();
    } catch (err) { showError(apiErrorMessage(err)); }
  });
  return form;
}

async function loadOverview() {
  try {
    const data = await api('/_shunt/api/overview');
    renderOverview(data);
  } catch (err) {
    showError(apiErrorMessage(err));
  }
}

function isLoopbackIP(ip) {
  return ip === '::1' || ip === 'localhost' || ip.startsWith('127.');
}

async function submitEntry(confirmLan) {
  const payload = { ip: $('entry-ip').value.trim(), port: Number($('entry-port').value), confirm_lan: confirmLan };
  try {
    const res = await api('/_shunt/api/entry', { method: 'POST', body: payload });
    $('entry-notice').hidden = false;
    $('entry-notice').textContent = t('entry.changed', { url: res.url });
    setTimeout(() => { window.location.href = res.url; }, 1200);
  } catch (err) {
    if (err.code === 'lan_not_confirmed') {
      pendingEntry = payload;
      $('lan-modal').hidden = false;
      return;
    }
    showError(apiErrorMessage(err));
  }
}

function renderModels() {
  if (!lastModels) return;
  const cat = lastModels;
  const summary = t('models.summary', {
    total: cat.counts.total,
    free: cat.counts.free,
    paid: cat.counts.paid,
    unknown: cat.counts.unknown
  });
  $('models-summary').textContent = cat.stale ? t('models.stale', { summary }) : summary;
  const body = $('models-body');
  body.textContent = '';
  for (const m of cat.models || []) {
    body.appendChild(row([
      m.id,
      freeLabel(m.free),
      m.context_window == null ? '—' : m.context_window,
      m.max_output_tokens == null ? '—' : m.max_output_tokens,
      (m.capabilities || []).join(',')
    ]));
  }
}

async function loadModels(refresh) {
  const dialect = $('models-dialect').value;
  try {
    const query = `?dialect=${encodeURIComponent(dialect)}${refresh ? '&refresh=true' : ''}`;
    const cat = await api('/_shunt/api/models' + query);
    clientState.models[dialect] = cat.models || [];
    lastModels = cat;
    renderModels();
  } catch (err) {
    showError(apiErrorMessage(err));
  }
}

function setTab(name) {
  document.querySelectorAll('.tab').forEach((tab) => tab.classList.toggle('active', tab.dataset.tab === name));
  document.querySelectorAll('.panel').forEach((panel) => { panel.hidden = panel.id !== 'tab-' + name; });
  if (name === 'clients') loadClients();
}

document.querySelectorAll('.tab').forEach((tab) => {
  tab.addEventListener('click', () => setTab(tab.dataset.tab));
});
$('upstream-configure').addEventListener('click', () => setTab('endpoints'));

$('refresh').addEventListener('click', () => { showError(''); loadOverview(); });
$('switch-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  showError('');
  try {
    await api('/_shunt/api/switch', { method: 'POST', body: { profile: $('switch-profile').value, probe: $('switch-probe').checked } });
    await loadOverview();
  } catch (err) { showError(apiErrorMessage(err)); }
});
$('mode-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  showError('');
  try {
    await api('/_shunt/api/mode', { method: 'POST', body: { mode: $('mode-select').value, preferred: $('mode-preferred').value } });
    await loadOverview();
  } catch (err) { showError(apiErrorMessage(err)); }
});
$('entry-form').addEventListener('submit', (event) => {
  event.preventDefault();
  showError('');
  const ip = $('entry-ip').value.trim();
  if (!isLoopbackIP(ip)) {
    pendingEntry = { ip, port: Number($('entry-port').value), confirm_lan: true };
    $('lan-modal').hidden = false;
    return;
  }
  submitEntry(false);
});
$('lan-cancel').addEventListener('click', () => { $('lan-modal').hidden = true; pendingEntry = null; });
$('lan-confirm').addEventListener('click', () => {
  $('lan-modal').hidden = true;
  const payload = pendingEntry;
  pendingEntry = null;
  if (payload) submitEntry(true);
});
$('models-form').addEventListener('submit', (event) => {
  event.preventDefault();
  showError('');
  loadModels($('models-refresh').checked);
});

const clientState = {
  loaded: false,
  relay: '',
  clients: [],
  models: { anthropic: [], openai: [] }
};

function button(text, type) {
  const el = document.createElement('button');
  el.type = type;
  el.textContent = text;
  return el;
}

function field(labelText, control) {
  const label = document.createElement('label');
  label.textContent = labelText;
  label.appendChild(control);
  return label;
}

function modelSelect(dialect, name) {
  const input = document.createElement('input');
  input.name = name;
  input.dataset.dialect = dialect;
  input.placeholder = t('client.autoModel');
  input.autocomplete = 'off';
  input.setAttribute('list', `models-${dialect}`);
  ensureDatalist(dialect);
  fillModelList(dialect);
  return input;
}

function ensureDatalist(dialect) {
  const id = `models-${dialect}`;
  let list = document.getElementById(id);
  if (!list) {
    list = document.createElement('datalist');
    list.id = id;
    document.body.appendChild(list);
  }
  return list;
}

function fillModelList(dialect) {
  const list = ensureDatalist(dialect);
  list.textContent = '';
  for (const m of clientState.models[dialect] || []) {
    const opt = document.createElement('option');
    opt.value = m.id;
    opt.label = freeLabel(m.free);
    list.appendChild(opt);
  }
}

async function loadClientModels(dialects, status) {
  status.textContent = t('client.loadingModels');
  try {
    for (const dialect of dialects) {
      const cat = await api(`/_shunt/api/models?dialect=${encodeURIComponent(dialect)}`);
      clientState.models[dialect] = cat.models || [];
      fillModelList(dialect);
    }
    status.textContent = t('client.modelsLoaded');
  } catch (err) {
    status.textContent = t('client.modelsUnavailable', { error: apiErrorMessage(err) });
  }
}

function clientRequest(card) {
  const client = card.dataset.client;
  const value = (name) => {
    const el = card.querySelector(`[name="${name}"]`);
    return el ? el.value.trim() : '';
  };
  const checked = (name) => {
    const el = card.querySelector(`[name="${name}"]`);
    return !!(el && el.checked);
  };
  const req = { client };
  if (client === 'claude') {
    req.model = value('model');
    req.opus = value('opus');
    req.sonnet = value('sonnet');
    req.haiku = value('haiku');
    req.fable = value('fable');
    req.legacy = checked('legacy');
  } else if (client === 'codex') {
    req.model = value('model');
    req.force = checked('force');
  } else if (client === 'dsh') {
    req.api = value('api');
    req.anthropic_model = value('anthropic_model');
    req.openai_model = value('openai_model');
  }
  return req;
}

function clientNote(clientId, fallback) {
  const key = 'client.note.' + clientId;
  const text = t(key);
  return text === key ? (fallback || '') : text;
}

function appendCode(parent, title, content) {
  const head = document.createElement('div');
  head.className = 'client-output-head';
  const label = document.createElement('strong');
  label.textContent = title;
  const copy = button(t('client.copy'), 'button');
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(content);
      copy.textContent = t('client.copied');
      setTimeout(() => { copy.textContent = t('client.copy'); }, 1200);
    } catch {
      copy.textContent = t('client.copyFailed');
    }
  });
  head.append(label, copy);
  const pre = document.createElement('pre');
  pre.className = 'code';
  pre.textContent = content;
  parent.append(head, pre);
}

async function previewClient(card, status, output) {
  status.textContent = t('client.generating');
  const data = await api('/_shunt/api/clients/preview', { method: 'POST', body: clientRequest(card) });
  output.hidden = false;
  output.textContent = '';
  for (const warning of data.warnings || []) {
    const row = document.createElement('p');
    row.className = 'inline-warning';
    row.textContent = warning;
    output.appendChild(row);
  }
  appendCode(output, t('client.previewFor', { target: data.target }), data.content);
  if (data.snippet) {
    appendCode(output, t('client.envAlternative'), data.snippet);
  }
  if (data.note) {
    const note = document.createElement('p');
    note.className = 'muted';
    note.textContent = clientNote(card.dataset.client, data.note);
    output.appendChild(note);
  }
  status.textContent = (data.changed || []).length
    ? t('client.wouldUpdate', { keys: data.changed.join(', ') })
    : t('client.upToDate');
}

async function writeClient(card, status, output) {
  status.textContent = t('client.writing');
  const data = await api('/_shunt/api/clients/write', { method: 'POST', body: clientRequest(card) });
  output.hidden = false;
  output.textContent = '';
  const notice = document.createElement('div');
  notice.className = 'notice';
  const backup = data.backup ? ' ' + t('client.backupCreated', { path: data.backup }) : ' ' + t('client.noBackup');
  notice.textContent = t('client.wrote', { target: data.target, backup });
  output.appendChild(notice);
  status.textContent = (data.changed || []).length
    ? t('client.updated', { keys: data.changed.join(', ') })
    : t('client.noChange');
}

function clientRelayInfo() {
  const box = document.createElement('div');
  box.className = 'relay-info';
  const line = document.createElement('p');
  line.className = 'muted';
  line.append(t('clients.relay') + ' ');
  const code = document.createElement('code');
  code.textContent = clientState.relay || '—';
  line.appendChild(code);
  const copy = button(t('client.copy'), 'button');
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(clientState.relay);
      copy.textContent = t('client.copied');
      setTimeout(() => { copy.textContent = t('client.copy'); }, 1200);
    } catch {
      copy.textContent = t('client.copyFailed');
    }
  });
  line.appendChild(copy);
  const hint = document.createElement('p');
  hint.className = 'muted';
  hint.textContent = t('clients.relayHint');
  box.append(line, hint);
  return box;
}

function renderClients() {
  const grid = $('client-cards');
  if (!grid) return;
  grid.textContent = '';
  grid.appendChild(clientRelayInfo());
  for (const client of clientState.clients) {
    grid.appendChild(clientCard(client));
  }
}

function clientCard(client) {
  const card = document.createElement('article');
  card.className = 'client-card';
  card.dataset.client = client.id;

  const head = document.createElement('div');
  head.className = 'client-head';
  const title = document.createElement('h3');
  title.textContent = client.label;
  const badge = document.createElement('span');
  badge.className = 'badge';
  badge.textContent = client.write_supported ? t('client.writesWithBackup') : t('client.previewOnly');
  head.append(title, badge);

  const target = document.createElement('p');
  target.className = 'muted';
  target.append(t('client.target'));
  const targetCode = document.createElement('code');
  targetCode.textContent = client.target;
  target.appendChild(targetCode);

  const note = document.createElement('p');
  note.className = 'muted';
  note.textContent = clientNote(client.id, client.note);

  const form = document.createElement('form');
  form.className = 'client-form';
  const grid = document.createElement('div');
  grid.className = 'client-fields';
  form.appendChild(grid);

  if (client.id === 'claude') {
    const model = modelSelect('anthropic', 'model');
    grid.appendChild(field(t('client.mainModel'), model));

    const advanced = document.createElement('details');
    const summary = document.createElement('summary');
    summary.textContent = t('client.aliases');
    advanced.appendChild(summary);
    const aliases = document.createElement('div');
    aliases.className = 'client-fields';
    for (const [name, label] of [['opus', 'Opus'], ['sonnet', 'Sonnet'], ['haiku', 'Haiku'], ['fable', 'Fable']]) {
      const input = document.createElement('input');
      input.name = name;
      input.placeholder = t('client.aliasPlaceholder');
      aliases.appendChild(field(label, input));
    }
    const legacyLabel = document.createElement('label');
    legacyLabel.className = 'check';
    const legacy = document.createElement('input');
    legacy.type = 'checkbox';
    legacy.name = 'legacy';
    legacyLabel.append(legacy, ' ' + t('client.legacy'));
    aliases.appendChild(legacyLabel);
    advanced.appendChild(aliases);
    form.appendChild(advanced);
  } else if (client.id === 'codex') {
    const model = modelSelect('openai', 'model');
    grid.appendChild(field(t('client.model'), model));
    const forceLabel = document.createElement('label');
    forceLabel.className = 'check';
    const force = document.createElement('input');
    force.type = 'checkbox';
    force.name = 'force';
    forceLabel.append(force, ' ' + t('client.force'));
    grid.appendChild(forceLabel);
  } else if (client.id === 'dsh') {
    const apiSelect = document.createElement('select');
    apiSelect.name = 'api';
    for (const value of ['both', 'anthropic', 'openai']) {
      const opt = document.createElement('option');
      opt.value = value;
      opt.textContent = value;
      apiSelect.appendChild(opt);
    }
    grid.appendChild(field(t('client.api'), apiSelect));
    const anthropic = modelSelect('anthropic', 'anthropic_model');
    grid.appendChild(field(t('client.anthropicModel'), anthropic));
    const openai = modelSelect('openai', 'openai_model');
    grid.appendChild(field(t('client.openaiModel'), openai));
  }

  const actions = document.createElement('div');
  actions.className = 'actions';
  const load = button(t('client.loadModels'), 'button');
  const preview = button(t('client.preview'), 'submit');
  actions.append(load, preview);
  let write = null;
  let backups = null;
  if (client.write_supported) {
    write = button(t('client.write'), 'button');
    write.className = 'primary';
    actions.appendChild(write);
  }
  form.appendChild(actions);

  const status = document.createElement('p');
  status.className = 'muted client-status';
  const output = document.createElement('div');
  output.className = 'client-output';
  output.hidden = true;
  form.append(status, output);

  load.addEventListener('click', () => loadClientModels(client.dialects || [], status));
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    showError('');
    previewClient(card, status, output).catch((err) => { status.textContent = apiErrorMessage(err); });
  });
  if (write) {
    write.addEventListener('click', () => {
      if (!window.confirm(t('client.confirmWrite', { target: client.target }))) return;
      showError('');
      writeClient(card, status, output)
        .then(() => { if (backups) backups.refresh(); })
        .catch((err) => { status.textContent = apiErrorMessage(err); });
    });
  }

  card.append(head, target, note, form);
  if (client.write_supported) {
    backups = backupPanel(client);
    card.appendChild(backups.element);
  }
  return card;
}

function backupPanel(client) {
  const section = document.createElement('section');
  section.className = 'client-backups';
  const head = document.createElement('div');
  head.className = 'client-backups-head';
  const title = document.createElement('h4');
  title.textContent = t('backups.title');
  const refresh = button(t('refresh'), 'button');
  head.append(title, refresh);
  const status = document.createElement('p');
  status.className = 'muted client-status';
  const list = document.createElement('div');
  list.className = 'backup-list';
  const reload = () => loadClientBackups(client, list, status).catch((err) => { status.textContent = apiErrorMessage(err); });
  refresh.addEventListener('click', reload);
  section.append(head, status, list);
  reload();
  return { element: section, refresh: reload };
}

function formatBytes(value) {
  const size = Number(value) || 0;
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MiB`;
}

function formatTimestamp(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value || '') : date.toLocaleString(currentLanguage === 'fr' ? 'fr' : 'en');
}

async function loadClientBackups(client, list, status, quiet = false) {
  if (!quiet) status.textContent = t('backups.loading');
  const data = await api(`/_shunt/api/clients/backups?client=${encodeURIComponent(client.id)}`);
  const backups = data.backups || [];
  list.textContent = '';
  for (const backup of backups) {
    const rowEl = document.createElement('div');
    rowEl.className = 'backup-row';
    const name = document.createElement('code');
    name.textContent = backup.name;
    const meta = document.createElement('span');
    meta.className = 'muted';
    meta.textContent = `${formatBytes(backup.size)} · ${formatTimestamp(backup.modified)}`;
    const restore = button(t('backups.restore'), 'button');
    restore.className = 'danger';
    restore.addEventListener('click', () => {
      restoreClientBackup(client, backup, list, status).catch((err) => { status.textContent = apiErrorMessage(err); });
    });
    rowEl.append(name, meta, restore);
    list.appendChild(rowEl);
  }
  if (!quiet) {
    if (backups.length === 0) {
      status.textContent = t('backups.none');
    } else if (backups.length === 1) {
      status.textContent = t('backups.countOne', { dir: data.backup_dir });
    } else {
      status.textContent = t('backups.count', { count: backups.length, dir: data.backup_dir });
    }
  }
}

async function restoreClientBackup(client, backup, list, status) {
  if (!window.confirm(t('backups.confirmRestore', { name: backup.name, target: client.target }))) return;
  status.textContent = t('backups.restoring');
  const data = await api('/_shunt/api/clients/restore', {
    method: 'POST',
    body: { client: client.id, backup: backup.name }
  });
  await loadClientBackups(client, list, status, true);
  status.textContent = t('backups.restored', {
    name: data.restored,
    target: data.target,
    backup: data.backup || t('backups.noSafety')
  });
}

async function loadClients() {
  if (clientState.loaded) return;
  try {
    const data = await api('/_shunt/api/clients');
    clientState.clients = data.clients || [];
    clientState.relay = data.relay || '';
    renderClients();
    clientState.loaded = true;
  } catch (err) {
    showError(apiErrorMessage(err));
  }
}

applyLanguage(detectLanguage());
loadOverview();
