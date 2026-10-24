# Changelog

All notable changes to MagiSend are listed here. Versions follow [semantic versioning](https://semver.org).

## 1.0.4

Fairer downloads.

- **Download limits hold against Range requests:** every request that delivers the start of a file counts, including suffix ranges and ranges served as the whole file, and conditional headers no longer change what is sent. Resuming is free only for the client whose download was counted.
- **Nothing is used up by a download that fails:** a download is counted only once the file is open and verified, so an offline NAS or a damaged file no longer spends a one-time link.
- **Slow or hostile clients cannot block downloads:** 32 download slots instead of 4, at most 6 per visitor, and a connection must keep receiving at least about 2 KB/s.

## 1.0.3

Fixes for the files set aside in 1.0.2.

- **A copy is only deleted as a leftover when the other copy is verified:** a stored file the database places in another storage location is now only deleted when that location is reachable and holds the file at its recorded size. Otherwise it is set aside, so the only copy is never lost while a NAS is offline or after an older database was restored.
- **Files go back to the account that owned them:** the index records owners by account, and files whose account no longer exists go to the administrator's Recovered files, in a folder named after the old owner, never to someone who now has that name.

## 1.0.2

Safer restores and smaller backups.

- **Files are never lost after a restore:** files in the storage folders that the database does not know, for example after restoring an older backup, are set aside for 30 days instead of deleted, and can be put back into the library from Administration → Storage. A small index next to the files lets MagiSend put them back with their names, owners and folders.
- **Backups without storage locations:** backups that include files can now leave out the files on added storage locations (a NAS), which is the default for new settings; the backup page shows how large the database, the data folder's files and each storage location are, and how much space the kept backups will take. Settings that already included files keep including everything.

## 1.0.1

Security hardening.

- **Signed releases:** every release is signed with a key kept off GitHub, and the updater only installs a release whose signature the installed MagiSend accepts. Updates only ever go to newer versions.
- **Safer updater:** it keeps its status and snapshot in a folder only root can write, instead of the app's data folder. Any failure after MagiSend was stopped now restores the previous version and starts it again.
- **Setup code:** the first administrator account needs a one-time code that the installer prints and the server logs, so nobody else on the network can claim a fresh installation.
- **Backups say when they are incomplete:** files on an unavailable storage location or files that cannot be read are listed in the admin page and in `MISSING.txt` in the backup, and files deleted while a backup runs are kept until it is done.
- **Releases wait for the tests**, and the build steps are pinned to exact versions.

## 1.0.0

First public release.

- **Notes:** Markdown notes in the library with a typed title or one taken from the first line, autosave, checklists that can be ticked off, a notes view with search, and a reading page when a note is shared.
- **Trash:** deleted library files can be restored for 30 days, or emptied right away.
- **Versions:** replace a file with a new upload while its links keep working; every file keeps a history that can be opened and restored.
- **Link previews in chat apps:** shared links show a title, file count and picture in Discord, Slack and other apps.
- **Expiry reminders:** an email before a link expires, with a one-click way to keep it open.
- **Push notifications** to ntfy, Gotify, Discord or a webhook for downloads and uploads.
- **Photo location removal:** GPS data and camera serial numbers can be stripped from photos when they are shared.
- **Gallery:** the viewer steps between files with buttons or arrow keys and plays a slideshow of pictures.
- **ZIP extraction** into a library folder, with folders kept and name clashes numbered.
- **Backups:** scheduled or on-demand backups of the database and files under Administration, with download and retention.
- **Nested folders** in the library and on shared folder links; folder counts include subfolders.
- **Send page:** files sent as a link can be kept in a chosen library folder after the link expires.
- **Word, Excel, Markdown and code previews,** with syntax highlighting and line numbers.
- **Four looks** to choose from, profiles with display names and pictures, and search across pages, links and files.
- **Folder upload:** drop or choose a folder; in the library it becomes a folder of the same name.
- **Previews:** PDF documents rendered with pdf.js, video and audio players with seeking, in shares, on public links and in the library. Images and PDFs zoom with the buttons, the mouse wheel (Ctrl + wheel in PDFs), a double click or a pinch on phones, and every preview opens in full screen.
- **Sharing:** password-protected, expiring or permanent links with custom names, descriptions and QR codes, editable after sending; resumable uploads; streaming ZIP downloads; safe image previews and thumbnails.
- **Reliable uploads:** files go up in 4 MiB pieces, three files at a time, with speed and time left shown. A dropped connection is retried automatically, and an interrupted upload continues where it stopped, on the Send page, in the library and through upload links.
- **Library:** private files and folders, starred files; live folder links and fixed selections.
- **Storage locations:** keep files on a mounted network share or a second disk, chosen in Administration; move files between locations with checked copies. Files on an unreachable share pause while the rest of the app keeps working, and administrators are emailed when a share goes offline and comes back.
- **Collaboration:** named, signed-in recipients can be allowed to edit text files, add files or delete files.
- **Text editing** of files up to 256 KiB with version history, conflict detection and restore.
- **Permissions:** capability defaults with per-user overrides (administrators have every capability), plus per-link grants from the sender.
- **Email (SMTP):** send share links by email with a personal message, and get bundled notifications when your files are downloaded; download counts per share.
- **Upload requests:** links through which anyone can upload files into a folder of your library, with optional password, expiry and email notifications.
- **Download limits:** let each file be downloaded a set number of times (or once); the link then closes and its files are deleted. Per-file download statistics for the sender.
- **Invitations and password reset:** administrators invite people by email; users reset a forgotten password through a one-hour, single-use link. Links in these emails use an address the administrator set, never the requesting browser's.
- **API tokens:** personal upload keys for ShareX, scripts and curl (POST /api/v1/upload returns the share link); a token cannot sign in or read files.
- **Two-step login:** authenticator-app codes (TOTP) with single-use recovery codes; administrators can reset it for a lost phone.
- **Accounts:** optional email address when signing up; change your own password; administrators can reset passwords, disable and delete accounts.
- **Admin overview and activity log:** accounts, links, files, storage, downloads and failed sign-ins at a glance, with hints for missing setup; a 90-day log of security-relevant events with who, when and from which IP address.
- **Updates:** the app checks GitHub releases and administrators can update from the web interface; a failed update is rolled back automatically.
- **Installable app (PWA)** with an offline page and sharing from the phone's share menu.
- **Installers** for Proxmox VE (LXC), Debian/Ubuntu and Docker; static binaries for amd64 and arm64.
- **Security:** trusted reverse proxies, Secure cookies and HSTS behind HTTPS, per-visitor and per-account sign-in limits, strict Content-Security-Policy.
- **Branding:** administrators set the installation's name, accent color and PNG logo, used in the interface, on public pages and in emails.
- **Interface:** every view has its own address (refresh, bookmarks and Back work); redesigned for phone, tablet and desktop in dark and light; English and Swedish.
