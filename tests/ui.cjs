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
      '-e', `LACRON_TOKEN=${token}`, '-e', 'TZ=Asia/Shanghai',
      '--read-only', '--tmpfs', '/run:rw,noexec,nosuid,size=8m,mode=0755',
      '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m,mode=1777',
      '--cap-drop', 'ALL', '--cap-add', 'SETUID', '--cap-add', 'SETGID', '--cap-add', 'KILL',
      '--security-opt', 'no-new-privileges:true', process.argv[2] || 'lacron:ci');
    const port = docker('port', name, '8080/tcp').split(':').pop();
    const base = `http://127.0.0.1:${port}`;
    let ready = false;
    for (let attempt = 0; attempt < 60; attempt++) {
      try { if ((await fetch(`${base}/healthz`)).ok) { ready = true; break; } } catch { /* Container starting. */ }
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
    await page.locator('#schedule-preset').selectOption('0 * * * *');
    assert.equal(await page.locator('#job-schedule').inputValue(), '0 * * * *');
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
    await page.locator('#search').fill('不存在的名称');
    await page.locator('#jobs-empty h2').filter({ hasText: '没有找到匹配的任务。' }).waitFor();
    await page.locator('#search').fill('');

    // system -> light -> dark; verify saved theme and session after reloading.
    await page.locator('#workspace .theme-button').click();
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'light');
    await page.screenshot({ path: 'test-results/ui-light.png', fullPage: true });
    await page.locator('#workspace .theme-button').click();
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.reload();
    await page.locator('#workspace').waitFor({ state: 'visible' });
    assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark');
    await page.screenshot({ path: 'test-results/ui-dark.png', fullPage: true });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('.mobile-nav [data-view=runs]').click();
    await page.locator('#runs-view').waitFor({ state: 'visible' });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'mobile page overflows viewport');
    await page.screenshot({ path: 'test-results/ui-mobile.png', fullPage: true });
    await page.locator('.mobile-nav [data-view=jobs]').click();
    await page.locator('#job-rows .delete-job').click();
    await page.locator('#confirm-delete').click();
    await page.locator('#confirm-dialog').waitFor({ state: 'hidden' });
    await page.waitForFunction(() => document.querySelector('#metric-total').textContent === '0');
    await page.locator('#logout').click();
    await page.locator('#login').waitFor({ state: 'visible' });
    await page.reload();
    await page.locator('#login').waitFor({ state: 'visible' });
    assert.equal(await page.evaluate(() => sessionStorage.getItem('lacron-token')), null);
    assert.deepEqual(pageErrors, []);
    console.log('WebUI: login, CRUD, presets, pause/enable, manual Node run, plain-text logs, search, light/dark persistence, mobile navigation, logout: passed');
  } catch (error) {
    if (page) await page.screenshot({ path: 'test-results/ui-failure.png', fullPage: true }).catch(() => {});
    try { console.error(docker('logs', name)); } catch { /* Keep original failure. */ }
    throw error;
  } finally {
    if (browser) await browser.close();
    try { docker('rm', '-fv', name); } catch { /* Container might not have started. */ }
  }
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
