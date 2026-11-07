const MULTI_THREADS = 4;
const UPLOAD_CHUNK = 1024 * 1024;

// Chart 实例不能放进 Alpine 的响应式数据里，否则其内部循环引用会被代理包裹导致栈溢出。
let chartInstance = null;

function speedTest() {
  return {
    LANGUAGES,
    /* ---------- 配置 ---------- */
    types: { latency: true, download: true, upload: true },
    threads: 'single',   // single | multi
    runSpec: '10s',         // 5s | 10s | 30s | 50MB | 100MB | 500MB
    unit: 'Mbps',

    /* ---------- 运行状态 ---------- */
    running: false,
    stopping: false,
    finished: false,
    wasStopped: false,
    controller: null,
    stopAt: 0,
    stopBytes: 0,

    phase: 'latency',       // 当前正在执行的阶段
    stageNo: 0,
    stageCount: 0,
    progress: 0,            // 当前阶段进度 0..1
    progressLabel: '',
    message: '',

    /* ---------- 实时数据 ---------- */
    speedMbps: 0,
    avgMbps: 0,
    peakMbps: 0,
    transferred: 0,
    elapsed: 0,

    /* ---------- 本次结果 ---------- */
    latency: null,
    download: null,
    upload: null,

    /* ---------- 曲线 ---------- */
    downloadPoints: [],
    uploadPoints: [],
    xMax: 0,   // 0 表示自动；持续模式固定为测试秒数

    uploadChunk: null,
    history: [],
    lang: 'en',
    theme: 'dark',
    tab: 'test',   // test | history
    resultTab: 'overview',

    /* ---------- 初始化 ---------- */
    init() {
      // 主题：跟随系统 / 浅色 / 深色
      const savedTheme = localStorage.getItem('netest-theme');
      this.theme = (savedTheme === 'light' || savedTheme === 'dark' || savedTheme === 'auto') ? savedTheme : 'auto';
      document.documentElement.dataset.theme = this.effectiveTheme;
      this.watchSystemTheme();

      // 语言：本地设置 > 浏览器语言（只有英/简中有完整文案，其余语言先占位）
      this.lang = resolveLang(localStorage.getItem('netest-lang')) || detectLang();
      this.applyLang();

      try {
        const saved = JSON.parse(localStorage.getItem('netest-history') || '[]');
        // 本地最多展示并保留 60 条，避免历史记录无限增长。
        this.history = Array.isArray(saved) ? saved.map((it) => this.migrate(it)).slice(0, 60) : [];
        if (Array.isArray(saved) && saved.length > 60) {
          localStorage.setItem('netest-history', JSON.stringify(this.history));
        }
      } catch (e) {
        this.history = [];
      }
      try {
        const ui = JSON.parse(localStorage.getItem('netest-ui') || '{}');
        if (ui.tab === 'test' || ui.tab === 'history') this.tab = ui.tab;
        if (ui.threads === 'single' || ui.threads === 'multi') this.threads = ui.threads;
        if (ui.types && typeof ui.types === 'object') {
          this.types = {
            latency: ui.types.latency !== false,
            download: ui.types.download !== false,
            upload: ui.types.upload !== false
          };
        }
      } catch (e) { /* 忽略 */ }

      this.initChart();
      this.applyChartTheme();
      this.message = this.defaultMessage();
      this.measureCards();
      window.addEventListener('resize', () => this.measureCards());
    },

    // 兼容旧版本记录（扁平 speed / peak 字段）
    migrate(item) {
      if (!item) return item;
      if (item.download || item.upload || item.latency) return item;
      const out = {
        id: item.id,
        time: item.time,
        type: item.type,
        unit: item.unit || 'Mbps',
        mode: item.mode,
        value: item.value,
        stopped: !!item.stopped,
        bytes: item.bytes || 0,
        seconds: item.seconds || 0
      };
      if (item.type === 'latency') {
        out.latency = { avg: item.avg, min: item.min, max: item.max, jitter: item.jitter, samples: item.samples };
        out.bytes = 0;
      } else {
        const f = item.unit === 'MB/s' ? 8 : item.unit === 'KB/s' ? 8 / 1024 : 1;
        out[item.type] = { speedMbps: item.speed * f, peakMbps: item.peak * f, bytes: item.bytes, seconds: item.seconds };
      }
      return out;
    },

    /* ---------- 主题 / 语言 ---------- */
    get L() {
      return getTexts(this.lang);
    },

    get labels() {
      return {
        full: this.L.scopeFull,
        download: this.L.download,
        upload: this.L.upload,
        latency: this.L.latency
      };
    },

    // 实际生效的主题：auto 时读系统偏好
    get effectiveTheme() {
      if (this.theme !== 'auto') return this.theme;
      return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    },

    get themeTitle() {
      const name = this.theme === 'auto' ? this.L.themeAuto
        : this.theme === 'light' ? this.L.themeLight
        : this.L.themeDark;
      const sep = this.lang === 'zh' || this.lang === 'zh-Hant' ? '：' : ': ';
      return this.L.themeLabel + sep + name;
    },

    /* ---------- 展示计算 ---------- */
    get specMode() {
      return this.runSpec.endsWith('s') ? 'duration' : 'size';
    },

    get specValue() {
      return parseInt(this.runSpec, 10);
    },

    get statusText() {
      if (this.running) return this.L.statusRunning;
      if (this.finished) return this.wasStopped ? this.L.statusStopped : this.L.statusDone;
      return this.L.statusIdle;
    },

    // 表盘量程按当前峰值自动升档：<1000 → 1000；1000~2500 → 2500；>2500 → 10000
    get gaugeTier() {
      if (this.phase === 'latency') return { max: 200, segments: 4 };
      const peak = this.toUnit(this.peakMbps);
      if (peak <= 1000) return { max: 1000, segments: 4 };
      if (peak <= 2500) return { max: 2500, segments: 5 };
      return { max: 10000, segments: 5 };
    },

    get gaugeMax() {
      return this.gaugeTier.max;
    },

    // 刻度沿 240° 弧均分，半径 80
    get ticks() {
      const { max, segments } = this.gaugeTier;
      const out = [];
      for (let i = 0; i <= segments; i++) {
        const rad = ((210 - (240 * i) / segments) * Math.PI) / 180;
        out.push({
          x: (150 + 80 * Math.cos(rad)).toFixed(1),
          y: (140 - 80 * Math.sin(rad)).toFixed(1),
          label: this.formatTick((max * i) / segments)
        });
      }
      return out;
    },

    // 内部统一按 Mbps 存储，切换单位只影响显示。
    get speed() {
      if (this.phase === 'latency') return this.latency ? this.latency.avg : 0;
      return this.toUnit(this.speedMbps);
    },

    get avg() {
      if (this.phase === 'latency') return this.latency ? this.latency.avg : 0;
      return this.toUnit(this.avgMbps);
    },

    get peak() {
      return this.toUnit(this.peakMbps);
    },

    get dashOffset() {
      const r = Math.min(1, Math.max(0, this.speed / this.gaugeMax));
      return 439.8 - r * 439.8;
    },

    // 位数多时自动缩小字号，避免顶到表盘两侧
    get readoutSize() {
      const n = this.format(this.speed).length;
      return n >= 8 ? 'size-8' : n === 7 ? 'size-7' : '';
    },

    // 进度条已移除，百分比仅用于内部调试
    get overallPct() {
      const total = Math.max(1, this.stageCount || 1);
      const done = (Math.max(0, this.stageNo - 1) + this.progress) / total;
      return Math.round(Math.min(1, Math.max(0, done)) * 100);
    },

    get totalBytes() {
      return (this.download ? this.download.bytes : 0) + (this.upload ? this.upload.bytes : 0);
    },

    get threadCount() {
      return this.threads === 'multi' ? MULTI_THREADS : 1;
    },

    get canStart() {
      return !!(this.types.latency || this.types.download || this.types.upload);
    },

    get hasResults() {
      return !!(this.latency || this.download || this.upload);
    },

    toUnit(mbps) {
      if (this.unit === 'MB/s') return mbps / 8;
      if (this.unit === 'KB/s') return mbps / 8 * 1024;
      return mbps;
    },

    format(v) {
      return Number(v || 0).toFixed(2);
    },

    formatBytes(n) {
      n = Number(n || 0);
      if (!n) return '0 B';
      const units = ['B', 'KB', 'MB', 'GB', 'TB'];
      const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1);
      return (n / Math.pow(1024, i)).toFixed(i ? 2 : 0) + ' ' + units[i];
    },

    formatTick(v) {
      if (v >= 1000) {
        const k = v / 1000;
        return (k % 1 === 0 ? k.toFixed(0) : k.toFixed(1)) + 'k';
      }
      return v % 1 === 0 ? String(v) : v.toFixed(1);
    },

    setTab(tab) {
      this.tab = tab;
      this.saveUi();
      this.measureCards();
      // 曲线所在的卡片从 display:none 恢复后需要重新测量尺寸
      if (tab === 'test') this.resizeChartSoon();
    },

    saveUi() {
      try {
        localStorage.setItem('netest-ui', JSON.stringify({ tab: this.tab, types: this.types, threads: this.threads }));
      } catch (e) { /* 忽略 */ }
    },

    // 两张卡片共用同一高度：取自然高度的最大值，切换视图时不会跳动。
    measureCards() {
      const dash = document.querySelector('.dashboard');
      const hist = document.querySelector('.history');
      if (!dash || !hist) return;

      const apply = () => {
        const d = document.querySelector('.dashboard');
        const h = document.querySelector('.history');
        if (!d || !h) return;
        d.style.height = '';
        h.style.height = '';
        // 先量自然高度（两张都清零），再统一锁定
        const max = Math.ceil(Math.max(d.getBoundingClientRect().height, h.getBoundingClientRect().height));
        d.style.height = max + 'px';
        h.style.height = max + 'px';
      };

      // 等一帧，确保 Alpine 已把 x-show 应用到两张卡片上
      requestAnimationFrame(() => requestAnimationFrame(apply));
    },

    // 卡片从 display:none 恢复后需要重新测量画布尺寸
    resizeChartSoon() {
      requestAnimationFrame(() => requestAnimationFrame(() => {
        if (chartInstance) chartInstance.resize();
      }));
    },

    toggleTheme() {
      this.theme = this.theme === 'auto' ? 'light' : this.theme === 'light' ? 'dark' : 'auto';
      localStorage.setItem('netest-theme', this.theme);
      document.documentElement.dataset.theme = this.effectiveTheme;
      this.applyChartTheme();
    },

    // auto 模式下跟随系统深浅色变化
    watchSystemTheme() {
      if (!window.matchMedia) return;
      const mq = window.matchMedia('(prefers-color-scheme: dark)');
      const onChange = () => {
        if (this.theme !== 'auto') return;
        document.documentElement.dataset.theme = this.effectiveTheme;
        this.applyChartTheme();
      };
      if (mq.addEventListener) mq.addEventListener('change', onChange);
      else if (mq.addListener) mq.addListener(onChange);
    },

    toggleLang() {
      const idx = LANGUAGES.findIndex((l) => l.code === this.lang);
      this.lang = LANGUAGES[(idx + 1) % LANGUAGES.length].code;
      this.saveLang();
    },

    applyLang() {
      document.documentElement.lang = langHtmlTag(this.lang);
      this.measureCards();
      document.title = this.L.title;
      if (!this.running) this.message = this.defaultMessage();
      this.applyChartTheme();
    },

    saveLang() {
      localStorage.setItem('netest-lang', this.lang);
      this.applyLang();
    },

    toggleView() {
      this.setTab(this.tab === 'test' ? 'history' : 'test');
    },

    defaultMessage() {
      return '';
    },

    // 图表颜色/文案跟随主题与语言
    applyChartTheme() {
      if (!chartInstance) return;
      const css = getComputedStyle(document.documentElement);
      const v = (name, fallback) => css.getPropertyValue(name).trim() || fallback;
      const muted = v('--muted', '#8fa0b5');
      const grid = v('--grid', 'rgba(41,56,75,.55)');
      const border = v('--line-soft', '#29384b');
      const c = chartInstance;

      c.data.datasets[0].label = this.L.download;
      c.data.datasets[1].label = this.L.upload;
      c.data.datasets[0].borderColor = v('--accent', '#36d399');
      c.data.datasets[1].borderColor = v('--accent-2', '#7cc4ff');

      c.options.plugins.legend.display = false;
      c.options.plugins.tooltip.callbacks.title = (items) => items.length
        ? items[0].parsed.x.toFixed(1) + ' ' + this.L.secondsAxis
        : '';
      c.options.plugins.tooltip.callbacks.label = (item) =>
        (item.dataset.label || '') + ': ' + item.parsed.y.toFixed(2) + ' ' + this.unit;

      c.options.scales.x.ticks.color = muted;
      c.options.scales.x.title.color = muted;
      c.options.scales.x.title.text = this.L.timeAxis;
      c.options.scales.y.ticks.color = muted;
      c.options.scales.y.title.color = muted;
      c.options.scales.x.grid.color = grid;
      c.options.scales.y.grid.color = grid;
      c.options.scales.x.border.color = border;
      c.options.scales.y.border.color = border;
      c.update('none');
    },

    /* ---------- 曲线 ---------- */
    initChart() {
      const canvas = document.getElementById('speed-chart');
      if (!canvas || typeof Chart === 'undefined') return;

      const ctx = canvas.getContext('2d');
      const green = ctx.createLinearGradient(0, 0, 0, 210);
      green.addColorStop(0, 'rgba(54, 211, 153, .32)');
      green.addColorStop(1, 'rgba(54, 211, 153, 0)');
      const blue = ctx.createLinearGradient(0, 0, 0, 210);
      blue.addColorStop(0, 'rgba(124, 196, 255, .28)');
      blue.addColorStop(1, 'rgba(124, 196, 255, 0)');

      chartInstance = new Chart(canvas, {
        type: 'line',
        data: {
          datasets: [
            {
              label: '下载', data: [],
              borderColor: '#36d399', backgroundColor: green,
              borderWidth: 2, fill: true, tension: .3, cubicInterpolationMode: 'monotone',
              pointRadius: 0, pointHoverRadius: 3
            },
            {
              label: '上传', data: [],
              borderColor: '#7cc4ff', backgroundColor: blue,
              borderWidth: 2, fill: true, tension: .3, cubicInterpolationMode: 'monotone',
              pointRadius: 0, pointHoverRadius: 3
            }
          ]
        },
        options: {
          responsive: true,
          maintainAspectRatio: false,
          animation: { duration: 0 },
          transitions: {
            resize: { animation: { duration: 0 } }
          },
          animations: {
            x: { duration: 0 },
            y: { duration: 120, easing: 'linear' }
          },
          normalized: true,
          interaction: { mode: 'index', intersect: false },
          plugins: {
            // 不使用图例：曲线下方已有对应标签，图例属于冗余信息
            legend: { display: false },
            tooltip: {
              displayColors: true,
              // 只响应真实指针交互，避免移动端或数据刷新时自动弹出
              enabled: true,
              // 数值为 0 或空数据时不显示
              filter: (item) => Number.isFinite(item.parsed.y),
              position: 'nearest',
              events: ['mousemove', 'mouseout', 'click', 'touchstart', 'touchmove'],
              callbacks: {
                title: (items) => items.length ? items[0].parsed.x.toFixed(1) + ' ' + this.L.secondsAxis : '',
                label: (item) => (item.dataset.label || '') + ': ' + item.parsed.y.toFixed(2) + ' ' + this.unit
              }
            }
          },
          scales: {
            x: {
              type: 'linear',
              min: 0,
              grid: { color: 'rgba(41, 56, 75, .55)' },
              border: { color: '#29384b' },
              ticks: { color: '#8fa0b5', maxTicksLimit: 8, callback: (v) => v + 's' },
              title: { display: true, text: '时间（秒）', color: '#8fa0b5', font: { size: 11 } }
            },
            y: {
              beginAtZero: true,
              grid: { color: 'rgba(41, 56, 75, .55)' },
              border: { color: '#29384b' },
              ticks: { color: '#8fa0b5', maxTicksLimit: 6 },
              title: { display: true, text: 'Mbps', color: '#8fa0b5', font: { size: 11 } }
            }
          }
        }
      });
      window.__netestChart = chartInstance;
    },

    renderChart() {
      if (!chartInstance) return;
      const ds = chartInstance.data.datasets;
      const map = (points) => points.map((p) => ({ x: p.x, y: this.toUnit(p.v) }));

      ds[0].data = map(this.downloadPoints);
      ds[1].data = map(this.uploadPoints);
      ds[0].hidden = !this.downloadPoints.length;
      ds[1].hidden = !this.uploadPoints.length;
      // 上传尚未开始时不要画一条空心占位线
      ds[0].borderWidth = ds[0].hidden ? 0 : 2;
      ds[1].borderWidth = ds[1].hidden ? 0 : 2;
      chartInstance.options.scales.y.title.text = this.unit;
      chartInstance.options.scales.x.max = this.xMax || undefined;
      // 数据刷新时收起悬停高亮，避免 tooltip 残留/自动弹出
      chartInstance.setActiveElements([]);
      if (chartInstance.tooltip) chartInstance.tooltip.setActiveElements([], { x: 0, y: 0 });
      chartInstance.update('none');
    },

    // 曲线数据始终按原始 Mbps 存储，切换单位时只重算显示值。
    pushPoint(bucket, x, mbps) {
      bucket.push({ x: +x.toFixed(2), v: +mbps.toFixed(3) });
      if (bucket.length > 600) bucket.shift();
      this.renderChart();
    },

    onUnitChange() {
      this.renderChart();
    },

    /* ---------- 交互 ---------- */
    resetAll() {
      this.finished = false;
      this.wasStopped = false;
      this.progress = 0;
      this.progressLabel = '';
      this.stageNo = 0;
      this.stageCount = 0;
      this.speedMbps = this.avgMbps = this.peakMbps = this.transferred = this.elapsed = 0;
      this.latency = null;
      this.download = null;
      this.upload = null;
      this.downloadPoints = [];
      this.uploadPoints = [];
      this.xMax = 0;
      this.renderChart();
    },

    async start() {
      if (this.running) return;
      const plan = ['latency', 'download', 'upload'].filter((t) => this.types[t]);
      if (!plan.length) {
        this.message = this.L.pickOne;
        return;
      }

      this.resetAll();
      this.controller = new AbortController();
      this.running = true;
      this.stageCount = plan.length;

      let error = null;
      try {
        for (let i = 0; i < plan.length; i++) {
          if (this.stopping) break;
          this.stageNo = i + 1;
          if (plan[i] === 'latency') await this.runLatency();
          else await this.runThroughput(plan[i]);
        }
      } catch (e) {
        error = e;
      } finally {
        this.wasStopped = this.stopping;
        this.running = false;
        this.stopping = false;
        this.controller = null;
        this.finished = true;
        this.progress = 1;
        if (error) {
          this.message = this.L.testFailed + (error.message || error);
        } else if (this.hasResults) {
          this.message = '';
          this.saveResult();
        } else {
          this.message = this.L.stoppedNoData;
        }
        // 测完后仪表归零（结果数据仍保留在 Tab 和曲线里）
        this.resetGauge();
      }
    },

    resetGauge() {
      this.speedMbps = 0;
      this.avgMbps = 0;
      this.peakMbps = 0;
      this.transferred = 0;
      this.elapsed = 0;
      this.progress = 0;
      this.progressLabel = '';
    },

    // 测试中可随时中止：中断所有在途请求，并保留已采集的数据。
    stop() {
      if (!this.running || this.stopping) return;
      this.stopping = true;
      this.stopAt = performance.now();
      this.stopBytes = this.transferred;
      this.progressLabel = this.L.stopping;
      if (this.controller) this.controller.abort();
    },

    /* ---------- 延迟 ---------- */
    async runLatency() {
      const samples = 10;
      const values = [];
      const started = performance.now();

      this.phase = 'latency';
      this.transferred = 0;
      this.elapsed = 0;
      this.progress = 0;
      this.progressLabel = '';

      for (let i = 0; i < samples; i++) {
        const t = performance.now();
        let res;
        try {
          res = await fetch('/api/ping?x=' + Date.now() + '-' + i, {
            cache: 'no-store',
            signal: this.controller.signal
          });
        } catch (e) {
          if (e.name === 'AbortError') break;
          throw e;
        }
        if (!res.ok) throw new Error(this.L.serverError + res.status);

        values.push(performance.now() - t);
        this.latency = this.stats(values);
        this.elapsed = (performance.now() - started) / 1000;
        this.progress = (i + 1) / samples;
        this.progressLabel = this.L.probe + ' ' + (i + 1) + ' / ' + samples;
      }

      if (values.length) {
        this.latency = this.stats(values);
      }
    },

    stats(values) {
      const min = Math.min(...values);
      const max = Math.max(...values);
      const avg = values.reduce((a, b) => a + b, 0) / values.length;
      let jitter = 0;
      for (let i = 1; i < values.length; i++) jitter += Math.abs(values[i] - values[i - 1]);
      jitter = values.length > 1 ? jitter / (values.length - 1) : 0;
      return { min, avg, max, jitter, samples: values.length };
    },

    /* ---------- 吞吐 ---------- */
    async runThroughput(phase) {
      const isDownload = phase === 'download';
      const seconds = this.specMode === 'duration' ? this.specValue : 3600;
      const bytes = this.specMode === 'size' ? Math.round(this.specValue * 1024 * 1024) : 0;
      const controller = this.controller;
      const bucket = isDownload ? this.downloadPoints : this.uploadPoints;

      this.phase = phase;
      this.transferred = 0;
      this.elapsed = 0;
      this.speedMbps = 0;
      this.avgMbps = 0;
      this.peakMbps = 0;
      this.progress = 0;
      this.progressLabel = '';
      bucket.length = 0;
      // 持续模式把 x 轴固定为测试总时长，曲线从 0 秒向右推进
      this.xMax = this.specMode === 'duration' ? seconds : 0;
      this.pushPoint(bucket, 0, 0);
      this.renderChart();

      const started = performance.now();
      // 10Hz 采样 + 约 300ms 滑动窗口求速度：既跟得上，又不会锯齿状跳动
      const window = [];
      const SAMPLE_MS = 100;
      const SMOOTH_MS = 300;

      const reached = () => {
        const spent = (performance.now() - started) / 1000;
        return spent >= seconds || (bytes > 0 && this.transferred >= bytes);
      };

      const ticker = setInterval(() => {
        const now = performance.now();
        const elapsed = (now - started) / 1000;
        this.elapsed = elapsed;

        window.push({ t: now, b: this.transferred });
        while (window.length > 2 && now - window[0].t > SMOOTH_MS) window.shift();

        const base = window[0];
        const dt = (now - base.t) / 1000;
        if (dt > 0.05) {
          const mbps = (this.transferred - base.b) * 8 / dt / 1e6;
          if (mbps >= 0) {
            this.speedMbps = mbps;
            this.peakMbps = Math.max(this.peakMbps, mbps);
            this.pushPoint(bucket, Math.min(elapsed, seconds), mbps);
          }
        }

        if (bytes > 0) {
          this.progress = this.transferred / bytes;
          this.progressLabel = this.formatBytes(this.transferred) + ' / ' + this.formatBytes(bytes);
        } else {
          this.progress = elapsed / seconds;
          this.progressLabel = elapsed.toFixed(1) + ' / ' + seconds + ' ' + this.L.seconds;
        }
      }, SAMPLE_MS);

      try {
        const workers = [];
        for (let i = 0; i < this.threadCount; i++) {
          workers.push(isDownload
            ? this.downloadWorker(controller, seconds, bytes, reached)
            : this.uploadWorker(controller, seconds, bytes, reached));
        }
        await Promise.allSettled(workers);
      } finally {
        clearInterval(ticker);
      }

      const stopped = this.stopping;
      this.elapsed = stopped && this.stopAt > started
        ? Math.max((this.stopAt - started) / 1000, 0.001)
        : Math.max((performance.now() - started) / 1000, 0.001);
      if (stopped) this.transferred = this.stopBytes;

      const measured = stopped || this.specMode !== 'duration'
        ? this.elapsed
        : Math.min(this.elapsed, seconds);
      const avgMbps = this.transferred * 8 / Math.max(measured, 0.001) / 1e6;

      this.avgMbps = avgMbps;
      this.speedMbps = avgMbps;
      this.peakMbps = Math.max(this.peakMbps, avgMbps);
      this.progress = 1;

      // 极快的测试可能在第一个采样点之前就结束，补一个收尾点（x 必须单调递增）。
      if (this.transferred) {
        const lastX = bucket.length ? bucket[bucket.length - 1].x : 0;
        this.pushPoint(bucket, Math.max(measured, lastX), avgMbps);
      }

      const result = {
        speedMbps: avgMbps,
        peakMbps: this.peakMbps,
        bytes: this.transferred,
        seconds: +measured.toFixed(2),
        connections: this.threadCount
      };
      if (isDownload) this.download = result;
      else this.upload = result;

      if (this.transferred) {
      }
    },

    async downloadWorker(controller, seconds, bytes, reached) {
      const per = bytes > 0 ? Math.ceil(bytes / this.threadCount) : 0;
      const url = '/api/download?duration=' + Math.ceil(seconds) +
        '&size=' + per + '&x=' + Math.random().toString(36).slice(2);

      try {
        const res = await fetch(url, { cache: 'no-store', signal: controller.signal });
        if (!res.ok) throw new Error(this.L.serverError + res.status);
        if (!res.body) throw new Error(this.L.streamingUnsupported);

        const reader = res.body.getReader();
        let own = 0;

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          this.transferred += value.byteLength;
          own += value.byteLength;
          if (controller.signal.aborted || reached()) break;
        }
        try { await reader.cancel(); } catch (e) { /* 已结束 */ }
      } catch (e) {
        if (controller.signal.aborted || e.name === 'AbortError') return;
        throw e;
      }
    },

    // 用 XHR 而不是 fetch：能拿到 upload.onprogress，慢速网络下也能匀速刷新进度。
    async uploadWorker(controller, seconds, bytes, reached) {
      if (!this.uploadChunk) this.uploadChunk = this.randomBlob(UPLOAD_CHUNK);
      const chunk = this.uploadChunk;
      const per = bytes > 0 ? Math.ceil(bytes / this.threadCount) : 0;
      let own = 0;

      while (!controller.signal.aborted) {
        if (per > 0 && own >= per) return;
        if (reached && reached()) return;

        const size = per > 0 ? Math.min(chunk.size, per - own) : chunk.size;
        const body = size === chunk.size ? chunk : chunk.slice(0, size);
        const url = '/api/upload?duration=' + Math.ceil(seconds) +
          '&size=' + size + '&x=' + Math.random().toString(36).slice(2);

        let counted = 0;
        try {
          const result = await this.sendChunk(url, body, controller, (delta) => {
            counted += delta;
            own += delta;
            this.transferred += delta;
          });

          // 用服务端确认的字节数对齐客户端估算值。
          const diff = result.bytes - counted;
          if (diff > 0) {
            own += diff;
            this.transferred += diff;
          }
        } catch (e) {
          if (controller.signal.aborted || e.name === 'AbortError') return;
          throw e;
        }
      }
    },

    sendChunk(url, blob, controller, onProgress) {
      return new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        let loaded = 0;

        const onAbort = () => {
          xhr.abort();
          reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
        };

        xhr.open('POST', url);
        xhr.responseType = 'json';
        xhr.upload.onprogress = (e) => {
          const delta = e.loaded - loaded;
          if (delta > 0) {
            loaded = e.loaded;
            onProgress(delta);
          }
        };
        xhr.onload = () => {
          controller.signal.removeEventListener('abort', onAbort);
          if (xhr.status >= 200 && xhr.status < 300) {
            const body = xhr.response || {};
            resolve({ bytes: body.bytes || blob.size, sent: loaded });
          } else {
            reject(new Error(this.L.serverError + xhr.status));
          }
        };
        xhr.onerror = () => {
          controller.signal.removeEventListener('abort', onAbort);
          reject(new Error(this.L.networkError));
        };
        xhr.onabort = () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));

        controller.signal.addEventListener('abort', onAbort, { once: true });
        xhr.send(blob);
      });
    },

    randomBlob(size) {
      const buf = new Uint8Array(size);
      for (let i = 0; i < size; i += 65536) {
        crypto.getRandomValues(buf.subarray(i, Math.min(i + 65536, size)));
      }
      return new Blob([buf], { type: 'application/octet-stream' });
    },

    /* ---------- 历史记录 ---------- */
    saveResult() {
      const item = {
        id: Date.now(),
        time: new Date().toLocaleString(),
        type: 'full',
        mode: this.specMode,
        value: this.runSpec,
        unit: this.unit,
        stopped: this.wasStopped,
        latency: this.latency ? { ...this.latency } : null,
        download: this.download ? { ...this.download } : null,
        upload: this.upload ? { ...this.upload } : null,
        bytes: (this.download ? this.download.bytes : 0) + (this.upload ? this.upload.bytes : 0),
        seconds: +(((this.download ? this.download.seconds : 0) + (this.upload ? this.upload.seconds : 0))).toFixed(2)
      };

      this.history.unshift(item);
      this.history = this.history.slice(0, 60);
      try {
        localStorage.setItem('netest-history', JSON.stringify(this.history));
      } catch (e) {
        this.message += this.L.saveFailed;
      }
    },

    typeLabel(item) {
      return (this.labels[item.type] || item.type) + (item.stopped ? this.L.stoppedSuffix : '');
    },

    latencyCell(item) {
      return item.latency ? item.latency.avg.toFixed(2) + ' ms' : '—';
    },

    downloadCell(item) {
      return item.download ? this.toDisplay(item.download.speedMbps, item.unit) : null;
    },

    uploadCell(item) {
      return item.upload ? this.toDisplay(item.upload.speedMbps, item.unit) : null;
    },

    toDisplay(mbps, unit) {
      if (unit === 'MB/s') return mbps / 8;
      if (unit === 'KB/s') return mbps / 8 * 1024;
      return mbps;
    },

    speedText(mbps, unit) {
      if (mbps === null || mbps === undefined) return '—';
      return mbps.toFixed(2) + ' ' + (unit || 'Mbps');
    },

    volume(item) {
      return item.bytes ? this.formatBytes(item.bytes) : '—';
    },

    clearHistory() {
      if (!confirm(this.L.confirmClear)) return;
      this.history = [];
      localStorage.removeItem('netest-history');
    },

    exportHistory() {
      if (!this.history.length) return;
      const blob = new Blob([JSON.stringify(this.history, null, 2)], { type: 'application/json' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'netest-history.json';
      a.click();
      URL.revokeObjectURL(a.href);
    }
  };
}
