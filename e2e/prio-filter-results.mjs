import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

const address = new URL('/prio?cutoff=2026-11-20T10%3A00%3A00Z', process.argv[2]);
const browser = await chromium.launch({ headless: process.env.PRIO_TEST_HEADED !== '1' });

try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  page.setDefaultTimeout(8000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const shared = page.locator('[data-prio-shared-controls]');
  const cutoff = shared.getByLabel('Stichtag', { exact: true });
  const overview = page.getByTestId('prio-filters');
  const preset = overview.getByLabel('Jira-Filter');
  const changedSince = overview.getByLabel('Nur seit Stichtag');
  const level = shared.locator('label[for=prio-level-unparented]');
  const settled = () => page.locator('#prio-panel:not(.htmx-settling) [data-prio-agenda]').waitFor();

  async function expectKeys(selector, field, ids) {
    const actual = await page.locator(selector).evaluateAll((rows, attr) => rows.map(row => row.getAttribute(attr)).sort(), field);
    assert.deepEqual(actual, ids.map(id => 'DCAI-' + id).sort(), page.url());
  }
  const rows = ids => expectKeys('[data-testid="prio-row"]', 'data-key', ids);
  const queue = ids => expectKeys('[data-prio-queue-key]', 'data-prio-queue-key', ids);
  const has = (key, value) => page.waitForURL(url => url.searchParams.get(key) === value);

  await page.goto(address.href);
  await page.locator('[data-testid="prio-row"]').first().waitFor();
  await preset.locator('option[value="42"]').waitFor({ state: 'attached' });
  await rows([1, 2, 4, 6]);
  await overview.locator('summary').click();
  assert.deepEqual(await preset.locator('option').evaluateAll(options => options.map(option => option.value)), ['all', '42']);
  assert.equal(await overview.locator('input[name=level]:not([type=hidden])').count(), 0);
  assert.equal(await shared.getByRole('button').count(), 0);
  assert.equal(await page.getByRole('link', { name: 'Stichtag im Link speichern' }).count(), 0);

  await changedSince.check();
  await has('period', 'since');
  await settled();
  await rows([1, 4, 6]);

  await level.click();
  await has('level', 'unparented');
  await settled();
  await rows([1, 3, 4, 5, 6, 9]);

  await preset.selectOption('42');
  await has('preset', '42');
  await settled();
  await rows([1, 3, 4, 5, 9]);

  const historyLength = await page.evaluate(() => history.length);
  await cutoff.fill('2026-11-20T13:00');
  await has('cutoff', '2026-11-20T12:00:00Z');
  await page.getByTestId('prio-no-match').waitFor();
  await rows([]);
  assert.equal(await page.evaluate(() => history.length), historyLength);
  await cutoff.fill('2026-11-20T11:00');
  await cutoff.blur();
  await has('cutoff', '2026-11-20T10:00:00Z');
  await settled();
  await rows([1, 3, 4, 5, 9]);

  const evidence = process.env.PRIO_TEST_EVIDENCE;
  if (evidence) {
    await mkdir(evidence, { recursive: true });
    await page.screenshot({ path: evidence + '/step-0.png' });
  }
  for (const [step, ids] of [['epic-check', [6]], ['done', [4, 5]], ['new', [1, 3, 6, 9]], ['backlog', [1, 3, 6, 9]]]) {
    await page.locator('[data-prio-step=' + step + ']').click();
    await has('step', step);
    await settled();
    assert.equal(await shared.isVisible(), true);
    assert.equal(await overview.count(), 0);
    const params = new URL(page.url()).searchParams;
    assert.equal(params.get('level'), 'unparented');
    assert.equal(params.get('cutoff'), '2026-11-20T10:00:00Z');
    assert.equal(params.has('period') || params.has('preset'), false);
    await queue(ids);
    if (evidence && step === 'done') await page.screenshot({ path: evidence + '/step-2.png' });
  }

  // Simulate an open tab whose JavaScript predates the revision header.
  await page.evaluate(() => { window.prioPreviousDocument = true; });
  await page.route(url => url.pathname === '/prio' && url.searchParams.get('step') === 'overview', async route => {
    const headers = { ...route.request().headers() };
    delete headers['x-prio-agenda-revision'];
    await route.continue({ headers });
  }, { times: 1 });
  const navigation = page.waitForEvent('framenavigated', { predicate: frame => frame === page.mainFrame() });
  await page.locator('[data-prio-step=overview]').click();
  await navigation;
  await page.waitForLoadState('domcontentloaded');
  assert.equal(await page.evaluate(() => window.prioPreviousDocument), undefined);
  await has('preset', '42');
  await has('period', 'since');
  await has('level', 'unparented');
  await page.locator('[data-testid="prio-row"]').first().waitFor();
  await rows([1, 3, 4, 5, 9]);
  assert.deepEqual(errors, []);
  console.log('Prio browser results passed for each filter, the cutoff change, all five steps, and a stale-client reload.');
} finally {
  await browser.close();
}
