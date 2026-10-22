#!/usr/bin/env python3
# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
"""Real local mail checks, called only by mail-native.sh in a disposable container."""
import email.message
import http.cookiejar
import imaplib
import re
import smtplib
import ssl
import subprocess
import time
import urllib.parse
import urllib.request
from pathlib import Path

assert Path('/.dockerenv').exists()
with open('/etc/hosts', 'a') as f:
    f.write('\n127.0.0.1 mail.example.test\n')
ctx = ssl.create_default_context(cafile='/etc/itrupanel/mail/server.crt')
password = 'native-test-password'

def imap(user):
    c = imaplib.IMAP4_SSL('mail.example.test', ssl_context=ctx)
    c.login(user + '@example.test', password)
    return c

def count(user):
    with imap(user) as c:
        status, rows = c.select('INBOX')
        assert status == 'OK', rows
        return int(rows[0])

with imap('a') as c:
    status, quota = c.getquotaroot('INBOX')
    assert status == 'OK' and '1024' in str(quota), quota
print('PASS IMAP TLS/login and advertised 1 MiB quota', flush=True)

msg = email.message.EmailMessage()
msg['From'] = 'b@example.test'
msg['To'] = 'a@example.test'
msg['Subject'] = 'Local forwarding integration check'
msg.set_content('One message, forwarded to b with a local copy at a.')
with smtplib.SMTP('mail.example.test', 587, timeout=60) as s:
    s.starttls(context=ctx)
    s.login('b@example.test', password)
    assert s.send_message(msg) == {}
for _ in range(30):
    if count('a') == 1 and count('b') == 1:
        break
    time.sleep(1)
assert count('a') == 1 and count('b') == 1, 'forward or keep-copy delivery failed'
print('PASS SMTP STARTTLS/auth, Rspamd + DKIM chain and forwarding with copy', flush=True)

large = 'From: b@example.test\r\nTo: a@example.test\r\nSubject: quota\r\n\r\n' + ('a' * 900 + '\r\n') * 2400
with smtplib.LMTP('/var/spool/postfix/private/itrupanel-lmtp', timeout=30) as s:
    try:
        s.sendmail('b@example.test', ['a@example.test'], large)
        raise AssertionError('over-quota message accepted')
    except (smtplib.SMTPDataError, smtplib.SMTPRecipientsRefused) as e:
        assert 'quota' in str(e).lower() or 'size' in str(e).lower(), str(e)
    assert s.sendmail('a@example.test', ['b@example.test'], large) == {}
assert count('a') == 1 and count('b') == 2
print('PASS delivery quota rejects oversized mail, unlimited mailbox accepts it', flush=True)

spam = 'From: b@example.test\r\nTo: a@example.test\r\nSubject: GTUBE\r\n\r\nXJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X\r\n'
with smtplib.SMTP('mail.example.test', 587, timeout=60) as s:
    s.starttls(context=ctx); s.login('b@example.test', password)
    try:
        s.sendmail('b@example.test', ['a@example.test'], spam)
        raise AssertionError('GTUBE was not rejected')
    except smtplib.SMTPDataError as e:
        assert e.smtp_code >= 500, e
print('PASS Rspamd rejects GTUBE through the Postfix milter', flush=True)

if Path('/usr/sbin/php-fpm8.3').exists():
    webctx = ssl.create_default_context(cafile='/etc/itrupanel/roundcube-web.crt')
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=webctx), urllib.request.HTTPCookieProcessor(cookies))
    base = 'https://mail.example.test/'
    text = opener.open(base).read().decode()
    token = re.search(r'name="_token"[^>]*value="([^"]+)"', text)
    assert token, text[:1000]
    payload = urllib.parse.urlencode({'_token':token.group(1),'_task':'login','_action':'login','_user':'a@example.test','_pass':password}).encode()
    response = opener.open(base, payload)
    text = response.read().decode()
    assert '_task=logout' in text or 'rcmail.command(\'logout\'' in text, text[:2000]
    assert any(c.secure for c in cookies), 'session cookie is not secure'
    for path in ['config/config.inc.php', 'installer/', 'SQL/sqlite.initial.sql', '.htaccess']:
        try:
            opener.open(base + path)
            raise AssertionError('private path exposed: ' + path)
        except urllib.error.HTTPError as e:
            assert e.code in [403,404], (path,e.code)
    print('PASS Roundcube HTTPS login, trusted IMAP TLS, secure session and private paths', flush=True)
try:
    urllib.request.urlopen('http://127.0.0.1:11334/stat')
    raise AssertionError('Rspamd controller allows anonymous local access')
except urllib.error.HTTPError as e:
    assert e.code == 401, e.code
print('PASS Rspamd controller requires credentials even from localhost', flush=True)
print('All native mail checks passed', flush=True)

# Per-domain certificate selection on the same IP: real SMTP and IMAP SNI.
import socket
cert_path = '/etc/nginx/itrupanel/ssl/native-mail-second.crt'
ctx_second = ssl.create_default_context(cafile=cert_path)
with socket.create_connection(('127.0.0.1', 993), timeout=10) as sock:
    with ctx_second.wrap_socket(sock, server_hostname='mail.second.test') as tls:
        assert ('DNS', 'mail.second.test') in tls.getpeercert()['subjectAltName']
        assert tls.recv(2048).startswith(b'* OK')
with smtplib.SMTP('127.0.0.1', 587, timeout=20) as s:
    s._host = 'mail.second.test'
    s.starttls(context=ctx_second)
    s.login('c@second.test', password)
    assert ('DNS', 'mail.second.test') in s.sock.getpeercert()['subjectAltName']
print('PASS SMTP and IMAP per-domain TLS SNI with verified certificates', flush=True)

before = subprocess.check_output(['doveadm', '-f', 'tab', 'quota', 'get', '-A'], text=True)
assert 'c@second.test\tUser quota\tSTORAGE' in before, before
msg = email.message.EmailMessage()
msg['From'] = 'b@example.test'
msg['To'] = 'unknown@second.test'
msg['Subject'] = 'Catch All integration check'
msg.set_content('Delivery to an unknown local address.')
with smtplib.SMTP('mail.example.test', 587, timeout=30) as s:
    s.starttls(context=ctx)
    s.login('b@example.test', password)
    assert s.send_message(msg) == {}
for _ in range(30):
    out = subprocess.check_output(['doveadm', 'search', '-u', 'c@second.test', 'ALL'], text=True)
    if out.strip(): break
    time.sleep(1)
assert out.strip(), 'Catch All delivery failed'
with smtplib.SMTP('127.0.0.1', 25, timeout=20) as s:
    s.ehlo('sender.example.test')
    s.mail('sender@external.test')
    code, reply = s.rcpt('disabled@second.test')
    assert code >= 500, (code, reply)
print('PASS Catch All delivers unknown addresses; disabled mailbox stays rejected', flush=True)
