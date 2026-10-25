// HEIC and HEIF photos for browsers that cannot draw them (all but Safari):
// decoded here with libheif, which is only loaded when one is opened, and
// turned into a JPEG the browser can show.
const maxPixels = 200_000_000;

interface HeifImage {
  get_width(): number;
  get_height(): number;
  display(target: ImageData, done: (shown: ImageData | null) => void): void;
  free?(): void;
}
interface Libheif {
  HeifDecoder: new () => { decode(data: Uint8Array): HeifImage[]; free?(): void };
}

let library: Promise<Libheif> | null = null;
function libheif() {
  library ??= import('libheif-js/libheif-wasm/libheif-bundle.mjs').then((module) =>
    (module.default as () => Libheif)(),
  );
  return library;
}

export function isHeif(type: string) {
  return type === 'image/heic' || type === 'image/heif';
}

// Whether this browser draws the picture itself.
export async function browserDraws(blob: Blob) {
  const url = URL.createObjectURL(blob);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    return image.naturalWidth > 0;
  } catch {
    return false;
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function heifToJPEG(blob: Blob): Promise<Blob> {
  const { HeifDecoder } = await libheif();
  const decoder = new HeifDecoder();
  const images = decoder.decode(new Uint8Array(await blob.arrayBuffer()));
  try {
    const image = images[0];
    if (!image) throw new Error('not a HEIF picture');
    const width = image.get_width();
    const height = image.get_height();
    if (!width || !height || width * height > maxPixels) throw new Error('too large');
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('no canvas');
    const pixels = context.createImageData(width, height);
    await new Promise<void>((resolve, reject) =>
      image.display(pixels, (shown) => (shown ? resolve() : reject(new Error('unreadable')))),
    );
    context.putImageData(pixels, 0, 0);
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('unreadable'))), 'image/jpeg', 0.92),
    );
  } finally {
    for (const image of images) image.free?.();
    decoder.free?.();
  }
}
