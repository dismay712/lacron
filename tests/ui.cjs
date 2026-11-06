'use strict';

// Optional browser verification. CI installs Playwright outside the source tree.
const { chromium } = require('playwright');
const { execFileSync } = require('node:child_process');
const { randomBytes } = require('node:crypto');
const fs = require('node:fs');
const assert = require('node:assert/strict');

const docker = (...args) => execFileSync('docker', args, { encoding: 'utf8' }).trim();
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const name = `lacron-ui-${randomBytes(5).toString('hex')}`;
const token = randomBytes(32).toString('hex');

async function main() {
  let browser;
  let page;
  fs.mkdirSync('test-results', { recursive: true });
  try {
    docker('run', '-d', '--name', name, '-p', '127.0.0.1::8080',
      '-e', `LACRON_TOKEN=${token}`, '-e', `LACRON_ENCRYPTION_KEY=${randomBytes(32).toString('base64')}`, '-e', 'TZ=Asia/Shanghai',
      process.argv[2] || 'lacron:ci');
    const port = docker('port', name, '8080/tcp').split(':').pop();
    const base = `http://localhost:${port}`;
    let ready = false;
    for (let attempt = 0; attempt < 60; attempt++) {
      try { if ((await fetch(`http://127.0.0.1:${port}/healthz`)).ok) { ready = true; break; } } catch { /* Container starting. */ }
      await delay(250);
    }
    assert(ready, 'container did not become healthy');
    browser = await chromium.launch({ headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN', timezoneId: 'Asia/Shanghai' });
    page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));
    await page.goto(base);
    await page.locator('#login').waitFor({ state: 'visible' });
    await page.locator('#token').fill(token);
    await page.locator('#login-form button[type=submit]').click();
    await page.locator('#workspace').waitFor({ state: 'visible' });
    assert.equal(await page.locator('#metric-total').innerText(), '0');

    await page.locator('#new-job').click();
    await page.locator('#job-name').fill('Node.js 状态检查');
    await page.locator('#schedule-preset').selectOption('*/5 * * * * *');
    assert.equal(await page.locator('#job-schedule').inputValue(), '*/5 * * * * *');
    await page.locator('#job-command').fill("node /scripts/hello.js\nprintf '<script>window.pwned=true</script>\\n'");
    await page.locator('#job-timeout').fill('10');
    await page.locator('#job-enabled').uncheck();
    await page.locator('#save-job').click();
    await page.locator('#job-dialog').waitFor({ state: 'hidden' });
    await page.locator('#job-rows .job-name').filter({ hasText: 'Node.js 状态检查' }).waitFor();
    assert.equal(await page.locator('#metric-total').innerText(), '1');
    assert.equal(await page.locator('#metric-enabled').innerText(), '0');

    await page.locator('#job-rows .toggle').click();
    await page.waitForFunction(() => document.querySelector('#job-rows .toggle')?.getAttribute('aria-checked') === 'true');
    await page.locator('#job-rows .toggle').click();
    await page.waitForFunction(() => document.querySelector('#job-rows .toggle')?.getAttribute('aria-checked') === 'false');
    await page.locator('#job-rows .run-job').click();
    await page.locator('#job-rows .badge.success').waitFor({ timeout: 20000 });
    await page.locator('#job-rows .job-history').click();
    await page.locator('#run-rows .open-log').first().click();
    await page.waitForFunction(() => document.querySelector('#log-output').textContent.includes('uid=10001 gid=10001'));
    assert((await page.locator('#log-output').innerText()).includes('<script>window.pwned=true</script>'));
    assert.equal(await page.evaluate(() => window.pwned), undefined, 'log HTML executed as script');
    await page.locator('#log-dialog .modal-footer .close-dialog').click();
    await page.locator('#nav-jobs').click();

    await page.locator('#job-rows .edit-job').click();
    await page.locator('#job-name').fill('Node.js 日常检查');
    await page.locator('#save-job').click();
    await page.locator('#job-dialog').waitFor({ state: 'hidden' });
    await page.locator('#job-rows .job-name').filter({ hasText: 'Node.js 日常检查' }).waitFor();
    assert.equal(await page.locator('#job-rows .schedule-code').innerText(), '*/5 * * * * *');
    await page.locator('#search').fill('不存在的名称');
    await page.locator('#jobs-empty h2').filter({ hasText: '没有找到匹配的任务。' }).waitFor();
    await page.locator('#search').fill('');

    await page.locator('#nav-variables').click();
    await page.locator('#variables-view').waitFor({ state: 'visible' });
    await page.locator('#new-variable').click();
    await page.locator('#variable-name').fill('CHECKIN_COOKIE');
    const sampleCookie = 'ui-cookie-example-not-a-real-secret';
    await page.locator('#variable-value').fill(sampleCookie);
    await page.locator('#variable-description').fill('签到 Cookie 测试');
    assert.equal(await page.locator('#variable-job-options input:checked').count(), 0);
    await page.locator('#save-variable').click();
    await page.locator('#variable-dialog').waitFor({ state: 'hidden' });
    await page.locator('#variable-rows .variable-name').filter({ hasText: 'CHECKIN_COOKIE' }).waitFor();
    assert.equal(await page.locator('#variable-value').inputValue(), '');
    assert(!(await page.locator('body').innerText()).includes(sampleCookie));
    assert((await page.locator('#variable-rows .variable-grants').innerText()).includes('未授权'));
    await page.locator('#variable-rows .edit-variable').click();
    assert.equal(await page.locator('#variable-value').inputValue(), '', 'secret must not be echoed when editing');
    await page.locator('#variable-job-options input').first().check();
    await page.locator('#save-variable').click();
    await page.locator('#variable-dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.querySelector('#variable-rows .variable-grants').textContent.includes('Node.js 日常检查'));
    await page.locator('#nav-jobs').click();
    await page.locator('#job-rows .edit-job').click();
    assert((await page.locator('#job-variables').innerText()).includes('CHECKIN_COOKIE'));
    await page.locator('#job-dialog .modal-footer .close-dialog').click();
    await page.locator('#nav-variables').click();
    await page.locator('#variable-rows .edit-variable').click();
    await page.locator('#variable-job-options input').first().uncheck();
    await page.locator('#save-variable').click();
    await page.locator('#variable-dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.querySelector('#variable-rows .variable-grants').textContent.includes('未授权'));
    await page.locator('#variable-search').fill('missing-variable');
    await page.locator('#variables-empty h2').filter({ hasText: '没有找到匹配的变量。' }).waitFor();
    await page.locator('#variable-search').fill('');

    // system -> light -> dark; verify saved theme and session after reloading.
    await page.locator('#workspace .theme-button').click();
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'light');
    await page.screenshot({ path: 'test-results/ui-variables-light.png', fullPage: true });
    await page.locator('#nav-jobs').click();
    await page.screenshot({ path: 'test-results/ui-light.png', fullPage: true });
    await page.locator('#workspace .theme-button').click();
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.reload();
    await page.locator('#workspace').waitFor({ state: 'visible' });
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.screenshot({ path: 'test-results/ui-dark.png', fullPage: true });
    await page.locator('#nav-variables').click();
    await page.screenshot({ path: 'test-results/ui-variables-dark.png', fullPage: true });
    assert(!(await page.locator('body').innerText()).includes(sampleCookie));

    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('.mobile-nav [data-view=runs]').click();
    await page.locator('#runs-view').waitFor({ state: 'visible' });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'mobile page overflows viewport');
    await page.screenshot({ path: 'test-results/ui-mobile.png', fullPage: true });
    await page.locator('.mobile-nav [data-view=variables]').click();
    await page.locator('#variables-view').waitFor({ state: 'visible' });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'mobile variables page overflows');
    await page.screenshot({ path: 'test-results/ui-variables-mobile.png', fullPage: true });
    await page.locator('#variable-rows .delete-variable').click();
    await page.locator('#confirm-delete').click();
    await page.locator('#confirm-dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.querySelector('#variables-count').textContent === '共 0 个变量');
    await page.locator('.mobile-nav [data-view=jobs]').click();
    await page.locator('#job-rows .delete-job').click();
    await page.locator('#confirm-delete').click();
    await page.locator('#confirm-dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.querySelector('#metric-total').textContent === '0');
    await page.setViewportSize({ width: 1440, height: 1000 });
    await exerciseOptimizations(page, context, base);
    await page.reload();
    await page.locator('#login').waitFor({ state: 'visible' });
    assert.equal(await page.evaluate(() => sessionStorage.getItem('lacron-token')), null);
    assert.deepEqual(pageErrors, []);
    console.log('WebUI: task and variable CRUD, sorting, copy, incremental logs, stop, cleanup, settings, Passkey, session revocation, themes and mobile: passed');
  } catch (error) {
    if (page) {
      const errors = await page.locator('.form-error:visible').allTextContents().catch(() => []);
      if (errors.length) console.error('Visible form errors:', errors);
      await page.screenshot({ path: 'test-results/ui-failure.png', fullPage: true }).catch(() => {});
    }
    try { console.error(docker('logs', name)); } catch { /* Keep original failure. */ }
    throw error;
  } finally {
    if (browser) await browser.close();
    try { docker('rm', '-fv', name); } catch { /* Container might not have started. */ }
  }
}

async function exerciseOptimizations(page, context, base) {
  await page.locator('#nav-jobs').click();
  await page.locator('#new-job').click();
  await page.locator('#job-name').fill('Z 持续输出');
  await page.locator('#job-command').fill("printf 'first output\\n'; sleep 6; printf 'second output\\n'; sleep 45");
  await page.locator('#job-directory').fill('/');
  await page.locator('#job-enabled').uncheck();
  await page.locator('#save-job').click();
  await page.locator('#job-dialog').waitFor({ state: 'hidden' });
  await page.locator('#job-rows .copy-job').click();
  assert.equal(await page.locator('#copy-name').inputValue(), 'Z 持续输出（副本）');
  await page.locator('#copy-name').fill('A 任务副本');
  await page.locator('#copy-submit').click();
  await page.locator('#copy-dialog').waitFor({ state: 'hidden' });
  await page.locator('#job-rows .job-name').filter({ hasText: 'A 任务副本' }).waitFor();

  await page.locator('#job-sort').selectOption('name');
  assert.deepEqual(await page.locator('#job-rows .job-name').allTextContents(), ['A 任务副本', 'Z 持续输出']);
  await page.locator('#job-sort-direction').click();
  assert.deepEqual(await page.locator('#job-rows .job-name').allTextContents(), ['Z 持续输出', 'A 任务副本']);
  const jobs = await page.evaluate(() => fetch('/api/jobs').then((r) => r.json()));
  const original = jobs.find((job) => job.name === 'Z 持续输出');
  const copy = jobs.find((job) => job.name === 'A 任务副本');
  assert(!copy.enabled && copy.run_uid !== original.run_uid && copy.command === original.command);

  const offsets = [];
  const recordOffset = (req) => {
    const url = new URL(req.url());
    if (url.pathname.endsWith('/log')) offsets.push(Number(url.searchParams.get('offset')));
  };
  page.on('request', recordOffset);
  const row = page.locator('#job-rows tr').filter({ hasText: original.name });
  await row.locator('.run-job').click();
  await row.locator('.job-history').click();
  await page.locator('#run-rows .open-log').first().click();
  await page.waitForFunction(() => document.querySelector('#log-output').textContent.includes('first output'));
  await page.waitForFunction(() => document.querySelector('#log-output').textContent.includes('second output'), null, { timeout: 16000 });
  assert(offsets.some((offset) => offset > 0), 'log did not resume from a byte offset');
  const output = await page.locator('#log-output').textContent();
  assert.equal(output.match(/first output/g).length, 1, 'log output was duplicated');
  await page.locator('#log-stop').click();
  await page.locator('#log-meta .badge.cancelled').waitFor({ timeout: 16000 });
  page.off('request', recordOffset);

  await page.locator('#log-clean').click();
  await page.locator('#preview-cleanup').click();
  await page.locator('#cleanup-preview').filter({ hasText: '将清理 1 条' }).waitFor();
  await page.locator('#apply-cleanup').click();
  await page.locator('#cleanup-dialog').waitFor({ state: 'hidden' });
  await page.locator('#log-output').filter({ hasText: '本次日志已清理' }).waitFor();
  await page.locator('#log-dialog .modal-footer .close-dialog').click();
  await page.locator('#run-status-filter').selectOption('cancelled');
  await page.locator('#run-rows .badge.cancelled').waitFor();

  await page.locator('#nav-settings').click();
  await page.waitForFunction(() => document.querySelector('#settings-concurrent').value === '4');
  await page.locator('#settings-concurrent').fill('2');
  await page.locator('#settings-runs').fill('300');
  await page.locator('#settings-log').fill('64');
  await page.locator('#settings-passkey-enabled').check();
  await page.locator('#settings-origin').fill(base);
  await page.locator('#save-settings').click();
  await page.waitForFunction(() => !document.querySelector('#add-passkey').disabled);
  const settings = await page.evaluate(() => fetch('/api/settings').then((r) => r.json()));
  assert.deepEqual(settings, {
    max_concurrent: 2, max_runs: 300, max_log_kib: 64, passkey_enabled: true, passkey_origin: base
  });

  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true,
      isUserVerified: true, automaticPresenceSimulation: true }
  });
  await page.locator('#add-passkey').click();
  await page.locator('#passkey-name').fill('Chromium 测试设备');
  await page.locator('#passkey-submit').click();
  await page.locator('#passkey-list li').filter({ hasText: 'Chromium 测试设备' }).waitFor();
  const session = (await context.cookies(base)).find((cookie) => cookie.name === 'lacron_session');
  assert(session.httpOnly && session.sameSite === 'Strict');
  await page.locator('#logout').click();
  await page.locator('#login').waitFor({ state: 'visible' });
  const reused = await context.request.get(`${base}/api/settings`, {
    headers: { Cookie: `${session.name}=${session.value}` }
  });
  assert.equal(reused.status(), 401, 'logout did not revoke the server session');
  await page.locator('#passkey-login').click();
  await page.locator('#workspace').waitFor({ state: 'visible' });
  await page.locator('#nav-settings').click();
  await page.locator('#passkey-list li').filter({ hasText: '最近使用' }).waitFor();
  assert.equal(await page.locator('#settings-log').inputValue(), '64');
  await page.locator('#passkey-list button').click();
  await page.locator('#confirm-delete').click();
  await page.locator('#login').waitFor({ state: 'visible' });
  await page.locator('#passkey-login').waitFor({ state: 'hidden' });
  await cdp.detach();
}

module.exports = { exerciseOptimizations };
if (require.main === module) main().catch((error) => { console.error(error); process.exitCode = 1; });
