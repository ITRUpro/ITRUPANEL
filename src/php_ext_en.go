// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

// Английские названия расширений и разделов для интерфейса на английском.
var phpExtTitleEn = map[string]string{
	"ioncube": "ionCube Loader (run encoded scripts)", "mysql": "MySQL / MariaDB (mysqli, PDO)", "pgsql": "PostgreSQL (pgsql, PDO)",
	"sqlite3": "SQLite (sqlite3, PDO)", "mongodb": "MongoDB", "odbc": "ODBC (odbc, PDO)", "dba": "DBA (Berkeley DB and similar)",
	"ldap": "LDAP / Active Directory", "interbase": "Firebird / InterBase", "opcache": "OPcache (PHP accelerator)", "apcu": "APCu (in-memory cache)",
	"redis": "Redis", "memcached": "Memcached", "memcache": "Memcache (old client)", "igbinary": "igbinary (fast serialization)", "msgpack": "MessagePack",
	"lz4": "LZ4 (compression)", "zstd": "Zstandard (compression)", "amqp": "AMQP / RabbitMQ", "stomp": "STOMP (message brokers)", "zmq": "ZeroMQ",
	"gd": "GD (images)", "imagick": "ImageMagick (imagick)", "gmagick": "GraphicsMagick (gmagick)", "vips": "libvips (fast image processing)",
	"exif": "EXIF (photo metadata)", "mbstring": "mbstring (multibyte strings)", "intl": "intl (localization, ICU)", "iconv": "iconv (encodings)",
	"xml": "XML (dom, simplexml, xmlreader, xmlwriter)", "xsl": "XSL (XSLT transformations)", "xmlrpc": "XML-RPC", "yaml": "YAML", "bcmath": "BCMath (precise math)",
	"gmp": "GMP (big numbers)", "uuid": "UUID", "ds": "DS (data structures)", "mailparse": "Mailparse (parse emails)", "enchant": "Enchant (spell checking)",
	"pspell": "Pspell (spell checking)", "tidy": "Tidy (HTML cleanup)", "gettext": "gettext (translations)", "calendar": "Calendar",
	"curl": "cURL (HTTP requests)", "soap": "SOAP", "sockets": "Sockets", "ftp": "FTP", "ssh2": "SSH2", "snmp": "SNMP", "imap": "IMAP (mail)",
	"http": "pecl_http (extended HTTP)", "oauth": "OAuth", "raphf": "raphf (resources for pecl_http)", "grpc": "gRPC", "protobuf": "Protocol Buffers",
	"smbclient": "SMB (Windows network shares)", "swoole": "Swoole (async server)", "event": "Event (libevent)", "ev": "Ev (libev)",
	"sodium": "Sodium (modern cryptography)", "gnupg": "GnuPG", "mcrypt": "mcrypt (legacy encryption)", "xdebug": "Xdebug (debugging)",
	"xhprof": "XHProf (profiling)", "pcov": "PCOV (code coverage)", "excimer": "Excimer (profiling)", "spx": "SPX (profiler)", "uopz": "uopz (code overriding for tests)",
	"zip": "Zip", "bz2": "Bzip2", "rar": "RAR", "ffi": "FFI (call C libraries)", "readline": "Readline (console)", "pcntl": "pcntl (process control)",
	"posix": "POSIX", "shmop": "shmop (shared memory)", "rrd": "RRDtool", "solr": "Apache Solr",
}

var phpExtGroupEn = map[string]string{
	grpDB: "Databases", grpCache: "Cache and queues", grpGfx: "Graphics and media", grpText: "Strings, XML and data", grpNet: "Network and protocols",
	grpSec: "Encryption and security", grpDebug: "Debugging and profiling", grpArch: "Archives", grpCode: "Code protection", grpMisc: "Other",
}
