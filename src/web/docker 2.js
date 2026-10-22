// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
// Раздел «Docker» (только администратор).
const dockerTabs = [['overview', 'Обзор'], ['containers', 'Контейнеры'], ['compose', 'Compose'], ['images', 'Образы'], ['networks', 'Сети'], ['volumes', 'Тома'], ['settings', 'Настройки']];
const dockerLines = s => String(s || '').split(/\r?\n/).map(x => x.trim()).filter(Boolean);
async function dockerJob(op, body) { try { const r = await api(op, body); watchJob(r.job); } catch (e) { fail(e); } }
async function dockerDo(op, body, ok) { try { const r = await api(op, body); if (ok || r?.info) toast(r?.info || ok, 'ok'); await route(); } catch (e) { fail(e); } }
function dockerStateTag(c) {
  const t = { running: ['ok', 'работает'], paused: ['warn', 'приостановлен'], restarting: ['acc', 'перезапускается'], created: ['warn', 'создан'], exited: ['err', 'остановлен'], dead: ['err', 'ошибка'] }[c.state] || ['warn', c.state];
  return `<span class="tag ${t[0]}">${h(t[1])}</span>`;
}

function dockerContainerForm(networks, image = '') {
  openForm({ title: 'Создать контейнер', ok: 'Создать и запустить', wide: true, fields: [
    { name: 'image', label: 'Образ', value: image, placeholder: 'nginx:latest', required: true, hint: 'Образ из Docker Hub или частного реестра. Если его нет на сервере, он будет загружен.' },
    { name: 'name', label: 'Имя контейнера', placeholder: 'my-app', hint: 'Необязательно. Латинские буквы, цифры, «_», «.» и «-».' },
    { name: 'ports', label: 'Порты (сервер:контейнер)', type: 'textarea', rows: 3, placeholder: '127.0.0.1:8080:80\n53:53/udp', hint: 'По одному на строку. Опубликованные порты Docker открывает в обход брандмауэра панели. Чтобы открыть приложение только через сайт, используйте 127.0.0.1:порт и режим сайта «Прокси».' },
    { name: 'volumes', label: 'Тома и папки', type: 'textarea', rows: 3, placeholder: '/var/lib/my-app:/data\nmy-volume:/var/lib/data:ro', hint: 'По одному на строку: /путь/на/сервере:/путь/в/контейнере или имя_тома:/путь. Добавьте :ro для доступа только на чтение.' },
    { name: 'env', label: 'Переменные окружения', type: 'textarea', rows: 3, placeholder: 'TZ=Europe/Moscow', hint: 'По одной на строку: ИМЯ=значение.' },
    { name: 'restart', label: 'Перезапуск', type: 'select', value: 'unless-stopped', options: [['unless-stopped', 'Всегда, если не остановлен вручную'], ['always', 'Всегда'], ['on-failure', 'При ошибке'], ['no', 'Не перезапускать']] },
    { name: 'network', label: 'Сеть', type: 'select', value: 'bridge', options: (networks || []).filter(n => n.name !== 'none').map(n => [n.name, n.name + (n.driver ? ' (' + n.driver + ')' : '')]) },
    { name: 'command', label: 'Команда', placeholder: 'Пусто — команда образа по умолчанию' },
    { name: 'memory', label: 'Лимит памяти, МБ', placeholder: '0 — без ограничения' },
    { name: 'cpus', label: 'Лимит процессора, ядер', placeholder: '0 — без ограничения, например 0.5' },
  ], submit: async v => {
    const r = await api('docker.container.create', { image: v.image.trim(), name: v.name.trim(), ports: dockerLines(v.ports), volumes: dockerLines(v.volumes), env: String(v.env || '').split(/\r?\n/).filter(x => x.trim()), restart: v.restart, network: v.network, command: v.command, memory: Number(v.memory) || 0, cpus: Number(String(v.cpus).replace(',', '.')) || 0 });
    watchJob(r.job);
  } });
}

async function dockerTerminal(c) {
  if (!$('link[href="/vendor/xterm.css"]')) { const l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/vendor/xterm.css'; document.head.append(l); }
  try { await loadScript('/vendor/xterm.js'); await loadScript('/vendor/addon-fit.js'); } catch (e) { fail(e); return; }
  const m = modal(`<h3>Терминал: ${h(c.name)}</h3><div class="term-box" style="height:60vh"></div><div class="btns"><button class="btn primary" data-close>Закрыть</button></div>`, { wide: true });
  const term = new Terminal({ fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 14, cursorBlink: true, theme: { background: '#0f1424' } });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit); term.open($('.term-box', m.el)); fit.fit();
  const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/docker-shell?id=' + encodeURIComponent(c.id));
  ws.binaryType = 'arraybuffer';
  const sendSize = () => { if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows })); };
  ws.onopen = () => { sendSize(); term.focus(); };
  ws.onmessage = e => term.write(typeof e.data === 'string' ? e.data : new Uint8Array(e.data));
  ws.onclose = () => term.write('\r\n\x1b[33m[сессия закрыта]\x1b[0m\r\n');
  term.onData(d => ws.readyState === 1 && ws.send(new TextEncoder().encode(d)));
  const onResize = () => { fit.fit(); sendSize(); };
  window.addEventListener('resize', onResize);
  const watch = setInterval(() => { if (!m.el.isConnected) { clearInterval(watch); window.removeEventListener('resize', onResize); ws.close(); term.dispose(); } }, 500);
}

async function dockerLogs(c) {
  try { const r = await api('docker.container.logs', { id: c.id, tail: 500 }); showOutput('Журнал: ' + c.name, r.log); } catch (e) { fail(e); }
}

const dockerRender = {
  async overview(view, st) {
    const p = panel(view, 'Сводка');
    const df = st.df || [];
    p.innerHTML = `<div class="stats">
      <div class="stat"><div class="k">Служба</div><div class="v">${st.running ? '<span class="tag ok">работает</span>' : '<span class="tag err">остановлена</span>'}</div></div>
      <div class="stat"><div class="k">Контейнеры</div><div class="v">${h(st.containers_running ?? 0)} <small>из ${h(st.containers ?? 0)} работают</small></div></div>
      <div class="stat"><div class="k">Образы</div><div class="v">${h(st.images ?? 0)}</div></div>
    </div>
    <div class="kv" style="margin-top:20px">
      <div>Версия Docker</div><div>${h(st.version || st.client || '—')}</div>
      <div>Docker Compose</div><div>${st.compose ? h(st.compose) : 'не установлен'}</div>
      <div>Драйвер хранилища</div><div>${h(st.storage_driver || '—')}</div>
      <div>Каталог данных</div><div class="mono">${h(st.root_dir || '—')}</div>
      <div>Серверы БД панели в Docker</div><div>${h(st.db_servers)}</div>
    </div>`;
    if (!df.length) return;
    const u = panel(view, 'Использование диска');
    const names = { Images: 'Образы', Containers: 'Контейнеры', 'Local Volumes': 'Тома', 'Build Cache': 'Кэш сборки' };
    u.innerHTML = '<div class="toolbar"><span class="grow"></span><button class="btn" data-prune>' + icon('trash') + 'Очистить неиспользуемое</button></div><div data-t></div>';
    table($('[data-t]', u), { rows: df, columns: [
      { t: 'Тип', r: r => h(names[r.Type] || r.Type) },
      { t: 'Всего', r: r => h(r.TotalCount) },
      { t: 'Используется', r: r => h(r.Active) },
      { t: 'Размер', r: r => h(r.Size) },
      { t: 'Можно освободить', r: r => h(r.Reclaimable) },
    ] });
    $('[data-prune]', u).onclick = async () => { if (await confirmBox('Очистить неиспользуемое?', 'Будут удалены остановленные контейнеры, неиспользуемые сети, образы без тегов и кэш сборки. Тома и образы с тегами останутся.', { ok: 'Очистить' })) dockerJob('docker.prune', { what: 'system' }); };
  },

  async containers(view) {
    const p = panel(view);
    const d = await api('docker.containers');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + 'Создать контейнер</button><span class="grow"></span><span class="muted">Работают: ' + d.containers.filter(c => c.state === 'running').length + ' из ' + d.containers.length + '</span></div><div data-t></div>';
    $('[data-new]', p).onclick = () => dockerContainerForm(d.networks);
    const act = (c, action) => dockerDo('docker.container.action', { id: c.id, action });
    table($('[data-t]', p), { rows: d.containers, empty: { icon: 'docker', title: 'Контейнеров пока нет', text: '<p>Нажмите «Создать контейнер» или запустите проект на вкладке «Compose».</p>' }, columns: [
      { t: 'Контейнер', r: c => `<b>${h(c.name)}</b>${c.panel ? ' <span class="tag acc">сервер БД панели</span>' : ''}${c.project ? ' <span class="tag">Compose: ' + h(c.project) + '</span>' : ''}<div class="hint mono">${h(c.image)} · ${h(c.id)}</div>` },
      { t: 'Состояние', r: c => dockerStateTag(c) + `<div class="hint">${h(c.status)}</div>` },
      { t: 'Порты', r: c => c.ports ? c.ports.split(', ').map(p => `<div class="mono" style="font-size:12px;white-space:nowrap">${h(p.replace('->', ' → '))}</div>`).join('') : '—', cls: 'hide-sm' },
      { t: 'CPU / память', r: c => c.cpu ? `${h(c.cpu)}<div class="hint">${h(c.mem)}</div>` : '—', cls: 'hide-sm' },
    ], onRow: c => dockerLogs(c), actions: c => [
      c.state === 'running' ? (!c.panel && { t: 'Остановить', i: 'stop', fn: () => act(c, 'stop') }) : c.state === 'paused' ? { t: 'Продолжить', i: 'play', fn: () => act(c, 'unpause') } : { t: 'Запустить', i: 'play', fn: () => act(c, 'start') },
      { t: 'Перезапустить', i: 'refresh', fn: () => act(c, 'restart') },
      c.state === 'running' && !c.panel && { t: 'Приостановить', i: 'pause', fn: () => act(c, 'pause') },
      c.state === 'running' && { t: 'Терминал', i: 'terminal', fn: () => dockerTerminal(c) },
      { t: 'Журнал', i: 'file', fn: () => dockerLogs(c) },
      { t: 'Подробности', i: 'info', fn: async () => { try { const r = await api('docker.container.inspect', { id: c.id }); showOutput(c.name, r.text); } catch (e) { fail(e); } } },
      !c.panel && { t: 'Удалить', i: 'trash', danger: true, fn: async () => { const v = await confirmBox('Удалить контейнер?', `<b>${h(c.name)}</b> будет остановлен и удалён вместе со своими файлами. Папки сервера, подключённые в контейнер, не удаляются.`, { extra: '<label class="check"><input type="checkbox" name="purge" checked><span>Удалить также образ и тома контейнера, если их не используют другие контейнеры (данные в томах будут потеряны)</span></label>' }); if (v) dockerDo('docker.container.action', { id: c.id, action: 'remove', purge: !!v.purge }); } },
    ] });
  },

  async compose(view, st) {
    const p = panel(view);
    const d = await api('docker.compose.list');
    const templates = st.templates || {};
    const tplNames = { nginx: 'nginx (статический сайт)', redis: 'Redis', wordpress: 'WordPress + MariaDB', 'uptime-kuma': 'Uptime Kuma (мониторинг)', portainer: 'Portainer (управление Docker)' };
    const form = (proj, yaml = '', env = '') => openForm({ title: proj ? 'Проект ' + proj : 'Создать проект Compose', ok: proj ? 'Сохранить' : 'Создать', wide: true, fields: [
      !proj && { name: 'name', label: 'Имя проекта', placeholder: 'my-project', required: true, hint: 'Строчные латинские буквы, цифры, «_» и «-». Папка проекта: ' + h(d.dir) + '/имя' },
      !proj && { name: 'template', label: 'Шаблон', type: 'select', value: '', options: [['', 'Пустой'], ...Object.keys(templates).map(k => [k, tplNames[k] || k])] },
      { name: 'yaml', label: 'docker-compose.yml', type: 'textarea', rows: 18, value: yaml, required: true, placeholder: 'services:\n  app:\n    image: nginx:stable-alpine\n    ports:\n      - "127.0.0.1:8080:80"' },
      { name: 'env', label: 'Файл .env', type: 'textarea', rows: 4, value: env, placeholder: 'DB_PASSWORD=…', hint: 'Переменные для подстановки ${ИМЯ} в docker-compose.yml.' },
      { name: 'up', label: proj ? 'Применить изменения (docker compose up -d)' : 'Запустить после создания', type: 'checkbox', value: true },
      { type: 'html', html: '<p class="hint">Шаблоны публикуют порты только на 127.0.0.1. Чтобы открыть приложение по домену, создайте сайт в режиме «Прокси» на этот порт.</p>' },
    ].filter(Boolean), onChange: (v, n) => {
      if (n === 'template') { v.yaml = templates[v.template] || ''; v.env = v.template === 'wordpress' ? 'DB_PASSWORD=' + genPass(20) + '\n' : ''; }
    }, submit: async v => {
      const r = await api('docker.compose.save', { name: proj || v.name.trim(), yaml: v.yaml, env: v.env, new: !proj, up: !!v.up });
      if (r && r.job) watchJob(r.job); else { toast('Сохранено', 'ok'); await route(); }
    } });
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new ' + (d.compose ? '' : 'disabled') + '>' + icon('plus') + 'Создать проект</button><span class="grow"></span><span class="muted mono">' + h(d.dir) + '</span></div>' + (d.compose ? '' : '<p class="tag warn">Docker Compose не установлен. Нажмите «Установить Compose» вверху страницы.</p>') + '<div data-t></div>';
    $('[data-new]', p).onclick = () => form('');
    const act = (r, action) => dockerJob('docker.compose.action', { name: r.name, action });
    table($('[data-t]', p), { rows: d.projects, empty: { icon: 'docker', title: 'Проектов пока нет', text: '<p>Создайте проект из шаблона или вставьте свой docker-compose.yml.</p>' }, columns: [
      { t: 'Проект', r: r => `<b>${h(r.name)}</b><div class="hint mono">${h(r.file)}</div>` },
      { t: 'Контейнеры', r: r => r.containers ? `<span class="tag ${r.running === r.containers ? 'ok' : r.running ? 'warn' : 'err'}">${r.running} из ${r.containers} работают</span>` : '<span class="tag">не запущен</span>' },
      { t: 'Изменён', r: r => r.modified && !r.modified.startsWith('0001') ? fmtDate(r.modified) : '—', cls: 'hide-sm' },
    ], onRow: async r => { try { const c = await api('docker.compose.get', { name: r.name }); form(r.name, c.yaml, c.env); } catch (e) { fail(e); } }, actions: r => [
      { t: 'Запустить / применить', i: 'play', fn: () => act(r, 'up') },
      r.running > 0 && { t: 'Остановить', i: 'stop', fn: () => act(r, 'stop') },
      r.containers > 0 && { t: 'Перезапустить', i: 'refresh', fn: () => act(r, 'restart') },
      { t: 'Обновить образы', i: 'download', fn: async () => { if (await confirmBox('Обновить образы?', 'Будут загружены новые версии образов проекта. Чтобы контейнеры перешли на них, затем нажмите «Запустить / применить».', { ok: 'Загрузить', danger: false })) act(r, 'pull'); } },
      { t: 'Изменить', i: 'edit', fn: async () => { try { const c = await api('docker.compose.get', { name: r.name }); form(r.name, c.yaml, c.env); } catch (e) { fail(e); } } },
      { t: 'Журнал', i: 'file', fn: async () => { try { const l = await api('docker.compose.logs', { name: r.name }); showOutput('Журнал: ' + r.name, l.log); } catch (e) { fail(e); } } },
      { t: 'Удалить', i: 'trash', danger: true, fn: async () => {
        const v = await confirmBox('Удалить проект?', `Контейнеры проекта <b>${h(r.name)}</b> будут остановлены и удалены вместе с папкой ${h(r.path)}.`, { extra: '<label class="check"><input type="checkbox" name="volumes" checked><span>Удалить тома проекта (данные будут потеряны)</span></label><label class="check"><input type="checkbox" name="images" checked><span>Удалить образы проекта, если их не используют другие контейнеры</span></label>' });
        if (v) dockerJob('docker.compose.delete', { name: r.name, volumes: !!v.volumes, images: !!v.images });
      } },
    ] });
  },

  async images(view, st, networks) {
    const p = panel(view);
    const rows = await api('docker.images');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-pull>' + icon('download') + 'Загрузить образ</button><span class="grow"></span><button class="btn" data-prune>' + icon('trash') + 'Удалить неиспользуемые</button></div><div data-t></div>';
    $('[data-pull]', p).onclick = () => openForm({ title: 'Загрузить образ', ok: 'Загрузить', fields: [
      { name: 'image', label: 'Образ', placeholder: 'nginx:latest', required: true, hint: 'Имя образа Docker Hub (nginx:latest, redis:7-alpine) или полный адрес реестра (ghcr.io/org/app:1.0).' },
    ], submit: async v => { const r = await api('docker.image.pull', { image: v.image.trim() }); watchJob(r.job); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox('Удалить неиспользуемые образы?', 'Будут удалены все образы, на основе которых не создан ни один контейнер.', { ok: 'Удалить' })) dockerJob('docker.prune', { what: 'images' }); };
    const ref = r => r.repository && r.repository !== '<none>' ? r.repository + ':' + r.tag : r.id;
    table($('[data-t]', p), { rows, empty: { icon: 'docker', title: 'Образов пока нет', text: '<p>Образы появятся после загрузки или создания контейнера.</p>' }, columns: [
      { t: 'Образ', r: r => `<b>${h(r.repository)}</b>${r.tag && r.tag !== '<none>' ? ':' + h(r.tag) : ''}<div class="hint mono">${h(r.id.slice(0, 12))}</div>` },
      { t: 'Размер', r: r => h(r.size) },
      { t: 'Создан', r: r => h(r.since || r.created), cls: 'hide-sm' },
    ], actions: r => [
      r.repository !== '<none>' && { t: 'Создать контейнер', i: 'plus', fn: async () => { let n = networks; if (!n) { try { n = await api('docker.networks'); } catch (e) { n = []; } } dockerContainerForm(n, ref(r)); } },
      { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (await confirmBox('Удалить образ?', h(ref(r)))) dockerDo('docker.image.remove', { id: ref(r) }, 'Образ удалён'); } },
    ] });
  },

  async networks(view) {
    const p = panel(view);
    const rows = await api('docker.networks');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + 'Создать сеть</button><span class="grow"></span><button class="btn" data-prune>' + icon('trash') + 'Удалить неиспользуемые</button></div><div data-t></div>';
    $('[data-new]', p).onclick = () => openForm({ title: 'Создать сеть', ok: 'Создать', fields: [
      { name: 'name', label: 'Имя сети', placeholder: 'app-net', required: true },
      { name: 'driver', label: 'Драйвер', type: 'select', value: 'bridge', options: [['bridge', 'bridge — сеть на этом сервере'], ['macvlan', 'macvlan — адреса из локальной сети'], ['ipvlan', 'ipvlan']] },
      { name: 'subnet', label: 'Подсеть', placeholder: 'Автоматически, например 172.30.0.0/24' },
      { name: 'gateway', label: 'Шлюз', placeholder: 'Автоматически, например 172.30.0.1' },
      { name: 'internal', label: 'Внутренняя сеть (без выхода в интернет)', type: 'checkbox' },
    ], submit: async v => { await api('docker.network.create', { name: v.name.trim(), driver: v.driver, subnet: v.subnet, gateway: v.gateway, internal: !!v.internal }); toast('Сеть создана', 'ok'); await route(); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox('Удалить неиспользуемые сети?', 'Будут удалены созданные сети, к которым не подключён ни один контейнер.', { ok: 'Удалить' })) dockerJob('docker.prune', { what: 'networks' }); };
    table($('[data-t]', p), { rows, columns: [
      { t: 'Сеть', r: r => `<b>${h(r.name)}</b>${r.builtin ? ' <span class="tag">встроенная</span>' : ''}${r.internal ? ' <span class="tag warn">внутренняя</span>' : ''}<div class="hint mono">${h(r.id)}</div>` },
      { t: 'Драйвер', r: r => h(r.driver) },
      { t: 'Подсеть / шлюз', r: r => `<span class="mono">${h(r.subnet || '—')}</span><div class="hint mono">${h(r.gateway)}</div>` },
      { t: 'Контейнеров', r: r => h(r.containers) },
    ], actions: r => [
      !r.builtin && { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (await confirmBox('Удалить сеть?', h(r.name))) dockerDo('docker.network.remove', { id: r.name }, 'Сеть удалена'); } },
    ] });
  },

  async volumes(view) {
    const p = panel(view);
    const rows = await api('docker.volumes');
    p.innerHTML = '<div class="toolbar"><button class="btn primary" data-new>' + icon('plus') + 'Создать том</button><span class="grow"></span><button class="btn" data-prune>' + icon('trash') + 'Удалить неиспользуемые</button></div><div data-t></div>';
    $('[data-new]', p).onclick = () => openForm({ title: 'Создать том', ok: 'Создать', fields: [
      { name: 'name', label: 'Имя тома', placeholder: 'app-data', required: true },
    ], submit: async v => { await api('docker.volume.create', { id: v.name.trim() }); toast('Том создан', 'ok'); await route(); } });
    $('[data-prune]', p).onclick = async () => { if (await confirmBox('Удалить неиспользуемые тома?', 'Будут удалены анонимные тома, не подключённые ни к одному контейнеру. Данные в них будут потеряны.', { ok: 'Удалить' })) dockerJob('docker.prune', { what: 'volumes' }); };
    table($('[data-t]', p), { rows, empty: { icon: 'docker', title: 'Томов пока нет', text: '<p>Тома хранят данные контейнеров отдельно от них.</p>' }, columns: [
      { t: 'Том', r: r => `<b>${h(r.name)}</b><div class="hint mono">${h(r.mountpoint)}</div>` },
      { t: 'Драйвер', r: r => h(r.driver), cls: 'hide-sm' },
      { t: 'Используется', r: r => r.used_by && r.used_by.length ? h(r.used_by.join(', ')) : '<span class="muted">нет</span>' },
    ], actions: r => [
      { t: 'Открыть в файловом менеджере', i: 'folder', fn: () => go('files', { path: r.mountpoint }) },
      { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (await confirmBox('Удалить том?', `Данные тома <b>${h(r.name)}</b> будут удалены без возможности восстановления.`)) dockerDo('docker.volume.remove', { id: r.name }, 'Том удалён'); } },
    ] });
  },

  async settings(view, st) {
    const s = st.settings || {};
    const p = panel(view);
    const dbWarn = st.db_servers ? ` Контейнеры серверов БД панели (${h(st.db_servers)}) при этом тоже перезапускаются.` : '';
    p.innerHTML = `${st.config_error ? '<p class="tag err">' + h(st.config_error) + '</p>' : ''}
      <div class="field"><label>Служба Docker</label><div class="toolbar">${st.running ? '<span class="tag ok">работает</span>' : '<span class="tag err">остановлена</span>'}
        <button class="btn" data-svc="start">${icon('play')}Запустить</button><button class="btn" data-svc="restart">${icon('refresh')}Перезапустить</button><button class="btn" data-svc="stop">${icon('stop')}Остановить</button></div></div>
      <div class="field"><label for="dk-mirrors">URL для ускорения (зеркала реестра)</label><textarea id="dk-mirrors" rows="2" style="min-height:0" placeholder="https://mirror.gcr.io">${h((s.mirrors || []).join('\n'))}</textarea><div class="hint">По одному адресу на строку. Используются вместо Docker Hub при загрузке образов.</div></div>
      <div class="field"><label for="dk-insecure">Частный реестр без HTTPS</label><textarea id="dk-insecure" rows="2" style="min-height:0" placeholder="registry.local:5000">${h((s.insecure || []).join('\n'))}</textarea><div class="hint">Адреса реестров, к которым Docker подключается без проверки сертификата.</div></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-logrotate" ${s.log_rotate ? 'checked' : ''}><span>Ротация журналов контейнеров<small>Без ротации журналы растут без ограничений и могут заполнить диск.</small></span></label>
        <div class="cols2"><div class="field"><label for="dk-logsize">Размер файла</label><input type="text" id="dk-logsize" value="${h(s.log_max_size || '10m')}" placeholder="10m"></div><div class="field"><label for="dk-logfiles">Хранить файлов</label><input type="number" id="dk-logfiles" min="1" max="100" value="${h(s.log_max_file || 3)}"></div></div>
        <div class="hint">Действует для новых контейнеров. Существующие нужно пересоздать.</div></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-ipv6" ${s.ipv6 ? 'checked' : ''}><span>Сеть IPv6<small>Включает IPv6 в сети bridge по умолчанию.</small></span></label>
        <input type="text" id="dk-ipv6cidr" value="${h(s.ipv6_cidr || '')}" placeholder="fd00:d0c:1::/64"></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-iptables" ${s.iptables ? 'checked' : ''}><span>iptables<small>Docker сам настраивает правила для публикации портов и выхода контейнеров в интернет. Без этого контейнеры останутся без сети.</small></span></label></div>
      <div class="field"><label class="check"><input type="checkbox" id="dk-live" ${s.live_restore ? 'checked' : ''}><span>live-restore<small>Контейнеры продолжают работать при перезапуске или сбое службы Docker.</small></span></label></div>
      <div class="field"><label for="dk-dir">Директория проектов Compose</label><input type="text" id="dk-dir" value="${h(s.project_dir || '')}"><div class="hint">Существующие проекты при смене директории не переносятся.</div></div>
      <div class="toolbar"><button class="btn primary" data-save>Сохранить</button><button class="btn" data-config>${icon('file')}Конфигурационный файл</button><span class="grow"></span><button class="btn danger" data-remove>${icon('trash')}Удалить Docker</button></div>
      <div class="field"><div class="hint">При изменении параметров службы Docker перезапускается, и контейнеры без live-restore ненадолго останавливаются.${dbWarn}</div></div>`;
    $$('[data-svc]', p).forEach(b => b.onclick = async () => {
      const a = b.dataset.svc;
      if (a !== 'start' && !await confirmBox(a === 'stop' ? 'Остановить Docker?' : 'Перезапустить Docker?', (a === 'stop' ? 'Все контейнеры будут остановлены.' : 'Контейнеры без live-restore ненадолго остановятся.') + dbWarn, { ok: a === 'stop' ? 'Остановить' : 'Перезапустить' })) return;
      b.disabled = true; await dockerDo('docker.service', { action: a }, a === 'stop' ? 'Docker остановлен' : 'Docker запущен');
    });
    $('[data-save]', p).onclick = async e => {
      const val = id => $('#' + id, p);
      e.target.disabled = true;
      try {
        const r = await api('docker.settings.save', { mirrors: dockerLines(val('dk-mirrors').value), insecure: dockerLines(val('dk-insecure').value), log_rotate: val('dk-logrotate').checked, log_max_size: val('dk-logsize').value.trim(), log_max_file: Number(val('dk-logfiles').value) || 0, ipv6: val('dk-ipv6').checked, ipv6_cidr: val('dk-ipv6cidr').value.trim(), iptables: val('dk-iptables').checked, live_restore: val('dk-live').checked, project_dir: val('dk-dir').value.trim() });
        toast(r.restarted ? 'Настройки сохранены, Docker перезапущен' : 'Настройки сохранены', 'ok'); await route();
      } catch (err) { fail(err); e.target.disabled = false; }
    };
    $('[data-config]', p).onclick = async () => {
      try {
        const c = await api('docker.config.get');
        openForm({ title: c.path, ok: 'Сохранить и перезапустить', wide: true, fields: [
          { name: 'text', label: 'daemon.json', type: 'textarea', rows: 22, value: c.text || '{\n}\n' },
          { type: 'html', html: '<p class="hint">Файл проверяется как JSON. Если Docker не запустится с новыми настройками, прежний файл будет восстановлен.</p>' },
        ], submit: async v => { await api('docker.config.save', { text: v.text }); toast('Сохранено, Docker перезапущен', 'ok'); await route(); } });
      } catch (e) { fail(e); }
    };
    $('[data-remove]', p).onclick = async () => {
      if (st.db_servers) { toast('Docker используется серверами БД панели. Сначала удалите их в разделе «Серверы БД».', 'err'); return; }
      const v = await confirmBox('Удалить Docker?', 'Будут удалены пакеты Docker и Docker Compose. Все контейнеры остановятся.', { ok: 'Удалить', extra: '<label class="check"><input type="checkbox" name="purge" checked><span>Удалить также все образы, контейнеры, тома и настройки Docker (/var/lib/docker, /var/lib/containerd, /etc/docker) — освободит место на диске</span></label>' });
      if (v) dockerJob('docker.uninstall', { purge_data: !!v.purge });
    };
  },
};

page('docker', { title: 'Docker', async render(view, params) {
  const tab = dockerTabs.some(t => t[0] === params.tab) ? params.tab : 'overview';
  const st = await api('docker.status');
  if (!view.isConnected) return;
  head(view, 'Docker', { sub: 'Контейнеры, образы и проекты Docker Compose на этом сервере', actions: [
    st.installed && !st.compose && { t: 'Установить Compose', i: 'download', fn: () => dockerJob('docker.install') },
    { t: 'Обновить', i: 'refresh', fn: route },
  ] });
  if (!st.installed) {
    const p = panel(view);
    p.innerHTML = `<div class="empty"><div class="circle">${icon('docker')}</div><b>Docker не установлен</b><p>Установите Docker и Docker Compose, чтобы запускать приложения в контейнерах. Серверы БД панели (MySQL и MariaDB разных версий) тоже работают в Docker.</p><button class="btn primary" data-install>${icon('download')}Установить</button></div>`;
    $('[data-install]', p).onclick = () => dockerJob('docker.install');
    if (st.leftover) {
      const note = document.createElement('div'); note.className = 'panel';
      note.innerHTML = `<p>После удаления Docker остались его данные: образы, контейнеры и тома — <b>${h(st.leftover_text)}</b> в /var/lib/docker и /var/lib/containerd.</p><button class="btn danger" data-purge>${icon('trash')}Удалить оставшиеся данные Docker</button>`;
      p.after(note);
      $('[data-purge]', note).onclick = async () => { if (!(await confirmBox('Удалить данные Docker?', 'Образы, контейнеры, тома и настройки Docker будут удалены навсегда. Освободится ' + h(st.leftover_text) + '.'))) return; dockerJob('docker.uninstall', { purge_data: true }); };
    }
    return;
  }
  const bar = panel(view);
  bar.innerHTML = '<div class="seg">' + dockerTabs.map(([k, t]) => `<button data-tab="${k}" class="${k === tab ? 'on' : ''}">${h(t)}</button>`).join('') + '</div>';
  $$('[data-tab]', bar).forEach(b => b.onclick = () => go('docker', { tab: b.dataset.tab }));
  if (!st.running && tab !== 'settings') {
    const p = panel(view);
    p.innerHTML = `<div class="empty"><b>Служба Docker остановлена</b><p>Контейнеры не работают.</p><button class="btn primary" data-start>${icon('play')}Запустить</button></div>`;
    $('[data-start]', p).onclick = () => dockerDo('docker.service', { action: 'start' }, 'Docker запущен');
    return;
  }
  await dockerRender[tab](view, st);
} });
