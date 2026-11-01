'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { runCheckin } = require('../scripts/checkin.js');

async function withServer(t, handler) {
  const server = http.createServer(handler);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); }));
  return `http://127.0.0.1:${server.address().port}`;
}
function json(res, data, code = 0) { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ code, data })); }
const cookie = 'session=example-cookie-not-a-real-credential';

test('four real fetch requests chain response values and verify state without logging secrets', async (t) => {
  const calls = [], logs = [];
  let signed = false;
  const base = await withServer(t, async (req, res) => {
    let raw = ''; for await (const chunk of req) raw += chunk;
    calls.push({ path: req.url, method: req.method, cookie: req.headers.cookie, csrf: req.headers['x-csrf-token'], body: raw ? JSON.parse(raw) : null });
    if (req.url === '/api/status') json(res, { signed, userId: 'u1', csrfToken: 'private-csrf' });
    else if (req.url === '/api/checkin/prepare') json(res, { nonce: 'private-nonce' });
    else { signed = true; json(res, { success: true }); }
  });
  await runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base }, (s) => logs.push(s));
  assert.deepEqual(calls.map((c) => c.path), ['/api/status', '/api/checkin/prepare', '/api/checkin', '/api/status']);
  assert(calls.every((c) => c.cookie === cookie));
  assert.deepEqual(calls[1].body, { userId: 'u1' });
  assert.equal(calls[1].csrf, 'private-csrf');
  assert.deepEqual(calls[2].body, { userId: 'u1', nonce: 'private-nonce' });
  assert(logs.at(-1).includes('签到成功'));
  for (const secret of [cookie, 'private-csrf', 'private-nonce']) assert(!logs.join('\n').includes(secret));
});

test('already signed in only requests status', async (t) => {
  let count = 0;
  const base = await withServer(t, (req, res) => { count++; json(res, { signed: true }); });
  await runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base }, () => {});
  assert.equal(count, 1);
});

test('missing cookie fails before network access', async () => {
  await assert.rejects(runCheckin({}, () => {}), /CHECKIN_COOKIE/);
});

test('refuses cross-origin endpoints before sending Cookie', async (t) => {
  let count = 0;
  const base = await withServer(t, (req, res) => { count++; json(res, { signed: true }); });
  await assert.rejects(runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base, CHECKIN_STATUS_PATH: 'https://another.invalid/api/status' }, () => {}), /其他站点/);
  assert.equal(count, 0);
});

test('does not follow redirects or retry a failed mutating request', async (t) => {
  let count = 0;
  const base = await withServer(t, (req, res) => {
    count++;
    if (req.url === '/api/status') json(res, { signed: false, userId: 7, csrfToken: 'csrf' });
    else { res.writeHead(302, { Location: '/must-not-follow' }); res.end(); }
  });
  await assert.rejects(runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base }, () => {}), /重定向/);
  assert.equal(count, 2);
});

test('expired Cookie, invalid JSON, business failure and timeout are failures', async (t) => {
  for (const mode of ['expired', 'json', 'business', 'timeout']) {
    const base = await withServer(t, (req, res) => {
      if (mode === 'expired') { res.statusCode = 401; res.end(); }
      else if (mode === 'json') res.end('<html>not-json</html>');
      else if (mode === 'business') json(res, {}, 99);
      // timeout deliberately leaves the response pending.
    });
    await assert.rejects(runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base, CHECKIN_TIMEOUT_MS: '100' }, () => {}), /Cookie|JSON|业务|超时/);
  }
});

test('successful submission without a confirmed final state exits as failure', async (t) => {
  const base = await withServer(t, (req, res) => {
    if (req.url === '/api/status') json(res, { signed: false, userId: 1, csrfToken: 'csrf' });
    else if (req.url.endsWith('prepare')) json(res, { nonce: 'nonce' });
    else json(res, { success: true });
  });
  await assert.rejects(runCheckin({ CHECKIN_COOKIE: cookie, CHECKIN_BASE_URL: base }, () => {}), /尚未确认/);
});
