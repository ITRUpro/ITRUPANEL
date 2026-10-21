# Changelog

All notable changes to ITRUPANEL. Release archives and checksums: https://itrupanel.com/panel/

## 0.3.0

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
