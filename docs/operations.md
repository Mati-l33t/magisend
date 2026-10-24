# Operations

## What the installers set up

| Path | Contents |
|---|---|
| `/opt/magisend/magisend` | The program (web interface included). |
| `/opt/magisend/update.sh` | The updater, run as root. |
| `/var/lib/magisend` | Database (`magisend.sqlite`) and stored files (`blobs/`). Owned by the `magisend` user, mode 0700. |
| `/etc/magisend/environment` | Local settings (port, public address, trusted proxies). |
| `magisend.service` | The hardened service running as `magisend`. |
| `magisend-update.path` / `.service` | Starts the updater when an administrator presses **Update**. |
| `/var/backups/magisend` | Database copies taken before each update (the last five). |

Useful commands:

```sh
systemctl status magisend
journalctl -u magisend -f
journalctl -u magisend-update        # update history
nano /etc/magisend/environment && systemctl restart magisend
```

Never run two MagiSend processes against the same data directory.

## Reverse proxy

MagiSend only needs plain HTTP forwarding; there are no WebSockets. Requirements for any proxy (Nginx, Caddy, Traefik, Nginx Proxy Manager):

- pass the original `Host` header and set `X-Forwarded-For` and `X-Forwarded-Proto`;
- allow request bodies of at least 5 MB (uploads are sent in 4 MiB pieces);
- do not buffer requests or responses, and allow a few minutes for slow transfers;
- set `MAGISEND_PUBLIC_ORIGIN` to the exact public `https://` origin and list the proxy in `MAGISEND_TRUSTED_PROXIES`.

Caddy example:

```caddy
share.example.com {
    reverse_proxy 192.168.1.50:8080 {
        flush_interval -1
    }
}
```

Nginx example:

```nginx
location / {
    proxy_pass http://192.168.1.50:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 8m;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 300s;
}
```

## Updates

Administrators see new releases under **Administration → Updates**. The update button (or `/opt/magisend/update.sh` as root) looks up the latest release on GitHub and only continues if it is newer than the installed version. It downloads the release, its `SHA256SUMS` and `SHA256SUMS.sig`, and has the installed MagiSend check the signature (see below) and the checksum. Then it stops MagiSend, copies the database, snapshots the files in the data folder with hard links (instant, no extra space), installs the new version and waits for it to report healthy. If anything fails after MagiSend was stopped, or the new version does not report healthy, the previous program, database and files are restored and started again. Files on added storage locations are not snapshotted; a new version does not change them while it starts. The result is shown in the web interface.

The updater runs as root. It keeps its status and the snapshot in `/var/lib/magisend-update`, a folder only root can write (it refuses to run if any folder above it can be written by others). It writes into the data folder only during a rollback, after MagiSend was stopped: it clears the exact database file names and the file store, then puts the saved ones back. The database files are only saved if they are plain files. The file store is snapshotted with hard links when the data folder shares a file system with `/var/lib`; otherwise it is left as it is (stored files are never changed in place) and only the database is rolled back.

### Release signatures

Each release's `SHA256SUMS` is signed with an Ed25519 key that is not stored on GitHub. The public key is built into MagiSend and published in [release-signing.pub](release-signing.pub). To check a download by hand:

```sh
base64 -d SHA256SUMS.sig > SHA256SUMS.sig.bin
openssl pkeyutl -verify -pubin -inkey release-signing.pub -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig.bin
sha256sum -c --ignore-missing SHA256SUMS
```

The installer (`install.sh`) checks the checksum; the signature protects updates, since a first installation trusts the installer it downloads.

To skip update checks entirely, set `MAGISEND_UPDATE_CHECK=false`.

Docker installations update with `docker compose pull && docker compose up -d`; the data volume is kept.

## Backup and restore

The data directory holds the database and all stored files; back them up together while MagiSend is stopped:

```sh
systemctl stop magisend
tar -C /var/lib -czf magisend-$(date +%F).tgz magisend
systemctl start magisend
```

To restore, stop the service, replace `/var/lib/magisend` with the archive contents (owned by `magisend`), and start it again with the same or a newer MagiSend version. Downgrading the program alone is not supported once newer versions have written data.

Docker (with the container stopped): `docker run --rm -v magisend_data:/data -v "$PWD":/backup alpine tar -czf /backup/magisend.tgz -C /data .`

### Built-in backups

**Administration → Backups** makes archives while MagiSend keeps running: always the database, and optionally the files in the data folder and on added storage locations. Each archive carries a `RESTORE.txt`. Archive names include this installation's ID, so several installations can share one backup folder without listing or deleting each other's archives. A backup that could not copy everything (a storage location offline, an unreadable file) is named `…-incomplete.tar`, lists what is missing in `MISSING.txt`, and is reported on the page; the newest complete archive is always kept, however many incomplete ones follow. A backup that makes no progress for ten minutes (a share that stopped answering) is given up.

Restoring an older database next to newer files is safe: MagiSend starts even if files the database knows were deleted since (they are listed under **Administration → Storage**, where they can be removed), and files the database does not know are set aside for 30 days instead of deleted, to be put back from the same page.

## Uninstall

```sh
systemctl disable --now magisend magisend-update.path
rm -f /etc/systemd/system/magisend.service /etc/systemd/system/magisend-update.service /etc/systemd/system/magisend-update.path
systemctl daemon-reload
rm -rf /opt/magisend /etc/magisend
# To also delete all files and accounts:
rm -rf /var/lib/magisend /var/backups/magisend && userdel magisend
```

## Uploading from scripts and ShareX

Create a token under **Account menu → API tokens** (a ready-made ShareX config can be downloaded there). Then:

```sh
curl -H "Authorization: Bearer ms_…" -F "file=@photo.jpg" "https://share.example.com/api/v1/upload?days=7"
```

Several `-F file=@…` parts become one share. Optional query parameters: `days` (0 = no expiry), `downloads` (per-file limit), `title`, `password`. The JSON answer contains `url`. Tokens can only upload; revoke them in the same dialog.

## Storage limits

The storage budget (Administration → Limits) caps all stored files together; new installations start at three quarters of the disk. Per-user quotas, maximum file and share size and default expiry are set in the same place. Unpublished upload drafts expire after 24 hours and a cleanup runs every five minutes.
