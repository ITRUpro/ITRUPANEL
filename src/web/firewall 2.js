// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
const firewallActions = [['allow', 'Разрешить'], ['deny', 'Запретить'], ['allow_selected', 'Частично разрешить'], ['deny_selected', 'Частично запретить']];
function firewallForm(rule, state) {
  const partial = v => v.action === 'allow_selected' || v.action === 'deny_selected';
  openForm({ title: rule ? 'Изменить правило' : 'Создать правило', fields: [
    { name: 'name', label: 'Название', value: rule?.name || '', placeholder: 'Доступ к базе данных', required: true },
    { name: 'action', label: 'Действие', type: 'select', value: rule?.action || 'allow', options: firewallActions },
    { type: 'html', html: v => '<p class="hint">' + ({allow: 'Разрешить соединения из указанной сети источника.', deny: 'Запретить соединения из указанной сети источника.', allow_selected: 'Разрешить только доверенные адреса. Остальные адреса в сети источника будут заблокированы.', deny_selected: 'Заблокировать перечисленные адреса. Остальные адреса в сети источника будут разрешены.'})[v.action] + '</p>' },
    { name: 'protocol', label: 'Протокол', type: 'select', value: rule?.protocol || 'tcp', options: [['tcp','TCP'], ['udp','UDP'], ['icmp','ICMP (IPv4)'], ['icmpv6','ICMPv6'], ['any','Все протоколы']] },
    { name: 'port', label: 'Порт или диапазон', value: rule?.port || '', placeholder: '3306 или 8000-8100', required: true, show: v => ['tcp','udp'].includes(v.protocol) },
    { name: 'source', label: 'Адрес источника', value: rule?.source === '*' ? '0.0.0.0' : rule?.source || '0.0.0.0', required: true, hint: 'IP или CIDR, например 192.168.31.0/24. Значение 0.0.0.0 означает любой IPv4 и IPv6. Для всех IPv4: 0.0.0.0/0; для всех IPv6: ::/0.' },
    { name: 'trusted', label: 'Доверенные IP-адреса', type: 'textarea', value: (rule?.addresses || []).join('\n'), show: v => v.action === 'allow_selected', placeholder: '192.168.31.10\n2001:db8::/64', hint: 'По одному IP или сети на строку; допускаются запятые. Адреса должны входить в сеть источника.' },
    { name: 'blocked', label: 'Запрещённые IP-адреса', type: 'textarea', value: (rule?.addresses || []).join('\n'), show: v => v.action === 'deny_selected', placeholder: '192.168.31.20', hint: 'По одному IP или сети на строку; допускаются запятые.' },
    { name: 'enabled', label: 'Правило включено', type: 'checkbox', value: rule ? rule.enabled : true },
    { type: 'html', html: '<p class="hint">Первое совпавшее правило определяет действие. SSH и порт панели защищены от блокировки этим модулем. При включённой фильтрации изменения применяются сразу.</p>' },
  ], submit: async v => {
    const addresses = partial(v) ? (v.action === 'allow_selected' ? v.trusted : v.blocked).split(/[\s,;]+/).filter(Boolean) : [];
    await api('firewall.save', { revision: state.revision, rule: { id: rule?.id || '', name: v.name, action: v.action, protocol: v.protocol, port: ['tcp','udp'].includes(v.protocol) ? v.port : '', source: v.source, addresses, enabled: v.enabled } });
    toast(state.enabled ? 'Правило сохранено и применено' : 'Правило сохранено. Фильтрация пока выключена.', 'ok'); await route();
  } });
}
page('firewall', { title: 'Брандмауэр', async render(view) {
  const d = await api('firewall.list'), s = d.state;
  const change = async (op, body) => { try { await api('firewall.' + op, { ...body, revision: s.revision }); await route(); } catch (e) { fail(e); } };
  head(view, 'Брандмауэр', { sub: 'Правила входящих соединений', actions: [
    s.ready ? { t: 'Создать', i: 'plus', primary: true, fn: () => firewallForm(null, s) } : { t: 'Установить модуль', i: 'shield', primary: true, fn: async () => { try { const r = await api('firewall.install'); watchJob(r.job); } catch (e) { fail(e); } } },
    ...(s.ready ? [{ t: s.enabled ? 'Выключить фильтрацию' : 'Включить фильтрацию', i: s.enabled ? 'stop' : 'play', fn: async () => { if (await confirmBox(s.enabled ? 'Выключить фильтрацию?' : 'Применить правила?', s.enabled ? 'Правила сохранятся, но перестанут ограничивать новые соединения. Настройки других брандмауэров сохранятся.' : 'Активные правила начнут действовать на новые входящие соединения. SSH и порт панели останутся доступны через этот модуль.', { ok: s.enabled ? 'Выключить' : 'Включить', danger: false })) await change('enable', { enabled: !s.enabled }); } }, { t: 'Конфигурация', i: 'file', fn: async () => { try { const r = await api('firewall.preview'); showOutput('Правила при включённой фильтрации', r.script); } catch (e) { fail(e); } } }] : []),
    { t: 'Обновить', i: 'refresh', fn: route },
  ] });
  const info = panel(view);
  info.innerHTML = `<div class="row"><span class="tag ${d.active ? 'ok' : 'warn'}">${!s.ready ? 'Модуль не установлен' : d.active ? 'Фильтрация включена' : 'Фильтрация выключена'}</span><span class="muted">Правил: ${(s.rules || []).length}</span></div>
    <p>Правила проверяются сверху вниз до первого совпадения. Без совпадения соединение разрешено. Учитываются IPv4 и IPv6.</p>
    <p class="hint">Всегда пропускаются TCP-порты управления: ${h((d.protected_ports || []).join(', '))}, локальные и уже установленные соединения, DHCP и служебные сообщения обнаружения соседей IPv6. Исходящие соединения и перенаправляемый трафик (например, Docker) здесь не фильтруются.</p>
    <p class="hint">Другие брандмауэры сохраняют свои правила и могут блокировать даже разрешённое здесь соединение.${d.other_firewalls?.length ? ' Обнаружены: ' + h(d.other_firewalls.join(', ')) + '.' : ''}</p>
    ${d.error ? '<p class="tag err">' + h(d.error) + '</p>' : ''}
    ${s.ready && s.enabled !== d.active && !d.error ? '<p class="tag warn">Сохранённое состояние отличается от работающих правил. Повторно примените нужное состояние переключателем фильтрации.</p>' : ''}`;
  if (!s.ready) { const p = panel(view); p.innerHTML = '<div class="empty"><b>Настройте доступ к сервисам сервера</b><p>Установите модуль, создайте правила и включите фильтрацию. Установка использует nftables и не включает ограничения автоматически.</p></div>'; return; }
  const list = panel(view, 'Правила');
  table(list, { rows: s.rules || [], empty: { icon: 'shield', title: 'Правил пока нет', text: '<p>Нажмите «Создать», чтобы настроить доступ к сервису.</p>' }, columns: [
    { t: '№', r: r => s.rules.indexOf(r) + 1 },
    { t: 'Название', r: r => `<b>${h(r.name)}</b><div class="hint">${r.enabled ? 'Включено' : 'Отключено'}</div>` },
    { t: 'Действие', r: r => h(firewallActions.find(a => a[0] === r.action)?.[1] || r.action) },
    { t: 'Протокол / порт', r: r => h(r.protocol === 'any' ? 'Все' : r.protocol.toUpperCase()) + (r.port ? ' / ' + h(r.port) : '') },
    { t: 'Источник', r: r => h(r.source === '*' ? 'Любой IPv4 / IPv6' : r.source) + (r.addresses?.length ? '<div class="hint">' + h(r.addresses.join(', ')) + '</div>' : '') },
  ], onRow: r => firewallForm(r, s), actions: r => [
    { t: 'Изменить', i: 'edit', fn: () => firewallForm(r, s) },
    { t: r.enabled ? 'Отключить правило' : 'Включить правило', i: r.enabled ? 'stop' : 'play', fn: () => change('save', { rule: { ...r, enabled: !r.enabled } }) },
    s.rules.indexOf(r) > 0 && { t: 'Выше', i: 'chevron', fn: () => change('move', { id: r.id, direction: -1 }) },
    s.rules.indexOf(r) < s.rules.length - 1 && { t: 'Ниже', i: 'chevdown', fn: () => change('move', { id: r.id, direction: 1 }) },
    { t: 'Удалить', i: 'trash', danger: true, fn: async () => { if (await confirmBox('Удалить правило?', h(r.name))) await change('delete', { id: r.id }); } },
  ] });
} });
