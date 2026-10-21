// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// Конфигурация в стиле ISPmanager 6: корень задан переменной, PHP — пул на пользователя, HTTPS — отдельный блок.
const impIspNginx = `
# configuration file /etc/nginx/nginx.conf:
http {
    include /etc/nginx/vhosts/*/*.conf;
    server { listen 80 default_server; server_name _; return 444; }
}
# configuration file /etc/nginx/vhosts/shop/shop.example.com.conf:
server {
    server_name shop.example.com www.shop.example.com;
    listen 203.0.113.5:80;
    set $root_path /var/www/shop/data/www/shop.example.com;
    root $root_path;
    location / {
        location ~ [^/]\.ph(p\d*|tml)$ { try_files /does_not_exists @php; }
    }
    location @php {
        fastcgi_pass unix:/var/www/php-fpm/shop.sock;
    }
}
server {
    server_name shop.example.com www.shop.example.com;
    listen 203.0.113.5:443 ssl;
    ssl_certificate "/var/www/httpd-cert/shop/shop.example.com_le1.crt";
    set $root_path /var/www/shop/data/www/shop.example.com;
    root $root_path;
    location @php { fastcgi_pass unix:/var/www/php-fpm/shop.sock; }
}
# configuration file /etc/nginx/vhosts/blog/blog.example.org.conf:
server {
    server_name blog.example.org;
    listen 80;
    set $root_path /var/www/blog/data/www/blog.example.org;
    root $root_path;
    location / { try_files $uri $uri/ =404; }
}
server {
    server_name old.example.org;
    listen 80;
    return 301 https://new.example.org$request_uri;
}
server {
    server_name api.example.net;
    listen 80;
    location / { proxy_pass http://127.0.0.1:3000; }
}
`

type fakeRun map[string]string

func (f fakeRun) Run(_ context.Context, cmd string) (string, error) {
	for k, v := range f {
		if strings.HasPrefix(cmd, k) {
			if strings.HasPrefix(v, "ERR:") {
				return "", errors.New(strings.TrimPrefix(v, "ERR:"))
			}
			return v, nil
		}
	}
	return "", errors.New("command not found: " + cmd)
}
func (f fakeRun) Stream(context.Context, string, io.Writer) error { return nil }
func (f fakeRun) Close() error                                    { return nil }

func TestParseNginxIspmanager(t *testing.T) {
	sites := sitesFromNginx(impIspNginx)
	by := map[string]ImpSite{}
	for _, s := range sites {
		by[s.Domain] = s
	}
	if len(sites) != 3 {
		t.Fatalf("sites: %+v", sites)
	}
	shop := by["shop.example.com"]
	if shop.Root != "/var/www/shop/data/www/shop.example.com" || shop.Mode != "php" || !shop.SSL || shop.sock != "/var/www/php-fpm/shop.sock" || strings.Join(shop.Aliases, ",") != "www.shop.example.com" {
		t.Fatalf("shop: %+v", shop)
	}
	if blog := by["blog.example.org"]; blog.Mode != "static" || blog.SSL || blog.Root != "/var/www/blog/data/www/blog.example.org" {
		t.Fatalf("blog: %+v", blog)
	}
	if api := by["api.example.net"]; api.Mode != "proxy" || api.ProxyTo != "http://127.0.0.1:3000" {
		t.Fatalf("api: %+v", api)
	}
	if _, ok := by["old.example.org"]; ok {
		t.Fatal("a redirect-only server must not become a site")
	}
}

func TestPHPVersionFrom(t *testing.T) {
	for in, want := range map[string]string{
		"/run/php/php8.1-fpm.sock":             "8.1",
		"/var/run/php-fpm/php74-php-fpm.sock":  "7.4",
		"/etc/php/8.3/fpm/pool.d/shop.conf":    "8.3",
		"/etc/opt/remi/php82/php-fpm.d/a.conf": "8.2",
		"/var/www/php-fpm/shop.sock":           "",
		"/opt/php-8.0/etc/php-fpm.d/shop.conf": "8.0",
		"unix:/run/php-fpm/www.sock":           "",
	} {
		if got := phpVersionFrom(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestScanSSH(t *testing.T) {
	r := fakeRun{
		impCmdOS:      "Debian GNU/Linux 12 (bookworm)\nx86_64\n",
		impCmdPanel:   "ispmanager\n",
		impCmdNginx:   impIspNginx,
		`for d in `:   "/var/www/shop/data/www/shop.example.com\tshop\t52428800\n/var/www/blog/data/www/blog.example.org\tblog\t1048576\n",
		`grep -rlF `:  "/etc/php/8.1/fpm/pool.d/shop.conf\n",
		impCmdPHPVers: "/usr/bin/php8.1\n/etc/php/8.1/\n/etc/php/7.4/\n",
		impCmdMySQL:   "shop_db\t10485760\nblog_db\t2048\n",
		impCmdDBUsers: "shop_db\tshop_user\nblog_db\tblog_user\n",
		impCmdSvc:     "nginx\nmariadb\nexim4\ndovecot\nnamed\npure-ftpd\n",
		impCmdMailBox: "info@shop.example.com\nsales@shop.example.com\nme@blog.example.org\n",
		impCmdMailDom: "shop.example.com\nblog.example.org\nspare.example.net\n",
		impCmdDNS:     "shop.example.com\nblog.example.org\n",
		impCmdFTP:     "shopftp:/var/www/shop/data/www\n",
		"crontab -l":  "MAILTO=a@b.c\n*/5 * * * * php /var/www/shop/data/www/shop.example.com/cron.php\n# note\n0 3 * * * /usr/bin/true\n",
	}
	inv, err := impScanSSH(context.Background(), r, "old.example.net")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Panel != "ispmanager" || !strings.HasPrefix(inv.OS, "Debian") || len(inv.Sites) != 3 {
		t.Fatalf("inventory: %+v", inv)
	}
	var shop ImpSite
	for _, s := range inv.Sites {
		if s.Domain == "shop.example.com" {
			shop = s
		}
	}
	if shop.Owner != "shop" || shop.Size != 52428800 || shop.PHP != "8.1" {
		t.Fatalf("shop details: %+v", shop)
	}
	if strings.Join(inv.PHPVersions, ",") != "7.4,8.1" {
		t.Fatalf("php versions: %v", inv.PHPVersions)
	}
	if len(inv.DBs) != 2 || inv.DBs[1].Name != "shop_db" || inv.DBs[1].Size != 10485760 || strings.Join(inv.DBs[1].Users, ",") != "shop_user" {
		t.Fatalf("dbs: %+v", inv.DBs)
	}
	boxes := map[string]int{}
	for _, m := range inv.MailDomains {
		boxes[m.Name] = m.Boxes
	}
	if boxes["shop.example.com"] != 2 || boxes["blog.example.org"] != 1 || len(boxes) != 3 {
		t.Fatalf("mail: %v", boxes)
	}
	if len(inv.DNS) != 2 || len(inv.FTP) != 1 || inv.FTP[0].Name != "shopftp" {
		t.Fatalf("dns/ftp: %+v %+v", inv.DNS, inv.FTP)
	}
	if len(inv.Cron) != 4 && len(inv.Cron) != 2 {
		t.Fatalf("cron: %+v", inv.Cron)
	}
	for _, c := range inv.Cron {
		if c.Schedule == "" || c.Command == "" || strings.HasPrefix(c.Command, "a@b.c") {
			t.Fatalf("bad cron entry: %+v", c)
		}
	}
}

func TestScanSSHDatabaseAccessFailure(t *testing.T) {
	r := fakeRun{impCmdOS: "x\ny\n", impCmdPanel: "", impCmdNginx: impIspNginx, impCmdMySQL: "ERR:Access denied for user 'root'", `for d in `: "", impCmdSvc: ""}
	inv, err := impScanSSH(context.Background(), r, "old")
	if err != nil {
		t.Fatal(err)
	}
	if inv.DBAccessError == "" || len(inv.DBs) != 0 || len(inv.Warnings) == 0 {
		t.Fatalf("a database access error must be reported: %+v", inv)
	}
}

func TestSystemVhostsSkipped(t *testing.T) {
	text := `
server { server_name mail.example.com; listen 443 ssl; root /var/lib/roundcube/public_html; location ~ \.php$ { fastcgi_pass unix:/run/php/php8.3-fpm.sock; } }
server { server_name pma.example.com; listen 80; root /usr/share/phpmyadmin; location ~ \.php$ { fastcgi_pass unix:/run/php/php8.3-fpm.sock; } }
server { server_name panel.example.com; listen 80; root /usr/local/mgr5/skins; }
server { server_name site.example.com; listen 80; root /var/www/u/data/www/site.example.com; }
`
	sites := sitesFromNginx(text)
	if len(sites) != 1 || sites[0].Domain != "site.example.com" {
		t.Fatalf("service web applications must not be listed as websites: %+v", sites)
	}
}

func TestACMEVhostNotASite(t *testing.T) {
	text := `
server {
    listen 80;
    server_name mail.example.com;
    location ^~ /.well-known/acme-challenge/ { root /var/www/itrupanel-acme; try_files $uri =404; }
    location / { return 301 https://mail.example.com$request_uri; }
}
server {
    listen 80;
    server_name real.example.com;
    location ^~ /.well-known/acme-challenge/ { root /var/www/itrupanel-acme; }
    root /var/www/u/data/www/real.example.com;
}
`
	sites := sitesFromNginx(text)
	if len(sites) != 1 || sites[0].Domain != "real.example.com" || sites[0].Root != "/var/www/u/data/www/real.example.com" {
		t.Fatalf("ACME location roots must not become websites: %+v", sites)
	}
}
