import sharp from 'sharp';
import { readFile, mkdir } from 'node:fs/promises';
const en = JSON.parse(await readFile(new URL('./src/locales/en.json', import.meta.url), 'utf8'));
const text = (key) => en[key].replace(/&/g, '&amp;').replace(/</g, '&lt;');
await mkdir('public', { recursive: true });
await sharp('brand/logo-source.png')
  .extract({ left: 110, top: 130, width: 1080, height: 940 })
  .resize(512, 512, { fit: 'contain', background: '#000' })
  .png()
  .toFile('public/logo.png');
const mark = await sharp('public/logo.png').resize(400, 400).png().toBuffer();
const base = Buffer.from(
  `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630"><rect width="1200" height="630" fill="#050505"/><rect x="60" y="70" width="40" height="4" fill="#ff5f00"/><text x="60" y="128" fill="#aaa" font-family="sans-serif" font-size="17" letter-spacing="3">${text('brand.headerByline')}</text><text x="60" y="280" fill="#fff" font-family="sans-serif" font-size="88" font-weight="bold">${text('brand.name')}</text><text x="60" y="370" fill="#ff5f00" font-family="sans-serif" font-size="55" font-weight="bold">${text('brand.tagline')}</text><line x1="60" y1="540" x2="1140" y2="540" stroke="#333"/><text x="60" y="580" fill="#aaa" font-family="sans-serif" font-size="20">${text('app.edition')}</text></svg>`,
);
await sharp(base)
  .composite([{ input: mark, left: 770, top: 90 }])
  .png()
  .toFile('public/og.png');
console.log('Generated approved logo crop and OG image');
