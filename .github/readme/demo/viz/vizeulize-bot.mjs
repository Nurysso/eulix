#!/usr/bin/env node
// vizEulize demo bot. Opens vizEulize on an indexed repository in a real
// browser window and walks through it at a pace a viewer can follow, with
// step captions and a visible pointer, so the screen can be recorded with
// wf-recorder. See "vizEulize recording" in docs/scripts.md.
//
// Setup, once (from this folder):
//   npm install && npm run setup        # Playwright and its Chromium
//
// Run:
//   node vizeulize-bot.mjs --record vizeulize.mp4   # bot starts/stops wf-recorder
//   node vizeulize-bot.mjs                          # you record; 5 s countdown first
//
// Options:
//   --repo <dir>        indexed repository (default /tmp/eulix-demo/work/flask,
//                       the README demo's Flask checkout)
//   --url <url>         use an `eulix vizeulize` that is already running
//   --record <file>     record the browser window with wf-recorder. On Hyprland
//                       the window is found with hyprctl; elsewhere you pick
//                       the area with slurp
//   --countdown <s>     without --record: seconds to start your recorder (5)
//   --windowed          a normal window instead of full screen
//   --theme dark|light  starting color scheme (dark)
//   --pace <factor>     multiply every pause; 1.5 is slower (1)
//   --zoom <factor>     render the page larger, so its text stays readable
//                       once the video is scaled down for the README (1.5)
//   --no-captions       don't draw the step captions
//
//   --dry-run <dir>     headless check of every step: no window, no
//                       recording, a screenshot per step in <dir>
//
// The steps are written for Flask. Names are matched as vizEulize shows them:
// methods keep their class prefix (Flask_full_dispatch_request), classes and
// functions don't (Scaffold). For another repository, change STEPS below.

import { chromium } from 'playwright';
import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';

const { values: opt } = parseArgs({
  options: {
    repo: { type: 'string', default: '/tmp/eulix-demo/work/flask' },
    url: { type: 'string' },
    record: { type: 'string' },
    countdown: { type: 'string', default: '5' },
    windowed: { type: 'boolean', default: false },
    theme: { type: 'string', default: 'dark' },
    pace: { type: 'string', default: '1' },
    zoom: { type: 'string', default: '1.5' },
    'no-captions': { type: 'boolean', default: false },
    'dry-run': { type: 'string' },
  },
});
const DRY = opt['dry-run'];
const PACE = Number(opt.pace) || 1;
const MIN_VERSION = '0.2.0'; // first release with the keyboard shortcuts used below

const die = (msg) => { console.error(`vizeulize-bot: ${msg}`); process.exit(1); };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const pause = (ms) => sleep(ms * PACE);

// ---------------------------------------------------------------------------
// The walkthrough. Each step: a caption, then what the bot does.
// ---------------------------------------------------------------------------
const STEPS = [
  ["Eulix's call graph of Flask, most-connected functions first", async (b) => {
    await pause(1500);
    await b.key('f');
    await pause(2500);
  }],
  ['Search for any function', async (b) => {
    await b.key('/');
    await b.type('full_dispatch_request');
    await b.waitForListItem('Flask_full_dispatch_request');
    await pause(700);
    await b.key('Enter');                // focuses the first match
    await pause(1000);
    await b.clearSearch();               // otherwise the neighbours stay faded
    await pause(1800);
  }],
  ['Who calls it?', async (b) => {
    await b.setDirection('callers');
    await pause(900);
    await b.key(']');                    // depth 3
    await pause(3000);
  }],
  ['And what it calls', async (b) => {
    await b.setDirection('both');
    await pause(3200);
  }],
  ['Click a name to walk the graph', async (b) => {
    await b.click(b.detailsEntry('Flask_wsgi_app'));
    await pause(3000);
    await b.key('Backspace');            // back to full_dispatch_request
    await pause(2200);
  }],
  ['Class hierarchies too', async (b) => {
    await b.key('r');                    // reset: clears focus and the search box
    await pause(1000);
    await b.key('/');
    await b.type('Scaffold');
    await b.click(await b.waitForListItem('Scaffold'));
    await pause(800);
    await b.clearSearch();
    await pause(800);
    await b.setDirection('inh');         // inheritance, both ways
    await pause(3500);
  }],
  ['Light or dark', async (b) => {
    await b.key('t');
    await pause(2500);
    await b.key('t');
    await pause(1500);
  }],
  ['eulix vizeulize: served from your machine', async (b) => {
    await b.key('r');
    await pause(800);
    await b.key('f');
    await pause(3000);
  }],
];

// ---------------------------------------------------------------------------
// Start `eulix vizeulize`, or use --url.
// ---------------------------------------------------------------------------
async function startServer(repo) {
  if (!existsSync(join(repo, '.eulix', 'kb_call_graph.json'))) {
    die(`${repo} has no .eulix/kb_call_graph.json. Run \`eulix analyze\` there first ` +
        '(or .github/readme/demo/record.sh walkthrough, which indexes Flask).');
  }
  // The launcher opens the default browser with xdg-open; a no-op xdg-open
  // first on PATH keeps that window out of the recording.
  const shim = mkdtempSync(join(tmpdir(), 'vizeulize-bot-'));
  writeFileSync(join(shim, 'xdg-open'), '#!/bin/sh\nexit 0\n', { mode: 0o755 });
  // `script` gives eulix a terminal: with its output in a plain pipe it can
  // stall on a terminal color query.
  const proc = spawn('script', ['-qfec', 'eulix vizeulize', '/dev/null'], {
    cwd: repo,
    env: { ...process.env, PATH: `${shim}:${process.env.PATH}` },
    stdio: ['pipe', 'pipe', 'pipe'],   // stderr kept for errors; script prints a goodbye line there
  });
  let out = '';
  const url = await new Promise((res, rej) => {
    const timer = setTimeout(() => rej(new Error(`no URL from eulix vizeulize after 30 s:\n${out}`)), 30000);
    proc.stderr.on('data', (d) => { out += d; });
    proc.stdout.on('data', (d) => {
      out += d;
      const m = /http:\/\/127\.0\.0\.1:\d+\//.exec(out);
      if (m) { clearTimeout(timer); res(m[0]); }
    });
    proc.on('exit', (code) => { clearTimeout(timer); rej(new Error(`eulix vizeulize exited (${code}):\n${out}`)); });
  }).catch((e) => die(e.message));
  proc.removeAllListeners('exit');
  return { url, stop: () => { proc.kill('SIGTERM'); rmSync(shim, { recursive: true, force: true }); } };
}

async function checkVersion(url) {
  const html = await (await fetch(url)).text();
  const ver = /<meta name="version" content="([\d.]+)"/.exec(html)?.[1];
  const older = (a, b) => {
    const [x, y] = [a, b].map((v) => v.split('.').map(Number));
    for (let i = 0; i < 3; i++) if ((x[i] || 0) !== (y[i] || 0)) return (x[i] || 0) < (y[i] || 0);
    return false;
  };
  if (!ver || older(ver, MIN_VERSION)) {
    die(`the vizEulize page being served is ${ver ? `version ${ver}` : 'older than 0.2.0'}, ` +
        `and the bot needs ${MIN_VERSION} or later. Install the repository's copy with ` +
        '`make install-viz` from the repo root, then run the bot again.');
  }
  return ver;
}

// ---------------------------------------------------------------------------
// Recording with wf-recorder.
// ---------------------------------------------------------------------------
function windowGeometry() {
  try {   // Hyprland: the bot's Chromium window, by title (not a vizEulize tab in another browser)
    const clients = JSON.parse(execFileSync('hyprctl', ['clients', '-j'], { encoding: 'utf8' }));
    const viz = clients.filter((w) => /vizEulize/.test(w.title));
    const c = viz.find((w) => /chrom/i.test(w.class)) ?? viz[0];
    if (c) return `${c.at[0]},${c.at[1]} ${c.size[0]}x${c.size[1]}`;
  } catch { /* not Hyprland */ }
  try {   // anywhere else on Wayland: let the user draw the area
    console.log('Select the browser window to record…');
    return execFileSync('slurp', { encoding: 'utf8' }).trim();
  } catch { return null; }
}

// Seconds since wf-recorder started, logged per step into <file>.steps.json
// so record.sh and edit.sh can cut excerpts. Approximate: wf-recorder's
// first frame lands a fraction of a second after it starts.
let recordingStart = null;
const steplog = [];

async function startRecorder(file) {
  const geom = windowGeometry();
  const args = ['-y', '-f', resolve(file)];
  if (geom) args.push('-g', geom);
  else console.log('No window geometry found; recording the whole output.');
  const rec = spawn('wf-recorder', args, { stdio: ['ignore', 'ignore', 'pipe'] });
  recordingStart = Date.now();
  let err = '';
  rec.stderr.on('data', (d) => { err += d; });
  rec.on('error', (e) => die(`could not start wf-recorder: ${e.message}`));
  await sleep(1500);                     // wf-recorder needs a moment for its first frame
  if (rec.exitCode !== null) die(`wf-recorder stopped at once:\n${err}`);
  console.log(`Recording ${geom ?? 'the whole output'} to ${resolve(file)}`);
  return async () => {
    rec.kill('SIGINT');                  // SIGINT lets it finish the file
    if (rec.exitCode === null) await once(rec, 'exit');
    console.log(`Saved ${resolve(file)}`);
  };
}

// ---------------------------------------------------------------------------
// In-page helpers: a pointer that glides to what the bot clicks, captions.
// ---------------------------------------------------------------------------
const OVERLAY_CSS = `
#bot-cap{position:fixed;left:50%;bottom:72px;transform:translateX(-50%);z-index:9998;
  padding:9px 20px;border-radius:20px;background:var(--tx,#111);color:var(--bg,#fff);
  font:600 17px/1.3 ui-sans-serif,system-ui,sans-serif;opacity:0;transition:opacity .35s;
  pointer-events:none;white-space:nowrap}
#bot-cap.on{opacity:.93}
#bot-cur{position:fixed;left:0;top:0;width:26px;height:26px;z-index:9999;pointer-events:none;
  transform:translate(70vw,60vh);transition:transform .75s cubic-bezier(.45,0,.2,1)}
#bot-cur svg{filter:drop-shadow(0 1px 2px rgba(0,0,0,.45))}
#bot-cur i{position:absolute;left:-14px;top:-14px;width:28px;height:28px;border-radius:50%;
  border:2px solid #a78bfa;opacity:0}
#bot-cur.tap i{animation:bot-tap .45s ease-out}
@keyframes bot-tap{from{opacity:.9;transform:scale(.4)}to{opacity:0;transform:scale(1.5)}}`;
const CURSOR_HTML = '<i></i><svg width="26" height="26" viewBox="0 0 24 24">' +
  '<path d="M4 2l15 10.5-6.6 1.2 3.8 7.3-2.8 1.4-3.8-7.3L4 20z" fill="#fff" stroke="#111" stroke-width="1.4" stroke-linejoin="round"/></svg>';

function makeBot(page, captions) {
  const b = {
    async key(k) { await page.keyboard.press(k); await pause(250); },
    async type(text) { await page.keyboard.type(text, { delay: 95 * PACE }); },
    async caption(text) {
      if (!captions) return;
      await page.evaluate((t) => {
        const el = document.getElementById('bot-cap');
        el.classList.remove('on');
        setTimeout(() => { el.textContent = t; el.classList.add('on'); }, 250);
      }, text);
      await pause(600);
    },
    // Sidebar row whose name is exactly `name` (rows read "name ·degree").
    // The search box updates the list after a short delay, and the list is
    // re-sorted when it does, so wait that out before using the row.
    async waitForListItem(name) {
      await sleep(600);
      const esc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      const row = page.locator('#list .it[data-id]')
        .filter({ has: page.locator('.n', { hasText: new RegExp(`^${esc}\\s·`) }) }).first();
      await row.waitFor({ timeout: 10000 });
      return row;
    },
    // Empty the search box without leaving the focused node. While a node is
    // focused, a search fades every neighbour that doesn't match it.
    async clearSearch() {
      await page.fill('#q', '');           // fires the page's input handler
      await page.$eval('#q', (q) => q.blur());
    },
    // A name in the details panel's calls / called-by lists.
    detailsEntry(name) {
      const esc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      return page.locator('#det .c[data-id]', { hasText: new RegExp(`^${esc}(conditional)?$`) }).first();
    },
    // Glide the drawn pointer to the target, then click it. Playwright's own
    // click re-checks the element's position, so a list that re-renders
    // between the glide and the click can't send the click to another row.
    async click(locator) {
      await locator.scrollIntoViewIfNeeded();
      const box = await locator.boundingBox();
      if (!box) throw new Error('target is not on screen');
      const pos = { x: Math.min(box.width / 2, 70), y: box.height / 2 };
      await page.evaluate(([px, py]) => {
        document.getElementById('bot-cur').style.transform = `translate(${px}px,${py}px)`;
      }, [box.x + pos.x, box.y + pos.y]);
      await pause(900);
      await page.evaluate(() => {
        const c = document.getElementById('bot-cur');
        c.classList.remove('tap'); void c.offsetWidth; c.classList.add('tap');
      });
      await locator.click({ position: pos });
      await pause(300);
    },
    // Press `d` (cycle direction) until the select shows `value`; the page
    // shows a toast for each step, which reads well on video.
    async setDirection(value) {
      for (let i = 0; i < 8; i++) {
        if (await page.$eval('#dir', (s) => s.value) === value) return;
        await b.key('d');
        await pause(650);
      }
      throw new Error(`direction never reached ${value}`);
    },
  };
  return b;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
const server = opt.url ? { url: opt.url, stop: () => {} } : await startServer(resolve(opt.repo));
let browser, stopRecorder;
const cleanup = async () => {
  if (stopRecorder) { const s = stopRecorder; stopRecorder = null; await s(); }
  if (browser) { await browser.close().catch(() => {}); browser = null; }
  server.stop();
};
process.on('SIGINT', async () => { await cleanup(); process.exit(130); });

try {
  const ver = await checkVersion(server.url);
  console.log(`vizEulize ${ver} at ${server.url}`);

  browser = await chromium.launch({
    headless: Boolean(DRY),
    args: ['--ozone-platform-hint=auto', '--window-size=1600,940',
      ...(DRY ? [] : [`--force-device-scale-factor=${Number(opt.zoom) || 1}`])],
    ignoreDefaultArgs: ['--enable-automation'],   // no "controlled by automated software" bar
  });
  const context = await browser.newContext({
    viewport: DRY ? { width: 1600, height: 900 } : null,   // headed: follow the real window size
    colorScheme: opt.theme === 'light' ? 'light' : 'dark',
  });
  const page = await context.newPage();
  if (!DRY && !opt.windowed) {
    // Full screen like F11: no tabs or address bar. Chromium's --kiosk only
    // applies to its first window, and Playwright opens the page in a new one.
    const cdp = await context.newCDPSession(page);
    const { windowId } = await cdp.send('Browser.getWindowForTarget');
    await cdp.send('Browser.setWindowBounds', { windowId, bounds: { windowState: 'fullscreen' } });
    await sleep(800);
  }
  await page.goto(server.url);
  await page.waitForFunction(() => /^Loaded /.test(document.getElementById('status')?.textContent ?? ''),
    null, { timeout: 60000 });
  await page.addStyleTag({ content: OVERLAY_CSS });
  await page.evaluate((html) => {
    const cap = Object.assign(document.createElement('div'), { id: 'bot-cap' });
    const cur = Object.assign(document.createElement('div'), { id: 'bot-cur', innerHTML: html });
    document.body.append(cap, cur);
  }, CURSOR_HTML);
  await page.mouse.move(5, 5);                     // keep Playwright's pointer off the graph
  await sleep(1500);                               // first layout settles
  if (!DRY) console.log('Move your own mouse pointer off the browser window: the recording shows it.');

  if (DRY) {
    mkdirSync(DRY, { recursive: true });
    console.log(`Dry run: screenshots in ${resolve(DRY)}`);
  } else if (opt.record) {
    stopRecorder = await startRecorder(opt.record);
  } else {
    console.log('Browser ready. Start your recorder now, for example:\n' +
                '  wf-recorder -y -g "$(slurp)" -f vizeulize.mp4');
    for (let s = Number(opt.countdown) || 0; s > 0; s--) { process.stdout.write(`${s}… `); await sleep(1000); }
    console.log('go');
  }

  const bot = makeBot(page, !opt['no-captions']);
  const mark = (caption) => {
    if (recordingStart !== null) steplog.push({ caption, t: (Date.now() - recordingStart) / 1000 });
  };
  for (const [n, [caption, run]] of STEPS.entries()) {
    console.log(`  ${caption}`);
    mark(caption);
    await bot.caption(caption);
    await run(bot);
    if (DRY) await page.screenshot({ path: join(DRY, `step${n + 1}.png`) });
  }
  await page.evaluate(() => document.getElementById('bot-cap')?.classList.remove('on'));
  await pause(1200);
  mark('end');
  if (opt.record) writeFileSync(`${resolve(opt.record)}.steps.json`, JSON.stringify(steplog, null, 2) + '\n');
  if (!opt.record && !DRY) console.log('Done. Stop your recorder.');
} catch (e) {
  console.error(`vizeulize-bot: ${e.message}`);
  process.exitCode = 1;
} finally {
  await cleanup();
}
