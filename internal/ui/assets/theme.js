(function () {
  var stored = null;
  try { stored = window.localStorage.getItem('shunt-theme'); } catch (e) { stored = null; }
  var theme = stored === 'light' || stored === 'dark' ? stored : 'auto';
  document.documentElement.setAttribute('data-theme', theme);
  window.__shuntTheme = theme;
})();
