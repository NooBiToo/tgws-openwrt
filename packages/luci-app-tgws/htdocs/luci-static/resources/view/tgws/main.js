'use strict';
'require view';
'require form';
'require rpc';
'require poll';
'require dom';
'require ui';

var callStatus = rpc.declare({ object: 'luci.tgws', method: 'status' });
var callService = rpc.declare({ object: 'luci.tgws', method: 'service', params: [ 'action' ] });

function row(label, value) {
	return E('tr', { 'class': 'tr' }, [
		E('td', { 'class': 'td left', 'width': '30%' }, label),
		E('td', { 'class': 'td left' }, value)
	]);
}

function fmtBytes(n) {
	if (!n) return '0';
	var u = [ 'B', 'KB', 'MB', 'GB', 'TB' ], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	return (i ? n.toFixed(1) : String(n)) + ' ' + u[i];
}

// Одно предложение о том, что происходит, вместо россыпи флагов: человеку нужен
// ответ «работает ли», а не состояние трёх подсистем.
function verdict(st) {
	if (!st.installed)
		return { level: 'danger', head: _('The tgws daemon is not installed'),
			detail: _('Run install.sh again: it installs the daemon next to this package.') };
	if (!st.enabled)
		return { level: 'info', head: _('Telegram acceleration is off'),
			detail: _('Turn it on below and press Save & Apply.') };
	if (!st.running)
		return { level: 'danger', head: _('The service is not running'),
			detail: _('Check the system log: logread -e tgws') };
	if (!st.intercepting)
		return { level: 'warning', head: _('The daemon runs, but Telegram traffic is not intercepted'),
			detail: _('The firewall rules were not loaded. Telegram works as before. See logread -e tgws.') };
	return { level: 'success', head: _('Telegram is accelerated for every device on the network'), detail: '' };
}

return view.extend({
	load: function() {
		return callStatus();
	},

	renderStatus: function(st) {
		var v = verdict(st);
		var s = st.stats || {};
		var alert = E('div', { 'class': 'alert-message ' + v.level },
			v.detail ? [ E('strong', {}, v.head), E('br'), v.detail ] : [ E('strong', {}, v.head) ]);

		var rows = [
			row(_('Version'), st.version || E('em', {}, _('unknown'))),
			row(_('Through WebSocket'), String(s.ws || 0)),
			row(_('Passed through directly'), String(s.fallback || 0)),
			row(_('Active connections'), String(s.active || 0)),
			row(_('Sent / received'), fmtBytes(s.bytes_up) + ' / ' + fmtBytes(s.bytes_down))
		];
		var unknown = s.unknown_dc || [];
		if (unknown.length)
			rows.push(row(_('Telegram addresses with no known data center'),
				unknown.join(', ')));

		return E('div', {}, [ alert, E('table', { 'class': 'table', 'style': 'margin-top:1em' }, rows) ]);
	},

	handleAction: function(action) {
		ui.showModal(_('Please wait'), [ E('p', { 'class': 'spinning' }, _('Running…')) ]);
		return callService(action).then(function(res) {
			ui.hideModal();
			if (res && res.not_running)
				ui.addNotification(null, E('p', {}, _('The service did not start. The system log says why.')), 'warning');
			else if (res && res.code !== 0)
				ui.addNotification(null, E('pre', {}, res.output || _('Command failed')), 'warning');
		}).catch(function(e) {
			ui.hideModal();
			ui.addNotification(null, E('p', {}, e.message || String(e)), 'danger');
		});
	},

	render: function(st) {
		var self = this;
		var box = E('div', {}, this.renderStatus(st));

		poll.add(function() {
			return callStatus().then(function(s) {
				dom.content(box, self.renderStatus(s));
			});
		}, 10);

		var m = new form.Map('tgws', _('Telegram'),
			_('Carries Telegram traffic of every device on the network over WebSocket, which often works where the direct connection is slow or blocked. Nothing needs to be set up on the devices.'));
		var s = m.section(form.NamedSection, 'main', 'tgws');
		var o;

		o = s.option(form.Flag, 'enabled', _('Enable'));
		o.rmempty = false;

		o = s.option(form.Value, 'port', _('Local port'),
			_('The port the daemon listens on. Telegram connections are redirected to it by the firewall; change it only if the port is taken.'));
		o.datatype = 'port';
		o.default = '5454';

		o = s.option(form.Value, 'dc_ip', _('WebSocket targets'),
			_('DC:IP pairs, comma-separated. Data centers not listed here are passed through directly. If photos and videos do not load, try leaving only 4:149.154.167.220, or enter none to use no WebSocket at all.'));
		o.default = '2:149.154.167.220,4:149.154.167.220';
		o.validate = function(section_id, value) {
			// Номер DC и адрес проверяются по смыслу, как это делает сам
			// демон: иначе значение вроде 7:1.2.3.4 принималось бы формой,
			// а демон отказывался бы запускаться.
			var pair = '(1|2|3|4|5|203):((25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})\\.){3}(25[0-5]|2[0-4][0-9]|1?[0-9]{1,2})';
			if (value === '' || value === 'none' ||
			    new RegExp('^' + pair + '(,[ ]*' + pair + ')*$').test(value))
				return true;
			return _('Expected pairs like 2:149.154.167.220, separated by commas, or none');
		};

		o = s.option(form.Value, 'lan_devices', _('LAN interfaces'),
			_('Space-separated list whose traffic is intercepted. Empty means the device of the lan network. Only interfaces of firewall zones that accept input to the router work: Telegram cannot connect from a zone set to reject.'));
		o.placeholder = 'br-lan';
		o.optional = true;
		o.validate = function(section_id, value) {
			if (value === '' || /^[A-Za-z0-9._ -]+$/.test(value))
				return true;
			return _('Only letters, digits, dots, dashes and spaces are allowed');
		};

		o = s.option(form.Value, 'fallback_mark', _('Mark for traffic that cannot use WebSocket'),
			_('Telegram traffic that cannot go over WebSocket (websites, data centers without a WebSocket endpoint) is sent directly by default. To send it through the TrustTunnel tunnel instead, enter the tunnel mark, 0x9527 by default. Empty means direct.'));
		o.placeholder = '0x9527';
		o.optional = true;
		o.validate = function(section_id, value) {
			if (value === '' || /^(0[xX][0-9a-fA-F]{1,8}|[0-9]{1,10})$/.test(value))
				return true;
			return _('Expected a number like 0x9527');
		};

		return m.render().then(function(formNode) {
			return E('div', {}, [
				E('h2', {}, _('Telegram')),
				E('div', { 'class': 'cbi-section' }, [
					box,
					E('div', { 'style': 'margin-top:1em' }, [
						E('button', { 'class': 'cbi-button cbi-button-apply',
							'click': ui.createHandlerFn(self, 'handleAction', 'start') }, _('Start')),
						' ',
						E('button', { 'class': 'cbi-button cbi-button-reset',
							'click': ui.createHandlerFn(self, 'handleAction', 'stop') }, _('Stop')),
						' ',
						E('button', { 'class': 'cbi-button cbi-button-neutral',
							'click': ui.createHandlerFn(self, 'handleAction', 'restart') }, _('Restart'))
					])
				]),
				E('div', { 'class': 'cbi-section' }, [
					E('p', { 'class': 'cbi-section-descr' },
						_('Only IPv4 is intercepted. If the router has IPv6 enabled, Telegram may connect over IPv6 and bypass this; disabling IPv6 on the LAN makes the acceleration complete.'))
				]),
				formNode
			]);
		});
	}
});
