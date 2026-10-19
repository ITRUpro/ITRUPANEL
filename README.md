# ITRUPANEL — free server control panel & free hosting control panel

**ITRUPANEL** is a free web server control panel and hosting control panel for Linux.
Manage websites, PHP versions, databases, mail, DNS, SSL certificates, FTP, backups,
Docker, antivirus and the firewall from one fast web interface in **English and Russian**.
A single Go binary with the interface embedded — no Go toolchain needed on the server.

[Website](https://itrupanel.com/) · [Documentation](https://itrupanel.com/en/docs.html) ·
[Demo](https://itrupanel.com/en/#demo) · [Community forum](https://itrupanel.com/forum/) ·
[Terms of use](LICENSE.txt)

## Install

Run as root on a clean Linux server:

    wget -O install.sh https://itrupanel.com/panel/install-itrupanel.sh && bash install.sh

Open `https://SERVER_IP:3333` and sign in with the **random login and password** printed by the
installer. To choose your own: `PANEL_LOGIN=name PANEL_PASSWORD=password bash install.sh`.
Both can be changed later in the panel (key icon) or with `itrupanel passwd <password> [login]`.
The same command updates an installed panel. Optional: `PHP_VERSION=8.4 bash install.sh`
or `PHP_VERSION=none bash install.sh`.

**Supported systems:** Ubuntu 22.04 / 24.04 / 26.04, Debian 11–13, AlmaLinux and Rocky Linux 8–10,
CentOS Stream 9–10, RHEL 8–10 · **amd64 and arm64** · root access and systemd required.

## What you get

| Area | Features |
| --- | --- |
| **Websites** | Nginx + PHP-FPM, static sites, reverse proxy, per-site system user and PHP version, aliases, Cyrillic domains, redirects, gzip, custom Nginx directives, default site |
| **PHP** | Several PHP versions side by side, modules, php.ini editor |
| **Databases** | MariaDB databases and users, phpMyAdmin, dump import/export, per-IP access, extra MySQL/MariaDB servers in Docker |
| **Mail** | Postfix, Dovecot, OpenDKIM, Rspamd antispam, Roundcube webmail, quotas, aliases, forwarding, DNS checks |
| **DNS & SSL** | BIND zones and records, Let’s Encrypt (automatic issue and renewal), self-signed and custom certificates |
| **Files & FTP** | Browser file manager with recycle bin and right-click menu, Pure-FTPd users |
| **Backups** | Files, databases and settings; full + incremental chains; local or FTP; browse a backup and restore only what you need |
| **Import** | Move websites, databases, mail (with messages and passwords), DNS zones, FTP users, cron jobs and backup settings from another ITRUPANEL server using only its address and administrator login; missing PHP is installed automatically; works across Debian, Ubuntu, AlmaLinux and Rocky Linux |
| **Docker** | Containers, Compose projects, images, networks, volumes, full cleanup |
| **Security** | Per-site request limits (HTTP 429/403), login protection with Fail2ban for FTP, incoming-connection firewall, antivirus with optional ClamAV, quarantine and cleanup from backups |
| **Monitoring** | Dashboard, resource history, traffic per site, disk usage report, site logs, operations log, background tasks, notifications |
| **Admin** | Browser root terminal, cron scheduler, server reboot, one-click panel updates with signed releases |

Every section is described in the [documentation](https://itrupanel.com/en/docs.html).

## Screenshots

Interface screenshots (light and dark themes) are on the [website](https://itrupanel.com/en/#interface).

## Security and updates

Releases are full archives with a SHA-256 checksum and an Ed25519 signature checked by the
installer and by the panel before updating. Panel passwords are stored as bcrypt hashes;
login attempts are rate-limited; the panel can be placed on a domain behind HTTPS without a port.

## Ports

Panel TCP 3333 · websites 80/443 · FTP 21 and passive 30000–30100 · DNS 53 (if used) ·
mail 25/587/993 (if mail is enabled).

## License and copyright

Copyright (c) 2026 Oleg Sergeevich Dmitriev. ITRUPANEL. All rights reserved.

Free for personal and commercial use. The source is available to study and adapt for your own
needs; selling the panel or redistributing modified versions requires separate permission.
See [LICENSE.txt](LICENSE.txt), [NOTICE.txt](NOTICE.txt) and the licenses of bundled
third-party components in [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt).

## Contributing and feedback

Use GitHub Issues for bugs and ideas, or the [community forum](https://itrupanel.com/forum/).
Please include your OS, panel version and the exact error text.


# ITRUPANEL — бесплатная панель для сервера и бесплатная панель для хостинга

**ITRUPANEL** — бесплатная панель управления сервером и хостингом для Linux.
Сайты, версии PHP, базы данных, почта, DNS, SSL, FTP, резервные копии, Docker,
антивирус и брандмауэр — в одном быстром веб-интерфейсе на **русском и английском**.
Один бинарный файл на Go с встроенным интерфейсом: Go на сервере не нужен.

[Сайт](https://itrupanel.com/) · [Документация](https://itrupanel.com/docs.html) ·
[Демо](https://itrupanel.com/#demo) · [Форум](https://itrupanel.com/forum/) ·
[Условия использования](LICENSE.txt)

## Установка

От root на чистом Linux-сервере:

    wget -O install.sh https://itrupanel.com/panel/install-itrupanel.sh && bash install.sh

Откройте `https://IP_СЕРВЕРА:3333` и войдите с **случайными логином и паролем**, которые выводятся
в конце установки. Свои можно задать так: `PANEL_LOGIN=имя PANEL_PASSWORD=пароль bash install.sh`.
Позже и логин, и пароль меняются в панели (значок ключа) или командой `itrupanel passwd пароль [логин]`.
Та же команда обновляет установленную панель.

**Поддерживаются:** Ubuntu 22.04 / 24.04 / 26.04, Debian 11–13, AlmaLinux и Rocky Linux 8–10,
CentOS Stream 9–10, RHEL 8–10 · **amd64 и arm64** · нужны root и systemd.

## Возможности

- **Сайты:** Nginx + PHP-FPM, статика, прокси, свой пользователь и версия PHP у каждого сайта, алиасы, кириллические домены, редиректы, gzip.
- **Базы данных:** MariaDB, phpMyAdmin, импорт и экспорт дампов, доступ по IP, дополнительные MySQL/MariaDB в Docker.
- **Почта:** Postfix, Dovecot, OpenDKIM, антиспам Rspamd, веб-почта Roundcube, квоты, алиасы, пересылка, проверка DNS.
- **DNS и SSL:** зоны BIND, Let’s Encrypt с автоматическим выпуском и продлением.
- **Файлы и FTP:** файловый менеджер с корзиной и меню по правой кнопке, пользователи Pure-FTPd.
- **Резервные копии:** файлы, базы и настройки; полные и инкрементные; на сервер или FTP; частичное восстановление.
- **Импорт данных:** перенос сайтов, баз, почты (с письмами и паролями), DNS-зон, FTP-пользователей, cron и настроек резервного копирования с другого сервера ITRUPANEL по адресу панели и логину администратора; недостающий PHP ставится сам; работает между Debian, Ubuntu, AlmaLinux и Rocky Linux.
- **Docker, безопасность, мониторинг:** контейнеры и Compose, защита сайтов и входа, брандмауэр, антивирус с лечением из резервных копий, ресурсы, трафик, журналы, задачи, уведомления.
- **Администрирование:** терминал root в браузере, планировщик CRON, перезагрузка сервера, обновления панели в один клик с подписанными выпусками.

Подробно о каждом разделе — в [документации](https://itrupanel.com/docs.html).

## Лицензия и авторские права

Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. All rights reserved.

Бесплатно для личных и коммерческих задач. Исходники доступны для изучения и изменения
под свои нужды; продажа панели и распространение изменённых версий требуют отдельного разрешения.
См. [LICENSE.txt](LICENSE.txt), [NOTICE.txt](NOTICE.txt) и лицензии сторонних компонентов в
[THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt).
