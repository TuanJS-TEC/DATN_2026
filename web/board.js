// Price board page (board.html). Needs ui.js (createPage, n2, ago, ref, computed…) loaded first.

const { reactive, watch } = Vue;

const n0 = (v) => Math.round(v ?? 0).toLocaleString('vi-VN');
// Giá trị khớp: tỷ, đổi sang "N" (nghìn tỷ) khi vượt 1000 — như bảng của SSI/VPS.
const bil = (v) => {
  const t = (v ?? 0) / 1e9;
  return t >= 1000 ? n2(t / 1000) + 'N' : n2(t);
};

const IndexCard = {
  props: ['id'],
  template: `
    <div class="card" v-if="s">
      <canvas ref="cv"></canvas>
      <div class="row">
        <span class="name">{{ s.label }}</span>
        <span class="val" :class="dir">{{ n2(s.indexValue) }}</span>
        <span :class="dir">({{ n2(s.change) }} - {{ n2(s.changePercent) }}%) {{ arrow }}</span>
      </div>
      <div class="row">
        <span class="mut">{{ n0(s.allQty) }}<span class="unit">CP</span></span>
        <span class="mut">{{ bil(s.allValue) }}<span class="unit">Tỷ</span></span>
      </div>
      <div class="row sp">
        <span><span class="up">{{ s.advances || 0 }} Up</span>
          <span class="ceil">({{ s.ceiling || 0 }})</span></span>
        <span class="ref">{{ s.nochanges || 0 }} Mid</span>
        <span><span class="down">{{ s.declines || 0 }} Down</span>
          <span class="fl">({{ s.floor || 0 }})</span></span>
        <span class="mut">GDTT {{ bil(s.totalValuePT) }}</span>
      </div>
    </div>`,
  setup(props) {
    const s = ref(null);
    const cv = ref(null);
    let pts = [], timer = null;

    const dir = computed(() => s.value?.change > 0 ? 'up' : s.value?.change < 0 ? 'down' : 'ref');
    const arrow = computed(() => s.value?.change > 0 ? '▲' : s.value?.change < 0 ? '▼' : '■');

    const draw = () => {
      const c = cv.value, d = s.value;
      if (!c || !d || pts.length < 2) return;
      const dpr = window.devicePixelRatio || 1;
      const w = c.clientWidth, h = c.clientHeight;
      c.width = w * dpr; c.height = h * dpr;
      const g = c.getContext('2d');
      g.setTransform(dpr, 0, 0, dpr, 0, 0);
      g.clearRect(0, 0, w, h);

      // Tham chiếu nằm trong thang đo, nếu không đường kẻ TC sẽ chạy ra ngoài khung.
      const refv = d.prevIndexValue;
      let lo = Math.min(refv, ...pts), hi = Math.max(refv, ...pts);
      const pad = (hi - lo) * 0.12 || 1;
      lo -= pad; hi += pad;
      const X = (i) => i * w / (pts.length - 1);
      const Y = (v) => h - (v - lo) / (hi - lo) * h;
      const col = d.change >= 0 ? '#0a7a3c' : '#c4161c';

      g.setLineDash([3, 3]); g.strokeStyle = '#9a9a9a'; g.lineWidth = 1;
      g.beginPath(); g.moveTo(0, Y(refv)); g.lineTo(w, Y(refv)); g.stroke();
      g.setLineDash([]);

      const path = () => {
        g.beginPath();
        pts.forEach((v, i) => i ? g.lineTo(X(i), Y(v)) : g.moveTo(X(i), Y(v)));
      };
      const fill = g.createLinearGradient(0, 0, 0, h);
      fill.addColorStop(0, col + '33'); fill.addColorStop(1, col + '00');
      path(); g.lineTo(w, h); g.lineTo(0, h); g.closePath();
      g.fillStyle = fill; g.fill();
      path(); g.strokeStyle = col; g.lineWidth = 1.5; g.stroke();
    };

    const load = async () => {
      try {
        const j = await (await fetch(`/api/index?id=${props.id}`)).json();
        if (j.error || !j.snap?.indexValue) return;
        s.value = j.snap;
        pts = j.c || [];
        requestAnimationFrame(draw);  // chờ v-if dựng canvas xong mới vẽ
      } catch (e) { /* lần poll sau thử lại */ }
    };

    // Cards change width with the grid; redraw so the line isn't stretched.
    const redraw = () => requestAnimationFrame(draw);
    onMounted(() => { load(); timer = setInterval(load, 5000); window.addEventListener('resize', redraw); });
    onUnmounted(() => { clearInterval(timer); window.removeEventListener('resize', redraw); });

    return { s, cv, dir, arrow, n2, n0, bil };
  },
};

createPage({
  setup() {
    // Poll rate scales with payload: VN30 is 55KB, UPCOM is ~1.3MB.
    const boards = [
      { key: 'VN30', ms: 2000 }, { key: 'HNX30', ms: 2000 }, { key: 'VN100', ms: 3000 },
      { key: 'HOSE', ms: 5000 }, { key: 'HNX', ms: 5000 }, { key: 'UPCOM', ms: 10000 },
    ];

    const indexes = ['VNINDEX', 'VN30', 'HNX', 'HNX30', 'UPCOM'];

    const board = ref('VN30');

    const symNews = ref(null);   // null = đang tải
    const rows = ref([]);
    const search = ref('');
    const error = ref('');
    const updated = ref('');
    const ticking = ref(false);
    const sel = ref(null);
    const chartReady = ref(false);
    const dlgOpen = computed({ get: () => !!sel.value, set: (v) => { if (!v) sel.value = null; } });
    const flash = reactive({});
    const prev = new Map();
    let timer = null;

    const p = (v) => (v === null || v === undefined || v === 0) ? '' : (v / 1000).toFixed(2);
    const fmtVol = (v) => (v === null || v === undefined || v === 0) ? '' : Math.round(v).toLocaleString('vi-VN');
    // Foreign-room figures run to 13 digits; millions keep those columns narrow.
    const short = (v) => Math.abs(v) >= 1e6 ? (v / 1e6).toLocaleString('vi-VN', { maximumFractionDigits: 1 }) + 'M' : fmtVol(v);

    // Columns are dropped least-important first until the table fits its box:
    // measured, not guessed, since widths depend on font and on the data (UPCOM volumes).
    const LEVELS = [
      { foreign: true, hilo: true, depth: 3, limits: true },
      { hilo: true, depth: 3, limits: true },
      { depth: 3, limits: true },
      { depth: 2, limits: true },
      { depth: 1, limits: true },
      { depth: 1 },
      { depth: 0 },
    ];
    const lvl = ref(0);
    const cols = computed(() => LEVELS[lvl.value]);
    const scroller = ref(null);
    const shrink = async () => {
      await nextTick();
      const el = scroller.value;
      while (el && lvl.value < LEVELS.length - 1 && el.scrollWidth > el.clientWidth) {
        lvl.value++;
        await nextTick();
      }
    };
    const refit = () => { lvl.value = 0; shrink(); };
    let raf = 0;
    const onResize = () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(refit); };
    const bids = computed(() => [3, 2, 1].slice(3 - cols.value.depth));
    const asks = computed(() => [1, 2, 3].slice(0, cols.value.depth));
    const chg = (r) => !r.matchedPrice ? '' : (r.priceChange > 0 ? '+' : '') + (r.priceChange / 1000).toFixed(2);

    const cls = (price, r) => {
      if (!price) return '';
      if (price >= r.ceiling) return 'ceil';
      if (price <= r.floor) return 'fl';
      if (price > r.refPrice) return 'up';
      if (price < r.refPrice) return 'down';
      return 'ref';
    };

    const shown = computed(() => {
      const q = search.value.trim().toUpperCase();
      return q ? rows.value.filter(r => r.stockSymbol.includes(q)) : rows.value;
    });

    const stats = computed(() => {
      const s = { up: 0, down: 0, flat: 0, ceil: 0, floor: 0, vol: 0, val: 0 };
      for (const r of rows.value) {
        s.vol += r.nmTotalTradedQty || 0;
        s.val += r.nmTotalTradedValue || 0;
        if (!r.matchedPrice) { s.flat++; continue; }
        if (r.matchedPrice >= r.ceiling) s.ceil++;
        if (r.matchedPrice <= r.floor) s.floor++;
        if (r.priceChange > 0) s.up++;
        else if (r.priceChange < 0) s.down++;
        else s.flat++;
      }
      return s;
    });

    const selRow = computed(() => rows.value.find(r => r.stockSymbol === sel.value));

    const chartUrl = computed(() =>
      `https://web5.vps.com.vn/?sym=${sel.value}&theme=light&lang=vi&loadLastChart=true`);

    const loadBoard = async () => {
      try {
        const res = await fetch(`/api/board?group=${board.value}`);
        const j = await res.json();
        if (j.error) throw new Error(j.error);
        const data = (j.data || []).sort((a, b) => a.stockSymbol.localeCompare(b.stockSymbol));
        for (const r of data) {
          const was = prev.get(r.stockSymbol);
          if (was !== undefined && r.matchedPrice && was !== r.matchedPrice) {
            flash[r.stockSymbol] = r.matchedPrice > was ? 'f-up' : 'f-down';
            setTimeout(() => delete flash[r.stockSymbol], 700);
          }
          if (r.matchedPrice) prev.set(r.stockSymbol, r.matchedPrice);
        }
        rows.value = data;
        shrink();  // wider numbers may have pushed it over
        updated.value = new Date().toLocaleTimeString('vi-VN');
        error.value = '';
        ticking.value = false;
        requestAnimationFrame(() => { ticking.value = true; });
      } catch (e) {
        error.value = 'Không tải được bảng giá: ' + e.message;
      }
    };

    const open = (r) => {
      sel.value = sel.value === r.stockSymbol ? null : r.stockSymbol;
      chartReady.value = false;
    };

    const start = () => {
      clearInterval(timer);
      const ms = boards.find(b => b.key === board.value).ms;
      timer = setInterval(loadBoard, ms);
    };

    watch(sel, async (s) => {
      symNews.value = null;
      if (!s) return;
      try {
        const j = await (await fetch(`/api/news?sym=${s}`)).json();
        if (sel.value === s) symNews.value = Array.isArray(j) ? j : [];
      } catch (e) { if (sel.value === s) symNews.value = []; }
    });

    watch(board, () => {
      rows.value = []; sel.value = null; prev.clear(); lvl.value = 0; error.value = '';
      loadBoard(); start();
    });

    onMounted(() => { loadBoard(); start(); window.addEventListener('resize', onResize); });
    onUnmounted(() => { clearInterval(timer); window.removeEventListener('resize', onResize); });

    return { cols, scroller, bids, asks, short, boards, board, indexes, rows, search, error, updated, ticking, sel, selRow, flash, dlgOpen,
             shown, stats, chartUrl, chartReady, p, fmtVol, chg, cls, open,
             ago, symNews };
  },
}, { 'index-card': IndexCard });
