// Runs before stylesheets so a saved theme never flashes the default palette.
(function () {
  const root = document.documentElement;
  function apply(theme, collapsed) {
    root.dataset.theme = theme === 'dark' ? 'dark' : 'light';
    root.classList.toggle('dark', theme === 'dark');
    root.classList.toggle('sidebar-collapsed', collapsed === 'true');
  }
  try {
    apply(localStorage.getItem('api-console-theme'), localStorage.getItem('api-console-sidebar'));
  } catch (_) { apply('light', 'false'); }
  function labels() {
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      const dark = root.dataset.theme === 'dark';
      button.setAttribute('aria-label', dark ? '切换浅色主题' : '切换深色主题');
      button.setAttribute('title', dark ? '切换浅色主题' : '切换深色主题');
      button.setAttribute('aria-pressed', String(dark));
      const text = button.querySelector('[data-theme-label]');
      if (text) text.textContent = dark ? '浅色模式' : '深色模式';
    });
    document.querySelectorAll('[data-sidebar-collapse]').forEach((button) => {
      const collapsed = root.classList.contains('sidebar-collapsed');
      button.setAttribute('aria-expanded', String(!collapsed));
      button.setAttribute('aria-label', collapsed ? '展开侧栏' : '收起侧栏');
      button.setAttribute('title', collapsed ? '展开侧栏' : '收起侧栏');
    });
  }
  document.addEventListener('DOMContentLoaded', () => {
    labels();
    document.querySelectorAll('.sidebar-link').forEach((link) => {
      link.title = link.textContent.trim();
    });
    document.querySelectorAll('[data-theme-toggle]').forEach((button) => {
      button.addEventListener('click', () => {
        root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
        root.classList.toggle('dark', root.dataset.theme === 'dark');
        try { localStorage.setItem('api-console-theme', root.dataset.theme); } catch (_) {}
        labels();
      });
    });
    document.querySelectorAll('[data-sidebar-collapse]').forEach((button) => {
      button.addEventListener('click', () => {
        const collapsed = root.classList.toggle('sidebar-collapsed');
        try { localStorage.setItem('api-console-sidebar', String(collapsed)); } catch (_) {}
        labels();
        // SVG charts measure their container when the sidebar changes width.
        window.dispatchEvent(new Event('resize'));
      });
    });
    window.addEventListener('storage', (event) => {
      if (event.key !== 'api-console-theme' && event.key !== 'api-console-sidebar') return;
      try { apply(localStorage.getItem('api-console-theme'), localStorage.getItem('api-console-sidebar')); } catch (_) {}
      labels();
      window.dispatchEvent(new Event('resize'));
    });
  });
})();
