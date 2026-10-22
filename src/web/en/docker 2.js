// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Раздел «Docker» (только администратор).
const dockerTabs = [['overview', "Overview"], ['containers', "Containers"], ['compose', 'Compose'], ['images', "Images"], ['networks', "Networks"], ['volumes', "Volumes"], ['settings', "Settings"]];
const dockerLines = s => String(s || '').split(/\r?\n/).map(x => x.trim()).filter(Boolean);
async function dockerJob(op, body) { try { const r = await api(op, body); watchJob(r.job); } catch (e) { fail(e); } }
async function dockerDo(op, body, ok) { try { const r = await api(op, body); if (ok || r?.info) toast(r?.info || ok, 'ok'); await route(); } catch (e) { fail(e); } }
function dockerStateTag(c) {
  const t = { running: ['ok', "running"], paused: ['warn', "paused"], restarting: ['acc', "restarting"], created: ['warn', "created"], exited: ['err', "stopped"], dead: ['err', "error"] }[c.state] || ['warn', c.state];
  return `<span class="tag ${t[0]}">${h(t[1])}</span>`;
}

function dockerContainerForm(networks, image = '') {
  openForm({ title: "Create container", ok: "Create and start", wide: true, fields: [
    { name: 'image', label: "Image", value: image, placeholder: 'nginx:latest', required: true, hint: "An image from Docker Hub or a private registry. It is pulled if not present on the server." },
    { name: 'name', label: "Container name", placeholder: 'my-app', hint: "Optional. Latin letters, digits, “_”, “.” and “-”." },
    { name: 'ports', label: "Ports (server:container)", type: 'textarea', rows: 3, placeholder: '127.0.0.1:8080:80\n53:53/udp', hint: "One per line. Docker opens published ports bypassing the panel firewall. To expose the app only through a site, use 127.0.0.1:port and the “Proxy” site mode." },
    { name: 'volumes', label: "Volumes and folders", type: 'textarea', rows: 3, placeholder: '/var/lib/my-app:/data\nmy-volume:/var/lib/data:ro', hint: "One per line: /server/path:/container/path or volume_name:/path. Add :ro for read-only access." },
    { name: 'env', label: "Environment variables", type: 'textarea', rows: 3, placeholder: 'TZ=Europe/Moscow', hint: "One per line: NAME=value." },
    { name: 'restart', label: "Restart", type: 'select', value: 'unless-stopped', options: [['unless-stopped', "Always, unless stopped manually"], ['always', "Always"], ['on-failure', "On failure"], ['no', "Never"]] },
    { name: 'network', label: "Network", type: 'select', value: 'bridge', options: (networks || []).filter(n => n.name !== 'none').map(n => [n.name, n.name + (n.driver ? ' (' + n.driver + ')' : '')]) },
    { name: 'command', label: "Command", placeholder: "Empty — the image default command" },
    { name: 'memory', label: "Memory limit, MB", placeholder: "0 — unlimited" },
    { name: 'cpus', label: "CPU limit, cores", placeholder: "0 — unlimited, for example 0.5" },
  ], submit: async v => {
    const r = await api('docker.container.create', { image: v.image.trim(), name: v.name.trim(), ports: dockerLines(v.ports), volumes: dockerLines(v.volumes), env: String(v.env || '').split(/\r?\n/).filter(x => x.trim()), restart: v.restart, network: v.network, command: v.command, memory: Number(v.memory) || 0, cpus: Number(String(v.cpus).replace(',', '.')) || 0 });
    watchJob(r.job);
  } });
}

async function dockerTerminal(c) {
  if (!$('link[href="/vendor/xterm.css"]')) { const l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/vendor/xterm.css'; document.head.append(l); }
  try { await loadScript('/vendor/xterm.js'); await loadScript('/vendor/addon-fit.js'); } catch (e) { fail(e); return; }
  const m = modal(`<h3>Terminal: ${h(c.name)}</h3><div class="term-box" style="height:60vh"></div><div class="btns"><button class="btn primary" data-close>Close</button></div>`, { wide: true });
  const term = new Terminal({ fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 14, cursorBlink: true, theme: { background: '#0f1424' } });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit); term.open($('.term-box', m.el)); fit.fit();
  const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/docker-shell?id=' + encodeURIComponent(c.id));
  ws.binaryType = 'arraybuffer';
  const sendSize = () => { if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows })); };
  ws.onopen = () => { sendSize(); term.focus(); };
  ws.onmessage = e => term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data));
  ws.onclose = () => term.write("\r\n\u001b[33m[session closed]\u001b[0m\r\n");
  term.onData(d => ws.readyState === 1 && ws.send(new TextEncoder().encode(d)));
  const onResize = () => { fit.fit(); sendSize(); };
  window.addEventListener('resize', onResize);
  const watch = setInterval(() => { if (!m.el.isConnected) { clearInterval(watch); window.removeEventListener('resize', onResize); ws.close(); term.dispose(); } }, 500);
}

async function dockerLogs(c) {
  try { const r = await api('docker.container.logs', { id: c.id, tail: 500 }); showOutput("Log: " + c.name, r.log); } catch (e) { fail(e); }
}

const dockerRender = {
  async overview(view, st) {
    const p = panel(view, "Summary");
    const df = st.df || [];
    p.innerHTML = `<div class="stats">
      <div class="stat"><div class="k">Service</div><div class="v">${st.running ? "<span class=\"tag ok\">running</span>" : "<span class=\"tag err\">stopped</span>"}</div></div>
      <div class="stat"><div class="k">Containers</div><div class="v">${h(st.containers_running ?? 0)} <small>of ${h(st.containers ?? 0)} running</small></div></div>
      <div class="stat"><div class="k">Images</div><div class="v">${h(st.images ?? 0)}</div></div>
    </div>
    <div class="kv" style="margin-top:20px">
      <div>Docker version</div><div>${h(st.version || st.client || '—')}</div>
      <div>Docker Compose</div><div>${st.compose ? h(st.compose) : "not installed"}</div>
      <div>Storage driver</div><div>${h(st.storage_driver || '—')}</div>
      <div>Data directory</div><div class="mono">${h(st.root_dir || '—')}</div>
      <div>Panel DB servers in Docker</div><div>${h(st.db_servers)}</div>
    </div>`;
    if (!df.length) return;
    const u = panel(view, "Disk usage");
    const names = { Images: "Images", Containers: "Containers", 'Local Volumes': "Volumes", 'Build Cache': "Build cache" };
    u.innerHTML = '<div class="toolbar"><span class="grow"></span><button class="btn" data-prune>' + icon('trash') + "Clean up unused</button></div><div data-t></div>";
    table($('[data-t]', u), { rows: df, columns: [
      { t: "Type", r: r => h(names[r.Type] || r.Type) },
      { t: "Total", r: r => h(r.TotalCount) },
      { t: "In use", r: r => h(r.Active) },
      { t: "Size", r: r => h(r.Size) },
      { t: "Reclaimable", r: r => h(r.Reclaimable) },
    ] });
    $('[data-prune]', u).onclick = async () => { if (await confirmBox("Clean up unused data?", "Stopped containers, unused networks, untagged images and the build cache will be removed. Volumes and tagged images are kept.", { ok: "Clear" })) dockerJob('docker.prune', { what: 'system' }); };
  },

  async containers(view) {
    const p = panel(view);
    const d = await api('docker.containers');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + "Create container</button><span class=\"grow\"></span><span class=\"muted\">Running: " + d.containers.filter(c => c.state === 'running').length + " of " + d.containers.length + '</span></div><div data-t></div>';
    $('[data-new]', p).onclick = () => dockerContainerForm(d.networks);
    const act = (c, action) => dockerDo('docker.container.action', { id: c.id, action });
    table($('[data-t]', p), { rows: d.containers, empty: { icon: 'docker', title: "No containers yet", text: "<p>Click “Create container” or start a project on the “Compose” tab.</p>" }, columns: [
      { t: "Container", r: c => `<b>${h(c.name)}</b>${c.panel ? " <span class=\"tag acc\">panel DB server</span>" : ''}${c.project ? ' <span class="tag">Compose: ' + h(c.project) + '</span>' : ''}<div class="hint mono">${h(c.image)} · ${h(c.id)}</div>` },
      { t: "Status", r: c => dockerStateTag(c) + `<div class="hint">${h(c.status)}</div>` },
      { t: "Ports", r: c => c.ports ? c.ports.split(', ').map(p => `<div class="mono" style="font-size:12px;white-space:nowrap">${h(p.replace('->', ' → '))}</div>`).join('') : '—', cls: 'hide-sm' },
      { t: "CPU / memory", r: c => c.cpu ? `${h(c.cpu)}<div class="hint">${h(c.mem)}</div>` : '—', cls: 'hide-sm' },
    ], onRow: c => dockerLogs(c), actions: c => [
      c.state === 'running' ? (!c.panel && { t: "Stop", i: 'stop', fn: () => act(c, 'stop') }) : c.state === 'paused' ? { t: "Resume", i: 'play', fn: () => act(c, 'unpause') } : { t: "Start", i: 'play', fn: () => act(c, 'start') },
      { t: "Restart", i: 'refresh', fn: () => act(c, 'restart') },
      c.state === 'running' && !c.panel && { t: "Pause", i: 'pause', fn: () => act(c, 'pause') },
      c.state === 'running' && { t: "Terminal", i: 'terminal', fn: () => dockerTerminal(c) },
      { t: "Log", i: 'file', fn: () => dockerLogs(c) },
      { t: "Details", i: 'info', fn: async () => { try { const r = await api('docker.container.inspect', { id: c.id }); showOutput(c.name, r.text); } catch (e) { fail(e); } } },
      !c.panel && { t: "Delete", i: 'trash', danger: true, fn: async () => { const v = await confirmBox("Delete container?", `<b>${h(c.name)}</b> will be stopped and removed together with its files. Server folders mounted into the container are not deleted.`, { extra: "<label class=\"check\"><input type=\"checkbox\" name=\"purge\" checked><span>Also remove the container image and volumes if no other container uses them (data in volumes will be lost)</span></label>" }); if (v) dockerDo('docker.container.action', { id: c.id, action: 'remove', purge: !!v.purge }); } },
    ] });
  },

  async compose(view, st) {
    const p = panel(view);
    const d = await api('docker.compose.list');
    const templates = st.templates || {};
    const tplNames = { nginx: "nginx (static site)", redis: 'Redis', wordpress: 'WordPress + MariaDB', 'uptime-kuma': "Uptime Kuma (monitoring)", portainer: "Portainer (Docker management)" };
    const form = (proj, yaml = '', env = '') => openForm({ title: proj ? "Project " + proj : "Create Compose project", ok: proj ? "Save" : "Create", wide: true, fields: [
      !proj && { name: 'name', label: "Project name", placeholder: 'my-project', required: true, hint: "Lowercase Latin letters, digits, “_” and “-”. Project folder: " + h(d.dir) + "/name" },
      !proj && { name: 'template', label: "Template", type: 'select', value: '', options: [['', "Empty"], ...Object.keys(templates).map(k => [k, tplNames[k] || k])] },
      { name: 'yaml', label: 'docker-compose.yml', type: 'textarea', rows: 18, value: yaml, required: true, placeholder: 'services:\n  app:\n    image: nginx:stable-alpine\n    ports:\n      - "127.0.0.1:8080:80"' },
      { name: 'env', label: ".env file", type: 'textarea', rows: 4, value: env, placeholder: 'DB_PASSWORD=…', hint: "Variables substituted for ${NAME} in docker-compose.yml." },
      { name: 'up', label: proj ? "Apply changes (docker compose up -d)" : "Start after creation", type: 'checkbox', value: true },
      { type: 'html', html: "<p class=\"hint\">Templates publish ports on 127.0.0.1 only. To open the app on a domain, create a site in “Proxy” mode pointing to that port.</p>" },
    ].filter(Boolean), onChange: (v, n) => {
      if (n === 'template') { v.yaml = templates[v.template] || ''; v.env = v.template === 'wordpress' ? 'DB_PASSWORD=' + genPass(20) + '\n' : ''; }
    }, submit: async v => {
      const r = await api('docker.compose.save', { name: proj || v.name.trim(), yaml: v.yaml, env: v.env, new: !proj, up: !!v.up });
      if (r && r.job) watchJob(r.job); else { toast("Saved", 'ok'); await route(); }
    } });
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new ' + (d.compose ? '' : 'disabled') + '>' + icon('plus') + "Create project</button><span class=\"grow\"></span><span class=\"muted mono\">" + h(d.dir) + '</span></div>' + (d.compose ? '' : "<p class=\"tag warn\">Docker Compose is not installed. Click “Install Compose” at the top of the page.</p>") + '<div data-t></div>';
    $('[data-new]', p).onclick = () => form('');
    const act = (r, action) => dockerJob('docker.compose.action', { name: r.name, action });
    table($('[data-t]', p), { rows: d.projects, empty: { icon: 'docker', title: "No projects yet", text: "<p>Create a project from a template or paste your own docker-compose.yml.</p>" }, columns: [
      { t: "Project", r: r => `<b>${h(r.name)}</b><div class="hint mono">${h(r.file)}</div>` },
      { t: "Containers", r: r => r.containers ? `<span class="tag ${r.running === r.containers ? 'ok' : r.running ? 'warn' : 'err'}">${r.running} of ${r.containers} running</span>` : "<span class=\"tag\">not started</span>" },
      { t: "Modified", r: r => r.modified && !r.modified.startsWith('0001') ? fmtDate(r.modified) : '—', cls: 'hide-sm' },
    ], onRow: async r => { try { const c = await api('docker.compose.get', { name: r.name }); form(r.name, c.yaml, c.env); } catch (e) { fail(e); } }, actions: r => [
      { t: "Start / apply", i: 'play', fn: () => act(r, 'up') },
      r.running > 0 && { t: "Stop", i: 'stop', fn: () => act(r, 'stop') },
      r.containers > 0 && { t: "Restart", i: 'refresh', fn: () => act(r, 'restart') },
      { t: "Update images", i: 'download', fn: async () => { if (await confirmBox("Update images?", "New versions of the project images will be pulled. To switch containers to them, click “Start / apply” afterwards.", { ok: "Upload", danger: false })) act(r, 'pull'); } },
      { t: "Edit", i: 'edit', fn: async () => { try { const c = await api('docker.compose.get', { name: r.name }); form(r.name, c.yaml, c.env); } catch (e) { fail(e); } } },
      { t: "Log", i: 'file', fn: async () => { try { const l = await api('docker.compose.logs', { name: r.name }); showOutput("Log: " + r.name, l.log); } catch (e) { fail(e); } } },
      { t: "Delete", i: 'trash', danger: true, fn: async () => {
        const v = await confirmBox("Delete project?", `Containers of project <b>${h(r.name)}</b> will be stopped and removed together with the folder ${h(r.path)}.`, { extra: "<label class=\"check\"><input type=\"checkbox\" name=\"volumes\" checked><span>Remove project volumes (data will be lost)</span></label><label class=\"check\"><input type=\"checkbox\" name=\"images\" checked><span>Remove project images if no other container uses them</span></label>" });
        if (v) dockerJob('docker.compose.delete', { name: r.name, volumes: !!v.volumes, images: !!v.images });
      } },
    ] });
  },

  async images(view, st, networks) {
    const p = panel(view);
    const rows = await api('docker.images');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-pull>' + icon('download') + "Pull image</button><span class=\"grow\"></span><button class=\"btn\" data-prune>" + icon('trash') + "Delete unused</button></div><div data-t></div>";
    $('[data-pull]', p).onclick = () => openForm({ title: "Pull image", ok: "Upload", fields: [
      { name: 'image', label: "Image", placeholder: 'nginx:latest', required: true, hint: "A Docker Hub image name (nginx:latest, redis:7-alpine) or a full registry address (ghcr.io/org/app:1.0)." },
    ], submit: async v => { const r = await api('docker.image.pull', { image: v.image.trim() }); watchJob(r.job); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox("Delete unused images?", "All images not used by any container will be removed.", { ok: "Delete" })) dockerJob('docker.prune', { what: 'images' }); };
    const ref = r => r.repository && r.repository !== '<none>' ? r.repository + ':' + r.tag : r.id;
    table($('[data-t]', p), { rows, empty: { icon: 'docker', title: "No images yet", text: "<p>Images appear after a pull or when a container is created.</p>" }, columns: [
      { t: "Image", r: r => `<b>${h(r.repository)}</b>${r.tag && r.tag !== '<none>' ? ':' + h(r.tag) : ''}<div class="hint mono">${h(r.id.slice(0, 12))}</div>` },
      { t: "Size", r: r => h(r.size) },
      { t: "Created", r: r => h(r.since || r.created), cls: 'hide-sm' },
    ], actions: r => [
      r.repository !== '<none>' && { t: "Create container", i: 'plus', fn: async () => { let n = networks; if (!n) { try { n = await api('docker.networks'); } catch (e) { n = []; } } dockerContainerForm(n, ref(r)); } },
      { t: "Delete", i: 'trash', danger: true, fn: async () => { if (await confirmBox("Delete image?", h(ref(r)))) dockerDo('docker.image.remove', { id: ref(r) }, "Image deleted"); } },
    ] });
  },

  async networks(view) {
    const p = panel(view);
    const rows = await api('docker.networks');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + "Create network</button><span class=\"grow\"></span><button class=\"btn\" data-prune>" + icon('trash') + "Delete unused</button></div><div data-t></div>";
    $('[data-new]', p).onclick = () => openForm({ title: "Create network", ok: "Create", fields: [
      { name: 'name', label: "Network name", placeholder: 'app-net', required: true },
      { name: 'driver', label: "Driver", type: 'select', value: 'bridge', options: [['bridge', "bridge — a network on this server"], ['macvlan', "macvlan — addresses from the local network"], ['ipvlan', 'ipvlan']] },
      { name: 'subnet', label: "Subnet", placeholder: "Automatic, for example 172.30.0.0/24" },
      { name: 'gateway', label: "Gateway", placeholder: "Automatic, for example 172.30.0.1" },
      { name: 'internal', label: "Internal network (no internet access)", type: 'checkbox' },
    ], submit: async v => { await api('docker.network.create', { name: v.name.trim(), driver: v.driver, subnet: v.subnet, gateway: v.gateway, internal: !!v.internal }); toast("Network created", 'ok'); await route(); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox("Delete unused networks?", "User-created networks with no containers attached will be removed.", { ok: "Delete" })) dockerJob('docker.prune', { what: 'networks' }); };
    table($('[data-t]', p), { rows, columns: [
      { t: "Network", r: r => `<b>${h(r.name)}</b>${r.builtin ? " <span class=\"tag\">built-in</span>" : ''}${r.internal ? " <span class=\"tag warn\">internal</span>" : ''}<div class="hint mono">${h(r.id)}</div>` },
      { t: "Driver", r: r => h(r.driver) },
      { t: "Subnet / gateway", r: r => `<span class="mono">${h(r.subnet || '—')}</span><div class="hint mono">${h(r.gateway)}</div>` },
      { t: "Containers", r: r => h(r.containers) },
    ], actions: r => [
      !r.builtin && { t: "Delete", i: 'trash', danger: true, fn: async () => { if (await confirmBox("Delete network?", h(r.name))) dockerDo('docker.network.remove', { id: r.name }, "Network deleted"); } },
    ] });
  },

  async volumes(view) {
    const p = panel(view);
    const rows = await api('docker.volumes');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + "Create volume</button><span class=\"grow\"></span><button class=\"btn\" data-prune>" + icon('trash') + "Delete unused</button></div><div data-t></div>";
    $('[data-new]', p).onclick = () => openForm({ title: "Create volume", ok: "Create", fields: [
      { name: 'name', label: "Volume name", placeholder: 'app-data', required: true },
    ], submit: async v => { await api('docker.volume.create', { id: v.name.trim() }); toast("Volume created", 'ok'); await route(); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox("Delete unused volumes?", "Anonymous volumes not attached to any container will be removed. Their data will be lost.", { ok: "Delete" })) dockerJob('docker.prune', { what: 'volumes' }); };
    table($('[data-t]', p), { rows, empty: { icon: 'docker', title: "No volumes yet", text: "<p>Volumes keep container data separate from the containers.</p>" }, columns: [
      { t: "Volume", r: r => `<b>${h(r.name)}</b><div class="hint mono">${h(r.mountpoint)}</div>` },
      { t: "Driver", r: r => h(r.driver), cls: 'hide-sm' },
      { t: "In use", r: r => r.used_by && r.used_by.length ? h(r.used_by.join(', ')) : "<span class=\"muted\">none</span>" },
    ], actions: r => [
      { t: "Open in file manager", i: 'folder', fn: () => go('files', { path: r.mountpoint }) },
      { t: "Delete", i: 'trash', danger: true, fn: async () => { if (await confirmBox("Delete volume?", `Data of volume <b>${h(r.name)}</b> will be deleted permanently.`)) dockerDo('docker.volume.remove', { id: r.name }, "Volume deleted"); } },
    ] });
  },

  async settings(view, st) {
    const s = st.settings || {};
    const p = panel(view);
    const dbWarn = st.db_servers ? ` Panel DB server containers (${h(st.db_servers)}) are restarted as well.` : '';
    p.innerHTML = `${st.config_error ? '<p class="tag err">' + h(st.config_error) + '</p>' : ''}
      <div class="field"><label>Docker service</label><div class="toolbar">${st.running ? "<span class=\"tag ok\">running</span>" : "<span class=\"tag err\">stopped</span>"}
        <button class="btn" data-svc="start">${icon('play')}Start</button><button class="btn" data-svc="restart">${icon('refresh')}Restart</button><button class="btn" data-svc="stop">${icon('stop')}Stop</button></div></div>
      <div class="field"><label for="dk-mirrors">Registry mirrors</label><textarea id="dk-mirrors" rows="2" style="min-height:0" placeholder="https://mirror.gcr.io">${h((s.mirrors || []).join('\n'))}</textarea><div class="hint">One address per line. Used instead of Docker Hub when pulling images.</div></div>
      <div class="field"><label for="dk-insecure">Private registry without HTTPS</label><textarea id="dk-insecure" rows="2" style="min-height:0" placeholder="registry.local:5000">${h((s.insecure || []).join('\n'))}</textarea><div class="hint">Registries Docker connects to without certificate verification.</div></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-logrotate" ${s.log_rotate ? 'checked' : ''}><span>Container log rotation<small>Without rotation, logs grow without limit and may fill the disk.</small></span></label>
        <div class="cols2"><div class="field"><label for="dk-logsize">File size</label><input type="text" id="dk-logsize" value="${h(s.log_max_size || '10m')}" placeholder="10m"></div><div class="field"><label for="dk-logfiles">Files to keep</label><input type="number" id="dk-logfiles" min="1" max="100" value="${h(s.log_max_file || 3)}"></div></div>
        <div class="hint">Applies to new containers. Existing ones must be recreated.</div></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-ipv6" ${s.ipv6 ? 'checked' : ''}><span>IPv6 network<small>Enables IPv6 on the default bridge network.</small></span></label>
        <input type="text" id="dk-ipv6cidr" value="${h(s.ipv6_cidr || '')}" placeholder="fd00:d0c:1::/64"></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-iptables" ${s.iptables ? 'checked' : ''}><span>iptables<small>Docker manages the rules for published ports and container internet access. Without it, containers have no network.</small></span></label></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-live" ${s.live_restore ? 'checked' : ''}><span>live-restore<small>Containers keep running while the Docker service restarts or fails.</small></span></label></div>
      <div class="field"><label for="dk-dir">Compose projects directory</label><input type="text" id="dk-dir" value="${h(s.project_dir || '')}"><div class="hint">Existing projects are not moved when the directory changes.</div></div>
      <div class="toolbar"><button class="btn primary" data-save>Save</button><button class="btn" data-config>${icon('file')}Configuration file</button><span class="grow"></span><button class="btn danger" data-remove>${icon('trash')}Remove Docker</button></div>
      <div class="field"><div class="hint">Changing service settings restarts Docker, and containers briefly stop unless live-restore is on.${dbWarn}</div></div>`;
    $$('[data-svc]', p).forEach(b => b.onclick = async () => {
      const a = b.dataset.svc;
      if (a !== 'start' && !await confirmBox(a === 'stop' ? "Stop Docker?" : "Restart Docker?", (a === 'stop' ? "All containers will be stopped." : "Containers briefly stop unless live-restore is on.") + dbWarn, { ok: a === 'stop' ? "Stop" : "Restart" })) return;
      b.disabled = true; await dockerDo('docker.service', { action: a }, a === 'stop' ? "Docker stopped" : "Docker started");
    });
    $('[data-save]', p).onclick = async e => {
      const val = id => $('#' + id, p);
      e.target.disabled = true;
      try {
        const r = await api('docker.settings.save', { mirrors: dockerLines(val('dk-mirrors').value), insecure: dockerLines(val('dk-insecure').value), log_rotate: val('dk-logrotate').checked, log_max_size: val('dk-logsize').value.trim(), log_max_file: Number(val('dk-logfiles').value) || 0, ipv6: val('dk-ipv6').checked, ipv6_cidr: val('dk-ipv6cidr').value.trim(), iptables: val('dk-iptables').checked, live_restore: val('dk-live').checked, project_dir: val('dk-dir').value.trim() });
        toast(r.restarted ? "Settings saved, Docker restarted" : "Settings saved", 'ok'); await route();
      } catch (err) { fail(err); e.target.disabled = false; }
    };
    $('[data-config]', p).onclick = async () => {
      try {
        const c = await api('docker.config.get');
        openForm({ title: c.path, ok: "Save and restart", wide: true, fields: [
          { name: 'text', label: 'daemon.json', type: 'textarea', rows: 22, value: c.text || '{\n}\n' },
          { type: 'html', html: "<p class=\"hint\">The file is validated as JSON. If Docker fails to start with the new settings, the previous file is restored.</p>" },
        ], submit: async v => { await api('docker.config.save', { text: v.text }); toast("Saved, Docker restarted", 'ok'); await route(); } });
      } catch (e) { fail(e); }
    };
    $('[data-remove]', p).onclick = async () => {
      if (st.db_servers) { toast("Docker is used by panel DB servers. Delete them in “DB servers” first.", 'err'); return; }
      const v = await confirmBox("Remove Docker?", "Docker and Docker Compose packages will be removed. All containers will stop.", { ok: "Delete", extra: "<label class=\"check\"><input type=\"checkbox\" name=\"purge\" checked><span>Also remove all Docker images, containers, volumes and settings (/var/lib/docker, /var/lib/containerd, /etc/docker) to free disk space</span></label>" });
      if (v) dockerJob('docker.uninstall', { purge_data: !!v.purge });
    };
  },
};

page('docker', { title: 'Docker', async render(view, params) {
  const tab = dockerTabs.some(t => t[0] === params.tab) ? params.tab : 'overview';
  const st = await api('docker.status');
  if (!view.isConnected) return;
  head(view, 'Docker', { sub: "Containers, images and Docker Compose projects on this server", actions: [
    st.installed && !st.compose && { t: "Install Compose", i: 'download', fn: () => dockerJob('docker.install') },
    { t: "Update", i: 'refresh', fn: route },
  ] });
  if (!st.installed) {
    const p = panel(view);
    p.innerHTML = `<div class="empty"><div class="circle">${icon('docker')}</div><b>Docker is not installed</b><p>Install Docker and Docker Compose to run applications in containers. Panel DB servers (various MySQL and MariaDB versions) also run in Docker.</p><button class="btn primary" data-install>${icon('download')}Install</button></div>`;
    $('[data-install]', p).onclick = () => dockerJob('docker.install');
    if (st.leftover) {
      const note = document.createElement('div'); note.className = 'panel';
      note.innerHTML = `<p>Docker data remained after it was removed: images, containers and volumes — <b>${h(st.leftover_text)}</b> in /var/lib/docker and /var/lib/containerd.</p><button class="btn danger" data-purge>${icon('trash')}Remove remaining Docker data</button>`;
      p.after(note);
      $('[data-purge]', note).onclick = async () => { if (!(await confirmBox("Remove Docker data?", "Docker images, containers, volumes and settings will be deleted permanently. This frees " + h(st.leftover_text) + '.'))) return; dockerJob('docker.uninstall', { purge_data: true }); };
    }
    return;
  }
  const bar = panel(view);
  bar.innerHTML = '<div class="seg">' + dockerTabs.map(([k, t]) => `<button data-tab="${k}" class="${k === tab ? 'on' : ''}">${h(t)}</button>`).join('') + '</div>';
  $$('[data-tab]', bar).forEach(b => b.onclick = () => go('docker', { tab: b.dataset.tab }));
  if (!st.running && tab !== 'settings') {
    const p = panel(view);
    p.innerHTML = `<div class="empty"><b>Docker service is stopped</b><p>Containers are not running.</p><button class="btn primary" data-start>${icon('play')}Start</button></div>`;
    $('[data-start]', p).onclick = () => dockerDo('docker.service', { action: 'start' }, "Docker started");
    return;
  }
  await dockerRender[tab](view, st);
} });
