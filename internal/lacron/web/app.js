'use strict';
const $ = (id) => document.getElementById(id);
const paths = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  history: '<path d="M3 11a9 9 0 1 1 2.7 7.4M3 4v7h7"/><path d="M12 7v5l3 2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  x: '<path d="m6 6 12 12M6 18 18 6"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>',
  terminal: '<path d="m5 6 6 6-6 6M13 18h6"/>',
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

const state = { token: storage.get('lacron-token', true) || '', jobs: [], runs: [], system: {}, filter: 'all', view: 'jobs', editing: null, deleting: null, logID: null, refreshing: false };
const statusNames = { running: '执行中', success: '成功', failed: '失败', timeout: '已超时', interrupted: '已中断' };
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
  state.token = '';
  storage.set('lacron-token', null, true);
  document.querySelectorAll('dialog[open]').forEach((d) => d.close());
  $('workspace').hidden = true;
  $('login').hidden = false;
}
async function api(path, options = {}) {
  const token = state.token;
  const response = await fetch(`/api${path}`, { ...options, headers: { Authorization: `Bearer ${token}`, ...(options.body ? { 'Content-Type': 'application/json' } : {}) }, cache: 'no-store' });
  if (response.status === 401) { if (state.token === token) showLogin(); throw new Error('访问令牌无效，请重新登录'); }
  if (response.status === 204) return null;
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `请求失败 (${response.status})`);
  return data;
}
function connection(ok) {
  $('connection').querySelector('.status-dot').classList.toggle('offline', !ok);
  $('connection-text').textContent = ok ? '已连接' : '连接中断';
}
function date(value, long = false) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { timeZone: state.system.timezone || 'UTC', ...(long ? { year: 'numeric' } : {}), month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', ...(long ? { second: '2-digit' } : {}), hour12: false }).format(new Date(value));
}
function duration(run) {
  const seconds = Math.max(0, ((run.finished_at ? new Date(run.finished_at) : new Date()) - new Date(run.started_at)) / 1000);
  return seconds < 60 ? `${seconds.toFixed(1)}s` : `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
}
function badge(status) { const el = document.createElement('span'); el.className = `badge ${statusNames[status] ? status : ''}`; el.textContent = statusNames[status] || '尚未执行'; return el; }
function jobInput(job) { return { name: job.name, schedule: job.schedule, command: job.command, working_dir: job.working_dir, timeout_sec: job.timeout_sec, enabled: job.enabled }; }
function setView(view) {
  state.view = view;
  $('jobs-view').hidden = view !== 'jobs';
  $('runs-view').hidden = view !== 'runs';
  document.querySelectorAll('[data-view]').forEach((b) => { b.classList.toggle('selected', b.dataset.view === view); b.setAttribute('aria-current', b.dataset.view === view ? 'page' : 'false'); });
  $('page-title').textContent = view === 'jobs' ? '任务调度' : '执行记录';
  $('breadcrumb-current').textContent = $('page-title').textContent;
  $('page-description').textContent = view === 'jobs' ? '把重复的工作，交给准时的每一次执行。' : '查看每一次运行，了解任务的每一步。';
}
document.querySelectorAll('[data-view]').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
document.querySelectorAll('[data-filter]').forEach((b) => b.addEventListener('click', () => {
  state.filter = b.dataset.filter;
  document.querySelectorAll('[data-filter]').forEach((button) => button.classList.toggle('selected', button === b));
  renderJobs();
}));
$('search').addEventListener('input', renderJobs);
$('run-filter').addEventListener('change', renderRuns);

function renderOverview() {
  $('metric-total').textContent = state.jobs.length;
  $('metric-enabled').textContent = state.jobs.filter((j) => j.enabled).length;
  $('metric-running').textContent = state.jobs.filter((j) => j.running).length;
  const completed = state.runs.filter((r) => r.status !== 'running');
  $('metric-success').textContent = completed.length ? `${Math.round(completed.filter((r) => r.status === 'success').length * 100 / completed.length)}%` : '—';
  $('metric-runs').textContent = completed.length ? `最近 ${completed.length} 次已完成执行` : '暂无执行记录';
  $('metric-concurrency').textContent = `并发上限 ${state.system.max_concurrent}`;
  $('nav-count').textContent = state.jobs.length;
  $('tab-total').textContent = state.jobs.length;
  $('runtime-node').textContent = state.system.node_version ? `Node.js ${state.system.node_version}` : 'Node.js 未安装';
  $('runtime-timezone').textContent = state.system.timezone;
  $('runtime-version').textContent = `lacron ${state.system.version}`;
  $('cron-label').textContent = state.system.cron_enabled ? (state.system.cron_running ? 'Cron 正在运行' : 'Cron 未运行') : '开发模式 · Cron 已关闭';
  $('cron-dot').classList.toggle('offline', state.system.cron_enabled && !state.system.cron_running);
  $('cron-dot').classList.toggle('paused', !state.system.cron_enabled);
  const selected = $('run-filter').value;
  const options = new Map(state.jobs.map((j) => [j.id, j.name]));
  state.runs.forEach((r) => { if (!options.has(r.job_id)) options.set(r.job_id, `${r.job_name}（已删除）`); });
  const all = document.createElement('option'); all.value = ''; all.textContent = '全部任务';
  $('run-filter').replaceChildren(all);
  options.forEach((name, id) => { const option = document.createElement('option'); option.value = id; option.textContent = name; $('run-filter').append(option); });
  $('run-filter').value = options.has(selected) ? selected : '';
}
function renderJobs() {
  const query = $('search').value.trim().toLocaleLowerCase();
  const jobs = state.jobs.filter((j) => (state.filter === 'all' || (state.filter === 'enabled' ? j.enabled : !j.enabled)) && `${j.name} ${j.command} ${j.schedule}`.toLocaleLowerCase().includes(query));
  const fragment = document.createDocumentFragment();
  for (const job of jobs) {
    const row = $('job-row-template').content.cloneNode(true);
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
      try { await api(`/jobs/${job.id}`, { method: 'PUT', body: JSON.stringify({ ...jobInput(job), enabled: !job.enabled }) }); toast(job.enabled ? '已暂停调度' : '已启用调度'); await refresh(); } catch (error) { toast(error.message, true); } finally { toggle.disabled = false; }
    });
    const run = row.querySelector('.run-job');
    run.disabled = job.running;
    run.setAttribute('aria-label', `立即执行 ${job.name}`);
    run.addEventListener('click', async () => { run.disabled = true; try { await api(`/jobs/${job.id}/run`, { method: 'POST' }); toast('任务已开始执行'); await refresh(); } catch (error) { toast(error.message, true); } finally { run.disabled = job.running; } });
    row.querySelector('.edit-job').setAttribute('aria-label', `编辑 ${job.name}`);
    row.querySelector('.edit-job').addEventListener('click', () => editJob(job));
    row.querySelector('.job-history').setAttribute('aria-label', `查看 ${job.name} 的执行记录`);
    row.querySelector('.job-history').addEventListener('click', () => { $('run-filter').value = job.id; renderRuns(); setView('runs'); });
    row.querySelector('.delete-job').disabled = job.running;
    row.querySelector('.delete-job').setAttribute('aria-label', `删除 ${job.name}`);
    row.querySelector('.delete-job').addEventListener('click', () => { state.deleting = job.id; $('confirm-description').textContent = `将删除「${job.name}」。此操作无法撤销。`; setError('delete-error', ''); $('confirm-dialog').showModal(); });
    hydrateIcons(row); fragment.append(row);
  }
  $('job-rows').replaceChildren(fragment);
  $('jobs-table').hidden = jobs.length === 0;
  $('jobs-empty').hidden = jobs.length !== 0;
  $('jobs-empty').querySelector('h2').textContent = state.jobs.length ? '没有找到匹配的任务。' : '第一项任务，从这里开始。';
  $('jobs-empty').querySelector('p').textContent = state.jobs.length ? '试试其他关键词或切换筛选条件。' : '定时运行 Shell 或 JavaScript 脚本，让日常工作自动发生。';
  $('empty-new-job').hidden = state.jobs.length !== 0;
  $('jobs-count').textContent = jobs.length === state.jobs.length ? `共 ${jobs.length} 个任务` : `显示 ${jobs.length} / ${state.jobs.length} 个任务`;
}
function renderRuns() {
  const runs = state.runs.filter((r) => !$('run-filter').value || r.job_id === $('run-filter').value);
  const fragment = document.createDocumentFragment();
  for (const run of runs) {
    const row = $('run-row-template').content.cloneNode(true);
    row.querySelector('.run-name').textContent = run.job_name;
    row.querySelector('.run-name').title = run.job_name;
    row.querySelector('.run-status').append(badge(run.status));
    row.querySelector('.run-source').textContent = run.source === 'cron' ? '定时调度' : '手动执行';
    row.querySelector('.run-start').textContent = date(run.started_at);
    row.querySelector('.run-start').title = date(run.started_at, true);
    row.querySelector('.run-duration').textContent = duration(run);
    row.querySelector('.open-log').setAttribute('aria-label', `查看 ${run.job_name} 的执行日志`);
    row.querySelector('.open-log').addEventListener('click', () => openLog(run.id));
    hydrateIcons(row); fragment.append(row);
  }
  $('run-rows').replaceChildren(fragment);
  $('runs-table').hidden = !runs.length;
  $('runs-empty').hidden = !!runs.length;
  $('runs-count').textContent = `共 ${runs.length} 条执行记录`;
}
async function refresh() {
  const token = state.token;
  if (!token) throw new Error('请先登录');
  if (state.refreshing && state.refreshing.token === token) return state.refreshing.promise;
  const promise = (async () => {
    const [jobs, runs, system] = await Promise.all([api('/jobs'), api('/runs'), api('/system')]);
    if (state.token !== token) throw new Error('登录会话已更改');
    state.jobs = jobs; state.runs = runs; state.system = system;
    renderOverview(); renderJobs(); renderRuns(); connection(true);
    if ($('log-dialog').open) await updateLog();
  })();
  state.refreshing = { token, promise };
  try { return await promise; } catch (error) { if (state.token === token) connection(false); throw error; } finally { if (state.refreshing?.promise === promise) state.refreshing = null; }
}

function editJob(job = null) {
  state.editing = job?.id || null;
  $('job-form').reset();
  $('job-dialog-title').textContent = job ? '编辑任务' : '新建任务';
  $('save-job').textContent = job ? '保存更改' : '创建任务';
  $('job-name').value = job?.name || '';
  $('job-schedule').value = job?.schedule || '*/5 * * * *';
  $('job-command').value = job?.command || 'node /scripts/hello.js';
  $('job-directory').value = job?.working_dir || '/scripts';
  $('job-timeout').value = job?.timeout_sec || 300;
  $('job-enabled').checked = job?.enabled ?? true;
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
  try { await api(state.editing ? `/jobs/${state.editing}` : '/jobs', { method: state.editing ? 'PUT' : 'POST', body: JSON.stringify(input) }); $('job-dialog').close(); toast(state.editing ? '任务已更新' : '任务已创建'); setView('jobs'); await refresh(); } catch (error) { setError('job-error', error.message); } finally { $('save-job').disabled = false; }
});
$('confirm-delete').addEventListener('click', async () => {
  $('confirm-delete').disabled = true;
  try { await api(`/jobs/${state.deleting}`, { method: 'DELETE' }); $('confirm-dialog').close(); toast('任务已删除'); await refresh(); } catch (error) { setError('delete-error', error.message); } finally { $('confirm-delete').disabled = false; }
});
async function openLog(id) {
  state.logID = id;
  $('log-output').textContent = '正在读取日志…';
  $('log-truncated').hidden = true;
  $('log-dialog').showModal();
  try { await updateLog(); } catch (error) { $('log-output').textContent = error.message; }
}
async function updateLog() {
  const id = state.logID;
  const run = state.runs.find((r) => r.id === id);
  if (!run) { $('log-output').textContent = '该记录已超过保留上限。'; return; }
  $('log-title').textContent = run.job_name;
  const meta = $('log-meta'); meta.replaceChildren(badge(run.status));
  for (const text of [date(run.started_at, true), duration(run), run.exit_code == null ? '执行中' : `退出码 ${run.exit_code}`, run.source === 'cron' ? '定时调度' : '手动执行']) { const span = document.createElement('span'); span.textContent = text; meta.append(span); }
  $('log-truncated').hidden = !run.truncated;
  const data = await api(`/runs/${id}/log`);
  if (state.logID !== id) return;
  const output = $('log-output');
  const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 50;
  output.textContent = data.output || (run.status === 'running' ? '正在执行，等待输出…' : '本次执行没有输出。');
  if (atBottom) output.scrollTop = output.scrollHeight;
}
$('logout').addEventListener('click', () => { showLogin(); $('token').value = ''; });
$('login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = $('login-form').querySelector('button[type="submit"]'); button.disabled = true;
  state.token = $('token').value.trim();
  setError('login-error', '');
  try { await refresh(); storage.set('lacron-token', state.token, true); $('token').value = ''; $('login').hidden = true; $('workspace').hidden = false; } catch (error) { setError('login-error', error.message); } finally { button.disabled = false; }
});
setInterval(() => { if (state.token && !document.hidden) refresh().catch(() => {}); }, 5000);
document.addEventListener('visibilitychange', () => { if (!document.hidden && state.token) refresh().catch(() => {}); });
if (state.token) {
  const token = state.token;
  refresh().then(() => { if (state.token === token) { $('login').hidden = true; $('workspace').hidden = false; } }).catch((error) => { if (state.token === token || !state.token) { showLogin(); setError('login-error', error.message); } });
}
