'use strict';

// Node.js LTS built-in fetch; no npm packages and no login flow.
// This is an example API contract. Adapt the paths, request bodies and field
// extraction below to your service. Never print Cookie or entire API responses.
// Grant CHECKIN_COOKIE / CHECKIN_BASE_URL to this lacron task in the WebUI.

async function runCheckin(env = process.env, log = console.log) {
  const cookie = env.CHECKIN_COOKIE;
  if (!cookie || /[\r\n]/.test(cookie)) throw new Error('缺少有效的 CHECKIN_COOKIE，请检查任务的变量授权');
  let base;
  try { base = new URL(env.CHECKIN_BASE_URL); } catch { throw new Error('请设置有效的 CHECKIN_BASE_URL'); }
  if (!['https:', 'http:'].includes(base.protocol) || base.username || base.password) throw new Error('CHECKIN_BASE_URL 必须为 HTTP/HTTPS 地址，不能包含用户名密码');
  const timeout = Number(env.CHECKIN_TIMEOUT_MS || 15000);
  if (!Number.isInteger(timeout) || timeout < 100 || timeout > 60000) throw new Error('CHECKIN_TIMEOUT_MS 须在 100–60000 之间');
  const paths = {
    status: env.CHECKIN_STATUS_PATH || '/api/status',
    prepare: env.CHECKIN_PREPARE_PATH || '/api/checkin/prepare',
    submit: env.CHECKIN_SUBMIT_PATH || '/api/checkin'
  };

  async function request(step, path, { method = 'GET', body, csrfToken } = {}) {
    let url;
    try { url = new URL(path, base); } catch { throw new Error(`${step}：API 地址无效`); }
    // A raw Cookie header has no browser cookie-domain protection. Keep every
    // request on the configured origin, and never follow a redirect with it.
    if (url.origin !== base.origin || url.username || url.password) throw new Error(`${step}：拒绝向其他站点发送 Cookie`);
    const headers = { Accept: 'application/json', Cookie: cookie };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (csrfToken) headers['X-CSRF-Token'] = csrfToken;
    const signal = AbortSignal.timeout(timeout);
    let response;
    try {
      response = await fetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal, redirect: 'manual' });
    } catch {
      throw new Error(`${step}：${signal.aborted ? '请求超时' : '网络请求失败'}，未自动重试`);
    }
    if (!response.ok) {
      await response.body?.cancel();
      if ([401, 403].includes(response.status)) throw new Error(`${step}：Cookie 已失效或权限不足，请手动更新 Cookie`);
      if (response.status >= 300 && response.status < 400) throw new Error(`${step}：API 返回重定向，已停止，未转发 Cookie`);
      throw new Error(`${step}：HTTP ${response.status}，未自动重试`);
    }
    const chunks = [];
    let bytes = 0;
    try {
      for await (const chunk of response.body) {
        bytes += chunk.byteLength;
        if (bytes > 1024 * 1024) throw new Error('response-too-large');
        chunks.push(Buffer.from(chunk));
      }
    } catch {
      throw new Error(`${step}：${bytes > 1024 * 1024 ? '返回内容超过 1 MiB' : signal.aborted ? '读取响应超时' : '读取响应失败'}`);
    }
    let result;
    try { result = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new Error(`${step}：返回内容不是有效 JSON`); }
    // EXAMPLE: APIs here use { code: 0, data: {...} }. Change this check if your
    // service uses a different success code or a different response structure.
    if (!result || result.code !== 0 || !result.data || typeof result.data !== 'object') throw new Error(`${step}：API 业务检查失败，请按真实接口调整成功判断`);
    return result.data;
  }

  log('[checkin] API1：查询签到状态');
  const status = await request('API1', paths.status);
  if (typeof status.signed !== 'boolean') throw new Error('API1：缺少布尔类型的 data.signed 字段');
  if (status.signed) { log('[checkin] 今日已签到，无需重复操作'); return; }
  const { userId, csrfToken } = status;
  if (!['string', 'number'].includes(typeof userId) || typeof csrfToken !== 'string' || !csrfToken) throw new Error('API1：缺少 userId 或 csrfToken，请调整字段提取');

  log('[checkin] API2：获取本次签到参数');
  const prepared = await request('API2', paths.prepare, { method: 'POST', csrfToken, body: { userId } });
  if (typeof prepared.nonce !== 'string' || !prepared.nonce) throw new Error('API2：缺少 data.nonce，请调整字段提取');

  log('[checkin] API3：提交签到');
  const submitted = await request('API3', paths.submit, { method: 'POST', csrfToken, body: { userId, nonce: prepared.nonce } });
  if (submitted.success !== true) throw new Error('API3：未返回 data.success=true，停止后续流程');

  log('[checkin] 再次查询状态，确认签到结果');
  const verified = await request('结果确认', paths.status);
  if (verified.signed !== true) throw new Error('提交已返回，但状态 API 尚未确认签到成功；请查询状态后再决定是否重试');
  log('[checkin] 签到成功（状态已确认）');
}

if (require.main === module) {
  runCheckin().catch((error) => {
    // Only our own sanitized errors are logged; no response body, headers,
    // Cookie, CSRF token, nonce or full environment is printed.
    console.error(`[checkin] ${error.message}`);
    process.exitCode = 1;
  });
}

module.exports = { runCheckin };
