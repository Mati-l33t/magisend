// MagiSend service worker. It never caches API responses or user files: only
// the offline page, icons and hashed build assets. Files shared to the
// installed app from the system share sheet are parked in a cache just long
// enough for the app to pick them up.
const SHELL = 'magisend-shell-v1';
const ASSETS = 'magisend-assets-v1';
const SHARED = 'magisend-shared';
const KEEP = [SHELL, ASSETS, SHARED];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((cache) =>
        cache.addAll(['/offline.html', '/offline.css', '/icons/icon-192.png']),
      )
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) =>
        Promise.all(names.filter((name) => !KEEP.includes(name)).map((name) => caches.delete(name))),
      )
      .then(() => self.clients.claim()),
  );
});

async function receiveShare(request) {
  const form = await request.formData();
  const files = form.getAll('files').filter((value) => value instanceof File);
  await caches.delete(SHARED);
  const cache = await caches.open(SHARED);
  await Promise.all(
    files.map((file, index) =>
      cache.put(
        '/shared/' + index,
        new Response(file, {
          headers: {
            'Content-Type': file.type || 'application/octet-stream',
            'X-File-Name': encodeURIComponent(file.name || 'shared-file'),
          },
        }),
      ),
    ),
  );
  return Response.redirect('/?shared=' + files.length, 303);
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (request.method === 'POST' && url.pathname === '/share-target') {
    event.respondWith(receiveShare(request));
    return;
  }
  if (request.method !== 'GET' || url.pathname.startsWith('/api/')) return;
  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(() => caches.match('/offline.html')));
    return;
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(
      caches.open(ASSETS).then(async (cache) => {
        const cached = await cache.match(request);
        if (cached) return cached;
        const response = await fetch(request);
        if (response.ok) cache.put(request, response.clone());
        return response;
      }),
    );
  }
});
