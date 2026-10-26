# Changelog

All notable changes to ITRUPANEL. Release archives and checksums: https://itrupanel.com/panel/

## 0.2.25

- **Web servers section** (Linux and FreeBSD): versions of nginx, Apache, OpenLiteSpeed and every installed PHP version; the panel checks the connected package sources once a night and on the **Check for updates** button, reports a new version with a notice and shows an *available* mark (also in the PHP section); an update is installed with a button after the settings are saved to `/var/backups/itrupanel/components`, the configuration is checked, the service is restarted and tested, and everything is restored if anything fails
- **Choosing nginx:** system packages, nginx.org **stable** or nginx.org **mainline** (on FreeBSD: `nginx` or `nginx-devel`); the panel connects the repository, replaces the package (including the file conflicts of the RHEL packages), sets the worker user and disables the default nginx.org page
- **Choosing Apache** on Debian and Ubuntu: system packages or the Ondřej Surý repository; OpenLiteSpeed and PHP-FPM are updated from their current repositories
- tested with the real panel on Debian 12 and 13, Ubuntu 24.04, Rocky Linux 9, AlmaLinux 10 and FreeBSD 14.5: stable ↔ mainline ↔ system for nginx, Apache from the system and from Ondřej Surý, the update of nginx from an older release, updates of OpenLiteSpeed and PHP, with working websites after every step

## 0.2.24

- **FreeBSD.** Fixed: the panel update was interrupted and rolled back when PHP-FPM did not start because the FreeBSD packages were built for a newer system release (`ld-elf.so.1: … FBSD_1.9 not found`), including a failing rollback. The update and the rollback now leave the PHP service alone in that case
- Linux is unchanged (shared version number)

## 0.2.23

- **FreeBSD only.** Fixed: updating the panel was interrupted and rolled back ("Install PHP first" in the migration log) when PHP was not installed on the server while phpMyAdmin was still there, for example after PHP was removed before switching to another version. Such an update now goes through; phpMyAdmin is reconfigured when PHP is installed again. The same migration is also safe on Linux servers without PHP
- **FreeBSD only.** When the packages are built for a newer system release than the one on the server (for example 15.0 with packages for 15.1) `php-fpm` fails with `ld-elf.so.1: … FBSD_1.9 not found`; the panel and the installer now say so and point to `freebsd-update`. The installed PHP version is detected from the package and from `php-fpm -v`, and the PHP service is stopped before its packages are removed

## 0.2.22

- **FreeBSD: switching the PHP version.** The PHP section has a **Switch to this version** button: the panel stops PHP-FPM, removes the installed PHP packages, installs the same packages of the chosen version (8.2–8.5), moves all websites, the default version and webmail to it and keeps the `php.ini` settings and the extensions that exist for the new version; if anything fails the previous version is restored. Tested with 8.3 → 8.5 → 8.4 with websites, a PECL extension, Roundcube and phpMyAdmin
- Linux is unchanged

## 0.2.21

- **FreeBSD 14 and 15 (beta)** with its own archive (`itrupanel-freebsd-<version>.zip`), installer (`install-freebsd.sh`), update script and online command: `fetch -o install.sh https://itrupanel.com/panel/install-itrupanel-freebsd.sh && sh install.sh`; updating from the panel works through a separately signed `latest-freebsd.json`
- on FreeBSD: websites (nginx, Apache, PHP-FPM), MariaDB, FTP (pure-ftpd), SSL (certbot), mail (Postfix, Dovecot, OpenDKIM, Rspamd with its own Redis, Roundcube), DNS (NSD: the FreeBSD packages of BIND and Postfix cannot be installed together), antivirus (built-in, ClamAV), the firewall on pf, FTP protection (fail2ban with pf), backups, cron, the file manager and PHP extensions from FreeBSD packages
- not on FreeBSD: OpenLiteSpeed, Docker and database servers (they run in Docker), the ionCube loader; only one PHP version is installed at a time because the FreeBSD packages php82–php85 conflict
- tested with the real panel in FreeBSD 14.5 (install, reboot, update, every section above); the Linux code paths are unchanged
- fixed: restoring files from a backup used `chown --no-dereference`, which only GNU `chown` understands

## 0.2.20

- file editor: a **Save and close** button next to Save; code highlighting for copies and drafts such as `config.php.new` or `index.html.bak` (the language is taken from the real extension before the suffix `.new`, `.bak`, `.old`, `.orig`, `.dist`, `.sample`, `.disabled` and similar)

## 0.2.19

- file manager: a click or double click opens any file in the editor, except obvious binaries (images, archives, video, office documents, fonts, programs); before, files with an unfamiliar extension such as `config.php.new` were downloaded instead

## 0.2.18

- **Site type at creation**: the question "What kind of site?" (regular PHP, WordPress, Laravel / Symfony, Joomla, 1C-Bitrix, single-page application, static site, application behind a proxy) sets the mode, template, pretty links and the root folder (`public` for Laravel)
- **Page cache**: choose a period from 1 minute to a day to turn on the nginx cache (FastCGI cache for PHP-FPM, proxy cache for Apache, OpenLiteSpeed and applications); requests with data, admin areas, logged-in users and carts are not cached; a **Clear cache** button in the site row
- **PHP load profiles** (economical, normal, high load; the number of processes is calculated from the server memory) and PHP memory and run time per site
- **Site protection with checkboxes**: security headers, protection of images and video from hotlinking, a password for the whole site (bcrypt; the Let's Encrypt check is not affected) and the admin area (`/wp-admin`, `/wp-login.php`, `/administrator`, `/bitrix/admin`, `/admin`, `/phpmyadmin`) only from chosen IP addresses or networks
- **Template library**: a signed set of ready templates (Drupal, OpenCart, MediaWiki, phpBB, MODX, PHP frameworks) in `templates/library.json` is checked once a day (GitHub, fallback itrupanel.com) and applied without a new panel release; the rules are validated and forbidden directives are rejected
- fixed: a site with the root in a subfolder (`public` for Laravel) was not served because the site folder belonged to root; PHP-FPM refused to start after several sites were created within seconds (systemd start limit); OpenLiteSpeed did not start on Ubuntu 24.04 (the package did not install its systemd unit)
- tested with the real panel on Debian 12 and 13, Ubuntu 24.04, Rocky Linux 9 and AlmaLinux 10 (with the real nginx, PHP-FPM, Apache and OpenLiteSpeed)

## 0.2.17

- minor bug fixes (the site templates window closes when you open a form from it; a site template that does not suit the site mode is refused with an explanation)

## 0.2.16

- minor bug fixes

## 0.2.15

- **Mail: greylisting and DNSBL in the Rspamd antispam window.** Greylisting (off by default) temporarily rejects messages from unknown senders with a spam score from a set threshold and accepts the retry after a chosen delay (1–60 minutes); authenticated users and local mail are not affected. DNSBL: the seven lists Rspamd checks by default (Spamhaus ZEN, NiX Spam, Blocklist.de, Virusfree, SenderScore, Mailspike, DNSWL) can be turned off one by one or all at once, and you can add up to 10 of your own lists with a score. The settings are written to `local.d`, checked with `rspamadm configtest` and rolled back on an error; the generated configuration was verified with Rspamd 3.4 on Debian 12

## 0.2.14

- **Site templates**: ready sets of nginx rules for a type of site — WordPress, Laravel / Symfony, Joomla, 1C-Bitrix, single-page application and "Protect service files" — and your own templates. A template is chosen in the site form; the panel keeps the base (ports, SSL, protection, PHP, closed service files) and the template only adds rules. Rules can use the variables `%DOMAIN%`, `%ROOT%`, `%USER%`, `%PHP%`, `%SOCK%`. Changing a template is applied to all its sites at once, nginx checks the result and everything is restored if it finds an error. The built-in templates were checked with a real nginx on Debian 12 in PHP and static modes
- PHP: the Modules window no longer lists modules that cannot be installed separately (they are part of the main PHP package)

## 0.2.13

- **PHP modules**: the Modules window of every PHP version now lists about 80 extensions by section — databases, cache and queues, graphics, strings and XML, network, encryption, debugging, archives and others — including **ionCube Loader**. Each module can be installed, **turned off without removing**, turned on again or removed, and the window has a search. ionCube Loader is downloaded from the developer's site (downloads.ioncube.com) and loaded first; PHP 5.6–8.5 are supported on amd64 and arm64
- verified on Debian 12 with PHP 8.3: installing, turning off, turning on and removing ionCube and Redis

## 0.2.12

- **Backups**: while a selected folder, files or a database are being prepared for download, a progress bar with the percentage and the estimated time left is shown in the corner of the screen (before, nothing was visible for minutes); the preparation can be stopped
- installer: the pre-installation check list is aligned by characters even when the Russian locale is not installed on the server

## 0.2.11

- **PHP**: the version list has a new **Release** column showing the exact release installed on the server (for example 8.3.36)
- **Websites**: the version of the web server (nginx, Apache, OpenLiteSpeed) is shown next to its name in the sites list and in the server information

## 0.2.10

- the antivirus is now described as two antiviruses, **ClamAV** and **AI-Bolit**, next to the always-on built-in scan; descriptions in the panel update notes and the documentation corrected
- minor bug fixes

## 0.2.9

- **AI-Bolit as an optional second antivirus**, next to ClamAV (the built-in scan always runs). The panel does not ship it (the scanner and its database have no licence that allows redistribution): in **Antivirus** press **Connect AI-Bolit** and enter the https address of the `ai-bolit.php` file from the developer's page. PHP on the command line is required. The antiviruses run one after another — built-in, ClamAV, AI-Bolit — at the lowest priority and without dangerous PHP functions; every finding shows its engine

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
