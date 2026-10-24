// Generates the installable-app icons from the approved logo crop.
import sharp from 'sharp';

const logo = 'public/logo.png';
const background = '#050505';
for (const size of [192, 512])
  await sharp(logo)
    .resize(size, size)
    .png({ compressionLevel: 9, palette: true, quality: 90 })
    .toFile(`public/icons/icon-${size}.png`);
await sharp(logo)
  .resize(180, 180)
  .png({ compressionLevel: 9, palette: true, quality: 90 })
  .toFile('public/icons/apple-touch-icon.png');
// Maskable icons are cropped to a circle or squircle by the launcher; keep the
// mark inside the central safe zone.
const mark = await sharp(logo).resize(400, 400).png().toBuffer();
await sharp({ create: { width: 512, height: 512, channels: 4, background } })
  .composite([{ input: mark, left: 56, top: 56 }])
  .png({ compressionLevel: 9, palette: true, quality: 90 })
  .toFile('public/icons/icon-maskable-512.png');
console.log('Generated PWA icons');
