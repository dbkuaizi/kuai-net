/* 在首次绘制前设置主题与语言，避免深色模式下刷新时闪白。
   必须在 <head> 中以同步方式引入（不要加 defer/async）。 */
(function () {
  var saved = null;
  try { saved = localStorage.getItem('netest-theme'); } catch (e) {}

  var mode = saved === 'light' || saved === 'dark' || saved === 'auto' ? saved : 'auto';
  var dark = mode === 'dark' ||
    (mode === 'auto' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);

  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');

  var lang = null;
  try { lang = localStorage.getItem('netest-lang'); } catch (e) {}
  if (lang !== 'zh' && lang !== 'en') {
    lang = (navigator.language || 'zh').toLowerCase().indexOf('zh') === 0 ? 'zh' : 'en';
  }
  document.documentElement.setAttribute('lang', lang === 'zh' ? 'zh-CN' : 'en');
})();
