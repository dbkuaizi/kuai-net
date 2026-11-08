/* ============================================================
   固定时长测速的取样内核（纯计算，不依赖 DOM，可用 Node 单测）

   与"自适应提前结束"完全不同：**测试一定跑满用户设定的时长**，
   这里只负责两件事：
     1. 给 UI 提供平滑后的实时速度（滑动平均）
     2. 测试结束后"掐头去尾"，只取稳定的中段算出最终速度

   时间轴设计（关键）：
     原始采样是 (采样时钟 t, 累计字节 b) 的序列。一次采样可能迟到
     （主线程繁忙/定时器被节流），所以所有速度都按"两点字节差 ÷ 两点
     时间差"算，而不是假设采样间隔恒定。
   ============================================================ */

const PROFILE_DEFAULTS = {
  // ---- 阶段划分（掐头去尾）----
  warmupMs: 1500,      // 掐头：前 1.5 秒是 TCP 慢启动，不计入结果
  minTailMs: 500,      // 去尾：最后 0.5 秒连接准备收尾，速度可能掉底
  maxTailRatio: 0.15,  // 去尾最多截到 15%（短测试时 0.5 秒占比过大就按比例缩）

  // ---- 实时曲线平滑 ----
  smoothMs: 1500,      // UI 折线：最近 1.5 秒的移动平均（去除单点毛刺）
  emaAlpha: 0.35,      // 再叠一层指数平滑，让曲线拐点圆滑且不迟钝
  sampleMs: 100,       // 10Hz 采样
  chartMaxPoints: 600, // 曲线最多保留的点数（防长时间测试撑爆）

  // ---- 稳态检测 ----
  steadyWindowMs: 2000, // 用最近 2 秒判断稳定性
  steadyCv: 0.05,       // 变异系数 ≤ 5% 视为稳态
  steadyMinSamples: 8,

  // ---- 结果取样 ----
  chopPeak: true,       // 稳态区内再剔除前后各 5% 的离群点
};

/**
 * 滑动窗口移动平均。
 *
 * 算法：窗口内保留 (t, v) 序列 + 增量累加 sum。
 *   入窗：sum += v            O(1)
 *   出窗：sum -= v            O(1)（按时间戳淘汰超出窗口的旧点）
 *   均值：sum / n             O(1)
 *
 * 入参 v 是"逐采样点的瞬时速度"，即相邻两点的字节差 ÷ 时间差。
 * 注意：不能对"累计字节"直接做平均（字节是单调递增的），必须先差分再平均。
 */
class MovingAverageWindow {
  constructor(windowMs = PROFILE_DEFAULTS.smoothMs) {
    this.windowMs = windowMs;
    this.points = []; // [{ t, v }]
    this.sum = 0;
  }

  /** 推入一个采样点；返回当前窗口的移动平均 */
  push(t, v) {
    if (!Number.isFinite(v) || v < 0) return this.value();
    this.points.push({ t, v });
    this.sum += v;
    // 至少保留 2 个点，否则刚起步时窗口会被清空导致均值为 0
    while (this.points.length > 2 && t - this.points[0].t > this.windowMs) {
      this.sum -= this.points.shift().v;
    }
    return this.value();
  }

  value() {
    return this.points.length ? this.sum / this.points.length : 0;
  }

  /** 实际覆盖的时间跨度，用于判断窗口是否"够满" */
  spanMs() {
    if (this.points.length < 2) return 0;
    return this.points[this.points.length - 1].t - this.points[0].t;
  }

  count() {
    return this.points.length;
  }
}

/**
 * 指数移动平均（EMA）——在滑窗平均之后再叠一层，专门用来"抹圆"曲线。
 *
 *   ema = α × 本次值 + (1 − α) × 上次 ema
 *
 * 与滑窗平均的分工：
 *   滑窗平均：把窗口内的点等权平均，能一次性抹掉单点毛刺，但曲线是"阶梯状"地
 *             随窗口滑动跳变（每进一个新点、出一个旧点，均值就跳一下）。
 *   EMA     ：对跳变再做时间加权，输出连续光滑，视觉上不会一帧一帧地顿。
 *
 * α 越大越跟手（滞后小、平滑弱），α 越小越平滑（滞后大）。
 * α = 0.35 时约 3 个采样点（300ms）就能跟上一次真实变化，同时抹平大部分抖动。
 */
class ExponentialSmoother {
  constructor(alpha = PROFILE_DEFAULTS.emaAlpha) {
    this.alpha = Math.min(1, Math.max(0.01, alpha));
    this.value = null;
  }

  push(v) {
    if (!Number.isFinite(v)) return this.value === null ? 0 : this.value;
    this.value = this.value === null ? v : this.alpha * v + (1 - this.alpha) * this.value;
    return this.value;
  }

  reset() {
    this.value = null;
  }

  get() {
    return this.value === null ? 0 : this.value;
  }
}

/**
 * 稳定性检测：最近 windowMs 内的逐点速度是否足够平稳。
 *
 *   均值  = Σv / n
 *   方差  = Σv²/n − 均值²          （增量累加，O(1)）
 *   标准差 = sqrt(max(0, 方差))     （浮点误差可能让方差略小于 0，需钳位）
 *   变异系数 cv = 标准差 / 均值     （消除带宽量级影响，不同链路可比）
 *
 * 判定为稳态需要：样本数够 + 跨度够 + cv ≤ 阈值。
 * 这里只用于"标记稳态区间"和给 UI 提示，**不影响测试是否结束**。
 */
class SteadyStateDetector {
  constructor(opts = {}) {
    this.windowMs = opts.steadyWindowMs || PROFILE_DEFAULTS.steadyWindowMs;
    this.cvThreshold = opts.steadyCv || PROFILE_DEFAULTS.steadyCv;
    this.minSamples = opts.steadyMinSamples || PROFILE_DEFAULTS.steadyMinSamples;
    this.points = [];
    this.sum = 0;
    this.sumSq = 0;
  }

  push(t, v) {
    if (!Number.isFinite(v) || v < 0) return this.stats();
    this.points.push({ t, v });
    this.sum += v;
    this.sumSq += v * v;
    while (this.points.length > 2 && t - this.points[0].t > this.windowMs) {
      const p = this.points.shift();
      this.sum -= p.v;
      this.sumSq -= p.v * p.v;
    }
    return this.stats();
  }

  stats() {
    const n = this.points.length;
    if (!n) return { n: 0, mean: 0, std: 0, cv: 0, steady: false };
    const mean = this.sum / n;
    const variance = Math.max(0, this.sumSq / n - mean * mean);
    const std = Math.sqrt(variance);
    const cv = mean > 0 ? std / mean : 0;
    const spanMs = n > 1 ? this.points[n - 1].t - this.points[0].t : 0;
    const steady = n >= this.minSamples && spanMs >= this.windowMs * 0.9 && cv <= this.cvThreshold;
    return { n, mean, std, cv, steady, spanMs };
  }
}

/**
 * 一次吞吐测试的取样器。
 *
 * 用法（每 100ms 调一次 sample）：
 *   const p = new SpeedProfile({ durationMs: 10000 });
 *   const uiMbps = p.sample(nowMs, totalBytes);  // 给图表用（已平滑）
 *   ...
 *   const r = p.result(endMs, totalBytes);       // 测试结束后算最终结果
 */
class SpeedProfile {
  constructor(opts = {}) {
    this.o = Object.assign({}, PROFILE_DEFAULTS, opts);
    this.samples = [];        // 原始样本 [{ t, b }]，用于最终掐头去尾
    this.smooth = new MovingAverageWindow(this.o.smoothMs);
    this.ema = new ExponentialSmoother(this.o.emaAlpha);
    this.steady = new SteadyStateDetector(this.o);
  }

  /** 推入一次采样，返回平滑后的瞬时速度（Mbps）供 UI 使用 */
  sample(t, bytes) {
    const last = this.samples[this.samples.length - 1];
    this.samples.push({ t, b: bytes });
    if (this.samples.length > 20000) this.samples.splice(1, 2000); // 极长时间测试的保护
    if (!last) return 0;

    const dt = (t - last.t) / 1000;
    if (dt <= 0.001) return this.ema.get(); // 同一毫秒内的重复采样，忽略
    const rawMbps = ((bytes - last.b) * 8) / dt / 1e6;

    // 预热期内速度还在爬坡，这些点不能进平滑窗口：
    // 否则窗口里的旧点会把均值拖低，曲线会出现"先低后高"的假爬升。
    // 预热结束后清空一次窗口，从第一个"已满速"的点重新开始平均。
    const elapsed = t - this.samples[0].t;
    if (elapsed < this.o.warmupMs) {
      this._warmed = false;
      return rawMbps;
    }
    if (!this._warmed) {
      this._warmed = true;
      this.smooth = new MovingAverageWindow(this.o.smoothMs);
      this.ema.reset();
    }
    this.steady.push(t, rawMbps);
    // 两级平滑：滑窗平均 → EMA，曲线既无毛刺也不会顿挫
    return this.ema.push(this.smooth.push(t, rawMbps));
  }

  get steadyState() {
    return this.steady.stats();
  }

  /**
   * 掐头去尾，计算最终速度。
   *
   * 时间轴：
   *   [0 ──── warmupMs ──── 稳态区 ──── durationMs − tailMs ──── durationMs]
   *    预热期(丢弃)          参与计算        收尾期(丢弃)
   *
   * 算法：
   *   headT = started + warmupMs
   *   tailT = end − min(max(duration*maxTailRatio, minTailMs), duration/2)
   *   在 samples 里取 [headT, tailT] 区间，用**区间首尾两个真实采样点**
   *   的字节差 ÷ 时间差 —— 这样不依赖采样间隔恒定，也不会把边界外的
   *   数据算进来。
   *
   * 为什么不直接"总流量 ÷ 总时间"：总时间里含慢启动（前 1.5 秒常常只有
   * 峰值的 30~60%），会把结果明显拉低。
   *
   * 峰值：在稳态区内按 1 秒宽的滑窗取最大值，避免单个采样点毛刺
   *      （比如一次 GC 卡顿后又突然补发）造成虚假峰值。
   */
  result(endMs, totalBytes) {
    const s = this.samples;
    const empty = {
      avgMbps: 0, peakMbps: 0, bytes: totalBytes, totalMs: Math.round(endMs - (s[0] ? s[0].t : 0)),
      measuredMs: 0, fromMs: 0, toMs: 0, usedBytes: 0, steady: false,
      warmupMs: this.o.warmupMs, tailMs: 0
    };
    if (s.length < 2) return empty;

    const start = s[0].t;
    const duration = Math.max(endMs - start, 1);

    // 掐头
    const headT = start + Math.min(this.o.warmupMs, duration * 0.6);

    // 去尾：至少 minTailMs，但不超过时长的 15%，且不超过一半
    const tailMs = Math.min(
      Math.max(duration * this.o.maxTailRatio, this.o.minTailMs),
      duration / 2
    );
    const tailT = Math.max(headT + 1, endMs - tailMs);

    // 取主体区内的真实采样点
    let body = s.filter((p) => p.t >= headT && p.t <= tailT);

    // 边界不足时用端点的真实样本兜底，保证区间定义不漂移
    if (body.length < 2) {
      // 太短或采样太稀：退化为"预热之后的部分"
      body = s.filter((p) => p.t >= headT);
      if (body.length < 2) return empty;
    }

    let a = body[0];
    let b = body[body.length - 1];

    // 稳态区内再剔除前后各 5% 的离群点（只影响字节口径，不改时间边界）
    if (this.o.chopPeak && body.length >= 20) {
      const k = Math.floor(body.length * 0.05);
      a = body[k];
      b = body[body.length - 1 - k];
    }

    const spanMs = b.t - a.t;
    const usedBytes = Math.max(0, b.b - a.b);
    const avgMbps = spanMs > 0 ? (usedBytes * 8) / (spanMs / 1000) / 1e6 : 0;

    const st = this.steady.stats();
    return {
      avgMbps,
      peakMbps: this.peakInSteady(body, totalBytes),
      bytes: totalBytes,
      totalMs: Math.round(duration),
      measuredMs: Math.round(spanMs),
      fromMs: Math.round(a.t - start),
      toMs: Math.round(b.t - start),
      usedBytes,
      steady: st.steady,
      steadyCv: st.cv,
      steadyStd: st.std,
      warmupMs: this.o.warmupMs,
      tailMs: Math.round(endMs - (a.t - start) === 0 ? 0 : Math.min(tailMs, duration / 2))
    };
  }

  /**
   * 稳态区内的峰值：先做 5 点中位数滤波，再取 1 秒滑窗的最大值。
   *
   * 为什么不用"瞬时最大值"：一次采样迟到后又突然补发积压数据，会算出一个
   * 远超真实带宽的单点毛刺；直接取最大值会让峰值虚高几十倍。
   * 中位数滤波对单点/双点异常免疫，滑窗再用 1 秒的跨度摊平抖动。
   */
  peakInSteady(body) {
    if (body.length < 3) return 0;

    // 1) 逐点瞬时速度
    const rates = [];
    for (let i = 1; i < body.length; i++) {
      const dt = (body[i].t - body[i - 1].t) / 1000;
      if (dt <= 0) continue;
      rates.push({ t: body[i].t, v: ((body[i].b - body[i - 1].b) * 8) / dt / 1e6 });
    }
    if (rates.length < 3) return 0;

    // 2) 5 点中位数滤波（边界用可用的点）
    const MEDIAN_K = 2;
    const filtered = rates.map((p, i) => {
      const lo = Math.max(0, i - MEDIAN_K);
      const hi = Math.min(rates.length - 1, i + MEDIAN_K);
      const win = rates.slice(lo, hi + 1).map((x) => x.v).sort((a, b) => a - b);
      return { t: p.t, v: win[win.length >> 1] };
    });

    // 3) 1 秒滑窗取最大值
    const PEAK_WINDOW_MS = 1000;
    let best = 0;
    let lo = 0;
    let sum = 0;
    for (let hi = 0; hi < filtered.length; hi++) {
      sum += filtered[hi].v;
      while (filtered[hi].t - filtered[lo].t > PEAK_WINDOW_MS && lo < hi) {
        sum -= filtered[lo].v;
        lo++;
      }
      const avg = sum / (hi - lo + 1);
      if (avg > best) best = avg;
    }
    return best;
  }
}

/* Node 下可 require 做单元测试；浏览器下挂到 window */
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { PROFILE_DEFAULTS, MovingAverageWindow, ExponentialSmoother, SteadyStateDetector, SpeedProfile };
}
if (typeof window !== 'undefined') {
  window.PROFILE_DEFAULTS = PROFILE_DEFAULTS;
  window.MovingAverageWindow = MovingAverageWindow;
  window.ExponentialSmoother = ExponentialSmoother;
  window.SteadyStateDetector = SteadyStateDetector;
  window.SpeedProfile = SpeedProfile;
}
