// RouteCat — Frontend Application
// ──────────────────────────────────────────────────────────────

// ── i18n ─────────────────────────────────────────────────────
var _i18n = {};
var _supportedLangs = ['en', 'ca', 'es', 'gl', 'eu', 'fr', 'de', 'it'];

function detectLang() {
  var saved = localStorage.getItem('routecat-lang');
  if (saved && _supportedLangs.indexOf(saved) !== -1) return saved;
  var browser = (navigator.language || navigator.userLanguage || 'en').substring(0, 2).toLowerCase();
  return _supportedLangs.indexOf(browser) !== -1 ? browser : 'en';
}

var _lang = detectLang();

async function loadLocale(lang) {
  if (_i18n[lang]) return _i18n[lang];
  try {
    var r = await fetch('/locales/' + lang + '.json');
    _i18n[lang] = await r.json();
    return _i18n[lang];
  } catch (e) {
    console.error('Failed to load locale:', lang);
    return {};
  }
}

function t(key, fallback) {
  var dict = _i18n[_lang];
  if (dict && dict[key]) return dict[key];
  var en = _i18n['en'];
  if (en && en[key]) return en[key];
  return fallback || key;
}

function updatePlaceholders() {
  var pgOut = document.getElementById('pg-output');
  if (pgOut) pgOut.setAttribute('data-placeholder', t('pg.empty', 'Response will appear here...'));
  var pgInput = document.getElementById('pg-input');
  if (pgInput) pgInput.placeholder = t('pg.placeholder', 'Type your message...');
  var acctName = document.getElementById('acct-name');
  if (acctName) acctName.placeholder = t('account.nokey.name', 'App name (optional)');
  var acctLogin = document.getElementById('acct-login-key');
  if (acctLogin) acctLogin.placeholder = 'rc_...';
  var customSats = document.getElementById('acct-custom-sats');
  if (customSats) customSats.placeholder = t('account.topup.placeholder', 'sats');
}

function applyI18n() {
  var dict = _i18n[_lang] || {};
  document.querySelectorAll('[data-i18n]').forEach(function (el) {
    var key = el.getAttribute('data-i18n');
    if (!el.getAttribute('data-orig')) el.setAttribute('data-orig', el.innerHTML);
    if (dict[key]) el.innerHTML = dict[key];
  });
  updatePlaceholders();
}

function updateLangUI() {
  var cur = document.getElementById('lang-current');
  if (cur) cur.textContent = _lang.toUpperCase();
  var dd = document.getElementById('lang-dropdown');
  if (dd) {
    dd.querySelectorAll('button').forEach(function (btn) {
      btn.classList.toggle('active', btn.getAttribute('data-lang') === _lang);
    });
  }
}

function toggleLangDropdown() {
  var dd = document.getElementById('lang-dropdown');
  if (dd) dd.classList.toggle('open');
}

// Close dropdown on click outside
document.addEventListener('click', function (e) {
  var switcher = document.querySelector('.lang-switcher');
  var dd = document.getElementById('lang-dropdown');
  if (dd && switcher && !switcher.contains(e.target)) {
    dd.classList.remove('open');
  }
});

async function switchLang(lang) {
  _lang = lang;
  localStorage.setItem('routecat-lang', lang);
  updateLangUI();
  var dd = document.getElementById('lang-dropdown');
  if (dd) dd.classList.remove('open');
  await loadLocale(lang);
  if (lang === 'en') {
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
      var orig = el.getAttribute('data-orig');
      if (orig) el.innerHTML = orig;
    });
    updatePlaceholders();
  } else {
    applyI18n();
  }
}

// ── Utilities ────────────────────────────────────────────────
function formatTokens(n) {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
  return n.toString();
}

// ── Stats & Models ───────────────────────────────────────────
async function loadStats() {
  try {
    var sr = await fetch('/v1/stats');
    var stats = await sr.json();
    document.getElementById('st-nodes').textContent = stats.nodes_online || 0;
    document.getElementById('st-jobs-24h').textContent = (stats.jobs_24h || 0).toLocaleString();
    document.getElementById('st-tokens-24h').textContent = formatTokens(stats.tokens_24h || 0);
    updateCalcBtcPrice(stats.btc_usd || 0);

    var r = await fetch('/v1/models');
    var d = await r.json();
    var models = d.data || [];
    document.getElementById('st-models').textContent = models.length;

    // Pricing table
    if (models.length > 0) {
      var rows = models.sort(function (a, b) {
        var pa = (a.routecat_pricing || {}).per_m_output_usd || 0;
        var pb = (b.routecat_pricing || {}).per_m_output_usd || 0;
        return pb - pa;
      }).map(function (m) {
        var p = m.routecat_pricing;
        var inp = p ? '$' + p.per_m_input_usd.toFixed(4) : '\u2014';
        var out = p ? '$' + p.per_m_output_usd.toFixed(4) : '\u2014';
        return '<tr><td>' + m.id + '</td><td class="price">' + inp + '</td><td class="price">' + out + '</td></tr>';
      }).join('');
      document.getElementById('pricing-body').innerHTML = rows;
    }

    updateCalcModels(models);

    // Playground model selector
    var sel = document.getElementById('pg-model');
    sel.innerHTML = models.sort(function (a, b) {
      var pa = (a.routecat_pricing || {}).per_m_output_usd || 0;
      var pb = (b.routecat_pricing || {}).per_m_output_usd || 0;
      return pb - pa;
    }).map(function (m) {
      var p = m.routecat_pricing;
      var label = m.id;
      if (p) label += '  (in: $' + p.per_m_input_usd.toFixed(4) + ' / out: $' + p.per_m_output_usd.toFixed(4) + ')';
      return '<option value="' + m.id + '">' + label + '</option>';
    }).join('');
  } catch (e) { }
}

// ── Token calculator ─────────────────────────────────────────
var _calcModels = [];
var _calcBtcPrice = 70000;

function updateCalcBtcPrice(price) {
  if (price > 0) _calcBtcPrice = price;
}

function updateCalcModels(models) {
  _calcModels = models;
  var sel = document.getElementById('calc-model');
  sel.innerHTML = models.sort(function (a, b) {
    var pa = (a.routecat_pricing || {}).per_m_output_usd || 0;
    var pb = (b.routecat_pricing || {}).per_m_output_usd || 0;
    return pb - pa;
  }).map(function (m) {
    var p = m.routecat_pricing;
    var price = p ? '$' + p.per_m_output_usd.toFixed(3) + '/M out' : '';
    return '<option value="' + m.id + '" data-in="' + (p ? p.per_m_input_usd : 0.01) + '" data-out="' + (p ? p.per_m_output_usd : 0.03) + '">' + m.id + (price ? '  (' + price + ')' : '') + '</option>';
  }).join('');
  updateCalc();
}

function updateCalc() {
  var sats = parseInt(document.getElementById('calc-sats').value);
  document.getElementById('calc-sats-val').textContent = sats.toLocaleString() + ' sats';
  var sel = document.getElementById('calc-model');
  var opt = sel.options[sel.selectedIndex];
  if (!opt) return;
  var priceOut = parseFloat(opt.getAttribute('data-out'));
  if (!priceOut || priceOut <= 0) return;
  doCalc(sats, priceOut, _calcBtcPrice);
}

function doCalc(sats, pricePerMOut, btcPrice) {
  var usd = sats / 100000000 * btcPrice;
  var tokens = Math.floor(usd / pricePerMOut * 1000000);
  var words = Math.floor(tokens * 0.75);
  var pages = Math.round(words / 300 * 10) / 10;

  document.getElementById('calc-tokens').textContent = tokens >= 1000000 ? (tokens / 1000000).toFixed(1) + 'M' : tokens >= 1000 ? (tokens / 1000).toFixed(0) + 'K' : tokens;
  document.getElementById('calc-words').textContent = words >= 1000000 ? (words / 1000000).toFixed(1) + 'M' : words >= 1000 ? (words / 1000).toFixed(0) + 'K' : words;
  document.getElementById('calc-pages').textContent = pages >= 100 ? Math.round(pages) : pages.toFixed(1);
}

// ── Account management ───────────────────────────────────────
var _pgKey = localStorage.getItem('routecat-key') || '';
var _pollInvoice = null;

function maskKey(k) {
  return k ? k.substring(0, 6) + '...' + k.substring(k.length - 4) : '';
}

function showAccountState() {
  if (_pgKey) {
    document.getElementById('acct-no-key').style.display = 'none';
    document.getElementById('acct-has-key').style.display = '';
    document.getElementById('acct-key').textContent = maskKey(_pgKey);
    refreshBalance();
  } else {
    document.getElementById('acct-no-key').style.display = '';
    document.getElementById('acct-has-key').style.display = 'none';
  }
}

function logout() {
  _pgKey = '';
  localStorage.removeItem('routecat-key');
  showAccountState();
}

async function createKey() {
  var name = document.getElementById('acct-name').value.trim() || 'web';
  try {
    var r = await fetch('/v1/auth/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name })
    });
    var d = await r.json();
    if (d.error) { alert(d.error); return; }
    _pgKey = d.api_key;
    localStorage.setItem('routecat-key', _pgKey);
    document.getElementById('acct-no-key').style.display = 'none';
    document.getElementById('acct-key-reveal').style.display = '';
    document.getElementById('acct-key-copy').value = _pgKey;
  } catch (e) { alert(t('account.err.generic', 'Error') + ': ' + e.message); }
}

function copyKey() {
  var inp = document.getElementById('acct-key-copy');
  inp.select();
  document.execCommand('copy');
  document.getElementById('acct-copy-btn').textContent = t('account.key.copied', 'Copied!');
  setTimeout(function () {
    document.getElementById('acct-copy-btn').textContent = t('account.key.copy', 'Copy');
  }, 2000);
}

function dismissKeyReveal() {
  document.getElementById('acct-key-reveal').style.display = 'none';
  showAccountState();
}

async function loginWithKey() {
  var key = document.getElementById('acct-login-key').value.trim();
  var errEl = document.getElementById('acct-login-error');
  errEl.style.display = 'none';
  if (!key || !key.startsWith('rc_')) {
    errEl.textContent = t('account.login.err.format', 'Invalid key format');
    errEl.style.display = '';
    return;
  }
  try {
    var r = await fetch('/v1/auth/balance', { headers: { 'Authorization': 'Bearer ' + key } });
    if (!r.ok) {
      errEl.textContent = t('account.login.err.notfound', 'Key not found or invalid');
      errEl.style.display = '';
      return;
    }
    _pgKey = key;
    localStorage.setItem('routecat-key', _pgKey);
    showAccountState();
  } catch (e) {
    errEl.textContent = t('account.login.err.conn', 'Connection error');
    errEl.style.display = '';
  }
}

async function refreshBalance() {
  if (!_pgKey) return;
  try {
    var r = await fetch('/v1/auth/balance', { headers: { 'Authorization': 'Bearer ' + _pgKey } });
    if (!r.ok) { logout(); return; }
    var d = await r.json();
    document.getElementById('acct-balance').textContent = (d.balance_sats || 0) + ' sats';
    document.getElementById('acct-free').textContent = d.free_remaining;
  } catch (e) { }
}

async function topUp(sats) {
  if (!_pgKey) return;
  if (!sats || sats < 10 || sats > 100000) {
    alert(t('account.topup.range', 'Amount must be between 10 and 100,000 sats'));
    return;
  }
  try {
    var r = await fetch('/v1/auth/topup', {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + _pgKey, 'Content-Type': 'application/json' },
      body: JSON.stringify({ amount_sats: sats })
    });
    var d = await r.json();
    if (d.error) { alert(d.error); return; }
    document.getElementById('acct-bolt11').value = d.invoice;
    document.getElementById('acct-invoice').style.display = '';
    document.getElementById('acct-inv-status').textContent = t('account.invoice.waiting', 'Waiting for payment...') + ' (' + sats + ' sats)';
    document.getElementById('acct-inv-status').style.color = 'var(--dim)';
    // QR code
    var qrImg = document.getElementById('acct-qr');
    qrImg.src = 'https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=' + encodeURIComponent(d.invoice.toUpperCase());
    qrImg.style.display = '';
    // Poll for payment
    if (_pollInvoice) clearInterval(_pollInvoice);
    _pollInvoice = setInterval(async function () {
      var b = await (await fetch('/v1/auth/balance', { headers: { 'Authorization': 'Bearer ' + _pgKey } })).json();
      document.getElementById('acct-balance').textContent = (b.balance_sats || 0) + ' sats';
      document.getElementById('acct-free').textContent = b.free_remaining;
      if (b.balance_sats > 0) {
        document.getElementById('acct-inv-status').textContent = t('account.invoice.received', 'Payment received!');
        document.getElementById('acct-inv-status').style.color = 'var(--green)';
        document.getElementById('acct-qr').style.display = 'none';
        clearInterval(_pollInvoice);
        _pollInvoice = null;
        setTimeout(function () { document.getElementById('acct-invoice').style.display = 'none'; }, 3000);
      }
    }, 3000);
  } catch (e) { alert(t('account.err.generic', 'Error') + ': ' + e.message); }
}

function copyInvoice() {
  var ta = document.getElementById('acct-bolt11');
  ta.select();
  document.execCommand('copy');
}

// ── Playground ───────────────────────────────────────────────
function ensureKey() {
  return _pgKey || '';
}

async function runPlayground() {
  var btn = document.getElementById('pg-btn');
  var out = document.getElementById('pg-output');
  var model = document.getElementById('pg-model').value;
  var input = document.getElementById('pg-input').value.trim();
  if (!model || !input) return;

  btn.disabled = true;
  btn.textContent = t('pg.sending', 'Generating...');
  out.textContent = '';

  var key = ensureKey();
  if (!key) {
    out.textContent = t('pg.nokey', 'Create an API key in the Account section above first.');
    btn.disabled = false;
    btn.textContent = t('pg.send', 'Send');
    return;
  }

  try {
    var resp = await fetch('/v1/chat/completions', {
      method: 'POST',
      headers: { 'Authorization': 'Bearer ' + key, 'Content-Type': 'application/json', 'X-Playground': 'true' },
      body: JSON.stringify({ model: model, messages: [{ role: 'user', content: input }], stream: true })
    });

    var reader = resp.body.getReader();
    var decoder = new TextDecoder();
    var buf = '';

    while (true) {
      var result = await reader.read();
      if (result.done) break;
      buf += decoder.decode(result.value, { stream: true });
      var lines = buf.split('\n');
      buf = lines.pop();
      for (var i = 0; i < lines.length; i++) {
        var line = lines[i].trim();
        if (!line.startsWith('data: ')) continue;
        var data = line.substring(6);
        if (data === '[DONE]') continue;
        try {
          var chunk = JSON.parse(data);
          if (chunk.choices && chunk.choices[0] && chunk.choices[0].delta && chunk.choices[0].delta.content) {
            out.textContent += chunk.choices[0].delta.content;
          }
        } catch (e) { }
      }
    }
  } catch (e) {
    out.textContent = 'Error: ' + e.message;
  }
  btn.disabled = false;
  btn.textContent = t('pg.send', 'Send');
}

// ── Init ─────────────────────────────────────────────────────
(async function () {
  // Load English first (for t() fallback), then current locale
  await loadLocale('en');
  updateLangUI();
  if (_lang !== 'en') {
    await loadLocale(_lang);
    applyI18n();
  }
  // Boot app
  showAccountState();
  loadStats();
  setInterval(loadStats, 30000);
  setInterval(refreshBalance, 15000);
})();
