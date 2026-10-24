// Runs the browser tests against disposable MagiSend servers.
//
// Each group gets its own freshly started server on 127.0.0.1:18087 with an
// empty temporary data directory, because several specs perform the one-time
// first-administrator setup. Usage: npm run e2e [-- spec-name ...]
import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const web = resolve(import.meta.dirname, '..');
const root = resolve(web, '..');
const origin = 'http://127.0.0.1:18087';
const groups = [
  ['layout.spec.ts'],
  ['app.spec.ts', 'i18n.spec.ts', 'previews.spec.ts', 'share-details.spec.ts'],
  ['folders.spec.ts'],
  ['pwa.spec.ts'],
  ['requests.spec.ts'],
  ['twofactor.spec.ts'],
  ['accountmail.spec.ts'],
  ['media.spec.ts'],
  ['tokens.spec.ts'],
  ['resume.spec.ts'],
  ['resume-request.spec.ts'],
  ['zoom.spec.ts'],
  ['storage.spec.ts'],
  ['admin-layout.spec.ts'],
  ['text-help.spec.ts'],
  ['keep.spec.ts'],
  ['trash.spec.ts'],
  ['versions.spec.ts'],
  ['push.spec.ts'],
  ['gallery.spec.ts'],
  ['notes.spec.ts'],
];
const only = process.argv.slice(2);

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

async function waitForServer() {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(origin + '/api/status')).ok) return;
    } catch {}
    await new Promise((done) => setTimeout(done, 100));
  }
  throw new Error('server did not start on ' + origin);
}

const work = mkdtempSync(join(tmpdir(), 'magisend-e2e-'));
const binary = process.env.MAGISEND_BIN || join(work, 'magisend');
if (!process.env.MAGISEND_BIN) run('go', ['build', '-o', binary, '.'], root);
run('npm', ['run', 'build'], web);

let failed = false;
try {
  for (const group of groups) {
    const specs = only.length ? group.filter((spec) => only.some((o) => spec.includes(o))) : group;
    if (!specs.length) continue;
    const data = mkdtempSync(join(work, 'data-'));
    const server = spawn(binary, [], {
      cwd: root,
      env: { ...process.env, MAGISEND_DATA: data, MAGISEND_LISTEN: '127.0.0.1:18087' },
      stdio: ['ignore', 'ignore', 'inherit'],
    });
    try {
      await waitForServer();
      const result = spawnSync('npx', ['playwright', 'test', ...specs], {
        cwd: web,
        stdio: 'inherit',
      });
      failed ||= result.status !== 0;
    } finally {
      if (server.exitCode === null) {
        const exited = new Promise((done) => server.once('exit', done));
        server.kill();
        await exited;
      }
    }
  }
} finally {
  rmSync(work, { recursive: true, force: true });
}
process.exit(failed ? 1 : 0);
