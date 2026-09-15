// Shared by every page: helpers, PrimeVue theme and the site-wide components
// (header with menu + search, footer, story card, ticker tags, "tin mới" button).
// Needs vue.global, primevue UMD and @primeuix/themes aura UMD loaded first.

const SITE = 'Tin Kinh Tế';
const CATS = [
  ['CHUNG-KHOAN', 'Chứng khoán'], ['BAT-DONG-SAN', 'Bất động sản'], ['DOANH-NGHIEP', 'Doanh nghiệp'],
  ['NGAN-HANG', 'Ngân hàng'], ['VI-MO', 'Vĩ mô'], ['HANG-HOA', 'Hàng hóa'], ['QUOC-TE', 'Quốc tế'],
];
const catName = (k) => (CATS.find((c) => c[0] === k) || [])[1] || '';
const qs = (key) => new URLSearchParams(location.search).get(key) || '';
const validCat = (k) => (catName(k) ? k : '');

// Listing only gives 320px thumbnails; dropping /zoom/WxH yields the original for big slots.
const big = (u) => (u || '').replace(/\/zoom\/\d+_\d+/, '');
const n2 = (v) => (v ?? 0).toLocaleString('vi-VN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
// Accent-insensitive match, so "gia vang" finds "giá vàng".
const fold = (s) => String(s || '').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/đ/g, 'd');

// "12 phút trước" under 24h, otherwise date + time.
const ago = (iso) => {
  if (!iso) return '';
  const t = new Date(iso);
  if (isNaN(t)) return iso;
  const m = Math.round((Date.now() - t) / 60000);
  if (m < 1) return 'vừa xong';
  if (m < 60) return m + ' phút trước';
  if (m < 24 * 60) return Math.floor(m / 60) + ' giờ trước';
  return t.toLocaleString('vi-VN', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
};

async function getJSON(url) {
  const j = await (await fetch(url)).json();
  if (j.error) throw new Error(j.error);
  return j;
}
const getNews = (cat) => getJSON('/api/news?cat=' + cat);
const getAllNews = () => Promise.all(CATS.map(([k]) => getNews(k).catch(() => [])));
const link = (a, cat) => `article.html?p=${encodeURIComponent(a.url)}&cat=${a.cat || cat || ''}`;

const { ref, computed, onMounted, onUnmounted, nextTick } = Vue;

// ---------- components ----------

const SymTags = {
  props: { syms: Array },
  setup: () => ({ go: (t) => { location.href = 'section.html?sym=' + encodeURIComponent(t); } }),
  // Cards are already <a>: stop the tag click from also opening the article.
  template: `<span v-if="syms && syms.length" class="syms">
    <p-tag v-for="t in syms" :key="t" :value="t" class="sym" v-tooltip.top="'Tin về ' + t"
           @click.prevent.stop="go(t)"></p-tag></span>`,
};

// kind: '', 'lead', 'small', 'thumb small'; img shows the photo; sapo=false hides the summary.
const StoryCard = {
  props: { a: Object, cat: String, kind: { type: String, default: '' }, img: Boolean, sapo: { type: Boolean, default: true } },
  setup() {
    const broken = ref(false);
    return { broken, big, ago, link };
  },
  template: `<a v-if="a" class="story" :class="kind" :href="link(a, cat)">
    <img v-if="img && a.img && !broken" :src="kind.includes('thumb') ? a.img : big(a.img)" alt="" loading="lazy" @error="broken = true">
    <h3>{{ a.title }}</h3>
    <p v-if="sapo && a.sapo">{{ a.sapo }}</p>
    <div class="meta">{{ ago(a.time) }}<sym-tags :syms="a.syms"></sym-tags></div>
  </a>`,
};

// Polls every minute for articles not on screen; offers a reload instead of
// re-rendering, so nothing shifts under someone who is mid-read.
const FreshNews = {
  props: { load: Function, urls: Array },
  setup(props) {
    const n = ref(0);
    let timer;
    onMounted(() => {
      timer = setInterval(async () => {
        if (!props.urls || !props.urls.length) return;
        const known = new Set(props.urls);
        const fresh = (await props.load().catch(() => [])).filter((a) => !known.has(a.url));
        n.value = fresh.length;
      }, 60000);
    });
    onUnmounted(() => clearInterval(timer));
    return { n, reload: () => location.reload() };
  },
  template: `<p-button v-if="n" class="fresh" rounded icon="pi pi-arrow-up" :label="n + ' tin mới – bấm để cập nhật'" @click="reload"></p-button>`,
};

// bare: board page — slim bar only, no masthead/sections/ticker.
const SiteHeader = {
  props: { active: String, compact: Boolean, bare: Boolean },
  setup(props) {
    const today = new Date().toLocaleDateString('vi-VN', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
    const menu = ref(false);
    const stuck = ref(false);
    const root = ref(null);
    const ticks = ref([]);

    const loadTicker = async () => {
      const ids = ['VNINDEX', 'VN30', 'HNX'];
      const snaps = await Promise.all(ids.map((id) => getJSON('/api/index?id=' + id).then((j) => j.snap).catch(() => null)));
      ticks.value = snaps.map((s, i) => s && s.indexValue && { id: ids[i], v: s.indexValue, pct: s.changePercent, ch: s.change }).filter(Boolean);
    };

    // ---- search: articles from every category, plus a ticker shortcut ----
    const searching = ref(false);
    const query = ref('');
    const hits = ref([]);
    let pool = null;
    const openSearch = async () => {
      searching.value = true;
      if (!pool) pool = getAllNews().then((ls) => {
        const seen = new Set();
        return ls.flat().filter((a) => !seen.has(a.url) && seen.add(a.url))
          .map((a) => ({ ...a, key: fold(a.title + ' ' + a.sapo + ' ' + (a.syms || []).join(' ')) }));
      });
    };
    const complete = async (e) => {
      const q = e.query.trim();
      const words = fold(q).split(/\s+/).filter(Boolean);
      const arts = (await pool).filter((a) => words.every((w) => a.key.includes(w))).slice(0, 8);
      const sym = /^[a-z0-9]{3}$/i.test(q) ? [{ sym: q.toUpperCase(), title: 'Tin về ' + q.toUpperCase() }] : [];
      hits.value = [...sym, ...arts];
    };
    const choose = (e) => {
      const o = e.value;
      location.href = o.sym ? 'section.html?sym=' + o.sym : link(o);
    };
    const focusSearch = () => nextTick(() => document.querySelector('.searchdlg input')?.focus());
    const onKey = (e) => {
      if (e.key === '/' && !/INPUT|TEXTAREA/.test(document.activeElement.tagName)) { e.preventDefault(); openSearch(); }
    };

    let timer, io;
    onMounted(() => {
      document.addEventListener('keydown', onKey);
      // Compact bar slides in once the real header has scrolled away.
      io = new IntersectionObserver(([en]) => { stuck.value = !en.isIntersecting; });
      io.observe(root.value);
      if (props.bare) return;
      loadTicker();
      timer = setInterval(loadTicker, 30000);
    });
    onUnmounted(() => { clearInterval(timer); io && io.disconnect(); document.removeEventListener('keydown', onKey); });

    return { SITE, CATS, today: today.charAt(0).toUpperCase() + today.slice(1), menu, stuck, root, ticks, n2,
             searching, query, hits, openSearch, complete, choose, focusSearch, ago, catName };
  },
  template: `<header ref="root" :class="{ compact, bare }">
    <div class="wrap">
      <div class="topbar">
        <div class="tools">
          <p-button icon="pi pi-bars" text rounded size="small" aria-label="Mở menu chuyên mục" @click="menu = true"></p-button>
          <p-button icon="pi pi-search" text rounded size="small" aria-label="Tìm kiếm" v-tooltip.bottom="'Tìm tin hoặc mã CK (phím /)'" @click="openSearch"></p-button>
        </div>
        <a v-if="bare" class="logo sm" href="index.html">{{ SITE }}</a>
        <a v-if="bare" href="index.html" class="toplink">Trang nhất ›</a>
        <span v-else class="toplinks">
          <a href="docs.html" class="toplink"><i class="pi pi-file-pdf"></i> Báo cáo tài chính ›</a>
          <a href="board.html" class="toplink"><i class="pi pi-chart-line"></i> Bảng giá trực tuyến ›</a>
        </span>
      </div>
      <template v-if="!bare">
        <div class="mast">
          <div class="date">{{ today }}<span>Tin tức tài chính hôm nay</span></div>
          <a class="logo" href="index.html">{{ SITE }}</a>
          <div class="ticker">
            <a v-for="t in ticks" :key="t.id" href="board.html"><b>{{ t.id }}</b>{{ n2(t.v) }}
              <span :class="t.ch > 0 ? 'up' : t.ch < 0 ? 'down' : ''">{{ t.ch > 0 ? '+' : '' }}{{ n2(t.pct) }}%</span></a>
          </div>
        </div>
        <nav class="sections" aria-label="Chuyên mục"><ul>
          <li v-for="[k, n] in CATS" :key="k"><a :href="'section.html?cat=' + k" :class="{ on: k === active }">{{ n }}</a></li>
        </ul></nav>
      </template>
    </div>

    <div class="stickybar" :class="{ on: stuck }" :aria-hidden="!stuck">
      <div class="wrap">
        <p-button icon="pi pi-bars" text rounded size="small" aria-label="Mở menu chuyên mục" @click="menu = true"></p-button>
        <a class="logo" href="index.html">{{ SITE }}</a>
        <span class="mini" v-if="ticks.length"><a href="board.html"><b>{{ ticks[0].id }}</b> {{ n2(ticks[0].v) }}
          <span :class="ticks[0].ch > 0 ? 'up' : ticks[0].ch < 0 ? 'down' : ''">{{ ticks[0].ch > 0 ? '+' : '' }}{{ n2(ticks[0].pct) }}%</span></a></span>
        <p-button icon="pi pi-search" text rounded size="small" aria-label="Tìm kiếm" @click="openSearch"></p-button>
      </div>
    </div>

    <p-drawer v-model:visible="menu" class="menu" :header="SITE">
      <nav aria-label="Menu">
        <a href="index.html"><i class="pi pi-home"></i>Trang nhất</a>
        <a v-for="[k, n] in CATS" :key="k" :href="'section.html?cat=' + k" :class="{ on: k === active }"><i class="pi pi-angle-right"></i>{{ n }}</a>
        <a href="docs.html"><i class="pi pi-file-pdf"></i>Báo cáo tài chính</a>
        <a href="board.html"><i class="pi pi-chart-line"></i>Bảng giá trực tuyến</a>
      </nav>
      <div class="menu-ticks" v-if="ticks.length">
        <a v-for="t in ticks" :key="t.id" href="board.html"><b>{{ t.id }}</b><span>{{ n2(t.v) }}</span>
          <span :class="t.ch > 0 ? 'up' : t.ch < 0 ? 'down' : ''">{{ t.ch > 0 ? '+' : '' }}{{ n2(t.pct) }}%</span></a>
      </div>
    </p-drawer>

    <p-dialog v-model:visible="searching" modal dismissable-mask position="top" :show-header="false"
              class="searchdlg" :style="{ width: 'min(640px, 94vw)' }" @show="focusSearch">
      <p-autocomplete v-model="query" :suggestions="hits" option-label="title" fluid :delay="150" :min-length="2"
                      placeholder="Tìm tin (vd: giá vàng) hoặc mã CK (vd: HPG)…" @complete="complete" @option-select="choose">
        <template #option="{ option }">
          <div class="hit" v-if="option.sym"><i class="pi pi-tag"></i><b>{{ option.title }}</b></div>
          <div class="hit" v-else>
            <img v-if="option.img" :src="option.img" alt="">
            <div><b>{{ option.title }}</b><small>{{ catName(option.cat) }} · {{ ago(option.time) }}</small></div>
          </div>
        </template>
        <template #empty><span class="dim">Không có tin nào khớp.</span></template>
      </p-autocomplete>
      <p class="hint">Gõ ít nhất 2 ký tự · không cần dấu · <kbd>Esc</kbd> để đóng</p>
    </p-dialog>
  </header>`,
};

const SiteFooter = {
  setup: () => ({ SITE, CATS }),
  template: `<footer><div class="wrap">
    <a class="logo" href="index.html">{{ SITE }}</a>
    <nav><a v-for="[k, n] in CATS" :key="k" :href="'section.html?cat=' + k">{{ n }}</a><a href="docs.html">Báo cáo tài chính</a><a href="board.html">Bảng giá</a></nav>
    <p>Tin tức: CafeF, VnExpress · Dữ liệu giá: SSI iBoard, Entrade · Báo cáo: Vietstock</p>
  </div><p-scrolltop :threshold="600" icon="pi pi-arrow-up"></p-scrolltop></footer>`,
};

// ---------- app bootstrap ----------

// Monochrome newspaper look on top of Aura: black primary, near-square corners.
const Theme = PrimeVue.definePreset(PrimeUIX.Themes.Aura, {
  primitive: { borderRadius: { none: '0', xs: '2px', sm: '2px', md: '3px', lg: '4px', xl: '6px' } },
  semantic: {
    primary: { 50: '{zinc.50}', 100: '{zinc.100}', 200: '{zinc.200}', 300: '{zinc.300}', 400: '{zinc.400}', 500: '{zinc.500}',
               600: '{zinc.600}', 700: '{zinc.700}', 800: '{zinc.800}', 900: '{zinc.900}', 950: '{zinc.950}' },
    colorScheme: {
      light: {
        // neutral greys instead of Aura's blue-tinted slate, to match the newsprint ink
        surface: { 0: '#ffffff', 50: '{zinc.50}', 100: '{zinc.100}', 200: '{zinc.200}', 300: '{zinc.300}', 400: '{zinc.400}',
                   500: '{zinc.500}', 600: '{zinc.600}', 700: '{zinc.700}', 800: '{zinc.800}', 900: '{zinc.900}', 950: '#121212' },
        text: { color: '#121212', hoverColor: '#363636', mutedColor: '#727272', hoverMutedColor: '#363636' },
        primary: { color: '#121212', contrastColor: '#ffffff', hoverColor: '{zinc.800}', activeColor: '{zinc.700}' },
        highlight: { background: '#121212', focusBackground: '{zinc.800}', color: '#ffffff', focusColor: '#ffffff' },
      },
    },
  },
});

const PRIME = ['Button', 'Dialog', 'Drawer', 'AutoComplete', 'InputText', 'IconField', 'InputIcon', 'Tag', 'Skeleton',
  'Paginator', 'ScrollTop', 'ProgressBar', 'SelectButton', 'Message', 'Toast', 'Badge'];

// createPage mounts a page app with PrimeVue and the shared components registered.
function createPage(root, components = {}) {
  const app = Vue.createApp(root);
  app.use(PrimeVue.Config, {
    theme: { preset: Theme, options: { darkModeSelector: false } },
    locale: { ...PrimeVue.defaultOptions?.locale, emptySearchMessage: 'Không có kết quả', emptyMessage: 'Không có dữ liệu' },
  });
  app.use(PrimeVue.ToastService);
  app.directive('tooltip', PrimeVue.Tooltip);
  for (const n of PRIME) app.component('p-' + n.toLowerCase(), PrimeVue[n]);
  const shared = { 'site-header': SiteHeader, 'site-footer': SiteFooter, 'story-card': StoryCard, 'sym-tags': SymTags, 'fresh-news': FreshNews };
  for (const [n, c] of Object.entries({ ...shared, ...components })) app.component(n, c);
  return app.mount('#app');
}
