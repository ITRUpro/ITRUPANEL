# Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
"""Real Fail2ban lifecycle test, using only a private socket and no firewall actions.

Run: python3 test/ftp_guard_integration.py /path/to/fail2ban-source
On macOS, polling replaces the Linux journal backend. This verifies jail
creation/restart/recovery, not systemd or nftables packet filtering.
"""
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

source = Path(sys.argv[1]).resolve()
root = Path(__file__).resolve().parents[1]
work = Path(tempfile.mkdtemp(prefix="f2b-", dir="/private/tmp" if sys.platform == "darwin" else "/tmp"))
env = dict(os.environ, PYTHONPATH=str(source))
config = work / "config"
shutil.copytree(source / "config", config)
# No firewall action definitions are available to this isolated server, even
# if a later change accidentally leaves an action reference in a test jail.
shutil.rmtree(config / "action.d")
(config / "action.d").mkdir()
# No distribution jails or system paths are used by this test server.
shutil.rmtree(config / "jail.d", ignore_errors=True)
(config / "jail.d").mkdir()
(config / "jail.conf").write_text("[DEFAULT]\nenabled = false\nusedns = no\n")
(config / "fail2ban.local").write_text("[Definition]\nlogtarget = " + str(work / "server.log") + "\ndbfile = " + str(work / "db.sqlite3") + "\n")
log = work / "ftp.log"
log.touch()
client = [sys.executable, str(source / "bin/fail2ban-client"), "-c", str(config), "-s", str(work / "socket"), "-p", str(work / "pid")]

def run(*args, expected=0):
    p = subprocess.run(client + list(args), env=env, capture_output=True, text=True, timeout=20)
    if p.returncode != expected:
        raise AssertionError((args, p.returncode, p.stdout, p.stderr))
    return p.stdout + p.stderr

go = (root / "ftp_guard.go").read_text()
template = re.search(r'return fmt.Sprintf\(`(.*?)`, ftpGuardHeader, ftpGuardJail, port\)', go, re.S).group(1)
policy = template.replace("%s", "# integration\n", 1).replace("%s", "itrupanel-ftp", 1).replace("%d", "21").replace("%%", "%")
policy = policy.replace("backend = systemd", "backend = polling\nlogpath = " + str(log))
policy = re.sub(r"^action = .*", "action =", policy, flags=re.M)
assert "nftables" not in policy and "action =\n" in policy
# Extract the production apply arguments: the test fails if they regress.
match = re.search(r'runTimeout\(30\*time.Second, "", "fail2ban-client", ("reload".*?), ftpGuardJail\)', go)
apply_args = re.findall(r'"([^"]+)"', match.group(1)) + ["itrupanel-ftp"]
target = config / "jail.d/itrupanel-ftp.local"
spectator = config / "jail.d/spectator.local"
spectator.write_text(policy.replace("[itrupanel-ftp]", "[spectator]"))
started = False
try:
    run("start")
    started = True
    run("set", "spectator", "maxretry", "31")
    target.write_text(policy)
    # Reproduce the reported failure against an actual Fail2ban server.
    output = run("reload", "--restart", "itrupanel-ftp", expected=255)
    assert "does not exist" in output, output
    print("Reproduced: targeted reload of absent jail fails")
    run(*apply_args)
    run("status", "itrupanel-ftp")
    assert run("get", "spectator", "maxretry").strip() == "31"
    print("PASS: creates missing jail without changing another jail's runtime settings")
    run(*apply_args)
    run("status", "itrupanel-ftp")
    print("PASS: reapplies existing jail")
    run("stop", "itrupanel-ftp")
    run(*apply_args)
    run("status", "itrupanel-ftp")
    assert run("get", "spectator", "maxretry").strip() == "31"
    print("PASS: recovers after a prior rollback removed the jail")
finally:
    if started:
        run("stop")
    shutil.rmtree(work)
