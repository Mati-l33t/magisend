// Takes the README screenshots: seeds a fresh MagiSend with sample data,
// captures it in every look and frames the captures.
//
// From the repository root, after `cd web && npm run build`:
//   go build -tags embedui -o magisend . &&
//     MAGISEND_DATA=$(mktemp -d) MAGISEND_LISTEN=127.0.0.1:18090 ./magisend &
//   node web/scripts/screenshots.mjs http://127.0.0.1:18090 docs/screenshots
import { join, resolve } from 'node:path';
import { mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { chromium } from '@playwright/test';
import sharp from 'sharp';

const web = resolve(import.meta.dirname, '..');
const [origin, out] = process.argv.slice(2);
const headers = { 'X-MagiSend': '1' };

const browser = await chromium.launch();
const maker = await browser.newPage();

// Sample photos: simple painted landscapes.
const scenes = {
  'Lofoten sunset.jpg': ['#ff7e5f', '#feb47b', '#2c3e50', '#1a252f'],
  'Fjord morning.jpg': ['#89c4f4', '#d6eaf8', '#2e4053', '#1b2631'],
  'Forest trail.jpg': ['#a8e063', '#56ab2f', '#1e5631', '#0b2e13'],
  'City lights.jpg': ['#0f2027', '#203a43', '#fca311', '#14213d'],
  'Desert dunes.jpg': ['#f6d365', '#fda085', '#c97b3a', '#8e5524'],
  'Northern lights.jpg': ['#0b132b', '#1c2541', '#3ddc97', '#0b132b'],
};
async function photo([a, b, c, d]) {
  await maker.setViewportSize({ width: 1200, height: 800 });
  await maker.setContent(`<body style="margin:0;width:1200px;height:800px;overflow:hidden;
    background:linear-gradient(${a},${b})">
    <div style="position:absolute;left:760px;top:170px;width:150px;height:150px;border-radius:50%;
      background:radial-gradient(#fff8,#fff0 70%)"></div>
    <div style="position:absolute;inset:0;background:${c};
      clip-path:polygon(0 70%,18% 42%,33% 62%,52% 30%,70% 58%,85% 40%,100% 60%,100% 100%,0 100%)"></div>
    <div style="position:absolute;inset:0;background:${d};
      clip-path:polygon(0 85%,25% 70%,45% 82%,68% 66%,100% 80%,100% 100%,0 100%)"></div></body>`);
  return maker.screenshot({ type: 'jpeg', quality: 85 });
}
async function pdf(title, lines) {
  await maker.setContent(`<body style="font:15px/1.6 sans-serif;padding:40px">
    <h1 style="color:#e8590c">${title}</h1>${lines.map((l) => `<p>${l}</p>`).join('')}</body>`);
  return maker.pdf({ format: 'A4' });
}

const context = await browser.newContext({
  baseURL: origin,
  viewport: { width: 1440, height: 900 },
});
const api = context.request;
async function json(path, data, method = 'POST') {
  const r = await api.fetch(path, { method, data, headers });
  if (!r.ok()) throw new Error(method + ' ' + path + ' ' + r.status() + ' ' + (await r.text()));
  return r.status() === 204 ? null : r.json();
}
const credentials = { username: 'alex', password: 'a long sample passphrase' };
await json('/api/setup', credentials);
await json('/api/login', credentials);
await json('/api/account/profile', { display_name: 'Alex Morgan' }, 'PUT').catch(() => {});

async function upload(folder, files) {
  const up = await json('/api/library/uploads', {
    folder_id: folder,
    files: files.map(([name, data]) => ({ name, size: data.length })),
  });
  for (let i = 0; i < files.length; i++) {
    const r = await api.patch('/api/uploads/' + up.files[i].id, {
      headers: { ...headers, 'Upload-Offset': '0', 'Content-Type': 'application/octet-stream' },
      data: files[i][1],
    });
    if (!r.ok()) throw new Error('chunk ' + r.status());
  }
  await json('/api/shares/' + up.id + '/publish', {});
  return up.files.map((f) => f.id);
}
const folder = async (name, parent_id = '') => (await json('/api/folders', { name, parent_id })).id;

const holiday = await folder('Holiday 2026');
const work = await folder('Work');
await folder('Clients', work);
await folder('Invoices', work);
await folder('Recipes');
const photos = [];
for (const [name, colors] of Object.entries(scenes)) photos.push([name, await photo(colors)]);
const photoIds = await upload(holiday, photos);
await upload(work, [
  [
    'Project plan.pdf',
    await pdf('Project plan', [
      'Milestones for the spring launch.',
      'Design review in March, beta in April, release in May.',
    ]),
  ],
  [
    'Quarterly report.pdf',
    await pdf('Quarterly report', ['Revenue grew 12% compared with last quarter.']),
  ],
  ['Brand colours.txt', Buffer.from('Primary  #ff5f00\nDark     #0e0e0e\nLight    #fafafa\n')],
]);
await upload('', [['Boarding pass.pdf', await pdf('Boarding pass', ['OSL → BOO, seat 14A'])]]);

const notes = [
  ['Weekly shop', '- [x] Milk\n- [x] Bread\n- [ ] Coffee beans\n- [ ] Tomatoes\n- [ ] Pasta'],
  [
    'Packing for Lofoten',
    '- [x] Passport\n- [x] Camera and charger\n- [ ] Rain jacket\n- [ ] Hiking boots',
  ],
  [
    '',
    '# Ideas for the garden\nRaised beds along the fence, a small herb corner by the kitchen door and **more tulips** next spring.',
  ],
  [
    'Meeting notes',
    '## Launch sync\n- Beta feedback is positive\n- Ship the mobile layout first\n- Next meeting on Friday',
  ],
];
for (const [title, text] of notes)
  await json('/api/library/notes', { title, text, folder_id: '', language: 'en' });

const day = 86400;
const link = await json('/api/library/shares', { files: photoIds, title: 'Holiday 2026' });
await json(
  '/api/shares/' + link.id,
  { title: 'Holiday 2026', description: 'Photos from our trip up north. Enjoy!', days: 14 },
  'PATCH',
);
await json('/api/library/shares', { folder_id: work }).catch(() => {});

// ---- Captures, at twice the pixel density so they stay sharp when framed.
const raw = join(tmpdir(), 'magisend-screenshots');
mkdirSync(raw, { recursive: true });
const state = await context.storageState();
async function capture(
  name,
  path,
  { theme = 'dark', look = 'aurora', phone = false, signedIn = true, then } = {},
) {
  const ctx = await browser.newContext({
    baseURL: origin,
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    isMobile: phone,
    hasTouch: phone,
    ...(signedIn ? { storageState: state } : {}),
  });
  await ctx.addInitScript(
    ([theme, look]) => {
      localStorage.setItem('magisend-language', 'en');
      localStorage.setItem('magisend-theme', theme);
      localStorage.setItem('magisend-look', look);
      localStorage.setItem('magisend-library-view-' + look, 'grid');
    },
    [theme, look],
  );
  const p = await ctx.newPage();
  await p.goto(path);
  await p.waitForTimeout(1200);
  if (then) await then(p);
  await p.waitForTimeout(1500);
  await p.screenshot({ path: join(raw, name + '.png') });
  await ctx.close();
}
const holidayFolder = (p) =>
  p
    .locator('.folder-list')
    .getByRole('button', { name: /^Holiday 2026/ })
    .click();

for (const [look, theme] of [
  ['aurora', 'dark'],
  ['classic', 'dark'],
  ['refined', 'light'],
  ['studio', 'light'],
])
  await capture(`library-${look}-${theme}`, '/library', { look, theme, then: holidayFolder });
await capture('notes-aurora-dark', '/library/notes');
await capture('admin-refined-dark', '/admin', { look: 'refined' });
await capture('send-aurora-light', '/send', {
  theme: 'light',
  then: (p) =>
    p
      .locator('input[type=file]')
      .first()
      .setInputFiles(
        photos.slice(0, 3).map(([name, buffer]) => ({ name, mimeType: 'image/jpeg', buffer })),
      ),
});
await capture('ready-studio-light', '/send', {
  theme: 'light',
  look: 'studio',
  then: async (p) => {
    await p
      .locator('input[type=file]')
      .first()
      .setInputFiles(
        photos.slice(3, 6).map(([name, buffer]) => ({ name, mimeType: 'image/jpeg', buffer })),
      );
    await p.waitForTimeout(800);
    await p.getByRole('button', { name: /Create share link/ }).click();
  },
});
await capture('viewer-dark', '/s/' + link.id, {
  signedIn: false,
  then: (p) => p.getByRole('button', { name: 'Lofoten sunset.jpg' }).first().click(),
});
await capture('phone-share', '/s/' + link.id, { phone: true, signedIn: false });
await capture('phone-notes', '/library/notes', { phone: true });
await capture('phone-library', '/library', {
  phone: true,
  theme: 'light',
  look: 'refined',
  then: holidayFolder,
});

// ---- Frames.
const font = (file) =>
  'data:font/woff2;base64,' + readFileSync(join(web, 'dist/assets', file)).toString('base64');
const fontFile = (prefix) =>
  readdirSync(join(web, 'dist/assets')).find((f) => f.startsWith(prefix));
const fonts = `
  @font-face { font-family: Geist; src: url(${font(fontFile('geist-latin-wght-normal'))}); font-weight: 100 900; }
  @font-face { font-family: Bricolage; src: url(${font(fontFile('bricolage-grotesque-latin-wght-normal'))}); font-weight: 200 800; }`;
const image = (name) =>
  'data:image/png;base64,' + readFileSync(join(raw, name + '.png')).toString('base64');
const style = `${fonts}
  * { box-sizing: border-box; margin: 0; }
  html, body { background: transparent; }
  .canvas {
    display: inline-flex; flex-direction: column; align-items: center; gap: 28px; padding: 56px;
    border-radius: 28px; font-family: Geist, sans-serif; color: #f4ece6;
    background:
      radial-gradient(900px 520px at 12% -10%, rgba(255, 95, 0, 0.55), transparent 62%),
      radial-gradient(760px 520px at 108% 112%, rgba(255, 150, 60, 0.32), transparent 60%),
      linear-gradient(140deg, #1c0f08, #0c0b0e 55%, #120c10);
  }
  .row { display: flex; gap: 36px; align-items: flex-start; }
  .win { border-radius: 14px; overflow: hidden; background: #18171b; box-shadow: 0 30px 70px rgba(0,0,0,.5), 0 0 0 1px rgba(255,255,255,.09); }
  .win.light { background: #f1efed; box-shadow: 0 30px 70px rgba(0,0,0,.45), 0 0 0 1px rgba(255,255,255,.25); }
  .bar { position: relative; display: flex; align-items: center; gap: 8px; height: 38px; padding: 0 16px; }
  .bar i { width: 12px; height: 12px; border-radius: 50%; background: #ff5f57; }
  .bar i:nth-child(2) { background: #febc2e; } .bar i:nth-child(3) { background: #28c840; }
  .url { position: absolute; left: 50%; transform: translateX(-50%); padding: 4px 18px; border-radius: 8px;
    font-size: 13px; color: #a7a2a8; background: rgba(255,255,255,.07); }
  .light .url { color: #6b6670; background: rgba(0,0,0,.06); }
  .win img { display: block; }
  .phone { padding: 12px; border-radius: 54px; background: #0b0b0c;
    box-shadow: 0 30px 70px rgba(0,0,0,.55), 0 0 0 2px #2b2a2e, inset 0 0 0 1px #3a393d; position: relative; }
  .screen { width: 390px; border-radius: 42px; overflow: hidden; }
  .screen img { display: block; width: 390px; }
  .status { display: flex; align-items: center; justify-content: space-between; height: 50px; padding: 6px 30px 0 34px;
    font-size: 16px; font-weight: 600; color: #fff; }
  .status.light { color: #111; }
  .icons { display: flex; align-items: center; gap: 7px; }
  .signal { display: flex; align-items: flex-end; gap: 2px; height: 12px; }
  .signal b { width: 3px; background: currentColor; border-radius: 1px; }
  .signal b:nth-child(1) { height: 4px; } .signal b:nth-child(2) { height: 6px; }
  .signal b:nth-child(3) { height: 9px; } .signal b:nth-child(4) { height: 12px; }
  .battery { position: relative; width: 25px; height: 12px; border: 1.5px solid currentColor; border-radius: 4px; opacity: .9; }
  .battery::before { content: ''; position: absolute; inset: 1.5px; right: 5px; border-radius: 1.5px; background: currentColor; }
  .battery::after { content: ''; position: absolute; right: -4px; top: 3px; width: 2px; height: 4px; border-radius: 1px; background: currentColor; }
  .phone::after { content: ''; position: absolute; top: 22px; left: 50%; width: 112px; height: 32px;
    transform: translateX(-50%); border-radius: 20px; background: #000; }
  .label { display: flex; justify-content: center; gap: 8px; margin-top: 14px; font-size: 17px; font-weight: 500; }
  .label b { font-weight: 650; color: #ffb27a; }
  h1 { font-family: Bricolage, sans-serif; font-size: 40px; font-weight: 700; letter-spacing: -0.02em; }
  h1 + p { margin-top: -18px; font-size: 18px; color: #c9bfb8; }`;
const win = (name, url, width, light = false, label = '') =>
  `<div><div class="win${light ? ' light' : ''}" style="width:${width}px"><div class="bar"><i></i><i></i><i></i>
   <span class="url">share.example.com${url}</span></div><img src="${image(name)}" style="width:${width}px"></div>
   ${label ? `<div class="label">${label}</div>` : ''}</div>`;
// A phone shows a status bar above the page, in the colour of the page's top
// edge, so the camera cutout never covers the app.
async function topColour(name) {
  const { channels } = await sharp(join(raw, name + '.png'))
    .extract({ left: 0, top: 0, width: 780, height: 6 })
    .stats();
  const [r, g, b] = channels.map((c) => Math.round(c.mean));
  return { css: `rgb(${r},${g},${b})`, dark: r * 0.3 + g * 0.59 + b * 0.11 < 128 };
}
async function phone(name) {
  const top = await topColour(name);
  return `<div class="phone"><div class="screen"><div class="status${top.dark ? '' : ' light'}" style="background:${top.css}">
    <span>9:41</span><span class="icons"><i class="signal"><b></b><b></b><b></b><b></b></i><i class="battery"></i></span></div>
    <img src="${image(name)}"></div></div>`;
}
const framer = await browser.newPage({
  deviceScaleFactor: 1.5,
  viewport: { width: 2400, height: 1600 },
});
async function frame(file, body) {
  await framer.setContent(`<style>${style}</style><div class="canvas">${body}</div>`);
  await framer.evaluate(() => document.fonts.ready);
  await framer.waitForTimeout(300);
  await framer.locator('.canvas').screenshot({ path: join(out, file), omitBackground: true });
}

await frame('library.png', win('library-aurora-dark', '/library', 1280));
await frame(
  'looks.png',
  `<h1>Four looks, dark and light</h1><p>Every person picks their own; the administrator sets the default.</p>
  <div class="row">${win('library-aurora-dark', '/library', 680, false, '<b>Aurora</b> dark')}${win('library-classic-dark', '/library', 680, false, '<b>Classic</b> dark')}</div>
  <div class="row">${win('library-refined-light', '/library', 680, true, '<b>Refined</b> light')}${win('library-studio-light', '/library', 680, true, '<b>Studio</b> light')}</div>`,
);
await frame('send.png', win('send-aurora-light', '/send', 1100, true));
await frame('admin.png', win('admin-refined-dark', '/admin', 1100));
await frame('notes.png', win('notes-aurora-dark', '/library/notes', 1100));
await frame('ready.png', win('ready-studio-light', '/send', 1100, true));
await frame('viewer.png', win('viewer-dark', '/s/holiday-2026', 1100));
await frame(
  'phones.png',
  `<div class="row">${await phone('phone-share')}${await phone('phone-notes')}${await phone('phone-library')}</div>`,
);

await browser.close();
