import { test, expect } from '@playwright/test';
import { createServer, type Server } from 'node:net';
// Real HTTP/backend + browser on a disposable server, with a local SMTP sink
// that captures the emails MagiSend sends.
test.use({ locale: 'en-GB', actionTimeout: 10000 });

function smtpSink() {
  const messages: string[] = [];
  const server: Server = createServer((socket) => {
    let data = false;
    let body = '';
    socket.write('220 sink\r\n');
    socket.on('data', (chunk) => {
      for (const line of chunk.toString().split('\r\n')) {
        if (data) {
          if (line === '.') {
            data = false;
            messages.push(body.replace(/=\r?\n/g, '').replace(/=3D/g, '='));
            body = '';
            socket.write('250 ok\r\n');
          } else body += line + '\n';
          continue;
        }
        const command = line.toUpperCase();
        if (!command) continue;
        if (command.startsWith('EHLO') || command.startsWith('HELO')) socket.write('250 sink\r\n');
        else if (command.startsWith('DATA')) {
          data = true;
          socket.write('354 go\r\n');
        } else if (command.startsWith('QUIT')) socket.end('221 bye\r\n');
        else socket.write('250 ok\r\n');
      }
    });
  });
  return new Promise<{ port: number; messages: string[]; close: () => void }>((resolve) =>
    server.listen(0, '127.0.0.1', () =>
      resolve({
        port: (server.address() as { port: number }).port,
        messages,
        close: () => server.close(),
      }),
    ),
  );
}

test('invitation and password reset by email', async ({ page, browser, baseURL }) => {
  const sink = await smtpSink();
  const headers = { 'X-MagiSend': '1' };
  const admin = { username: 'mailadmin', password: 'disposable mail passphrase' };
  expect((await page.request.post('/api/setup', { data: admin, headers })).status()).toBe(201);
  expect((await page.request.post('/api/login', { data: admin, headers })).status()).toBe(200);
  const settings = {
    host: '127.0.0.1',
    port: sink.port,
    security: 'none',
    username: '',
    password: '',
    from: 'share@example.com',
    from_name: 'MagiSend',
    site_url: baseURL,
  };
  expect((await page.request.put('/api/admin/email', { data: settings, headers })).status()).toBe(
    200,
  );

  await page.goto('/admin');
  await page.getByLabel('Email address', { exact: true }).first().fill('friend@example.com');
  await page.getByRole('button', { name: 'Send invitation' }).click();
  await expect(page.getByText('Invitation sent to friend@example.com.')).toBeVisible();
  await expect.poll(() => sink.messages.length).toBe(1);
  const invite = sink.messages[0].match(/\/invite\/[a-f0-9]{48}/)![0];

  const guest = await browser.newContext({ locale: 'en-GB' });
  const friend = await guest.newPage();
  await friend.goto(invite);
  await expect(friend.getByText('You were invited as friend@example.com.')).toBeVisible();
  await friend.getByLabel('Username').fill('friend');
  await friend.getByLabel('Password', { exact: true }).fill('a friendly long passphrase');
  await friend.getByLabel('Repeat password').fill('a friendly long passphrase');
  await friend.getByRole('button', { name: 'Create account' }).click();
  await expect(friend.getByRole('heading', { name: 'From you. To anyone.' })).toBeVisible();
  await friend.getByRole('button', { name: 'Account menu' }).click();
  await friend.getByRole('button', { name: 'Log out' }).click();

  await friend.getByRole('button', { name: 'Forgot your password?' }).click();
  await friend.getByLabel('Username or email').fill('FRIEND@example.com');
  await friend.getByRole('button', { name: 'Send reset link' }).click();
  await expect(friend.getByText(/a reset link is on its way/)).toBeVisible();
  await expect.poll(() => sink.messages.length).toBe(2);
  const reset = sink.messages[1].match(/\/reset\/[a-f0-9]{48}/)![0];
  await friend.goto(reset);
  await friend.getByLabel('New password').fill('a completely new passphrase');
  await friend.getByLabel('Repeat password').fill('a completely new passphrase');
  await friend.getByRole('button', { name: 'Save new password' }).click();
  await expect(friend.getByText('Your password was changed.')).toBeVisible();
  await friend.getByRole('link', { name: 'Log in' }).click();
  await friend.getByLabel('Username', { exact: true }).fill('friend');
  await friend.getByLabel('Password', { exact: true }).fill('a completely new passphrase');
  await friend.getByRole('button', { name: 'Log in', exact: true }).click();
  await expect(friend.getByRole('heading', { name: 'From you. To anyone.' })).toBeVisible();
  await guest.close();
  sink.close();
});
