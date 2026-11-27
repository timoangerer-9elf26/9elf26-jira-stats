import assert from 'node:assert/strict';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { chromium } from 'playwright';

const baseURL = process.env.PRIO_TEST_URL || (await readFile('tmp/review/url', 'utf8')).trim();
const evidenceDir = process.env.PRIO_TEST_EVIDENCE;
const browser = await chromium.launch();

try {
  for (const width of [1280, 390]) {
    const viewport = { width, height: width === 390 ? 1000 : 800 };
    const context = await browser.newContext({
      viewport,
      ...(evidenceDir && { recordVideo: { dir: evidenceDir, size: viewport } }),
    });
    try {
      const page = await context.newPage();
      await page.goto(new URL('/prio?level=all&period=since&preset=all', baseURL).href);
      const rows = page.getByTestId('prio-row');
      await rows.first().waitFor({ state: 'attached' });
      const filteredCount = await rows.count();
      await page.locator('[data-testid="prio-filters"] > summary').click();
      const cutoff = page.getByRole('checkbox', { name: 'Nur seit Stichtag' });
      await cutoff.uncheck();
      await page.waitForURL(url => url.searchParams.get('period') !== 'since');
      await page.locator('#prio-panel:not(.htmx-settling) [data-testid="prio-filters"][open]').waitFor();
      assert.ok(await rows.count() > filteredCount, 'the first toggle must load more fixture rows');

      await cutoff.focus();
      // Hold the warm request across a rendering opportunity so the pending
      // state is checked even when the fixture server responds immediately.
      await page.route(url => url.pathname === '/prio' && url.searchParams.get('period') === 'since', async route => {
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        await route.continue();
      }, { times: 1 });
      await page.evaluate(() => {
        const panel = document.querySelector('#prio-panel');
        const samples = [];
        const sample = phase => {
          const disclosure = panel.querySelector('[data-testid="prio-filters"]');
          const table = panel.querySelector('[data-testid="prio"]');
          const bounds = table?.getBoundingClientRect();
          samples.push({
            phase,
            open: disclosure?.open,
            top: bounds?.top,
            height: bounds?.height,
            scrollY: window.scrollY,
            skeleton: !!panel.querySelector('[data-prio-preset-loading]'),
          });
        };

        window.prioFilterSamples = new Promise(resolve => {
          let done = false;
          const frame = () => {
            sample('frame');
            if (done) resolve(samples);
            else requestAnimationFrame(frame);
          };
          const observe = event => {
            if (event.detail.target !== panel) return;
            sample(event.type);
            if (event.type === 'htmx:afterSettle') {
              done = true;
              document.removeEventListener('htmx:afterSwap', observe);
              document.removeEventListener('htmx:afterSettle', observe);
            }
          };
          document.addEventListener('htmx:afterSwap', observe);
          document.addEventListener('htmx:afterSettle', observe);
          sample('before');
          requestAnimationFrame(frame);
        });
      });
      await cutoff.check();
      await page.waitForURL(url => url.searchParams.get('period') === 'since');
      await page.locator('#prio-panel:not(.htmx-settling) [data-testid="prio-filters"][open]').waitFor();
      const samples = await page.evaluate(() => window.prioFilterSamples);
      if (evidenceDir) {
        await mkdir(evidenceDir, { recursive: true });
        await writeFile(`${evidenceDir}/frames-${width}.json`, JSON.stringify(samples, null, 2));
      }

      const [before] = samples;
      assert.ok(samples.some(sample => sample.phase === 'htmx:afterSwap'), 'the filter must swap through HTMX');
      for (const sample of samples) {
        const where = `${width}px, ${sample.phase}`;
        assert.ok(sample.open, `${where}: the filter disclosure closed`);
        assert.ok(sample.height > 0 && !sample.skeleton, `${where}: the results disappeared`);
        assert.equal(sample.scrollY, before.scrollY, `${where}: the page scrolled`);
        assert.ok(Math.abs(sample.top - before.top) <= 1,
          `${where}: the table moved from ${before.top} to ${sample.top}`);
      }
      assert.equal(await rows.count(), filteredCount);
      assert.equal(await cutoff.evaluate(el => el === document.activeElement && el.checked), true);
      console.log(`Prio filter layout stable at ${width}px (${samples.length} samples)`);
    } finally {
      await context.close();
    }
  }
} finally {
  await browser.close();
}
