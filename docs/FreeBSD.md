# ITRUPANEL on FreeBSD (beta)

FreeBSD 14 and 15 on amd64 and arm64 are supported through a separate archive, installer and update channel. The Linux archive and command are unchanged.

| File | Purpose |
| --- | --- |
| `itrupanel-freebsd-<version>.zip` | programs for amd64 and arm64, `install-freebsd.sh`, `update-freebsd.sh`, sources |
| `install-itrupanel-freebsd.sh` | online installer: downloads the archive, checks SHA-256, installs or updates |
| `latest-freebsd.json` | signed release description used by the in-panel updater |

## Install

As root on a clean server with at least 4 GB free:

```sh
fetch -o install.sh https://itrupanel.com/panel/install-itrupanel-freebsd.sh && sh install.sh
```

`PHP_VERSION=8.4` chooses another PHP (8.2–8.5), `PHP_VERSION=none` skips PHP, `PANEL_LOGIN` and `PANEL_PASSWORD` set the administrator. The installer does not change the firewall: open TCP 80, 443, 3333, 21 and 30000–30100 (and 25, 587, 993 for mail, 53 for DNS).

Update: from the panel, with the same command, or `sh update-freebsd.sh` from a new archive. The program is backed up first and restored on failure.

Keep the system up to date: FreeBSD packages are built for the current release, and on an older one (for example 15.0 while the packages are built for 15.1) programs fail to start with `ld-elf.so.1: … version FBSD_… not found`. Run `freebsd-update fetch install` before installing and, if the error remains, `freebsd-update upgrade -r <next release>`. The installer and the PHP section detect this and say so.

## What works

Websites (nginx + PHP-FPM, nginx + Apache, static, proxy), site templates, page cache, PHP profiles, site protection; MariaDB and phpMyAdmin; SSL (self-signed, Let's Encrypt through certbot); FTP (pure-ftpd); mail (Postfix, Dovecot, OpenDKIM, Rspamd with its own Redis, Roundcube); DNS (NSD); backups and restore; cron; file manager; PHP settings and extensions from FreeBSD packages; antivirus (built-in, ClamAV); firewall on pf; login protection and FTP protection (fail2ban + pf); monitoring, logs, jobs, shell client; updating from the panel.

## Web servers section

The **Web servers** section shows the versions of nginx, Apache and the installed PHP, checks for updates once a night and on a button, and installs them with one click. On FreeBSD nginx can be switched between **stable** (the `nginx` package) and **mainline** (`nginx-devel`); Apache and PHP are updated from the FreeBSD packages (OpenLiteSpeed does not exist for FreeBSD).

## What is different or missing

| Feature | On FreeBSD |
| --- | --- |
| OpenLiteSpeed | not available (no FreeBSD build) |
| Docker and Database servers | not available (Docker does not exist for FreeBSD; database servers run in Docker) |
| ionCube Loader | not available (no FreeBSD loader) |
| PHP versions | one version at a time, 8.2–8.5 (the FreeBSD packages `php82`…`php85` conflict); the PHP section has a **Switch to this version** button that replaces the installed PHP and moves all websites to the new one |
| DNS server | NSD instead of BIND: the FreeBSD packages of BIND and Postfix need different versions of lmdb and cannot be installed together; zones and records are the same, records of one set get one TTL |
| Firewall | pf instead of nftables |
| Services | rc.d instead of systemd; no per-service memory in monitoring |
| Log rotation | newsyslog instead of logrotate |
| PHP extensions | taken from FreeBSD packages; some of the Linux list do not exist (for example xhprof, gmagick, zmq) |

## Tested

Installed from the final archive on a clean FreeBSD 14.5 (arm64, virtual machine): sites on nginx and Apache, databases, FTP, SSL, cron, backups and restore, PHP settings and extensions, DNS, both antivirus engines, the pf firewall (real blocking), FTP protection, mail delivery with DKIM, Rspamd, Roundcube sign-in, reboot, and an update from the panel with a signed test release.

Not tested: a real Let's Encrypt certificate, FreeBSD 15, the amd64 build at run time, mail to and from the internet, cloud backup targets, import from other panels, AI-Bolit, running on real hardware.
