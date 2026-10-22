# Changelog

All notable changes to ITRUPANEL. Release archives and checksums: https://itrupanel.com/panel/

## 0.2.9

- **AI-Bolit as an optional third antivirus engine**, next to the built-in scan and ClamAV. The panel does not ship it (the scanner and its database have no licence that allows redistribution): in **Antivirus** press **Connect AI-Bolit** and enter the https address of the `ai-bolit.php` file from the developer's page. PHP on the command line is required. The engines run one after another — built-in, ClamAV, AI-Bolit — at the lowest priority and without dangerous PHP functions; every finding shows its engine

## 0.2.8

- **antivirus rules that update themselves**: a separate pack of detection rules (obfuscated function names, `eval` of remote or request data, uploads to a request-controlled path, PHP hidden in fake GIF files, known web shells, JS `eval(atob(...))`, search-engine redirects in `.htaccess`) is signed with the ITRUPANEL release key and published in the repository (`antivirus/rules.json`). Once a day the panel downloads it from GitHub (itrupanel.com is the fallback), checks the signature and applies it without restarting; there is also an **Update rules** button and a switch for automatic updates. The panel accepts only signed and newer packs and rejects any rule that would fire on ordinary code
- scans, cleanup, ClamAV database updates and rule updates now run **one after another**, never at the same time: the next job waits until the previous one has finished
- a file upload whose connection has stalled (the browser went away, the network dropped) is aborted after two minutes without data; before, it could hold the panel for hours and the **Update** button answered "Wait for the current operations to finish"
- clearer message when an update cannot start because a file upload or the Shell client is still open

## 0.2.7

- the installer checks the server before installing and shows the result: operating system, architecture, root, systemd, memory, free disk space, access to itrupanel.com and the ports 80, 443 and 3333 (in Russian or English, by the server locale); warnings do not stop the installation
- the website shows the current version and the download link taken from `panel/latest.json`, so the page does not have to be rewritten for every release

## 0.2.6

- the terminal of a Docker container is light like the rest of the interface (the Shell client of the server stays dark)

## 0.2.5

- the **Stored** column of the backup table says **Cloud** for every cloud kind (Yandex Disk, Mail.ru Cloud, Dropbox, Google Drive, S3) instead of "FTP"; backups attached from a cloud are marked "Imported from the cloud"
- downloading a database or selected files from a backup starts at once, without a task window
- task output and other service texts are plain text on a light background instead of a dark box

## 0.2.4

**Import from FASTPANEL and a step-by-step WebDAV connection check.**

- **Import data** can now read a **FASTPANEL** server over SSH (root, or a user with passwordless sudo; password or private key): websites with files, databases with their old passwords, FTP users with their old passwords, mail (mailboxes with their old passwords and messages) and cron jobs; the lists come from FASTPANEL itself (`fastpanel --json`), with a fallback to inspecting the nginx and MySQL settings
- **Other server over SSH** (ISPmanager, HestiaCP, aaPanel and others): websites, databases and cron jobs
- verified end to end against a real FASTPANEL installation (sites, PHP, database with data, cron, FTP login and IMAP login with the old passwords, a moved message)
- not moved from FASTPANEL: DNS (it lives at external providers), mail aliases and forwards, SSL certificates, Node.js applications
- WebDAV (Yandex Disk, Mail.ru Cloud and others): **Check connection** now runs every step of a backup (sign-in, free space, folder, writing 64 B and 2 MB, reading, deleting), shows the server's answer for each step on failure and reports a lack of free space directly; folders are created only when missing

## 0.2.3

**Fix: backups to Yandex Disk and other WebDAV storage.**

- uploads use HTTP/1.1 only, send no extra content type, and use `Expect: 100-continue` for files over 1 MB, so the server can refuse before any data is sent
- on temporary server errors (500, 503) the backup is sent again, up to four times
- the error message now shows the explanation given by the server

## 0.2.2

**More cloud storage for backups, two-step authentication and optional automatic updates.**

- backups can be kept in **Yandex Disk**, **Mail.ru Cloud** and any **WebDAV** server (Nextcloud, ownCloud) with a login and an app password, and in **Dropbox** and **Google Drive** through an access grant in your own account; for each one choose "In the cloud" or "Here and in the cloud"
- backups in these clouds are found, browsed, restored and deleted like S3 ones; the settings are moved by Import data and included in the panel settings backup; permanent tokens stay on the server and are never shown in the interface
- **two-step authentication** (TOTP, RFC 6238): QR code or manual key, codes from Google Authenticator, Yandex Key, Microsoft Authenticator, Aegis and others, ten one-time recovery codes; a used code cannot be replayed and code guessing is rate-limited
- **access to the panel only from selected IP addresses**; the panel refuses a list that does not contain your current address
- lost access can be reset on the server with `itrupanel 2fa-off` and `itrupanel allow-all-ips`
- **automatic panel updates** in Server information: fixes only or all releases, at the chosen hour; off by default, and manual updating with the Updates button stays available

Dropbox and Google Drive were tested against a model of their APIs, not against live accounts; please report any problem.

## 0.2.1

**ITRUPANEL is now open source.** The source code is published on GitHub under the GNU GPL v3 (`GPL-3.0-only`).

- `LICENSE.txt` is now the GPL text; the short in-panel terms of use are in `TERMS.txt` (edition 2.0)
- consent already given in the panel to the earlier free-use terms stays valid; nothing needs to be accepted again
- every source file carries the GPL notice and an SPDX identifier
- the names ITRUPANEL and itrupanel.com and the logo are not licensed under the GPL

## 0.2.0

**The backup list is now organized by item.** Instead of one long list with every backup, each tab
(**Website files**, **Databases**, **Panel settings**) shows one row per site, database or the panel.

- each row shows the number of backups, the date of the latest one, the total size and where they are stored
  (server, FTP, cloud); a "creating" mark appears while a backup of that item is running
- click a row to open all backups of that site, database or panel, newest first, with the same buttons as before:
  contents, download, restore, delete
- the **← All sites** button (**← All databases** in the Databases tab) returns to the overall list
- the counters on the tabs now count sites and databases, not individual backups
- English and Russian interface and documentation updated

Nothing changes in how backups are created, stored or restored; existing backups appear in the new view automatically.

## 0.1.99

**Backups can now be stored in the cloud.** The panel works with any S3-compatible storage — Amazon S3,
Yandex Object Storage, Cloudflare R2, Wasabi, DigitalOcean Spaces, MinIO and others.

- New storage options in the backup settings: **In the cloud (S3)** (the backup is kept only in the cloud) and
  **Here and in the cloud**; ready-made service templates, a connection test with clear error messages
  (wrong key, wrong region, missing bucket, clock skew)
- large archives are uploaded in parts; backups are restored from the cloud and deleted together with their
  cloud object
- **Find backups in the storage** also scans the cloud folder and attaches the ITRUPANEL archives it finds
- the cloud settings are moved by **Import data** from another ITRUPANEL (the schedule stays off on the new server)
- the secret key is stored on the server and is hidden in the interface; requests are signed with AWS Signature V4
  (no external libraries; checked against the examples in the AWS documentation)

Tested against a real S3 server, including an 80 MB multipart upload, restore with SHA-256 verification,
deletion and discovery. You create the bucket yourself in the cloud account; the key needs permission to read,
write and delete objects.

## 0.1.98

- **The code editor now fills the whole window height.** In the file manager the editor extends down to the Save and
  Cancel buttons (previously it stopped short and left empty space). Long files scroll inside the editor, and the
  status line with the line and column stays visible under it.

## 0.1.97

**A real code editor in the file manager.** Opening a file in the panel now shows a code editor (CodeMirror 6)
instead of a plain text field:

- syntax highlighting for PHP, HTML, CSS, JavaScript/TypeScript, JSON, YAML, SQL, Markdown, shell, Nginx and
  ini files (including `.htaccess`, `.env`, `php.ini`), chosen from the file name; the language can be changed by hand
- line numbers, code folding, bracket matching and automatic indentation
- find and replace (Ctrl/Cmd+F), with regular expressions, match case and whole word
- line wrapping, light and dark themes that follow the panel theme
- save with Ctrl/Cmd+S from anywhere in the editor window, and an “unsaved” mark
- works offline (the editor is bundled with the panel); if it fails to load, the previous text field opens

Other changes: the licenses of CodeMirror and its libraries are listed in `THIRD-PARTY-NOTICES.txt`;
the documentation page for the file manager describes the editor.

## 0.1.96

- **Import data now moves the backup settings.** If the old panel sends backups to FTP, you can move the FTP
  address, login, password, folder and backup parameters in the same import. The schedule on the new server
  stays **off** until you turn it on yourself, so two panels do not back up the same websites at the same time.
  Backups that already sit on the FTP are found and attached automatically.
- **Fixed a sign-in lockout after a power failure.** The login protection log could stay empty after a power loss
  or a forced stop, and the panel then refused every sign-in with “Login protection is temporarily unavailable”.
  The panel state and the protection log are now written with a flush to disk, and an empty or damaged log no
  longer blocks sign-in (a damaged one is kept as `login-guard.json.corrupt`).

Requirements for importing from another panel: the old ITRUPANEL must be version 0.1.94 or newer.

## 0.1.95

- Copyright: the rights holder is named in every source file; the archive now includes `NOTICE.txt` and
  `THIRD-PARTY-NOTICES.txt` with the licenses of third-party components. The terms of use are unchanged.
- Documentation: a new License and copyright section; the table of contents highlights the current section.

Included since 0.1.94: import of websites, databases, mail, DNS zones, FTP users and cron jobs from another
ITRUPANEL by its address and administrator login (no SSH); random administrator login at install
(`PANEL_LOGIN`, `PANEL_PASSWORD` to choose your own); documentation page https://itrupanel.com/en/docs.html
