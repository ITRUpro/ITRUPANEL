# Security policy

## Supported versions

Security fixes are released for the **latest version** of ITRUPANEL. Please update (the **Updates** button in the panel, or run
the installer again) before reporting a problem, and check that it still reproduces.

## Reporting a vulnerability

**Please do not report security problems in public issues, the forum or pull requests.**

Use GitHub private reporting: open the **Security** tab of this repository and choose **Report a vulnerability**.
Describe what you found, which version and operating system you tested, and how to reproduce it. If you can, include a
minimal proof of concept and the impact you expect.

What to expect:

- we acknowledge the report as soon as we can and keep you informed about the progress;
- the fix is released first, and details are published after most installations have had a chance to update;
- if you wish, we credit you in the release notes.

## Scope

In scope: the panel itself (authentication, sessions, the API, file and backup handling, import, update and installation
scripts, generated Nginx/PHP-FPM/mail configuration).

Out of scope: vulnerabilities in third-party software the panel installs (Linux, Nginx, PHP, MariaDB, Postfix, Dovecot and others) —
please report those to their own projects — and problems that require an already compromised administrator account or
root access to the server.

## Good practice for administrators

- keep the operating system and PHP updated; the panel does not install system updates by itself;
- put the panel behind HTTPS on a domain, restrict port 3333 with the built-in firewall if you can;
- use a strong administrator password and enable login protection.
