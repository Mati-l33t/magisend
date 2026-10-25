# Changelog

All notable changes to MagiSend are listed here. Versions follow [semantic versioning](https://semver.org).

## Unreleased

- **Virus scanning:** Administration → Virus scanning shows whether ClamAV runs and, on an installation made with the installer, installs it with one button (through the updater, which runs as root; it takes a few minutes and needs about 1.5 GB of memory). With Docker it explains the compose change instead. The ClamAV address is now under an advanced section, as it is only needed when ClamAV runs elsewhere.
- **Proxmox installer:** Advanced settings can add ClamAV, which gives the container at least 2560 MB of memory.
- **Installer:** `MAGISEND_WITH_CLAMAV=1` now also works on an existing installation. If ClamAV cannot be installed, MagiSend is still set up and says so, and the admin page can install ClamAV later.
- **Virus scanning, more thorough:** text written in the editor or in notes is scanned, an infected earlier version can no longer be restored as text, a file can no longer become another file's new version while its scan is waiting or found a virus, and files unpacked from an archive are scanned whenever scanning is on. A file from an upload request that was never completed is not served from the library. Only the file that changed is queued, not every file next to it. A file ClamAV could not scan whole is now marked unscanned instead of clean: the ClamAV install raises Debian's 25 MB file limit to 100 MB and reports files over its limits. ClamAV is installed only with at least 2.5 GB of memory, and on machines with less than 4 GB it loads a new virus database without keeping two in memory. A ZIP of files held back by their scan says so, and the public page no longer reloads (and drops what is being edited) while a file waits for its scan; pages that follow scans ask less and less often. An install request that never started is given up after 20 minutes, and its steps are shown in the page's language.
- **Unpacking:** in a tar file with the same name twice, the last copy is taken, as tar does; overlong names are skipped, which bounds the memory a tar file can use. Unpacking goes on when a proxy gives up waiting.
- **Backups:** a machine keeps the backups made under its earlier installation ID, and leftovers of an interrupted backup are removed only after an hour, so a copy of the database on a second machine does not remove a backup it is running. RESTORE.txt explains not to let two such machines share a backup folder.
- **Previews:** thumbnails, profile pictures and icons share the limit of two picture decodings at once, pictures are decoded once instead of twice for thumbnails, and files that are not pictures no longer wait for a turn. Thumbnails the browser makes are no longer given up on because a tab was closed or reloaded while one was being made, and a file given up on is tried again after a day.

## 1.0.9

- **Virus scanning (optional):** with ClamAV installed, new files, or only files received through upload requests and shared links, are scanned right after they are stored. A file waiting for its scan cannot be downloaded, previewed or unpacked; one with a virus stays blocked for everyone, earlier versions included, until its owner deletes it. Owners can be told through push notifications, and Administration → Virus scanning shows the scanner's state and the files found. Uploads are not slowed down, files too large to scan are not held back, and if ClamAV stops answering new files wait until it is back or scanning is switched off. The installer sets it up with `MAGISEND_WITH_CLAMAV=1`, and `docker-compose.yml` has a ready `clamav` service.
- **Unpacking archives:** besides ZIP, the library now unpacks `.tar`, `.tar.gz` / `.tgz` and `.tar.bz2` files with **Extract here**. Only plain files and folders are taken from them; links, devices and names that would leave the folder are left out, and a tar file is read no further than what the owner's quota, the storage budget and the free disk could hold.

## 1.0.8

Fixes from a third review, of the 1.0.7 changes.

- **Uploads with API tokens** without a stated length no longer stop early on a nearly full disk: bytes already written are no longer counted twice against the free space.
- **Restores:** a database restored on a new machine brings the old installation's ID, so its backups are listed and pruned there again.
- **Previews:** at most two pictures are decoded at once to check them.
- **Thumbnails:** HEIC thumbnails decode photos up to 50 megapixels and scale them without a full-size picture in between, and a file that never finishes is given up on after two tries.

## 1.0.7

Fixes from a second review, of the 1.0.6 fixes themselves.

- **Updates:** a second stop signal while the updater puts the previous version back can no longer remove the restored database; a database copy that fails partway stops the update before anything changes; a file store moved elsewhere behind a link no longer blocks updates. Docker images now hold the release's own binaries, used only after their signature and checksums are verified, and `latest` moves only for the newest release.
- **Restores:** a database-only restore starts even when earlier text versions are gone; the installation's ID is kept outside the database, so a restored database keeps its backups; archives from before installation IDs are pruned like the rest.
- **Storage:** missing files are checked without holding up the app, and a share that stops answering is skipped; nothing is removed from a location that looks like the wrong disk, only library files whose data is really gone are removed, and files in links are shown apart.
- **Backups:** a database copy whose permissions the folder cannot keep (CIFS, exFAT) no longer fails the backup; progress counts in every step, so long file listings are not given up; a given-up run stops instead of running alongside the next; deletions held for a backup never hang the app on a share; MagiSend waits for a running backup when it stops.
- **Sign-ins:** many wrong passwords for a link or request from many addresses now slow every check down instead of only changing the answer; a new password, a reset or a disabled account ends the pass known browsers had past the account-wide sign-in limit; password-reset requests do the same work whether or not an account matched.
- **Uploads with API tokens** reserve space as data arrives instead of the whole remaining quota.
- **Photo location:** TIFF and DNG files are cleaned where their directories point, without reading the whole file, every directory in the chain is checked, BigTIFF is refused, and PNG raw Exif profiles are replaced even when not compressed; replacing a file cleans it only once the replacement is allowed, and a photo that cannot be cleaned is refused with the right message.
- **Email settings:** clearing the web address uses this server's own address again rather than the old one.
- **Viewer:** WebP, AVIF, HEIC/HEIF, SVG, BMP, TIFF and ICO pictures open in the viewer (HEIC is decoded in the browser where it cannot draw it, TIFF is shown as PNG); large phone photos and pictures open up to 64 MB and 200 megapixels; text files up to 20 MB open, shown plain above 2 MB; a file without a preview opens to a short note and its download button instead of an error.
- **Third-party notices** are listed in THIRD-PARTY-NOTICES.md, included in the release download and the Docker image.

## 1.0.6

Fixes from a full review of the code.

- **Restores:** MagiSend starts after an older database was restored even when files were deleted since; they are listed under Administration → Storage to remove. Restoring a text version never replaces the newest one. A file moved away and back during a backup keeps its data.
- **Accounts:** a sign-in racing a password change no longer keeps a session; an administrator's own password and two-step login change only in their account settings; replacing the authenticator or renewing recovery codes needs the current code; changing the account email needs the password and tells the old address; failed sign-ins by others no longer lock out a browser that signed in before; password-reset answers take the same time whether or not an account matched; deleting an account removes its API tokens; recipients cannot write into a disabled account's links; shared pages ask for the two-step code.
- **Limits:** request bodies are read before the server's lock is taken; write requests and uploads have per-visitor slots; rate-limit keys stay short; the right password of a protected link always works; API-token uploads reserve their space before writing and must keep moving; push notifications reuse connections and test sends are limited; at most two thumbnails are decoded in a request when the queue is full.
- **Files:** a ZIP being unpacked cannot be written to or discarded meanwhile, and its files are checked before they are shown; storage moves copy a file again if it changed; replaced and restored versions on links that remove photo location are cleaned too; large TIFF files and compressed PNG metadata are cleaned; the version limit counts versions; long multibyte note titles no longer break saving.
- **Backups:** archives carry the installation's ID, incomplete ones are marked and never push out the last complete one, unreadable files no longer abort a backup, a backup stuck on a share is given up, location markers are included, leftovers are cleaned, and the database copy is private.
- **Updates and installs:** the updater keeps its working folder at /var/lib/magisend-update, saves only plain database files, never copies the whole file store, and rolls back when stopped by a signal; Docker images are built only from signed, published releases; the Proxmox installer passes the root password on standard input and says when the console signs in without one.
- **Links in emails** use the address the administrator set, and share emails need one; extending a library link resets its expiry reminder; Manage share keeps the expiry unless a new one is chosen and never saves recipients it could not load; files shared from another app wait for a confirmation; a crafted spreadsheet or Office file can no longer freeze or exhaust the browser tab.

## 1.0.5

Follow-ups from a security review.

- **A new password removes API tokens:** changing your password, an administrator's reset and a reset by email now remove the account's API tokens as well as its sign-ins, so whoever had the old password keeps no way in.
- **One-time links can be resumed:** a link whose last download was used stays open for an hour so that download can be resumed by the same visitor; nobody can start a new one. Then it closes and its files are removed as before.
- **Limited links do not show text in full:** like previews and the media player, the text view is not offered on links with a download limit, where it would be an uncounted download.
- **Download slots per household:** IPv6 visitors are grouped by /56, what a home connection usually gets, for the per-visitor limit.

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
