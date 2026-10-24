import { describe, expect, it } from 'vitest';
import JSZip from 'jszip';
import { openArchive } from './officeFile';

// Builds an archive whose one part says it unpacks to `declared` bytes.
async function archive(content: string, declared?: number) {
  const zip = new JSZip();
  zip.file('word/document.xml', content);
  const bytes = await zip.generateAsync({ type: 'uint8array', compression: 'DEFLATE' });
  if (declared !== undefined) {
    const view = new DataView(bytes.buffer);
    for (let i = 0; i + 4 <= bytes.length; i++) {
      const signature = view.getUint32(i, true);
      if (signature === 0x04034b50) view.setUint32(i + 22, declared, true);
      if (signature === 0x02014b50) view.setUint32(i + 24, declared, true);
    }
  }
  return bytes.buffer as ArrayBuffer;
}

describe('openArchive', () => {
  it('opens an honest archive', async () => {
    await expect(openArchive(await archive('<w:document/>'))).resolves.toBeTruthy();
  });
  it('refuses a part that unpacks to more than it declares', async () => {
    await expect(openArchive(await archive('A'.repeat(200000), 1000))).rejects.toBeTruthy();
  });
});
