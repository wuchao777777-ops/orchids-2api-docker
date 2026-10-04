const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/static/js/tutorial.js', 'utf8');
const registry = fs.readFileSync(__dirname + '/static/js/provider-registry.js', 'utf8');
function setup(fetcher) {
  const nodes = new Map(), calls = [];
  function node() {
    return { value: '', textContent: '', disabled: false, children: [], events: {},
      addEventListener(name, fn) { this.events[name] = fn; },
      appendChild(child) { this.children.push(child); },
      replaceChildren(...children) { this.children = children; this.value = children[0]?.value || ''; } };
  }
  const document = { querySelectorAll: () => [], addEventListener() {}, createElement: node,
    getElementById(id) { if (!nodes.has(id)) nodes.set(id, node()); return nodes.get(id); } };
  document.getElementById('tutImportApp').value = 'codex';
  const window = { location: { origin: 'https://gateway.example', href: '' } };
  const context = { window, document, URL, AbortController, Set, fetch: async (...args) => {
    calls.push(args); return fetcher ? fetcher(...args) : { ok: true, headers: new Headers({ 'content-type': 'application/json' }), json: async () => ({data: [{id: 'model/中文 +&?'}, {id: 'second'}]}) };
  } };
  vm.runInNewContext(registry, context); vm.runInNewContext(source, context);
  const el = id => document.getElementById('tut' + id);
  const event = { preventDefault() {} };
  return { el, window, calls, event, async load(channel = 'workbuddy') {
    el('ImportChannel').value = channel; el('ImportChannel').events.change();
    el('ImportKey').value = 'sk-test+/=&'; el('ImportKey').events.input();
    await el('LoadModels').events.click();
    el('ImportModel').value = 'model/中文 +&?'; el('ImportModel').events.change();
  } };
}
test('all four channels import Codex and Claude with encoded credentials and exact endpoints', async () => {
  for (const channel of ['workbuddy', 'qoder', 'cline', 'grok']) {
    const t = setup(); await t.load(channel);
    assert.equal(t.calls[0][0], 'https://gateway.example/' + channel + '/v1/models');
    assert.equal(t.calls[0][1].credentials, 'omit');
    assert.equal(t.calls[0][1].headers.Authorization, 'Bearer sk-test+/=&');
    for (const app of ['codex', 'claude']) {
      t.el('ImportApp').value = app; t.el('ImportApp').events.change();
      assert.equal(t.el('ImportButton').disabled, false);
      t.el('ImportForm').events.submit(t.event);
      const link = new URL(t.window.location.href), q = link.searchParams;
      assert.equal(link.protocol, 'ccswitch:'); assert.equal(link.hostname, 'v1'); assert.equal(link.pathname, '/import');
      assert.equal(q.get('resource'), 'provider'); assert.equal(q.get('app'), app);
      assert.equal(q.get('endpoint'), 'https://gateway.example/' + channel + (app === 'codex' ? '/v1' : ''));
      assert.equal(q.get('apiKey'), 'sk-test+/=&'); assert.equal(q.get('model'), 'model/中文 +&?');
      assert.equal(q.has('enabled'), false);
      for (const alias of ['haikuModel', 'sonnetModel', 'opusModel']) assert.equal(q.get(alias), app === 'claude' ? 'model/中文 +&?' : null);
      assert.match(t.el('ImportStatus').textContent, /请求打开/);
    }
  }
});
test('Key/channel changes invalidate catalog; arbitrary model cannot be imported', async () => {
  const t = setup(); await t.load();
  t.el('ImportModel').value = 'invented'; t.el('ImportModel').events.change();
  t.el('ImportForm').events.submit(t.event); assert.equal(t.window.location.href, '');
  t.el('ImportModel').value = 'second'; t.el('ImportModel').events.change();
  assert.equal(t.el('ImportButton').disabled, false);
  t.el('ImportKey').events.input(); assert.equal(t.el('ImportButton').disabled, true);
  t.el('ImportForm').events.submit(t.event); assert.equal(t.window.location.href, '');
});
test('failed, empty or malformed catalog never enables import or exposes error secrets', async () => {
  for (const value of [null, {}, {data: []}]) {
    const t = setup(async () => ({ok: true, headers: new Headers({'content-type':'application/json'}), json: async () => value}));
    await t.load(); assert.equal(t.el('ImportButton').disabled, true);
  }
  const t = setup(async () => { throw new Error('sk-secret-echo'); });
  await t.load(); assert.equal(t.window.location.href, ''); assert.equal(t.el('ImportButton').disabled, true);
  assert.doesNotMatch(t.el('CatalogStatus').textContent, /secret/);
});
test('late catalog cannot restore models after Key changes', async () => {
  let resolve;
  const t = setup(() => new Promise(r => { resolve = r; }));
  const pending = t.load();
  t.el('ImportKey').value = 'sk-new'; t.el('ImportKey').events.input();
  resolve({ok: true, headers: new Headers({'content-type':'application/json'}), json: async () => ({data:[{id:'model/中文 +&?'}]})});
  await pending; assert.equal(t.el('ImportButton').disabled, true); assert.equal(t.el('ImportModel').disabled, true);
});
test('tutorial describes all channel protocols and loads registry before import code', () => {
  const html = fs.readFileSync(__dirname + '/templates/pages/tutorial.html', 'utf8');
  assert.ok(html.indexOf('provider-registry.js') < html.indexOf('tutorial.js'));
  assert.match(html, /data-api-path="\/grok"/); assert.doesNotMatch(html, /不适用|grok-3|"hy3"|填写对应凭据/);
  assert.doesNotMatch(source, /localStorage|sessionStorage|console\./);
});
