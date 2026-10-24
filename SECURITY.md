# Security

## Reporting a vulnerability

Please do not open a public issue for security problems. Use GitHub's **private vulnerability reporting** on this repository (Security → Report a vulnerability) with steps to reproduce and the affected version. You will get a reply as soon as possible, and a fix and advisory will be published once it is resolved.

## Design summary

- **Accounts**: passwords are hashed with bcrypt (cost 12). Failed sign-ins are limited per visitor and per account, and responses do not reveal whether a username exists. Sessions are random tokens (stored hashed) in `HttpOnly`, `SameSite=Strict` cookies, `Secure` when served over HTTPS. Changing or resetting a password and disabling an account end existing sessions and remove the account's API tokens.
- **Requests**: every state-changing API request requires a custom header and a same-origin `Origin`, which blocks cross-site request forgery. Request bodies and upload chunks have size limits.
- **Headers**: a Content-Security-Policy without inline scripts or styles, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, and HSTS for HTTPS origins.
- **Files**: downloads are always sent as attachments. Previews are recognised by content, not file name, and served with `nosniff` and a sandboxing Content-Security-Policy, so an uploaded HTML or SVG file can never run script; text and code are shown as text. Storage access refuses symlinks and non-regular files, and ZIP extraction checks every path and size.
- **Shares**: link passwords are hashed; write access for recipients requires sign-in and is checked on every request, so revoking it takes effect immediately.
- **Limits**: per-user and global quotas are reserved before bytes are accepted, so concurrent uploads cannot exceed them.

## Deployment advice

- Serve MagiSend over **HTTPS** whenever it is reachable from outside a trusted network; plain HTTP exposes passwords and files.
- The first administrator account can only be created with the one-time setup code the installer prints; keep it to yourself until setup is done.
- Use the installers or the Docker image: both run MagiSend as an unprivileged user (a hardened systemd service or a non-root container).
- Updates are only installed when the release's signature, made with a key kept off GitHub, and its checksums are valid, and only to newer versions. The updater runs as root and keeps its working files in a folder only root can write; it writes into the data folder only to put the saved database back during a rollback, after MagiSend was stopped, and only to those exact files. Docker images are built only from signed, published releases but are not signed themselves. See `docs/operations.md`.
- Back up the data directory regularly (see `docs/operations.md`).
