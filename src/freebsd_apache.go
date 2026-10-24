// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"os"
	"strings"
)

// Apache на FreeBSD (пакет apache24): конфигурация целиком принадлежит панели, потому что в пакете
// включён mpm_prefork и мало нужных модулей. Исходный файл сохраняется рядом (.itrupanel-orig).

const (
	bsdApacheDir  = bsdPrefix + "/etc/apache24"
	bsdApacheConf = bsdApacheDir + "/httpd.conf"
	bsdApacheBin  = bsdPrefix + "/sbin/httpd"
	bsdApacheMark = "# ITRUPANEL: Apache работает за nginx"
)

var bsdApacheModules = []string{
	"mpm_event", "authn_core", "authn_file", "authz_core", "authz_host", "authz_user", "authz_groupfile", "auth_basic",
	"access_compat", "unixd", "log_config", "mime", "dir", "alias", "autoindex", "negotiation", "setenvif", "env",
	"filter", "deflate", "rewrite", "headers", "expires", "remoteip", "reqtimeout", "version",
	"proxy", "proxy_fcgi", "proxy_http", "proxy_wstunnel",
}

func bsdApacheConfig() string {
	var b strings.Builder
	b.WriteString(bsdApacheMark + "\n# Файл создаёт панель; исходный сохранён как httpd.conf.itrupanel-orig.\n")
	b.WriteString("ServerRoot \"" + bsdPrefix + "\"\nListen 127.0.0.1:" + apachePort + "\nServerName localhost\n")
	for _, m := range bsdApacheModules {
		b.WriteString("LoadModule " + m + "_module libexec/apache24/mod_" + m + ".so\n")
	}
	b.WriteString(`User www
Group www
ServerAdmin root@localhost
ServerTokens Prod
ServerSignature Off
TraceEnable Off
<Directory />
    AllowOverride None
    Require all denied
</Directory>
DocumentRoot "` + bsdPrefix + `/www/apache24/data"
<Directory "` + bsdPrefix + `/www/apache24/data">
    Require all denied
</Directory>
<Files ".ht*">
    Require all denied
</Files>
ErrorLog "/var/log/httpd-error.log"
LogLevel warn
<IfModule mime_module>
    TypesConfig etc/apache24/mime.types
    AddType application/x-compress .Z
    AddType application/x-gzip .gz .tgz
</IfModule>
<IfModule reqtimeout_module>
    RequestReadTimeout header=20-40,MinRate=500 body=20,MinRate=500
</IfModule>
IncludeOptional ` + bsdApacheDir + `/itrupanel/*.conf
`)
	return b.String()
}

// bsdEnsureApache ставит apache24 и пишет конфигурацию панели.
func bsdEnsureApache() error {
	if !fileExists(bsdApacheBin) {
		if err := bsdPkgInstall("apache24"); err != nil {
			return errf("Не удалось установить Apache: %s", firstLine(err.Error()))
		}
	}
	os.MkdirAll(P(bsdApacheDir+"/itrupanel"), 0755)
	if cur := readFile(bsdApacheConf); !strings.HasPrefix(cur, bsdApacheMark) {
		if cur != "" && !fileExists(bsdApacheConf+".itrupanel-orig") {
			writeFile(bsdApacheConf+".itrupanel-orig", []byte(cur), 0644)
		}
		if err := writeFile(bsdApacheConf, []byte(bsdApacheConfig()), 0644); err != nil {
			return err
		}
	}
	if out, err := run(bsdApacheBin, "-t"); err != nil {
		return errf("Конфигурация Apache: %s", out)
	}
	return service("enable", "apache2")
}
