(function () {
  'use strict';
  const base = window.location.origin.replace(/\/$/, '');
  document.querySelectorAll('[data-api-base]').forEach(node => { node.textContent = base; });
  document.querySelectorAll('[data-api-path]').forEach(node => { node.textContent = base + node.getAttribute('data-api-path'); });
  document.addEventListener('click', event => {
    const button = event.target.closest('[data-copy-target]');
    const target = button && document.getElementById(button.getAttribute('data-copy-target'));
    if (target && typeof copyToClipboard === 'function') copyToClipboard(target.textContent || '');
  });

  const el = id => document.getElementById(id);
  const form = el('tutImportForm');
  if (!form) return;
  const app = el('tutImportApp'), channel = el('tutImportChannel'), key = el('tutImportKey');
  const model = el('tutImportModel'), load = el('tutLoadModels'), submit = el('tutImportButton');
  const providers = window.OrchidsProviderRegistry;
  let catalog = [], sequence = 0, controller;
  function option(value, label) {
    const node = document.createElement('option');
    node.value = value; node.textContent = label;
    return node;
  }
  providers.providers.forEach(provider => channel.appendChild(option(provider.key, provider.label)));
  channel.value = providers.defaultProviderKey;
  const provider = () => providers.get(channel.value);
  const endpoint = () => base + (app.value === 'claude' ? provider().apiPrefix.replace(/\/v1$/, '') : provider().apiPrefix);
  function update() {
    el('tutImportEndpoint').textContent = endpoint();
    el('tutImportHint').textContent = app.value === 'claude'
      ? '将默认模型及 Haiku / Sonnet / Opus 三个别名都映射到所选模型，避免客户端使用渠道中不存在的官方模型 ID。'
      : 'CC Switch 将生成 wire_api = "responses" 的 Codex 配置，默认推理档位为 high；若模型不支持 high，请在 CC Switch 中按模型能力修改或移除 model_reasoning_effort 后启用。';
    submit.disabled = !key.value.trim() || !catalog.some(item => item.id === model.value);
  }
  function invalidate() {
    sequence++; controller?.abort(); catalog = [];
    model.replaceChildren(option('', '请先读取可用模型')); model.disabled = true;
    load.disabled = false;
    el('tutCatalogStatus').textContent = '尚未读取目录';
    el('tutImportStatus').textContent = '';
    update();
  }
  channel.addEventListener('change', invalidate);
  key.addEventListener('input', invalidate);
  app.addEventListener('change', update);
  model.addEventListener('change', update);
  load.addEventListener('click', async () => {
    const token = key.value.trim();
    if (!token || token.includes('*') || /\s/.test(token)) {
      el('tutCatalogStatus').textContent = '请填写完整 API Key，不能使用列表中的掩码。'; return;
    }
    invalidate();
    const current = sequence;
    controller = new AbortController(); load.disabled = true;
    el('tutCatalogStatus').textContent = '正在读取目录…';
    try {
      const response = await fetch(base + provider().apiPrefix + '/models', {
        credentials: 'omit',
        headers: { Authorization: 'Bearer ' + token }, signal: controller.signal,
      });
      if (current !== sequence) return;
      if (!response.ok || !response.headers.get('content-type')?.includes('application/json')) throw new Error('目录请求失败');
      const value = await response.json();
      if (current !== sequence) return;
      if (!value || !Array.isArray(value.data)) throw new Error('目录响应格式无效');
      const seen = new Set();
      catalog = value.data.filter(item => {
        if (!item || typeof item.id !== 'string' || !item.id.trim() || seen.has(item.id)) return false;
        seen.add(item.id); return true;
      });
      model.replaceChildren(option('', catalog.length ? '请选择模型' : '当前 Key 没有可用模型'));
      catalog.forEach(item => model.appendChild(option(item.id, item.id)));
      model.disabled = catalog.length === 0;
      el('tutCatalogStatus').textContent = catalog.length ? '已读取 ' + catalog.length + ' 个模型' : '没有可用模型，请检查账号、模型状态和 Key 权限。';
    } catch (error) {
      if (current !== sequence || error.name === 'AbortError') return;
      // Never expose response text: a misconfigured upstream could echo the Key.
      el('tutCatalogStatus').textContent = '目录读取失败，请检查完整 Key、登录状态与渠道可用性后重试。';
    } finally {
      if (current === sequence) { load.disabled = false; update(); }
    }
  });
  form.addEventListener('submit', event => {
    event.preventDefault();
    const token = key.value.trim();
    if (!['claude', 'codex'].includes(app.value) || !provider() || !token || !catalog.some(item => item.id === model.value)) return;
    const url = new URL('ccswitch://v1/import');
    const params = { resource: 'provider', app: app.value,
      name: 'API-Console · ' + provider().label + ' · ' + (app.value === 'claude' ? 'Claude Code' : 'Codex'),
      endpoint: endpoint(), apiKey: token, model: model.value, homepage: base };
    if (app.value === 'claude') Object.assign(params, { haikuModel: model.value, sonnetModel: model.value, opusModel: model.value });
    Object.entries(params).forEach(([name, value]) => url.searchParams.set(name, value));
    try {
      window.location.href = url.href;
      el('tutImportStatus').textContent = '已请求打开 CC Switch，请在应用中确认导入；未打开时检查安装与浏览器权限。';
    } catch (_) {
      el('tutImportStatus').textContent = '无法打开 CC Switch，请检查安装与浏览器权限。';
    }
  });
  update();
})();
