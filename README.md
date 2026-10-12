# ITRUPANEL
ITRUPANEL

A Linux server and web hosting control panel with a web interface in English and Russian. Manage websites, databases, PHP, FTP, DNS, mail, backups, and server services.

**Current version: 0.1.42 · Beta.**

[Official website](https://itrupanel.com/) · [Terms of use](LICENSE.txt)

## Features

- **Websites:** Nginx, PHP-FPM, static websites, reverse proxying, domains and aliases, redirects, and SSL certificates.
- **PHP:** install and manage PHP versions. The installer selects PHP 8.3 by default. Version availability depends on the OS and repositories.
- **Databases:** manage MariaDB databases and users, with access to phpMyAdmin.
- **Files and FTP:** file manager and Pure-FTPd user management.
- **DNS and mail:** manage BIND DNS zones and mail domain settings.
- **Backups:** manual and scheduled backups, local or FTP storage, and restoration of files, databases, and selected settings.
- **Monitoring:** system load, CPU, memory, disk usage, network traffic, disk I/O, and service status.
- **Administration:** cron jobs, terminal, background tasks, logs, and panel updates.
- **Security:** website HTTP request limits, IP lists, and brute-force protection for panel and FTP logins.

## Requirements and compatibility

Installation requires a clean Linux server with root access, systemd, and an internet connection. Prebuilt binaries are provided for **amd64 (x86_64)** and **arm64 (aarch64)**. Go is not required on the server: the web interface is embedded in the binary.

The installer includes installation paths for the following systems:

| Family | Versions |
| --- | --- |
| Ubuntu | 22.04, 24.04, 26.04 |
| Debian | 11, 12, 13 |
| AlmaLinux / Rocky Linux / RHEL / CentOS Stream | RHEL-family releases 8–10, depending on the distribution |

An installation path does not mean every OS release has been fully tested on a real server. Results depend on package availability and external repositories. The system installer is not intended for macOS or Windows.

The installer configures shared Nginx, MariaDB, FTP, and DNS services. If your server already runs another control panel or hosts live websites, prepare a separate server and a migration plan first.

## Quick installation

Run the following in a root session on your Linux server:

```bash
wget -O install.sh https://itrupanel.com/panel/install-itrupanel.sh && bash install.sh
```

The bootstrap script downloads the full release, verifies its SHA-256 checksum, and starts installation. For an initialized panel, it selects the update procedure.

After successful installation, open:

```text
https://SERVER_IP:3333
```

The username is `admin`. The generated password is printed in the terminal when installation finishes. The panel initially uses a self-signed certificate, so your browser may display a warning.

### Installing from the full archive

Download the latest full archive from the official website, extract it, and enter the `itrupanel` directory. In a root session, run:

```bash
bash install.sh
```

You can select a PHP version or install the panel without PHP and phpMyAdmin:

```bash
PHP_VERSION=8.4 bash install.sh
# Or:
PHP_VERSION=none bash install.sh
```

Installer help:

```bash
bash install.sh --help
```

### If initial installation is interrupted

Resolve the cause of the error, then download the current installer again using the command in “Quick installation.”

Version 0.1.42 fixes resuming initial setup after a PHP failure when the binary has already been copied but `state.json` has not yet been created. Missing required files on an initialized panel are still treated as an error. Existing state files are not automatically deleted.

Ubuntu 26.04 uses the `packages.sury.org/php` repository. The installer disables obsolete PHP PPA entries for `resolute` and backs up modified APT source files.

## Updating

Use the update option in the panel interface or rerun the bootstrap script from the official website. To update from an extracted full archive, run the following in its `itrupanel` directory:

```bash
bash update.sh
```

The update script updates the installed panel without repeating the initial setup of all services. It requires root access and systemd. Previous release archives are not required.

Create up-to-date backups of your websites and databases before updating.

## Network ports

| Purpose | Default ports |
| --- | --- |
| Panel web interface | TCP 3333 |
| Websites and HTTP certificate validation | TCP 80, 443 |
| FTP | TCP 21 |
| Passive FTP connections | TCP 30000–30100 |
| Public DNS, if used | TCP and UDP 53 |

Mail services require additional ports depending on the services enabled. Configure your hosting provider’s firewall separately from the firewall inside the server.

## Website protection

The **Website protection** section manages Nginx limits for each website:

- Request rate per IP, burst allowance, and concurrent request limits.
- Off, monitoring, and blocking modes. Monitoring records limit violations without blocking visitors.
- Allow and deny lists supporting IPv4, IPv6, and CIDR; the allow list takes priority.
- An activity chart and event log with filters for website, IP, and request path.
- Configuration validation before applying changes, with restoration of previous settings if an error occurs.

In blocking mode, exceeding limits returns **HTTP 429**, while deny-listed addresses receive **HTTP 403**. Let’s Encrypt validation requests are exempt. Protection is disabled by default; start with monitoring and adjust limits to match your website’s normal traffic.

This module limits HTTP requests. It does not include SQL injection or XSS filtering. DDoS attacks that saturate the internet connection require filtering by your hosting provider or an external protection service.

## Login protection

The **Login protection** section shows protection status, blocked IP addresses, and manual unblocking controls.

**Panel:** 5 failed login attempts from one IP within 10 minutes block new logins for 15 minutes. Protection is enabled automatically. Blocks persist across restarts, and existing sessions remain active. Concurrent attempts count toward the limit.

**FTP:** click **Install and enable** and specify the FTP port, usually `21`. Fail2ban monitors Pure-FTPd authentication failures and blocks the IP on the selected port: 5 failures within 10 minutes trigger a 15-minute block. Linux with systemd is required; the FTP service may restart during setup.

## Backups

Files, databases, website settings, and panel settings can be backed up separately or together. Files and databases are selected by default.

Full and incremental backups are supported. The first backup is full, followed by up to six consecutive incremental backups. Incremental backups save file changes and track deletions, while databases and selected settings are always saved in full.

The backup chain is assembled automatically during restoration. Parts of the chain required by other backups are protected from deletion. Backup settings include the schedule, retention count, storage destination, and an FTP connection test.
