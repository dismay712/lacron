'use strict';
const $ = (id) => document.getElementById(id);
const paths = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  history: '<path d="M3 11a9 9 0 1 1 2.7 7.4M3 4v7h7"/><path d="M12 7v5l3 2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  x: '<path d="m6 6 12 12M6 18 18 6"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  terminal: '<path d="m5 6 6 6-6 6M13 18h6"/>',
  key: '<circle cx="8" cy="9" r="5"/><path d="m12 13 8 8m-4-4 3-3m-6 0 3-3"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="m9 3-1 3-3 1 1 3-2 2 2 2-1 3 3 1 1 3h6l1-3 3-1-1-3 2-2-2-2 1-3-3-1-1-3z"/>',
  copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 5H6a2 2 0 0 0-2 2v10"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
  play: '<path d="m8 4 12 8-12 8z"/>',
  edit: '<path d="m15 5 4 4M4 20l5-1L20 8a2.8 2.8 0 0 0-4-4L5 15z"/>',
  trash: '<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
  layers: '<path d="m12 3 10 5-10 5L2 8zM2 12l10 5 10-5M2 16l10 5 10-5"/>',
  'check-circle': '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  activity: '<path d="M2 12h5l3-8 4 16 3-8h5"/>',
  chart: '<path d="M4 4v16h17M8 15l4-5 4 2 5-7"/>',
  globe: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
  calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4M17 3v4M3 10h18M8 15h2M14 15h2"/>',
  logout: '<path d="M9 4H4v16h5M9 12h12m-5-5 5 5-5 5"/>',
  file: '<path d="M14 3H5v18h14V8zM14 3v5h5M8 12h8M8 16h6"/>',
  github: '<path d="M9 19c-4 1.2-4-2-6-2m12 5v-3.4c0-1 .1-1.4-.5-2 3.3-.4 6.8-1.6 6.8-7.3 0-1.6-.5-2.8-1.5-3.8.2-.4.6-1.9-.2-3.8 0 0-1.3-.4-4.1 1.5a14 14 0 0 0-7.5 0C5.2 1.3 4 1.7 4 1.7c-.8 1.9-.4 3.4-.2 3.8-1 1-1.5 2.2-1.5 3.8 0 5.7 3.5 6.9 6.8 7.3-.5.4-.8 1.2-.8 2.3V22"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5"/>',
  moon: '<path d="M21 13A9 9 0 0 1 11 3a9 9 0 1 0 10 10Z"/>',
  monitor: '<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>'
};
const icon = (name) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || ''}</svg>`;
function hydrateIcons(root = document) { root.querySelectorAll('[data-icon]').forEach((n) => { n.innerHTML = icon(n.dataset.icon); }); }
hydrateIcons();
const storage = {
  get(key, session = false) { try { return (session ? sessionStorage : localStorage).getItem(key); } catch { return null; } },
  set(key, value, session = false) { try { const s = session ? sessionStorage : localStorage; value === null ? s.removeItem(key) : s.setItem(key, value); } catch { /* Session still works in memory. */ } }
};
let theme = storage.get('lacron-theme') || 'system';
if (!['light', 'dark', 'system'].includes(theme)) theme = 'system';
const media = matchMedia('(prefers-color-scheme: dark)');
function applyTheme() {
  document.documentElement.dataset.theme = theme === 'system' ? (media.matches ? 'dark' : 'light') : theme;
  const labels = { system: '跟随系统', light: '浅色模式', dark: '深色模式' };
  document.querySelectorAll('.theme-button').forEach((b) => {
    b.innerHTML = icon({ system: 'monitor', light: 'sun', dark: 'moon' }[theme]);
    b.append(document.createTextNode(labels[theme]));
    b.setAttribute('aria-label', `${labels[theme]}，点击切换颜色主题`);
  });
}
document.querySelectorAll('.theme-button').forEach((b) => b.addEventListener('click', () => { theme = { system: 'light', light: 'dark', dark: 'system' }[theme]; storage.set('lacron-theme', theme); applyTheme(); }));
media.addEventListener('change', applyTheme);
applyTheme();

const sortFields = ['created', 'name', 'last', 'next', 'status'];
const collator = new Intl.Collator('zh-CN', { numeric: true });
const state = {
  active: false, generation: 0, filter: 'all', view: 'jobs',
  sort: sortFields.includes(storage.get('lacron-sort')) ? storage.get('lacron-sort') : 'created',
  descending: storage.get('lacron-sort-descending') === 'true',
  runQuery: 0, ...sessionData()
};
function sessionData() {
  return {
    jobs: [], runs: [], variables: [], system: {}, changes: {}, revisions: {},
    runJobs: new Map(), cursors: [''], runPage: 0, nextCursor: '', refreshing: null,
    settings: null, settingsDirty: false, passkeys: [], log: null, cleanup: null
  };
}
const statusNames = { running: '执行中', success: '成功', failed: '失败', timeout: '已超时', interrupted: '已中断', cancelled: '已停止' };
let toastTimer;
function toast(message, error = false) {
  clearTimeout(toastTimer);
  $('toast').textContent = message;
  $('toast').classList.toggle('error', error);
  $('toast').hidden = false;
  toastTimer = setTimeout(() => { $('toast').hidden = true; }, 4500);
}
function setError(id, message) { $(id).textContent = message || ''; $(id).hidden = !message; }
function showLogin() {
  state.active = false;
  state.generation++;
  Object.assign(state, sessionData());
  document.querySelectorAll('dialog[open]').forEach((d) => d.close());
  for (const id of ['job-rows', 'run-rows', 'variable-rows', 'passkey-list', 'log-output', 'log-meta', 'variable-job-options']) $(id).replaceChildren();
  $('run-filter').replaceChildren(new Option('全部任务', ''));
  $('run-status-filter').value = '';
  $('workspace').hidden = true;
  $('login').hidden = false;
  updateAuthStatus();
}
async function api(path, options = {}) {
  const generation = state.generation;
  const response = await fetch(`/api${path}`, {
    ...options,
    headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers },
    cache: 'no-store', credentials: 'same-origin'
  });
  if (response.status === 401 && !path.startsWith('/auth/') && generation === state.generation && state.active) showLogin();
  if (response.status === 204 || response.status === 202 && !response.headers.has('Content-Type')) return null;
  const data = await response.json();
  if (!response.ok) {
    const error = new Error(data.error || `请求失败 (${response.status})`);
    error.status = response.status;
    throw error;
  }
  return data;
}
function post(path, body) {
  return api(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
}
function connection(ok) {
  $('connection').querySelector('.status-dot').classList.toggle('offline', !ok);
  $('connection-text').textContent = ok ? '已连接' : '连接中断';
}
const dateFormats = new Map();
function date(value, long = false) {
  if (!value) return '—';
  const timeZone = state.system.timezone || 'UTC', key = `${timeZone}:${long}`;
  if (!dateFormats.has(key)) {
    dateFormats.set(key, new Intl.DateTimeFormat('zh-CN', {
      timeZone, ...(long ? { year: 'numeric' } : {}), month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false
    }));
  }
  return dateFormats.get(key).format(new Date(value));
}
function duration(run) {
  const seconds = Math.max(0, ((run.finished_at ? new Date(run.finished_at) : new Date()) - new Date(run.started_at)) / 1000);
  return seconds < 60 ? `${seconds.toFixed(1)}s` : `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
}
function badge(status) { const el = document.createElement('span'); el.className = `badge ${statusNames[status] ? status : ''}`; el.textContent = statusNames[status] || '尚未执行'; return el; }
function jobInput(job) { return { name: job.name, schedule: job.schedule, command: job.command, working_dir: job.working_dir, timeout_sec: job.timeout_sec, enabled: job.enabled }; }
const views = {
  jobs: ['任务调度', '管理调度计划和运行状态。'],
  runs: ['执行记录', '查看执行结果、实时输出和保留记录。'],
  variables: ['环境变量', '集中管理 Cookie 与配置，只授权给需要的任务。'],
  settings: ['系统设置', '调整运行上限和 Passkey 登录。']
};
function setView(view) {
  state.view = view;
  for (const name of Object.keys(views)) $(`${name}-view`).hidden = view !== name;
  $('overview-metrics').hidden = view !== 'jobs' && view !== 'runs';
  $('new-job').hidden = view !== 'jobs';
  $('new-variable').hidden = view !== 'variables';
  document.querySelectorAll('[data-view]').forEach((b) => {
    b.classList.toggle('selected', b.dataset.view === view);
    b.setAttribute('aria-current', b.dataset.view === view ? 'page' : 'false');
  });
  [$('page-title').textContent, $('page-description').textContent] = views[view];
  $('breadcrumb-current').textContent = views[view][0];
}
function visit(view) {
  setView(view);
  refresh(true).catch((error) => toast(error.message, true));
}
document.querySelectorAll('[data-view]').forEach((b) => b.addEventListener('click', () => visit(b.dataset.view)));
document.querySelectorAll('[data-filter]').forEach((b) => b.addEventListener('click', () => {
  state.filter = b.dataset.filter;
  document.querySelectorAll('[data-filter]').forEach((button) => button.classList.toggle('selected', button === b));
  renderJobs();
}));
$('search').addEventListener('input', renderJobs);
$('variable-search').addEventListener('input', renderVariables);
for (const id of ['run-filter', 'run-status-filter']) {
  $(id).addEventListener('change', () => { resetRunPage(); visit('runs'); });
}
$('job-sort').value = state.sort;
function renderSortDirection() {
  $('job-sort-direction').textContent = state.descending ? '降序 ↓' : '升序 ↑';
}
renderSortDirection();
$('job-sort').addEventListener('change', () => {
  state.sort = $('job-sort').value;
  storage.set('lacron-sort', state.sort);
  renderJobs();
});
$('job-sort-direction').addEventListener('click', () => {
  state.descending = !state.descending;
  storage.set('lacron-sort-descending', String(state.descending));
  renderSortDirection();
  renderJobs();
});
function jobSortValue(job) {
  switch (state.sort) {
    case 'name': return job.name;
    case 'last': return job.last_run?.started_at ? Date.parse(job.last_run.started_at) : null;
    case 'next': return job.next_run ? Date.parse(job.next_run) : null;
    case 'status': return job.running ? 0 : job.enabled ? 1 : 2;
    default: return Date.parse(job.created_at);
  }
}
function compareJobs(left, right) {
  const a = jobSortValue(left), b = jobSortValue(right);
  if (a === null || b === null) return Number(a === null) - Number(b === null);
  const difference = typeof a === 'string' ? collator.compare(a, b) : a - b;
  return state.descending ? -difference : difference;
}

// Keep unchanged rows in place so background updates preserve focus.
function syncRows(container, items, render) {
  const previous = new Map([...container.children].map((row) => [row.dataset.id, row]));
  let position = container.firstElementChild;
  for (const item of items) {
    const version = JSON.stringify(item);
    const old = previous.get(item.id);
    previous.delete(item.id);
    const row = old?.dataset.version === version ? old : render(item);
    row.dataset.id = item.id;
    row.dataset.version = version;
    if (old && row !== old) old.replaceWith(row);
    if (position === old) position = row;
    if (row !== position) container.insertBefore(row, position);
    position = row.nextElementSibling;
  }
  for (const row of previous.values()) row.remove();
}
function renderOverview() {
  $('metric-total').textContent = state.jobs.length;
  $('metric-enabled').textContent = state.jobs.filter((j) => j.enabled).length;
  $('metric-running').textContent = state.jobs.filter((j) => j.running).length;
  const { completed, successful } = state.changes;
  $('metric-success').textContent = completed ? `${Math.round(successful * 100 / completed)}%` : '—';
  $('metric-runs').textContent = completed ? `最近 ${completed} 次已完成执行` : '暂无执行记录';
  $('metric-concurrency').textContent = `并发上限 ${state.system.max_concurrent}`;
  $('nav-count').textContent = state.jobs.length;
  $('tab-total').textContent = state.jobs.length;
  $('runtime-node').textContent = state.system.node_version ? `Node.js ${state.system.node_version}` : 'Node.js 未安装';
  $('runtime-timezone').textContent = state.system.timezone;
  $('runtime-version').textContent = `lacron ${state.system.version}`;
  $('cron-label').textContent = state.system.cron_enabled ? (state.system.cron_running ? '秒级调度正在运行' : '调度未运行') : '定时调度已关闭';
  $('cron-dot').classList.toggle('offline', state.system.cron_enabled && !state.system.cron_running);
  $('cron-dot').classList.toggle('paused', !state.system.cron_enabled);
  $('runs-retention').textContent = `保留最近 ${state.system.max_runs} 次`;
  $('runs-limit').textContent = `新执行的日志最多 ${state.system.max_log_kib} KiB`;
  $('encryption-notice').hidden = state.system.encryption_configured;
}
function renderRunOptions() {
  const selected = $('run-filter').value;
  const options = new Map(state.jobs.map((j) => [j.id, j.name]));
  state.runJobs.forEach((name, id) => {
    if (!options.has(id)) options.set(id, `${name}（已删除）`);
  });
  const all = new Option('全部任务', '');
  $('run-filter').replaceChildren(all, ...[...options].map(([id, name]) => new Option(name, id)));
  $('run-filter').value = options.has(selected) ? selected : '';
}
function renderJobs() {
  const query = $('search').value.trim().toLocaleLowerCase();
  const jobs = state.jobs.filter((j) => (state.filter === 'all' || (state.filter === 'enabled' ? j.enabled : !j.enabled)) && `${j.name} ${j.command} ${j.schedule}`.toLocaleLowerCase().includes(query));
  jobs.sort(compareJobs);
  syncRows($('job-rows'), jobs, (job) => {
    const row = $('job-row-template').content.firstElementChild.cloneNode(true);
    row.querySelector('.job-name').textContent = job.name;
    row.querySelector('.job-command').textContent = job.command.replace(/\s+/g, ' ');
    row.querySelector('.job-command').title = job.command;
    row.querySelector('.schedule-code').textContent = job.schedule;
    row.querySelector('.job-last').append(badge(job.running ? 'running' : job.last_run?.status));
    row.querySelector('.job-last').title = job.last_run ? date(job.last_run.started_at, true) : '';
    row.querySelector('.job-next').textContent = job.enabled ? (job.next_run ? date(job.next_run) : '未来 8 年无匹配时间') : '已暂停';
    const toggle = row.querySelector('.toggle');
    toggle.setAttribute('aria-checked', String(job.enabled));
    toggle.setAttribute('aria-label', `${job.enabled ? '暂停' : '启用'} ${job.name}`);
    toggle.title = job.enabled ? '暂停调度' : '启用调度';
    toggle.addEventListener('click', async () => {
      toggle.disabled = true;
      try { await api(`/jobs/${job.id}`, { method: 'PUT', body: JSON.stringify({ ...jobInput(job), enabled: !job.enabled }) }); toast(job.enabled ? '已暂停调度' : '已启用调度'); await refresh(true); } catch (error) { toast(error.message, true); } finally { toggle.disabled = false; }
    });
    const run = row.querySelector('.run-job');
    run.disabled = job.running;
    run.setAttribute('aria-label', `立即执行 ${job.name}`);
    run.addEventListener('click', async () => { run.disabled = true; try { await api(`/jobs/${job.id}/run`, { method: 'POST' }); toast('任务已开始执行'); await refresh(true); } catch (error) { toast(error.message, true); } finally { run.disabled = job.running; } });
    row.querySelector('.edit-job').setAttribute('aria-label', `编辑 ${job.name}`);
    row.querySelector('.edit-job').addEventListener('click', () => editJob(job));
    row.querySelector('.job-history').setAttribute('aria-label', `查看 ${job.name} 的执行记录`);
    row.querySelector('.job-history').addEventListener('click', () => {
      $('run-filter').value = job.id;
      $('run-status-filter').value = '';
      resetRunPage();
      visit('runs');
    });
    const stop = row.querySelector('.stop-job');
    stop.hidden = !job.running || !job.last_run;
    stop.setAttribute('aria-label', `停止 ${job.name} 的当前执行`);
    stop.addEventListener('click', () => stopRun(job.last_run?.id, stop));
    row.querySelector('.copy-job').setAttribute('aria-label', `复制 ${job.name}`);
    row.querySelector('.copy-job').addEventListener('click', () => copyJob(job));
    row.querySelector('.delete-job').disabled = job.running;
    row.querySelector('.delete-job').setAttribute('aria-label', `删除 ${job.name}`);
    row.querySelector('.delete-job').addEventListener('click', () => confirmDelete('jobs', job));
    hydrateIcons(row);
    return row;
  });
  $('jobs-table').hidden = jobs.length === 0;
  $('jobs-empty').hidden = jobs.length !== 0;
  $('jobs-empty').querySelector('h2').textContent = state.jobs.length ? '没有找到匹配的任务。' : '第一项任务，从这里开始。';
  $('jobs-empty').querySelector('p').textContent = state.jobs.length ? '试试其他关键词或切换筛选条件。' : '定时运行 Shell 或 JavaScript 脚本，让日常工作自动发生。';
  $('empty-new-job').hidden = state.jobs.length !== 0;
  $('jobs-count').textContent = jobs.length === state.jobs.length ? `共 ${jobs.length} 个任务` : `显示 ${jobs.length} / ${state.jobs.length} 个任务`;
}
function renderRuns() {
  const runs = state.runs;
  syncRows($('run-rows'), runs, (run) => {
    const row = $('run-row-template').content.firstElementChild.cloneNode(true);
    row.querySelector('.run-name').textContent = run.job_name;
    row.querySelector('.run-name').title = run.job_name;
    row.querySelector('.run-status').append(badge(run.status));
    row.querySelector('.run-source').textContent = run.source === 'cron' ? '定时调度' : '手动执行';
    row.querySelector('.run-start').textContent = date(run.started_at);
    row.querySelector('.run-start').title = date(run.started_at, true);
    row.querySelector('.open-log').textContent = run.log_cleared ? '日志已清理' : '查看日志';
    row.querySelector('.open-log').setAttribute('aria-label', `查看 ${run.job_name} 的执行日志`);
    row.querySelector('.open-log').addEventListener('click', () => openLog(run));
    const stop = row.querySelector('.stop-run');
    stop.hidden = run.status !== 'running';
    stop.addEventListener('click', () => stopRun(run.id, stop));
    const clean = row.querySelector('.clean-run');
    clean.hidden = run.status === 'running';
    clean.setAttribute('aria-label', `清理 ${run.job_name} 的执行记录`);
    clean.addEventListener('click', () => openCleanup(run));
    hydrateIcons(row);
    return row;
  });
  updateRunDurations();
  $('runs-table').hidden = !runs.length;
  $('runs-empty').hidden = !!runs.length;
  $('runs-count').textContent = runs.length ? `第 ${state.runPage + 1} 页 · ${runs.length} 条记录` : '暂无匹配的执行记录';
  $('runs-prev').disabled = state.runPage === 0;
  $('runs-next').disabled = !state.nextCursor;
}
function updateRunDurations() {
  const rows = $('run-rows').children;
  state.runs.forEach((run, i) => { rows[i].querySelector('.run-duration').textContent = duration(run); });
}
function resetRunPage() {
  state.runQuery++;
  state.cursors = [''];
  state.runPage = 0;
  state.nextCursor = '';
  state.runs = [];
  renderRuns();
}
$('runs-prev').addEventListener('click', () => {
  if (state.runPage === 0) return;
  state.runPage--;
  state.runQuery++;
  visit('runs');
});
$('runs-next').addEventListener('click', () => {
  if (!state.nextCursor) return;
  state.cursors[++state.runPage] = state.nextCursor;
  state.runQuery++;
  state.nextCursor = '';
  $('runs-next').disabled = true;
  visit('runs');
});
function fetchRuns(cursor) {
  const query = new URLSearchParams({ limit: '50' });
  if (cursor) query.set('cursor', cursor);
  if ($('run-filter').value) query.set('job_id', $('run-filter').value);
  if ($('run-status-filter').value) query.set('status', $('run-status-filter').value);
  return api(`/runs?${query}`);
}
function refresh(afterMutation = false) {
  if (!state.active) return Promise.resolve();
  const generation = state.generation;
  if (state.refreshing) {
    return afterMutation
      ? state.refreshing.catch(() => {}).then(() => generation === state.generation && refresh())
      : state.refreshing;
  }
  const promise = refreshData(generation).catch((error) => {
    if (generation === state.generation) connection(false);
    throw error;
  }).finally(() => {
    if (state.refreshing === promise) state.refreshing = null;
  });
  state.refreshing = promise;
  return promise;
}
async function refreshData(generation) {
  const changes = await api('/changes');
  if (generation !== state.generation) return;
  const key = (resource) => `${changes.instance}:${changes[resource]}`;
  const changed = (resource) => state.revisions[resource] !== key(resource);
  const jobsDue = state.jobs.some((job) => job.next_run && Date.parse(job.next_run) <= changes.now);
  const needJobs = changed('jobs') || jobsDue;
  const needSystem = state.revisions.system !== key('settings');
  const needVariables = changed('variables');
  const needSettings = state.view === 'settings' && changed('settings');
  const query = state.runQuery, runKey = `${key('runs')}:${query}`, cursor = state.cursors[state.runPage];
  const needRuns = state.view === 'runs' && state.revisions.runs !== runKey;
  const [jobs, system, variables, settings, passkeys, page] = await Promise.all([
    needJobs && api('/jobs'),
    needSystem && api('/system'),
    needVariables && api('/variables'),
    needSettings && api('/settings'),
    needSettings && api('/passkeys'),
    needRuns && fetchRuns(cursor).catch((error) => {
      if (error.status !== 409 || !cursor) throw error;
      return fetchRuns('').then((fresh) => ({ ...fresh, reset: true }));
    })
  ]);
  if (generation !== state.generation) return;
  state.changes = changes;
  if (jobs) { state.jobs = jobs; state.revisions.jobs = key('jobs'); }
  if (system) { state.system = system; state.revisions.system = key('settings'); }
  if (variables) { state.variables = variables; state.revisions.variables = key('variables'); }
  if (settings) {
    state.settings = settings;
    state.passkeys = passkeys;
    state.revisions.settings = key('settings');
    renderSettings();
  }
  if (page && query === state.runQuery) {
    if (page.reset) { state.cursors = ['']; state.runPage = 0; }
    state.runs = page.items;
    state.nextCursor = page.next_cursor || '';
    for (const run of state.runs) state.runJobs.set(run.job_id, run.job_name);
    state.revisions.runs = runKey;
    renderRuns();
  } else if (state.view === 'runs') {
    updateRunDurations();
  }
  if (jobs || page) renderRunOptions();
  if (jobs) renderJobs();
  if (jobs || variables) renderVariables();
  renderOverview();
  connection(true);
  if (state.log) updateLog().catch(() => {});
}

function editJob(job = null) {
  state.editing = job?.id || null;
  $('job-form').reset();
  $('job-dialog-title').textContent = job ? '编辑任务' : '新建任务';
  $('save-job').textContent = job ? '保存更改' : '创建任务';
  $('job-name').value = job?.name || '';
  $('job-schedule').value = job?.schedule || '0 */5 * * * *';
  $('job-command').value = job?.command || 'node /scripts/hello.js';
  $('job-directory').value = job?.working_dir || '/scripts';
  $('job-timeout').value = job?.timeout_sec || 300;
  $('job-enabled').checked = job?.enabled ?? true;
  const granted = state.variables.filter((v) => v.job_ids.includes(job?.id)).map((v) => v.name);
  $('job-variables').textContent = granted.length ? granted.join('、') : '无。在环境变量页面勾选此任务后生效。';
  $('job-identity').textContent = job ? `运行身份 UID ${job.run_uid} / GID ${job.run_gid}。可写目录需允许此身份访问。` : '保存后分配独立的任务运行身份。';
  setError('job-error', '');
  $('job-dialog').showModal();
}
$('new-job').addEventListener('click', () => editJob());
$('empty-new-job').addEventListener('click', () => editJob());
$('schedule-preset').addEventListener('change', () => { if ($('schedule-preset').value) $('job-schedule').value = $('schedule-preset').value; });
document.querySelectorAll('.close-dialog').forEach((b) => b.addEventListener('click', () => b.closest('dialog').close()));
$('job-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('save-job').disabled = true;
  setError('job-error', '');
  const input = { name: $('job-name').value, schedule: $('job-schedule').value, command: $('job-command').value, working_dir: $('job-directory').value, timeout_sec: Number($('job-timeout').value), enabled: $('job-enabled').checked };
  try { await api(state.editing ? `/jobs/${state.editing}` : '/jobs', { method: state.editing ? 'PUT' : 'POST', body: JSON.stringify(input) }); $('job-dialog').close(); toast(state.editing ? '任务已更新' : '任务已创建'); setView('jobs'); await refresh(true); } catch (error) { setError('job-error', error.message); } finally { $('save-job').disabled = false; }
});
$('confirm-delete').addEventListener('click', async () => {
  $('confirm-delete').disabled = true;
  try { await api(`/${state.deletingKind}/${state.deleting}`, { method: 'DELETE' }); $('confirm-dialog').close(); toast({ jobs: '任务已删除', variables: '变量已删除', passkeys: 'Passkey 已移除' }[state.deletingKind]); await refresh(true); } catch (error) { setError('delete-error', error.message); } finally { $('confirm-delete').disabled = false; }
});

function confirmDelete(kind, item) {
  state.deletingKind = kind; state.deleting = item.id;
  const labels = { jobs: '任务', variables: '变量', passkeys: 'Passkey' };
  const action = kind === 'passkeys' ? '移除' : '删除';
  $('confirm-title').textContent = `${action}这个${labels[kind]}？`;
  $('confirm-delete').textContent = `${action}${labels[kind]}`;
  $('confirm-description').textContent = `将${action}「${item.name}」。此操作无法撤销。`;
  $('confirm-note').textContent = {
    variables: '后续运行会应用变更，已在运行的进程会保留原有环境。',
    jobs: '删除后将停止调度，已有执行记录会继续保留。',
    passkeys: '将撤销这个凭据的登录会话。可使用管理令牌重新登录。'
  }[kind];
  setError('delete-error', ''); $('confirm-dialog').showModal();
}

function renderVariables() {
  const query = $('variable-search').value.trim().toLocaleLowerCase();
  const variables = state.variables.filter((v) => `${v.name} ${v.description}`.toLocaleLowerCase().includes(query));
  const fragment = document.createDocumentFragment();
  for (const variable of variables) {
    const row = $('variable-row-template').content.cloneNode(true);
    row.querySelector('.variable-name').textContent = variable.name;
    row.querySelector('.variable-description').textContent = variable.description || '无描述';
    const grants = state.jobs.filter((j) => variable.job_ids.includes(j.id)).map((j) => j.name);
    row.querySelector('.variable-grants').textContent = grants.length ? grants.join('、') : '未授权任何任务';
    row.querySelector('.variable-updated').textContent = date(variable.updated_at);
    row.querySelector('.edit-variable').setAttribute('aria-label', `编辑变量 ${variable.name}`);
    row.querySelector('.edit-variable').addEventListener('click', () => editVariable(variable));
    row.querySelector('.delete-variable').setAttribute('aria-label', `删除变量 ${variable.name}`);
    row.querySelector('.delete-variable').addEventListener('click', () => confirmDelete('variables', variable));
    hydrateIcons(row); fragment.append(row);
  }
  $('variable-rows').replaceChildren(fragment);
  $('variables-table').hidden = !variables.length;
  $('variables-empty').hidden = !!variables.length;
  $('variables-empty').querySelector('h2').textContent = state.variables.length ? '没有找到匹配的变量。' : '配置一次，按需授权。';
  $('empty-new-variable').hidden = !!state.variables.length;
  $('variables-count').textContent = `共 ${state.variables.length} 个变量`;
}

function editVariable(variable = null) {
  state.editingVariable = variable?.id || null;
  $('variable-form').reset();
  $('variable-dialog-title').textContent = variable ? '编辑变量与授权' : '新建变量';
  $('save-variable').textContent = variable ? '保存更改' : '创建变量';
  $('variable-name').value = variable?.name || '';
  $('variable-description').value = variable?.description || '';
  $('variable-value').required = !variable;
  $('variable-value').placeholder = variable ? '留空保留原值；输入新值则替换' : '粘贴 Cookie 或其他配置值';
  $('variable-value-help').textContent = variable ? '已有值不回显。仅修改授权时，变量值留空即可。' : '保存后不回显变量值。';
  const options = $('variable-job-options'); options.replaceChildren();
  for (const job of state.jobs) {
    const label = document.createElement('label'); label.className = 'grant-option';
    const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.value = job.id; checkbox.checked = !!variable?.job_ids.includes(job.id);
    const text = document.createElement('span'); const name = document.createElement('strong'); name.textContent = job.name;
    const command = document.createElement('code'); command.textContent = job.command.replace(/\s+/g, ' ');
    text.append(name, command); label.append(checkbox, text); options.append(label);
  }
  if (!state.jobs.length) { const p = document.createElement('p'); p.className = 'field-help'; p.textContent = '尚无任务。可先保存变量，再创建任务并回来授权。'; options.append(p); }
  updateGrantCount(); setError('variable-error', ''); $('variable-dialog').showModal();
}
function updateGrantCount() { $('variable-grant-count').textContent = `已选择 ${$('variable-job-options').querySelectorAll('input:checked').length} 项`; }
$('variable-job-options').addEventListener('change', updateGrantCount);
$('new-variable').addEventListener('click', () => editVariable());
$('empty-new-variable').addEventListener('click', () => editVariable());
$('variable-dialog').addEventListener('close', () => { $('variable-value').value = ''; });
$('variable-form').addEventListener('submit', async (event) => {
  event.preventDefault(); $('save-variable').disabled = true; setError('variable-error', '');
  const input = { name: $('variable-name').value, description: $('variable-description').value, job_ids: [...$('variable-job-options').querySelectorAll('input:checked')].map((c) => c.value) };
  if (!state.editingVariable || $('variable-value').value !== '') input.value = $('variable-value').value;
  try {
    await api(state.editingVariable ? `/variables/${state.editingVariable}` : '/variables', { method: state.editingVariable ? 'PUT' : 'POST', body: JSON.stringify(input) });
    $('variable-dialog').close(); toast('变量与授权已保存'); await refresh(true);
  } catch (error) { setError('variable-error', error.message); } finally { $('save-variable').disabled = false; }
});
function copyJob(job) {
  state.copyID = job.id;
  let name = job.name;
  const encoder = new TextEncoder(), suffix = '（副本）';
  while (encoder.encode(name + suffix).length > 120) name = [...name].slice(0, -1).join('');
  $('copy-name').value = name + suffix;
  setError('copy-error', '');
  $('copy-dialog').showModal();
}
$('copy-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('copy-submit').disabled = true;
  setError('copy-error', '');
  try {
    await post(`/jobs/${state.copyID}/copy`, { name: $('copy-name').value });
    $('copy-dialog').close();
    toast('已复制任务');
    await refresh(true);
  } catch (error) { setError('copy-error', error.message); }
  finally { $('copy-submit').disabled = false; }
});
async function stopRun(id, button) {
  if (!id) return;
  button.disabled = true;
  try {
    await post(`/runs/${id}/stop`);
    toast('已请求停止本次执行');
    await refresh(true);
  } catch (error) { toast(error.message, true); }
  finally { button.disabled = false; }
}

function openCleanup(run = null) {
  state.cleanup = { run, approved: null };
  $('cleanup-form').reset();
  $('cleanup-single').textContent = run ? `${run.job_name} · ${date(run.started_at, true)}` : '';
  $('cleanup-single').hidden = !run;
  $('cleanup-scope').hidden = !!run;
  $('cleanup-job').replaceChildren(...[...$('run-filter').options].map((option) => option.cloneNode(true)));
  $('cleanup-job').value = $('run-filter').value;
  invalidateCleanup();
  $('cleanup-dialog').showModal();
}
function invalidateCleanup() {
  state.cleanup.approved = null;
  $('apply-cleanup').disabled = true;
  $('cleanup-preview').textContent = '';
  setError('cleanup-error', '');
}
$('clean-logs').addEventListener('click', () => openCleanup());
$('cleanup-form').addEventListener('input', invalidateCleanup);
function cleanupInput() {
  const input = { mode: $('cleanup-mode').value };
  if (state.cleanup.run) {
    input.run_id = state.cleanup.run.id;
  } else {
    if ($('cleanup-job').value) input.job_id = $('cleanup-job').value;
    if ($('cleanup-before').value) input.before = new Date($('cleanup-before').value + 'T00:00:00').toISOString();
    if (!input.job_id && !input.before) input.all = true;
  }
  return input;
}
$('cleanup-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  invalidateCleanup();
  const cleanup = state.cleanup, input = cleanupInput(), key = JSON.stringify(input);
  $('preview-cleanup').disabled = true;
  try {
    const result = await post('/runs/cleanup/preview', input);
    if (cleanup !== state.cleanup || !$('cleanup-dialog').open || key !== JSON.stringify(cleanupInput())) return;
    cleanup.approved = { ...input, cutoff: result.cutoff };
    $('cleanup-preview').textContent = `将清理 ${result.matched} 条，跳过 ${result.skipped} 条。`;
    $('apply-cleanup').disabled = !result.matched;
  } catch (error) { setError('cleanup-error', error.message); }
  finally { $('preview-cleanup').disabled = false; }
});
$('apply-cleanup').addEventListener('click', async () => {
  const input = state.cleanup.approved;
  if (!input) return;
  $('apply-cleanup').disabled = true;
  try {
    const result = await post('/runs/cleanup', input);
    $('cleanup-dialog').close();
    toast(`已清理 ${result.matched} 条${result.pending ? `，${result.pending} 个文件将在重启时重试删除` : ''}`);
    await refresh(true);
  } catch (error) {
    setError('cleanup-error', error.message);
    $('apply-cleanup').disabled = !state.cleanup?.approved;
  }
});

function openLog(run) {
  state.log = { run, offset: 0, loaded: false, more: true, pending: false };
  $('log-output').textContent = '正在读取日志…';
  $('log-truncated').hidden = true;
  renderLogMeta(run);
  $('log-dialog').showModal();
  updateLog().catch(() => {});
}
function renderLogMeta(run) {
  $('log-title').textContent = run.job_name;
  const meta = $('log-meta');
  meta.replaceChildren(badge(run.status));
  const values = [date(run.started_at, true), duration(run), run.source === 'cron' ? '定时调度' : '手动执行'];
  if (run.exit_code != null) values.splice(2, 0, `退出码 ${run.exit_code}`);
  for (const text of values) {
    const span = document.createElement('span');
    span.textContent = text;
    meta.append(span);
  }
  $('log-truncated').hidden = !run.truncated;
  $('log-stop').hidden = run.status !== 'running';
  $('log-clean').hidden = run.status === 'running';
  $('log-refresh-status').textContent = run.status === 'running' ? '每 5 秒读取新输出' : '';
}
async function updateLog() {
  const log = state.log;
  if (!log || log.pending || log.missing) return;
  const revision = `${state.changes.instance}:${state.changes.runs}`;
  if (log.loaded && !log.more && log.run.status !== 'running' && log.revision === revision) return;
  log.pending = true;
  try {
    do {
      const chunk = await api(`/runs/${log.run.id}/log?offset=${log.offset}&limit=131072`);
      if (state.log !== log) return;
      const output = $('log-output');
      const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 50;
      if (!log.offset || chunk.reset) output.replaceChildren();
      if (chunk.output) output.append(document.createTextNode(chunk.output));
      if (!chunk.next_offset && !chunk.output) {
        output.textContent = chunk.run.log_cleared ? '本次日志已清理。' :
          chunk.run.status === 'running' ? '正在执行，等待输出…' : '本次执行没有输出。';
      }
      if (atBottom) output.scrollTop = output.scrollHeight;
      log.more = chunk.more && chunk.next_offset !== log.offset;
      log.offset = chunk.next_offset;
      log.run = chunk.run;
      log.loaded = true;
      log.revision = revision;
      renderLogMeta(log.run);
    } while (log.more);
  } catch (error) {
    if (state.log !== log) return;
    if (error.status === 404) {
      log.missing = true;
      $('log-output').textContent = '这条执行记录已删除或超过保留上限。';
      $('log-stop').hidden = $('log-clean').hidden = $('log-truncated').hidden = true;
      $('log-refresh-status').textContent = '';
    } else {
      $('log-refresh-status').textContent = error.message;
    }
  } finally {
    log.pending = false;
  }
}
$('log-stop').addEventListener('click', () => stopRun(state.log?.run.id, $('log-stop')));
$('log-clean').addEventListener('click', () => { if (state.log) openCleanup(state.log.run); });
$('log-dialog').addEventListener('close', () => { if (!$('log-dialog').open) state.log = null; });

function canUsePasskeys() {
  return window.isSecureContext && !!window.PublicKeyCredential && !!navigator.credentials;
}
function renderSettings() {
  const settings = state.settings;
  $('save-settings').disabled = false;
  if (!state.settingsDirty) {
    $('settings-concurrent').value = settings.max_concurrent;
    $('settings-runs').value = settings.max_runs;
    $('settings-log').value = settings.max_log_kib;
    $('settings-passkey-enabled').checked = settings.passkey_enabled;
    $('settings-origin').value = settings.passkey_origin;
  }
  const ready = settings.passkey_enabled && settings.passkey_origin === location.origin && canUsePasskeys();
  $('add-passkey').disabled = !ready;
  $('passkey-hint').textContent = !settings.passkey_enabled ? '请先启用 Passkey 并保存设置。' :
    ready ? '可为多个设备分别绑定。' : '请从已配置的站点地址用支持 Passkey 的浏览器打开。';
  const list = $('passkey-list');
  syncRows(list, state.passkeys, (key) => {
    const item = document.createElement('li');
    const details = document.createElement('div');
    const title = document.createElement('strong');
    title.textContent = key.name;
    const caption = document.createElement('p');
    caption.className = 'small muted';
    caption.textContent = `绑定于 ${date(key.created_at, true)}${key.last_used_at ? ` · 最近使用 ${date(key.last_used_at, true)}` : ''}`;
    details.append(title, caption);
    const remove = document.createElement('button');
    remove.className = 'text-button warning-action';
    remove.type = 'button';
    remove.textContent = '移除';
    remove.setAttribute('aria-label', `移除 Passkey ${key.name}`);
    remove.addEventListener('click', () => confirmDelete('passkeys', key));
    item.append(details, remove);
    return item;
  });
  $('passkey-hint').textContent += state.passkeys.length ? '' : ' 尚未绑定设备。';
}
$('settings-form').addEventListener('input', () => { state.settingsDirty = true; });
$('settings-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const input = {
    max_concurrent: Number($('settings-concurrent').value),
    max_runs: Number($('settings-runs').value),
    max_log_kib: Number($('settings-log').value),
    passkey_enabled: $('settings-passkey-enabled').checked,
    passkey_origin: $('settings-origin').value.trim()
  };
  if (state.settings && input.max_runs < state.settings.max_runs && !window.confirm('降低记录上限可能立即清理最早的执行记录和日志。保存设置？')) return;
  $('save-settings').disabled = true;
  setError('settings-error', '');
  try {
    state.settings = await api('/settings', { method: 'PUT', body: JSON.stringify(input) });
    state.settingsDirty = false;
    renderSettings();
    toast('系统设置已保存');
    await refresh(true);
  } catch (error) { setError('settings-error', error.message); }
  finally { $('save-settings').disabled = false; }
});

function decodeWebAuthn(value) {
  return Uint8Array.from(atob(value.replace(/-/g, '+').replace(/_/g, '/')), (byte) => byte.charCodeAt(0));
}
function encodeWebAuthn(value) {
  if (!value) return null;
  const bytes = new Uint8Array(value);
  let result = '';
  for (const byte of bytes) result += String.fromCharCode(byte);
  return btoa(result).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
function credentialOptions(options, register) {
  const key = options.publicKey;
  if (register && PublicKeyCredential.parseCreationOptionsFromJSON) {
    return PublicKeyCredential.parseCreationOptionsFromJSON(key);
  }
  if (!register && PublicKeyCredential.parseRequestOptionsFromJSON) {
    return PublicKeyCredential.parseRequestOptionsFromJSON(key);
  }
  key.challenge = decodeWebAuthn(key.challenge);
  if (key.user) key.user.id = decodeWebAuthn(key.user.id);
  for (const list of [key.excludeCredentials, key.allowCredentials]) {
    for (const credential of list || []) credential.id = decodeWebAuthn(credential.id);
  }
  return key;
}
function credentialJSON(credential, register) {
  if (credential.toJSON) return credential.toJSON();
  const response = credential.response;
  return {
    id: credential.id,
    rawId: encodeWebAuthn(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: encodeWebAuthn(response.clientDataJSON),
      ...(register ? { attestationObject: encodeWebAuthn(response.attestationObject), transports: response.getTransports?.() } :
        { authenticatorData: encodeWebAuthn(response.authenticatorData), signature: encodeWebAuthn(response.signature), userHandle: encodeWebAuthn(response.userHandle) })
    }
  };
}
async function usePasskey(register, body, signal) {
  const path = register ? '/passkeys' : '/auth/passkey';
  const challenge = await post(`${path}/begin`, body);
  const publicKey = credentialOptions(challenge.options, register);
  const credential = await (register ? navigator.credentials.create({ publicKey, signal }) : navigator.credentials.get({ publicKey, signal }));
  if (!credential) throw new Error('设备未返回 Passkey');
  return post(`${path}/finish`, { challenge_id: challenge.challenge_id, credential: credentialJSON(credential, register) });
}
function passkeyError(error) {
  return error.name === 'NotAllowedError' || error.name === 'AbortError' ? '设备验证已取消或超时，请重试。' : error.message;
}
$('add-passkey').addEventListener('click', () => {
  $('passkey-form').reset();
  setError('passkey-error', '');
  $('passkey-dialog').showModal();
});
let registration;
$('passkey-dialog').addEventListener('close', () => { if (!$('passkey-dialog').open) registration?.abort(); });
$('passkey-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  $('passkey-submit').disabled = true;
  setError('passkey-error', '');
  registration = new AbortController();
  try {
    await usePasskey(true, { name: $('passkey-name').value }, registration.signal);
    $('passkey-dialog').close();
    toast('Passkey 已绑定');
    await refresh(true);
  } catch (error) {
    if ($('passkey-dialog').open) setError('passkey-error', passkeyError(error));
  } finally {
    registration = null;
    $('passkey-submit').disabled = false;
  }
});
async function updateAuthStatus() {
  const generation = state.generation;
  $('passkey-login').hidden = true;
  try {
    const data = await api('/auth/status');
    if (generation === state.generation && !state.active) {
      $('passkey-login').hidden = !data.passkey_ready || data.passkey_origin !== location.origin || !canUsePasskeys();
    }
  } catch { /* Token login remains available. */ }
}
async function connect() {
  state.active = true;
  setView('jobs');
  $('token').value = '';
  await refresh();
  if (state.active) {
    $('login').hidden = true;
    $('workspace').hidden = false;
  }
}
function tokenLogin(token) {
  return api('/auth/token', { method: 'POST', headers: { Authorization: `Bearer ${token}` } });
}
const loginSubmit = $('login-form').querySelector('button[type="submit"]');
function loginBusy(busy) { loginSubmit.disabled = $('passkey-login').disabled = busy; }
$('logout').addEventListener('click', async () => {
  $('logout').disabled = true;
  try {
    await post('/auth/logout');
    $('token').value = '';
    showLogin();
  } catch (error) { toast(error.message, true); }
  finally { $('logout').disabled = false; }
});
$('login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  loginBusy(true);
  setError('login-error', '');
  try {
    await tokenLogin($('token').value.trim());
    await connect();
  } catch (error) { setError('login-error', error.message); }
  finally { loginBusy(false); }
});
$('passkey-login').addEventListener('click', async () => {
  loginBusy(true);
  setError('login-error', '');
  try {
    await usePasskey(false);
    await connect();
  } catch (error) { setError('login-error', passkeyError(error)); }
  finally { loginBusy(false); }
});
function poll() {
  if (state.active && !document.hidden) refresh().catch(() => {});
}
setInterval(poll, 5000);
document.addEventListener('visibilitychange', poll);
(async function restoreSession() {
  loginBusy(true);
  const previous = storage.get('lacron-token', true);
  storage.set('lacron-token', null, true);
  try {
    if (previous) await tokenLogin(previous).catch(() => {});
    await connect();
  } catch (error) {
    if (state.active) showLogin();
    if (error.status !== 401) setError('login-error', error.message);
  } finally { loginBusy(false); }
})();
