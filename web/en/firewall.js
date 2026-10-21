// Copyright (c) 2026 Oleg Sergeevich Dmitriev (Дмитриев Олег Сергеевич). ITRUPANEL. Licensed under GPL-3.0-only (SPDX-License-Identifier: GPL-3.0-only). See LICENSE.txt
'use strict';
const firewallActions = [['allow', "Allow"], ['deny', "Deny"], ['allow_selected', "Allow selected IPs"], ['deny_selected', "Deny selected IPs"]];
function firewallForm(rule, state) {
  const partial = v => v.action === 'allow_selected' || v.action === 'deny_selected';
  openForm({ title: rule ? "Edit rule" : "Create rule", fields: [
    { name: 'name', label: "Name", value: rule?.name || '', placeholder: "Database access", required: true },
    { name: 'action', label: "Action", type: 'select', value: rule?.action || 'allow', options: firewallActions },
    { type: 'html', html: v => '<p class="hint">' + ({allow: "Allow connections from the specified source network.", deny: "Block connections from the specified source network.", allow_selected: "Allow only trusted addresses. Other addresses in the source network are blocked.", deny_selected: "Block the listed addresses. Other addresses in the source network are allowed."})[v.action] + '</p>' },
    { name: 'protocol', label: "Protocol", type: 'select', value: rule?.protocol || 'tcp', options: [['tcp','TCP'], ['udp','UDP'], ['icmp','ICMP (IPv4)'], ['icmpv6','ICMPv6'], ['any',"All protocols"]] },
    { name: 'port', label: "Port or range", value: rule?.port || '', placeholder: "3306 or 8000-8100", required: true, show: v => ['tcp','udp'].includes(v.protocol) },
    { name: 'source', label: "Source address", value: rule?.source === '*' ? '0.0.0.0' : rule?.source || '0.0.0.0', required: true, hint: "IP or CIDR, e.g. 192.168.31.0/24. 0.0.0.0 means any IPv4 and IPv6 address. All IPv4: 0.0.0.0/0; all IPv6: ::/0." },
    { name: 'trusted', label: "Trusted IP addresses", type: 'textarea', value: (rule?.addresses || []).join('\n'), show: v => v.action === 'allow_selected', placeholder: '192.168.31.10\n2001:db8::/64', hint: "One IP or network per line; commas are allowed. Addresses must belong to the source network." },
    { name: 'blocked', label: "Blocked IP addresses", type: 'textarea', value: (rule?.addresses || []).join('\n'), show: v => v.action === 'deny_selected', placeholder: '192.168.31.20', hint: "One IP or network per line; commas are allowed." },
    { name: 'enabled', label: "Rule enabled", type: 'checkbox', value: rule ? rule.enabled : true },
    { type: 'html', html: "<p class=\"hint\">The first matching rule applies. This module protects SSH and the panel port from being blocked. Changes apply immediately when filtering is enabled.</p>" },
  ], submit: async v => {
    const addresses = partial(v) ? (v.action === 'allow_selected' ? v.trusted : v.blocked).split(/[\s,;]+/).filter(Boolean) : [];
    await api('firewall.save', { revision: state.revision, rule: { id: rule?.id || '', name: v.name, action: v.action, protocol: v.protocol, port: ['tcp','udp'].includes(v.protocol) ? v.port : '', source: v.source, addresses, enabled: v.enabled } });
    toast(state.enabled ? "Rule saved and applied" : "Rule saved. Filtering is currently disabled.", 'ok'); await route();
  } });
}
page('firewall', { title: "Firewall", async render(view) {
  const d = await api('firewall.list'), s = d.state;
  const change = async (op, body) => { try { await api('firewall.' + op, { ...body, revision: s.revision }); await route(); } catch (e) { fail(e); } };
  head(view, "Firewall", { sub: "Incoming connection rules", actions: [
    s.ready ? { t: "Create", i: 'plus', primary: true, fn: () => firewallForm(null, s) } : { t: "Install module", i: 'shield', primary: true, fn: async () => { try { const r = await api('firewall.install'); watchJob(r.job); } catch (e) { fail(e); } } },
    ...(s.ready ? [{ t: s.enabled ? "Disable filtering" : "Enable filtering", i: s.enabled ? 'stop' : 'play', fn: async () => { if (await confirmBox(s.enabled ? "Disable filtering?" : "Apply rules?", s.enabled ? "Rules are retained but stop restricting new connections. Other firewall settings are preserved." : "Active rules apply to new incoming connections. This module keeps SSH and the panel port accessible.", { ok: s.enabled ? "Disable" : "Enable", danger: false })) await change('enable', { enabled: !s.enabled }); } }, { t: "Configuration", i: 'file', fn: async () => { try { const r = await api('firewall.preview'); showOutput("Rules when filtering is enabled", r.script); } catch (e) { fail(e); } } }] : []),
    { t: "Update", i: 'refresh', fn: route },
  ] });
  const info = panel(view);
  info.innerHTML = `<div class="row"><span class="tag ${d.active ? 'ok' : 'warn'}">${!s.ready ? "Module is not installed" : d.active ? "Filtering enabled" : "Filtering disabled"}</span><span class="muted">Rules: ${(s.rules || []).length}</span></div>
    <p>Rules are checked top to bottom until the first match. Unmatched connections are allowed. Both IPv4 and IPv6 are supported.</p>
    <p class="hint">Management TCP ports are always allowed: ${h((d.protected_ports || []).join(', '))}, local and established connections, DHCP and IPv6 neighbour discovery. Outbound connections and forwarded traffic (such as Docker) are not filtered here.</p>
    <p class="hint">Other firewalls retain their rules and may block connections allowed here.${d.other_firewalls?.length ? " Detected: " + h(d.other_firewalls.join(', ')) + '.' : ''}</p>
    ${d.error ? '<p class="tag err">' + h(d.error) + '</p>' : ''}
    ${s.ready && s.enabled !== d.active && !d.error ? "<p class=\"tag warn\">Saved settings differ from active rules. Reapply the desired state using the filtering switch.</p>" : ''}`;
  if (!s.ready) { const p = panel(view); p.innerHTML = "<div class=\"empty\"><b>Configure access to server services</b><p>Install the module, create rules and enable filtering. Installation uses nftables and does not enable restrictions automatically.</p></div>"; return; }
  const list = panel(view, "Rules");
  table(list, { rows: s.rules || [], empty: { icon: 'shield', title: "No rules yet", text: "<p>Click Create to configure service access.</p>" }, columns: [
    { t: '№', r: r => s.rules.indexOf(r) + 1 },
    { t: "Name", r: r => `<b>${h(r.name)}</b><div class="hint">${r.enabled ? "Enabled" : "Disabled"}</div>` },
    { t: "Action", r: r => h(firewallActions.find(a => a[0] === r.action)?.[1] || r.action) },
    { t: "Protocol / port", r: r => h(r.protocol === 'any' ? "All" : r.protocol.toUpperCase()) + (r.port ? ' / ' + h(r.port) : '') },
    { t: "Source", r: r => h(r.source === '*' ? "Any IPv4 / IPv6" : r.source) + (r.addresses?.length ? '<div class="hint">' + h(r.addresses.join(', ')) + '</div>' : '') },
  ], onRow: r => firewallForm(r, s), actions: r => [
    { t: "Edit", i: 'edit', fn: () => firewallForm(r, s) },
    { t: r.enabled ? "Disable rule" : "Enable rule", i: r.enabled ? 'stop' : 'play', fn: () => change('save', { rule: { ...r, enabled: !r.enabled } }) },
    s.rules.indexOf(r) > 0 && { t: "Move up", i: 'chevron', fn: () => change('move', { id: r.id, direction: -1 }) },
    s.rules.indexOf(r) < s.rules.length - 1 && { t: "Move down", i: 'chevdown', fn: () => change('move', { id: r.id, direction: 1 }) },
    { t: "Delete", i: 'trash', danger: true, fn: async () => { if (await confirmBox("Delete rule?", h(r.name))) await change('delete', { id: r.id }); } },
  ] });
} });
