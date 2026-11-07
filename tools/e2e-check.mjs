// 通过 CDP 在无头浏览器里跑一遍测速流程，验证前端逻辑
// 用法：先启动服务，并用 Edge/Chrome 打开 http://127.0.0.1:8080/ 且开启 --remote-debugging-port=9223
const PORT = 9223;

async function connect() {
  const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const page = list.find((t) => t.type === 'page' && t.url.includes('8080'));
  if (!page) throw new Error('找不到页面 target，请先打开 http://127.0.0.1:8080/');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.addEventListener('open', r); ws.addEventListener('error', j); });

  let id = 0;
  const pending = new Map();
  const errors = [];
  const reqUrls = new Map();
  ws.addEventListener('message', (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const p = pending.get(msg.id); pending.delete(msg.id);
      msg.error ? p.reject(new Error(JSON.stringify(msg.error))) : p.resolve(msg.result);
    } else if (msg.method === 'Network.requestWillBeSent') {
      reqUrls.set(msg.params.requestId, msg.params.request.url);
    } else if (msg.method === 'Network.loadingFailed' && !msg.params.canceled) {
      // 只关心页面自身的请求，忽略浏览器扩展产生的噪音
      const url = reqUrls.get(msg.params.requestId) || '';
      if (url.startsWith('http://127.0.0.1:8080') && msg.params.errorText) {
        errors.push('loadingFailed: ' + msg.params.errorText + ' ' + url);
      }
    } else if (msg.method === 'Runtime.exceptionThrown') {
      errors.push('exception: ' + (msg.params.exceptionDetails.exception?.description || msg.params.exceptionDetails.text));
    }
  });
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    const mid = ++id; pending.set(mid, { resolve, reject });
    ws.send(JSON.stringify({ id: mid, method, params }));
  });
  const evaluate = async (expression) => {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  };
  await send('Runtime.enable');
  await send('Network.enable');
  return { ws, evaluate, errors };
}

async function main() {
  const { ws, evaluate, errors } = await connect();

  await evaluate(`localStorage.removeItem('netest-history'); localStorage.setItem('netest-lang','zh'); localStorage.setItem('netest-ui', JSON.stringify({ tab: 'test', types: { latency: true, download: true, upload: true }, threads: 'multi' })); location.reload(); 1`);
  await new Promise((r) => setTimeout(r, 1600));
  errors.length = 0;

  const snapshot = () => evaluate(`
    (() => {
      const d = Alpine.$data(document.querySelector('main'));
      const ds = window.__netestChart.data.datasets;
      return {
        status: d.statusText,
        latency: d.latency && +d.latency.avg.toFixed(2),
        download: d.download && +d.download.speedMbps.toFixed(0),
        upload: d.upload && +d.upload.speedMbps.toFixed(0),
        chart: ds.map((s) => ({ label: s.label, n: s.data.length, hidden: !!s.hidden })),
        tiers: d.ticks.map((t) => t.label),
        records: JSON.parse(localStorage.getItem('netest-history') || '[]').length,
        resultTabs: [...document.querySelectorAll('.result-tabs > button')].map((b) => b.textContent.trim()),
        resultValues: [...document.querySelectorAll('.result-grid')].map((g) => [...g.querySelectorAll('.stat b')].map((b) => b.textContent))
      };
    })()
  `);

  async function run(label, setup) {
    await evaluate(`location.reload(); 1`);
    await new Promise((r) => setTimeout(r, 1400));
    await evaluate(`(async () => {
      const d = Alpine.$data(document.querySelector('main'));
      ${setup}
      await d.start();
    })()`);
    await new Promise((r) => setTimeout(r, 250)); // 等 Alpine 刷新 DOM
    console.log(label.padEnd(16), JSON.stringify(await snapshot()));
  }

  await run('测速 5s', `d.runSpec='10s'; d.unit='Mbps';`);
  await run('测速 30s(取前段)', `d.runSpec='30s';`);
  await run('时长 10s', `d.runSpec='10s';`);
  await run('时长 15s', `d.runSpec='15s';`);

  // 完整流程中在下载阶段停止，不应继续执行上传
  await evaluate(`location.reload(); 1`);
  await new Promise((r) => setTimeout(r, 1400));
  const stopped = await evaluate(`
    (async () => {
      const d = Alpine.$data(document.querySelector('main'));
      d.runSpec = '30s';
      const p = d.start();
      await new Promise((r) => setTimeout(r, 900));
      const stageAtStop = d.phase;
      d.stop();
      await p;
      const rec = JSON.parse(localStorage.getItem('netest-history') || '[]')[0];
      return JSON.stringify({
        stageAtStop,
        phasesRun: { latency: !!d.latency, download: !!d.download, upload: !!d.upload },
        stopped: rec.stopped,
        typeCell: document.querySelector('tbody tr td:nth-child(2)').textContent,
        status: d.statusText
      });
    })()
  `);
  console.log('中途停止       ', stopped);

  // 旧版记录迁移
  const migrated = await evaluate(`
    (() => {
      localStorage.setItem('netest-history', JSON.stringify([{ id: 1, time: '2025-01-01 10:00', type: 'download', speed: 800, peak: 900, unit: 'Mbps', bytes: 1000000, seconds: 10 }]));
      location.reload();
      return 'reloading';
    })()
  `);
  await new Promise((r) => setTimeout(r, 1400));
  const row = await evaluate(`
    (() => {
      const d = Alpine.$data(document.querySelector('main'));
      const cells = [...document.querySelectorAll('tbody tr')[0].children].map((td) => td.textContent);
      return JSON.stringify({ migratedSpeed: d.history[0].download?.speedMbps, cells });
    })()
  `);
  console.log('旧记录迁移     ', migrated === 'reloading' ? row : 'failed');

  console.log('errors          ', errors.length ? JSON.stringify(errors.slice(0, 3)) : 'none');
  ws.close();
}

main().catch((e) => { console.error('FAILED:', e.message); process.exit(1); });
