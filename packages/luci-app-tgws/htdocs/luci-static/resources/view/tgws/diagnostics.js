'use strict';
'require view';
'require rpc';
'require dom';
'require ui';

var callDiagnose = rpc.declare({ object: 'luci.tgws', method: 'diagnose' });
var callProbe = rpc.declare({ object: 'luci.tgws', method: 'probe' });

// Слово вердикта вместо цветного кружка: на узком экране и в тёмной теме цвет
// читается хуже текста, а класс alert-message LuCI уже несёт и фон, и отступы.
var VERDICT_CLASS = { ok: 'success', warn: 'warning', fail: 'danger', skip: 'info' };

function verdictWord(v) {
	if (v === 'ok')   return _('everything checks out');
	if (v === 'warn') return _('works, with remarks');
	if (v === 'fail') return _('there are problems');
	return _('not checked');
}

// Статус одной проверки — короткое слово фиксированной ширины, чтобы список
// читался столбцом, а не рваным краем.
function checkMark(status) {
	var t = { ok: _('ok'), warn: _('check'), fail: _('problem'), skip: _('skipped') };
	var c = { ok: '#2e7d32', warn: '#ef6c00', fail: '#c62828', skip: '#757575' };
	return E('span', {
		'style': 'display:inline-block;min-width:6.5em;font-weight:bold;color:' + c[status]
	}, t[status] || status);
}

// Бэкенд сообщает ФАКТЫ (идентификатор проверки, статус, значение), а
// формулировки и подсказки принадлежат интерфейсу. Здесь литеральные _(), а не
// _(переменная): так строки гарантированно попадают в каталог перевода.
// Подсказка показывается только для статуса, к которому она написана.
var CHECK_TEXT = {
	binary: {
		title: _('The tgws daemon is installed'),
		fail: _('The binary /usr/bin/tgws is missing. Run install.sh again: it installs the daemon next to this package.')
	},
	enabled: {
		title: _('Telegram acceleration is turned on'),
		skip: _('It is off. Turn it on in Settings and press Save & Apply.')
	},
	running: {
		title: _('The service is running'),
		fail: _('The service is not running. See the log on the Status page or run: logread -e tgws')
	},
	ready: {
		title: _('The daemon accepts connections'),
		fail: _('The service runs, but the daemon does not report in. It may have failed to take its port; see the log.')
	},
	nft: {
		title: _('The interception rules are loaded'),
		fail: _('The firewall table inet tgws is missing, so Telegram is not intercepted. Restart the service.')
	},
	lan: {
		title: _('The LAN interfaces exist'),
		fail: _('These interfaces do not exist. Fix "LAN interfaces" in Settings.')
	},
	zone: {
		title: _('The LAN firewall zone accepts connections to the router'),
		warn: _('The zone does not accept input, so the firewall rejects the redirected connections and Telegram cannot connect from it. Use a zone with input set to ACCEPT.'),
		skip: _('Custom interfaces are set: check their firewall zones yourself.')
	},
	ipv6: {
		title: _('No IPv6 on the LAN'),
		warn: _('Only IPv4 is intercepted. A client with an IPv6 address may reach Telegram over IPv6 and bypass the acceleration. Turn IPv6 off on the LAN to make it complete.')
	},
	websocket: {
		title: _('Telegram answers over WebSocket'),
		warn: _('Only some of the addresses answer. It works, but connections may be slow to start.'),
		fail: _('Telegram does not answer through this address. If the direct path is blocked too, Telegram will not work. Check the WebSocket targets in Settings.'),
		skip: _('No WebSocket targets are set, so everything goes the direct way.')
	},
	direct: {
		title: _('Direct access to Telegram from the router')
	},
	subnets: {
		title: _('The list of Telegram subnets is up to date'),
		warn: _('The downloaded list is more than 45 days old. Try "Update subnet list" on the Status page; if the official address is blocked from the router, set a mirror in Settings.')
	},
	unknown_dc: {
		title: _('Every Telegram address has a known data center'),
		warn: _('These addresses had no known data center and went the direct way. They are worked out automatically among the data centers that have WebSocket; if the list keeps growing, add them by hand in Settings.')
	},
	ws_unused: {
		title: _('Connections go through WebSocket'),
		warn: _('Many connections arrived, but none went through WebSocket. The clients may use a proxy or a transport this package does not recognise.')
	},
	tt_telegram: {
		title: _('TrustTunnel does not route Telegram'),
		warn: _('TrustTunnel has Telegram subnets in its lists. tgws intercepts them first, so that traffic does not use the tunnel. Remove the Telegram list from TrustTunnel, or set a mark in Settings to send what WebSocket cannot carry through it.'),
		skip: _('TrustTunnel is not installed.')
	},
	fallback: {
		title: _('Telegram traffic that cannot use WebSocket'),
		fail: _('The mark is wrong or has no routing rule: that traffic is dropped. Check that TrustTunnel is running and that the mark matches its fwmark.'),
		skip: _('It goes directly. Set a mark in Settings to send it through TrustTunnel.')
	}
};

var GROUP_TITLE = {
	service: _('Service'),
	network: _('Network'),
	telegram: _('Telegram'),
	integration: _('Integration with TrustTunnel')
};

// Значения, которые бэкенд отдаёт словом, а не данными.
var VALUE_TEXT = {
	reachable: _('reachable'),
	blocked: _('blocked or very slow: the case this package is for'),
	direct: _('directly'),
	none: _('none')
};

// Значение проверки: бэкенд отдаёт факт ("9 builtin", "9 updated 3"), а фразу
// составляет интерфейс, чтобы она переводилась.
function fmtValue(c) {
	if (!c.value)
		return '';
	if (c.id === 'subnets') {
		var m = /^(\d+) (builtin|updated (\d+))$/.exec(c.value);
		if (m)
			return m[2] === 'builtin'
				? _('%d subnets, built-in list').format(+m[1])
				: _('%d subnets, updated %d d ago').format(+m[1], +m[3]);
	}
	return VALUE_TEXT[c.value] || c.value;
}

return view.extend({
	renderChecks: function(list) {
		var parts = [];
		var groups = [];
		list.forEach(function(c) {
			if (groups.indexOf(c.group) < 0)
				groups.push(c.group);
		});
		groups.forEach(function(g) {
			parts.push(E('h4', { 'style': 'margin:1em 0 0.3em' }, GROUP_TITLE[g] || g));
			var rows = list.filter(function(c) { return c.group === g; }).map(function(c) {
				var t = CHECK_TEXT[c.id] || {};
				var value = fmtValue(c);
				var hint = t[c.status] || '';
				var body = [ E('div', {}, t.title || c.id) ];
				if (value)
					body.push(E('div', { 'style': 'opacity:.75' }, E('code', {}, value)));
				if (hint && (c.status === 'warn' || c.status === 'fail' || c.status === 'skip'))
					body.push(E('div', { 'style': 'opacity:.85;margin-top:.2em' }, hint));
				return E('tr', { 'class': 'tr' }, [
					E('td', { 'class': 'td left', 'style': 'width:8em;vertical-align:top' }, checkMark(c.status)),
					E('td', { 'class': 'td left' }, body)
				]);
			});
			parts.push(E('table', { 'class': 'table' }, rows));
		});
		return parts;
	},

	renderDiagnose: function(res) {
		var counts = res.counts || {};
		var checks = res.checks || [];
		var problems = checks.filter(function(c) {
			return c.status === 'fail' || c.status === 'warn';
		});
		var passed = checks.filter(function(c) {
			return c.status !== 'fail' && c.status !== 'warn';
		});

		var parts = [
			E('div', { 'class': 'alert-message ' + (VERDICT_CLASS[res.verdict] || 'info') }, [
				E('strong', {}, verdictWord(res.verdict)),
				E('br'),
				_('checks passed: %d, remarks: %d, problems: %d, skipped: %d')
					.format(counts.ok || 0, counts.warn || 0, counts.fail || 0, counts.skip || 0)
			])
		];

		// Сначала то, что требует внимания; прошедшее сворачивается, чтобы
		// не прятать проблему среди десятка зелёных строк.
		if (problems.length)
			parts.push.apply(parts, this.renderChecks(problems));

		if (!passed.length)
			return parts;

		var restBox = E('div', { 'style': 'display:none' }, this.renderChecks(passed));
		var labelShow = problems.length ? _('Show the checks that passed') : _('Show all checks');
		var btn = E('button', { 'class': 'cbi-button' }, labelShow);
		btn.addEventListener('click', function(ev) {
			ev.preventDefault();
			var hidden = restBox.style.display === 'none';
			restBox.style.display = hidden ? '' : 'none';
			btn.textContent = hidden ? _('Hide') : labelShow;
		});
		parts.push(E('div', { 'style': 'margin-top:1em' }, btn), restBox);
		return parts;
	},

	handleDiagnose: function(container) {
		dom.content(container, E('p', { 'class': 'spinning' },
			_('Running checks — this takes a few seconds…')));
		return callDiagnose().then(function(res) {
			dom.content(container, this.renderDiagnose(res));
		}.bind(this)).catch(function(e) {
			// catch обязателен: без него отклонённый вызов (таймаут ubus, отказ
			// в правах, перегруженный роутер) оставил бы страницу с надписью
			// «идёт проверка…» навсегда. Застрявший индикатор хуже сообщения
			// об ошибке: он выглядит как проверка, которая не завершится.
			dom.content(container, E('div', { 'class': 'alert-message danger' },
				e.message || String(e)));
		});
	},

	// Тест связи: настоящий запрос к Telegram тем же путём, что и у демона, по
	// каждому домену отдельно, с временем. Нужен, чтобы видеть не «работает или
	// нет», а какой именно адрес отвечает и насколько быстро.
	handleProbe: function(container) {
		dom.content(container, E('p', { 'class': 'spinning' }, _('Asking Telegram…')));
		return callProbe().then(function(res) {
			if (res.error)
				return dom.content(container, E('div', { 'class': 'alert-message warning' }, res.error));
			var rows = [];
			(res.results || []).forEach(function(p) {
				(p.results || []).forEach(function(r) {
					rows.push(E('tr', { 'class': 'tr' }, [
						E('td', { 'class': 'td left' }, 'DC' + p.dc),
						E('td', { 'class': 'td left' }, E('code', {}, r.domain)),
						E('td', { 'class': 'td left' }, r.ok
							? E('span', { 'style': 'color:#2e7d32;font-weight:bold' }, _('answers'))
							: E('span', { 'style': 'color:#c62828;font-weight:bold' }, _('no answer'))),
						E('td', { 'class': 'td left' }, r.ok ? r.ms + ' ms' : '—'),
						E('td', { 'class': 'td left', 'style': 'opacity:.75' }, r.ok ? '' : (r.error || ''))
					]));
				});
			});
			dom.content(container, E('table', { 'class': 'table' }, [
				E('tr', { 'class': 'tr table-titles' }, [
					E('th', { 'class': 'th left' }, _('Data center')),
					E('th', { 'class': 'th left' }, _('Address')),
					E('th', { 'class': 'th left' }, _('Result')),
					E('th', { 'class': 'th left' }, _('Time')),
					E('th', { 'class': 'th left' }, _('Error'))
				])
			].concat(rows)));
		}).catch(function(e) {
			dom.content(container, E('div', { 'class': 'alert-message danger' }, e.message || String(e)));
		});
	},

	render: function() {
		var self = this;
		var diagBox = E('div', { 'style': 'margin-top:1em' },
			E('em', {}, _('Press the button to run the checks.')));
		var probeBox = E('div', { 'style': 'margin-top:1em' });

		return E('div', { 'class': 'cbi-map' }, [
			E('h2', {}, _('Telegram')),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Checks')),
				E('p', { 'class': 'cbi-section-descr' },
					_('From the settings to a real request to Telegram: what works and what to fix.')),
				E('button', { 'class': 'cbi-button cbi-button-apply',
					'click': function() { return self.handleDiagnose(diagBox); } }, _('Run checks')),
				diagBox
			]),

			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Connection test')),
				E('p', { 'class': 'cbi-section-descr' },
					_('Sends a real request to every Telegram data center from the settings the same way the daemon does, and shows which address answers and how fast.')),
				E('button', { 'class': 'cbi-button cbi-button-neutral',
					'click': function() { return self.handleProbe(probeBox); } }, _('Test the connection')),
				probeBox
			])
		]);
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
