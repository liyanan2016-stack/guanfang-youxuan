package com.cf.ip;

import androidx.appcompat.app.AppCompatActivity;
import androidx.appcompat.app.AppCompatDelegate;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.graphics.Typeface;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.RadioButton;
import android.widget.RadioGroup;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

import com.cf.ip.better.Better;

public class MainActivity extends AppCompatActivity {

    private SegmentedBar segIPVersion;
    private SegmentedBar segCount;
    private TextView txtCountHint;
    /** 当前选中的输出数量，档位由核心层 Better.resultCounts() 给出 */
    private int resultCount = 1;
    /** 档位列表，下标与 segCount 的格子一一对应 */
    private java.util.List<Integer> countOptions = new java.util.ArrayList<>();

    private SegmentedBar segSpeedSeconds;
    private TextView txtSpeedSecondsHint;
    /** 当前选中的测速时长（秒），档位由核心层 Better.speedSeconds() 给出 */
    private int speedSeconds = 5;
    /** 档位列表，下标与 segSpeedSeconds 的格子一一对应 */
    private java.util.List<Integer> speedSecondOptions = new java.util.ArrayList<>();

    private FlowLayout layoutSpeedSource;
    private TextView txtSpeedSourceHint;
    private View boxSpeedURL;
    /** 当前选中的测速源标识，取值来自 Better.speedSources() */
    private String speedSource = "auto";
    /** 标识列表，下标与 layoutSpeedSource 里的筹码一一对应 */
    private java.util.List<String> speedSourceIds = new java.util.ArrayList<>();
    private EditText editSpeedURL;
    private Switch checkTLS;
    private EditText editBandwidth;
    private EditText editCountries;
    private EditText editSNI;
    private FlowLayout layoutPorts;
    private FlowLayout layoutRegions;
    private TextView txtPortHint;
    // 端口 / 地区折叠区
    private View headerPorts;
    private View boxPorts;
    private View arrowPorts;
    private TextView txtPortsSummary;
    private View headerRegions;
    private View boxRegions;
    private View arrowRegions;
    private TextView txtRegionsSummary;

    // 指定 IP 段折叠区
    private View headerRanges;
    private View boxRanges;
    private View arrowRanges;
    private TextView txtRangesSummary;
    private EditText editRanges;
    private TextView txtRangesHint;
    /** 最近一次预检是否通过。false 时点扫描直接拦下 */
    private boolean rangesValid = true;

    private View headerAdvanced;
    private View boxAdvanced;
    private View arrowAdvanced;
    private TextView txtAdvancedSummary;
    private Button btnScan;
    private Button btnCancel;
    private Button btnUpdate;
    private Button btnClearHistory;
    private ProgressBar progressBar;
    private TextView txtProgressTitle;
    private TextView txtProgress;
    private TextView txtResult;
    private ImageView txtThemeMode;
    private TextView txtIpValue;
    private TextView txtTargetBandwidth;
    private TextView txtRealBandwidth;
    private TextView txtMaxSpeed;
    private TextView txtLatency;
    private TextView txtDataCenter;
    private TextView txtElapsed;
    private TextView txtEmptyHistory;
    private View layoutMoreResults;
    private TextView txtMoreResultsTitle;
    private LinearLayout layoutMoreList;

    // 三个分页与底部导航。分页靠 visibility 切换而不是重建视图：
    // 切页不能丢掉已填的输入和滚动位置，更不能打断正在跑的扫描。
    private SpringScrollView pageScan;
    private SpringScrollView pageHistory;
    private View topBarDivider;
    private View navScan;
    private View navHistory;
    private TextView txtPageTitle;
    private TextView txtVersion;
    /** 0=优选（参数+扫描+结果） 1=历史。 */
    private int currentTab = 0;
    private View layoutProgress;
    private View layoutResult;
    private LinearLayout layoutHistoryList;

    private final ExecutorService executor = Executors.newSingleThreadExecutor();
    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private final AtomicBoolean isRunning = new AtomicBoolean(false);
    private Runnable progressPoller;
    private static Toast activeToast;
    private String currentIp = "";

    private static final String PREFS_NAME = "cfip_prefs";
    private static final String PREFS_THEME = "theme_idx";
    private static final String PREFS_HISTORY = "history_records";
    private static final String PREFS_USE_IPV4 = "use_ipv4";
    private static final String PREFS_USE_TLS = "use_tls";
    private static final String PREFS_BANDWIDTH = "bandwidth";
    private static final String PREFS_PORTS = "ports";
    private static final String PREFS_REGIONS = "regions";
    private static final String PREFS_COUNTRIES_EXTRA = "countries_extra";
    private static final String PREFS_SNI = "sni";
    private static final String PREFS_RESULT_COUNT = "result_count";
    private static final String PREFS_SPEED_SECONDS = "speed_seconds";
    private static final String PREFS_SPEED_URL = "speed_url";
    private static final String PREFS_SPEED_SOURCE = "speed_source";
    // 折叠状态也要记：改过端口的人下次多半还想看到它
    private static final String PREFS_PORTS_OPEN = "ports_open";
    private static final String PREFS_REGIONS_OPEN = "regions_open";
    private static final String PREFS_ADVANCED_OPEN = "advanced_open";
    private static final String PREFS_RANGES_OPEN = "ranges_open";
    private static final String PREFS_IP_RANGES = "ip_ranges";
    private static final int MAX_HISTORY = 10;

    /**
     * 常用地区。代码是 cca2，和 locations.json 的字段一致。
     *
     * 只列常见的落地地区；其余用输入框补 —— 全列出来会是几十个筹码，
     * 反而不好选。
     */
    private static final String[][] COMMON_REGIONS = {
            {"HK", "香港"},
            {"TW", "台湾"},
            {"JP", "日本"},
            {"KR", "韩国"},
            {"SG", "新加坡"},
            {"US", "美国"},
    };

    /** 当前选中的端口，跟着 TLS 开关重建 */
    private final java.util.LinkedHashSet<Integer> selectedPorts = new java.util.LinkedHashSet<>();
    /** 当前选中的常用地区代码 */
    private final java.util.LinkedHashSet<String> selectedRegions = new java.util.LinkedHashSet<>();

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        segIPVersion = findViewById(R.id.segIPVersion);
        segCount = findViewById(R.id.segCount);
        // 等宽：IPv4/IPv6 标签长度相同，等宽最整齐
        segIPVersion.setItems(new String[]{"IPv4", "IPv6"}, true);
        txtCountHint = findViewById(R.id.txtCountHint);
        segSpeedSeconds = findViewById(R.id.segSpeedSeconds);
        txtSpeedSecondsHint = findViewById(R.id.txtSpeedSecondsHint);
        layoutSpeedSource = findViewById(R.id.layoutSpeedSource);
        txtSpeedSourceHint = findViewById(R.id.txtSpeedSourceHint);
        boxSpeedURL = findViewById(R.id.boxSpeedURL);
        editSpeedURL = findViewById(R.id.editSpeedURL);
        checkTLS = findViewById(R.id.checkTLS);
        editBandwidth = findViewById(R.id.editBandwidth);
        editCountries = findViewById(R.id.editCountries);
        editSNI = findViewById(R.id.editSNI);
        layoutPorts = findViewById(R.id.layoutPorts);
        layoutRegions = findViewById(R.id.layoutRegions);
        txtPortHint = findViewById(R.id.txtPortHint);
        headerPorts = findViewById(R.id.headerPorts);
        boxPorts = findViewById(R.id.boxPorts);
        arrowPorts = findViewById(R.id.arrowPorts);
        txtPortsSummary = findViewById(R.id.txtPortsSummary);
        headerRegions = findViewById(R.id.headerRegions);
        boxRegions = findViewById(R.id.boxRegions);
        arrowRegions = findViewById(R.id.arrowRegions);
        txtRegionsSummary = findViewById(R.id.txtRegionsSummary);
        headerRanges = findViewById(R.id.headerRanges);
        boxRanges = findViewById(R.id.boxRanges);
        arrowRanges = findViewById(R.id.arrowRanges);
        txtRangesSummary = findViewById(R.id.txtRangesSummary);
        editRanges = findViewById(R.id.editRanges);
        txtRangesHint = findViewById(R.id.txtRangesHint);
        headerAdvanced = findViewById(R.id.headerAdvanced);
        boxAdvanced = findViewById(R.id.boxAdvanced);
        arrowAdvanced = findViewById(R.id.arrowAdvanced);
        txtAdvancedSummary = findViewById(R.id.txtAdvancedSummary);
        btnScan = findViewById(R.id.btnScan);
        btnCancel = findViewById(R.id.btnCancel);
        btnUpdate = findViewById(R.id.btnUpdate);
        btnClearHistory = findViewById(R.id.btnClearHistory);
        progressBar = findViewById(R.id.progressBar);
        txtProgressTitle = findViewById(R.id.txtProgressTitle);
        txtProgress = findViewById(R.id.txtProgress);
        txtResult = findViewById(R.id.txtResult);
        layoutProgress = findViewById(R.id.layoutProgress);
        layoutResult = findViewById(R.id.layoutResult);
        txtThemeMode = findViewById(R.id.txtThemeMode);
        txtIpValue = findViewById(R.id.txtIpValue);
        txtTargetBandwidth = findViewById(R.id.txtTargetBandwidth);
        txtRealBandwidth = findViewById(R.id.txtRealBandwidth);
        txtMaxSpeed = findViewById(R.id.txtMaxSpeed);
        txtLatency = findViewById(R.id.txtLatency);
        txtDataCenter = findViewById(R.id.txtDataCenter);
        txtElapsed = findViewById(R.id.txtElapsed);
        txtEmptyHistory = findViewById(R.id.txtEmptyHistory);
        layoutMoreResults = findViewById(R.id.layoutMoreResults);
        txtMoreResultsTitle = findViewById(R.id.txtMoreResultsTitle);
        layoutMoreList = findViewById(R.id.layoutMoreList);
        pageScan = findViewById(R.id.pageScan);
        pageHistory = findViewById(R.id.pageHistory);
        topBarDivider = findViewById(R.id.topBarDivider);

        // 内容滚起来之后顶栏才浮出分隔线。停在顶部时不画线 ——
        // 玻璃是连续的一层，硬边界会把顶栏和内容切成两块。
        SpringScrollView.OnScrollProgressListener divider =
                scrollY -> Anim.fadeTo(topBarDivider, scrollY > dp(4));
        pageScan.setOnScrollProgressListener(divider);
        pageHistory.setOnScrollProgressListener(divider);
        navScan = findViewById(R.id.navScan);
        navHistory = findViewById(R.id.navHistory);
        txtPageTitle = findViewById(R.id.txtPageTitle);
        txtVersion = findViewById(R.id.txtVersion);
        setupVersionLabel();
        // 导航项：按住整块变暗缩小，切换成功后图标再弹一下。
        // 只做点击后回弹的话，按住不放期间毫无反馈。
        Anim.attachPressScale(0.94f, navScan, navHistory);
        navScan.setOnClickListener(v -> {
            if (currentTab != 0) Anim.segmentSelect(v.findViewById(R.id.navScanIcon));
            selectTab(0);
        });
        navHistory.setOnClickListener(v -> {
            if (currentTab != 1) Anim.segmentSelect(v.findViewById(R.id.navHistoryIcon));
            selectTab(1);
        });
        layoutHistoryList = findViewById(R.id.layoutHistoryList);
        // 放在所有 findViewById 之后：selectTab 会碰到分页内的视图
        selectTab(0);

        Better.setCacheDir(getFilesDir().getAbsolutePath());
        loadScanSettings();

        // 恢复主题模式
        themeModeIndex = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getInt(PREFS_THEME, 0);
        switch (themeModeIndex) {
            case 1: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_NO); break;
            case 2: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_YES); break;
            default: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_FOLLOW_SYSTEM); break;
        }
        updateThemeLabel();

        txtThemeMode.setOnClickListener(v -> {
            // 图标自转半圈：三态循环（跟随系统/浅色/深色）光靠换图标不明显，
            // 加个旋转让人确认自己点到了。
            //
            // 用 rotationBy 而不是 animate().rotation()：attachPressScale 会
            // 在同一个 View 上跑 scale 动画，两者共用 ViewPropertyAnimator，
            // 但 rotation 和 scale 是不同属性，不会互相取消。
            v.animate().rotationBy(180f).setDuration(Anim.DUR_COLLAPSE)
                    .setInterpolator(Anim.EASE_OUT).start();
            cycleThemeMode();
        });
        txtIpValue.setOnClickListener(v -> copyCurrentIp());
        editBandwidth.setOnEditorActionListener((v, actionId, event) -> {
            if (actionId == EditorInfo.IME_ACTION_DONE) {
                normalizeBandwidthInput();
                v.clearFocus();
                hideKeyboard(v);
                return true;
            }
            return false;
        });
        editBandwidth.setOnFocusChangeListener((v, hasFocus) -> {
            if (!hasFocus) {
                normalizeBandwidthInput();
            }
        });

        // 扫描与取消分成两个按钮。原来共用一个按钮，扫描中按钮变成"停止扫描"，
        // 用户想再点一次开始扫描时容易误取消，取消完还要再点一次才开始。
        //
        // 按压反馈一次性批量装配，不逐个写：这界面上可点元素十几个，
        // 逐个写必然漏，漏掉的那几个按下去没反应，用户会以为点歪了。
        Anim.attachPressScale(0.97f, btnScan);
        Anim.attachPressScale(0.93f, btnCancel, btnUpdate, btnClearHistory,
                txtThemeMode, txtIpValue, headerPorts, headerRegions);

        btnScan.setOnClickListener(v -> startScan());
        btnCancel.setOnClickListener(v -> cancelCurrentTask());
        btnUpdate.setOnClickListener(v -> updateData());
        btnClearHistory.setOnClickListener(v -> clearScanHistory());

        // TLS 开关切换时端口集合完全不同（80 系 vs 443 系），必须重建。
        // 已选的端口在新模式下不合法，留着会让扫描全灭。
        // 选中反馈由 SegmentedBar 自己的滑动指示块负责，这里只管存设置
        segIPVersion.setOnSelectListener(index -> saveScanSettings());

        checkTLS.setOnCheckedChangeListener((v, checked) -> rebuildPortChips());
        renderRegionChips();
        rebuildPortChips();

        buildCountOptions();
        buildSpeedSecondOptions();
        buildSpeedSourceChips();
        setupCollapsibles();

        renderHistory();
        playEntryAnimation();
    }

    /**
     * 启动时首页的卡片依次冒出。
     *
     * <p>要等一帧再跑（{@code post}）：onCreate 里视图还没测量，
     * 此时 {@code getVisibility} 可读但位置全是 0，动画会从错误的偏移开始。
     *
     * <p>只做一次，不在 onResume 里重播 —— 从后台切回来还要看一遍卡片飞入，
     * 第三次就开始烦人了。
     */
    private void playEntryAnimation() {
        final View content = findViewById(R.id.scanContent);
        content.post(() -> {
            java.util.List<View> cards = new java.util.ArrayList<>();
            if (content instanceof ViewGroup) {
                ViewGroup g = (ViewGroup) content;
                for (int i = 0; i < g.getChildCount(); i++) {
                    cards.add(g.getChildAt(i));
                }
            }
            Anim.staggerIn(cards.toArray(new View[0]), 55);
        });
    }

    /**
     * 按核心层给的档位构建输出数量选项。
     *
     * <p>档位来自 {@link Better#resultCounts()}（"1,5,10"）而不是界面里硬编码 ——
     * 和端口列表同理，两处各写一份就会出现「界面能选、核心层不认」的档位。
     *
     * <p>复用 IP 协议那套分段控件的样式（{@code segmented_option_bg} +
     * {@code segmented_text}），不另造一种选择器：同一个卡片里出现两种
     * 单选控件只会让人怀疑它们行为不同。
     */
    private void buildCountOptions() {
        java.util.List<Integer> counts = parseIntCsv(Better.resultCounts());
        // 核心层没给出档位时兜一个最小可用集，界面不能变成空白
        if (counts.isEmpty()) {
            counts.add(1);
        }
        countOptions = counts;

        String[] labels = new String[counts.size()];
        for (int i = 0; i < counts.size(); i++) {
            labels[i] = String.valueOf(counts.get(i));
        }
        // 不等宽：1 / 5 / 10 标签很短，等宽会把三格拉得很散
        segCount.setItems(labels, false);

        // 保存的值可能不在当前档位里（核心层调整过档位），回落到第一档
        int index = counts.indexOf(resultCount);
        if (index < 0) {
            index = 0;
            resultCount = counts.get(0);
        }
        // 静默：这是恢复初值，不该触发回调往 prefs 回写
        segCount.setSelectedSilently(index);

        segCount.setOnSelectListener(i -> {
            resultCount = countOptions.get(i);
            updateCountHint();
            saveScanSettings();
        });
        updateCountHint();
    }

    /**
     * 输出数量的说明文字。
     *
     * <p>必须写明「会变慢」：每个结果都要占一次完整测速预算，选 10 个
     * 单轮测速时间是选 1 个的好几倍。用户不知道代价就会觉得程序卡死了。
     */
    private void updateCountHint() {
        if (txtCountHint == null) return;
        if (resultCount <= 1) {
            txtCountHint.setText("只要最快的一个，出结果最快");
        } else {
            txtCountHint.setText("输出最快的 " + resultCount + " 个，耗时明显增加");
        }
    }

    /**
     * 按核心层给的档位构建测速时长选项。
     *
     * <p>档位来自 {@link Better#speedSeconds()}（"5,10,15"），同 buildCountOptions
     * 一样不在界面里硬编码。
     *
     * <p>为什么要开放这个选项：中国移动等运营商的国际出口对新连接有
     * 「突发不限速」窗口，前几秒跑得飞快、之后掉一个数量级。5 秒测速整段
     * 都落在窗口内，测出来的数字比持续可用带宽高得多 —— 用户会看到
     * 「优选出来很快、实际用起来很慢」。10/15 秒能跨过这个窗口。
     */
    private void buildSpeedSecondOptions() {
        java.util.List<Integer> secs = parseIntCsv(Better.speedSeconds());
        // 核心层没给出档位时兜一个最小可用集，界面不能变成空白
        if (secs.isEmpty()) {
            secs.add(5);
        }
        speedSecondOptions = secs;

        String[] labels = new String[secs.size()];
        for (int i = 0; i < secs.size(); i++) {
            labels[i] = secs.get(i) + "秒";
        }
        segSpeedSeconds.setItems(labels, false);

        int index = secs.indexOf(speedSeconds);
        if (index < 0) {
            index = 0;
            speedSeconds = secs.get(0);
        }
        // 静默：这是恢复初值，不该触发回调往 prefs 回写
        segSpeedSeconds.setSelectedSilently(index);

        segSpeedSeconds.setOnSelectListener(i -> {
            speedSeconds = speedSecondOptions.get(i);
            updateSpeedSecondsHint();
            updateAdvancedSummary();
            saveScanSettings();
        });
        updateSpeedSecondsHint();
    }

    /**
     * 测速时长的说明文字。
     *
     * <p>必须点出「优选快、实际慢」这个症状：这是用户唯一能自己识别的信号，
     * 不说明的话没人知道该动这个选项。
     */
    private void updateSpeedSecondsHint() {
        if (txtSpeedSecondsHint == null) return;
        if (speedSeconds <= 5) {
            txtSpeedSecondsHint.setText("5 秒最快出结果；实际使用比优选结果慢很多时改 10 或 15 秒");
        } else {
            txtSpeedSecondsHint.setText(speedSeconds + " 秒测速，能跨过运营商前几秒的不限速窗口，结果更接近长时间下载的真实速度，扫描会变慢");
        }
    }

    /**
     * 测速源筹码。标识和中文名都来自核心层（{@link Better#speedSources()} /
     * {@link Better#speedSourceLabels()}），不在界面里硬编码 —— 两处各写
     * 一份就会出现「界面能选、核心层不认」的档位。
     *
     * <p>用单选筹码而不是 SegmentedBar：有 5 个选项且中文名长短不一，
     * 分段条会挤成一条细缝。
     *
     * <p>为什么要有这个选项：测速源决定了速度数字有没有意义。旧版固定拿
     * 公共镜像站的静态大文件测，那条链路和用户自己的节点无关，而且运营商
     * （尤其移动国际出口）对不同域名的限速策略差别很大 —— 这正是
     * 「优选很快、实际很慢」的头号原因。
     */
    private void buildSpeedSourceChips() {
        if (layoutSpeedSource == null) return;

        java.util.List<String> ids = parseCsv(Better.speedSources());
        java.util.List<String> labels = parseCsv(Better.speedSourceLabels());
        // 核心层没给或两个列表长度不一致时兜一个最小可用集：界面不能空白，
        // 也不能按错位的下标把标识和显示名配对
        if (ids.isEmpty() || ids.size() != labels.size()) {
            ids = new java.util.ArrayList<>();
            labels = new java.util.ArrayList<>();
            ids.add("auto");
            labels.add("自动选择");
        }
        speedSourceIds = ids;

        // 存的标识在当前档位里不存在（降级安装、核心层改过档位）时回到第一项
        if (!speedSourceIds.contains(speedSource)) {
            speedSource = speedSourceIds.get(0);
        }

        layoutSpeedSource.removeAllViews();
        for (int i = 0; i < speedSourceIds.size(); i++) {
            final String id = speedSourceIds.get(i);
            layoutSpeedSource.addView(makeChip(labels.get(i), id.equals(speedSource), sel -> {
                // 单选：点已选中的那个不取消（否则会出现「一个都没选」的状态，
                // 那时该用哪个源没有答案）。makeChip 已经把它翻成未选中了，
                // syncSpeedSourceChips 负责按 speedSource 把选中态改回来。
                if (!sel) {
                    syncSpeedSourceChips();
                    return false;
                }
                speedSource = id;
                syncSpeedSourceChips();
                updateSpeedSourceHint();
                updateSpeedURLVisibility();
                updateAdvancedSummary();
                saveScanSettings();
                return true;
            }));
        }
        updateSpeedSourceHint();
        updateSpeedURLVisibility();
    }

    /** 把筹码选中态与 speedSource 对齐（单选语义靠这个维持）。 */
    private void syncSpeedSourceChips() {
        if (layoutSpeedSource == null) return;
        for (int i = 0; i < layoutSpeedSource.getChildCount() && i < speedSourceIds.size(); i++) {
            layoutSpeedSource.getChildAt(i)
                    .setSelected(speedSourceIds.get(i).equals(speedSource));
        }
    }

    /** 测速源的说明文字。每个源为什么存在，都要能一句话说清。 */
    private void updateSpeedSourceHint() {
        if (txtSpeedSourceHint == null) return;
        String text;
        switch (speedSource) {
            case "cloudflare":
                text = "Cloudflare 官方测速端点，通用、稳定";
                break;
            case "cm":
                text = "社区提供的测速端点，移动线路通常比官方源更接近实际速度";
                break;
            case "mobile":
                text = "移动专属测速源，直接回 100MB 文件，适合移动宽带/流量卡";
                break;
            case "custom":
                text = "用你自己的域名测速，测的是「CF 边缘回源到你服务器」这条实际会用的链路，最准";
                break;
            default:
                text = "自动选择：先探测你的运营商，移动线路会换用对移动更友好的测速源";
                break;
        }
        txtSpeedSourceHint.setText(text);
    }

    /** 只在选「手动输入」时露出地址输入框。 */
    private void updateSpeedURLVisibility() {
        if (boxSpeedURL == null) return;
        boolean want = "custom".equals(speedSource);
        // 平时露着一个空输入框，用户会以为不填就不测速；
        // 用展开/收起动画而不是直接切 visibility，和折叠区的手感保持一致
        if (want && boxSpeedURL.getVisibility() != View.VISIBLE) {
            Anim.expand(boxSpeedURL);
        } else if (!want && boxSpeedURL.getVisibility() == View.VISIBLE) {
            Anim.collapse(boxSpeedURL);
        }
    }

    /** 解析核心层给的字符串 CSV（如 "auto,cloudflare"），忽略空项。 */
    private java.util.List<String> parseCsv(String csv) {
        java.util.List<String> out = new java.util.ArrayList<>();
        if (csv == null) return out;
        for (String part : csv.split(",")) {
            part = part.trim();
            if (!part.isEmpty()) out.add(part);
        }
        return out;
    }

    /** 解析核心层给的档位 CSV（如 "5,10,15"），忽略空项和非数字。 */
    private java.util.List<Integer> parseIntCsv(String csv) {
        java.util.List<Integer> out = new java.util.ArrayList<>();
        if (csv == null) return out;
        for (String part : csv.split(",")) {
            part = part.trim();
            if (part.isEmpty()) continue;
            try {
                out.add(Integer.parseInt(part));
            } catch (NumberFormatException ignored) {
            }
        }
        return out;
    }

    /**
     * 顶栏版本号。
     *
     * <p>版本号取自 {@code PackageManager} 而不是写死在布局里 —— 写死的那份
     * 必然会忘了跟 build.gradle 一起改，然后界面显示 v1.19 而实际是 v1.20，
     * 排查问题时反而添乱。
     *
     * <p>点一下弹详情：外壳版本、versionCode、核心库版本。核心库版本单独存在
     * 是因为 Android 外壳和 Go 核心是两套独立版本号，理论上可能不一致
     * （比如只重打了包没重编 .aar），出问题时需要分别确认。
     */
    private void setupVersionLabel() {
        if (txtVersion == null) return;

        String appVer = "?";
        long code = 0;
        try {
            android.content.pm.PackageInfo pi = getPackageManager()
                    .getPackageInfo(getPackageName(), 0);
            appVer = pi.versionName;
            code = android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P
                    ? pi.getLongVersionCode() : pi.versionCode;
        } catch (android.content.pm.PackageManager.NameNotFoundException e) {
            // 拿不到自己的包信息属于不该发生的情况，但不值得为此崩掉
        }
        txtVersion.setText("v" + appVer);

        final String finalVer = appVer;
        final long finalCode = code;
        txtVersion.setOnClickListener(v -> {
            String libVer;
            try {
                libVer = Better.version();
            } catch (Throwable t) {
                // 核心库没加载成功时不该让「看版本」这个动作崩掉，
                // 而且这本身就是有用的信息
                libVer = "未加载";
            }
            new android.app.AlertDialog.Builder(this)
                    .setTitle("官方优选 v" + finalVer)
                    .setMessage("应用版本：" + finalVer + "（build " + finalCode + "）\n"
                            + "核心库版本：" + libVer + "\n\n"
                            + "Cloudflare 官方 IP 段优选工具。\n"
                            + "只测连通性与下载速度，不含任何代理功能。\n"
                            + "名字里的「官方」指数据源是 Cloudflare 官方公布的\n"
                            + "IP 段，与 Cloudflare 公司无关。\n\n"
                            + getString(R.string.credits_body))
                    // 三个按钮而不是三个入口控件：顶栏已经挤了「更新数据」和主题
                    // 切换，再塞两个图标会把标题压掉。使用说明和更新日志都是
                    // 「偶尔看一次」的东西，多点一下无所谓。
                    .setNeutralButton("使用说明",
                            (d, w) -> showTextDialog("使用说明", R.string.help_body))
                    .setNegativeButton("更新日志",
                            (d, w) -> showTextDialog("更新日志", R.string.changelog_body))
                    .setPositiveButton("知道了", null)
                    .show();
        });
    }

    /**
     * 弹一段可滚动的长文本。
     *
     * <p>不用 AlertDialog 自带的 message：它超过一屏时在部分 ROM 上不给滚动条，
     * 更新日志和使用说明都是几十行，直接用会看不到后半截。所以自己塞一个
     * ScrollView + TextView 当 view。
     */
    private void showTextDialog(String title, int bodyRes) {
        TextView body = new TextView(this);
        body.setText(getString(bodyRes));
        body.setTextSize(14f);
        body.setTextColor(getColor(R.color.text_primary));
        body.setLineSpacing(0f, 1.25f);
        int pad = dp(20);
        body.setPadding(pad, dp(8), pad, dp(8));
        body.setTextIsSelectable(true);

        android.widget.ScrollView sv = new android.widget.ScrollView(this);
        sv.addView(body);

        new android.app.AlertDialog.Builder(this)
                .setTitle(title)
                .setView(sv)
                .setPositiveButton("关闭", null)
                .show();
    }

    /**
     * 装折叠区。端口和地区各一块，行为完全一样，所以走同一个 helper。
     *
     * <p>默认收起 —— 绝大多数人用默认的 443 + 不限地区，这两块展开要占掉
     * 大半屏。但改过的人下次多半还想看到，所以展开状态存进 prefs。
     *
     * <p>首次进来不能走动画：{@link Anim#expand} 要靠父容器已测量出的宽度
     * 来算目标高度，onCreate 阶段宽度还是 0，会量成一行高然后把 chip 裁掉。
     * 所以初始状态直接设 visibility。
     */
    private void setupCollapsibles() {
        boolean portsOpen = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_PORTS_OPEN, false);
        boolean regionsOpen = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_REGIONS_OPEN, false);
        boolean advancedOpen = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_ADVANCED_OPEN, false);
        boolean rangesOpen = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_RANGES_OPEN, false);

        boxPorts.setVisibility(portsOpen ? View.VISIBLE : View.GONE);
        arrowPorts.setRotation(portsOpen ? 180f : 0f);
        boxRegions.setVisibility(regionsOpen ? View.VISIBLE : View.GONE);
        arrowRegions.setRotation(regionsOpen ? 180f : 0f);
        boxRanges.setVisibility(rangesOpen ? View.VISIBLE : View.GONE);
        arrowRanges.setRotation(rangesOpen ? 180f : 0f);
        boxAdvanced.setVisibility(advancedOpen ? View.VISIBLE : View.GONE);
        arrowAdvanced.setRotation(advancedOpen ? 180f : 0f);

        headerPorts.setOnClickListener(v ->
                toggleSection(boxPorts, arrowPorts, PREFS_PORTS_OPEN));
        headerRegions.setOnClickListener(v ->
                toggleSection(boxRegions, arrowRegions, PREFS_REGIONS_OPEN));
        headerRanges.setOnClickListener(v ->
                toggleSection(boxRanges, arrowRanges, PREFS_RANGES_OPEN));
        headerAdvanced.setOnClickListener(v ->
                toggleSection(boxAdvanced, arrowAdvanced, PREFS_ADVANCED_OPEN));

        // IP 段边打边校验。填错（少个斜杠、粘进中文标点、v4/v6 混填）很容易，
        // 而错误本来只在点了扫描、等完初始化之后才暴露。
        editRanges.addTextChangedListener(new android.text.TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence sq, int st, int c, int a) {
            }

            @Override
            public void onTextChanged(CharSequence sq, int st, int b, int c) {
            }

            @Override
            public void afterTextChanged(android.text.Editable e) {
                validateRanges();
            }
        });

        // 自由输入的地区代码也算在摘要里，不然收起后看到的是过时的值
        editCountries.addTextChangedListener(new android.text.TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence sq, int st, int c, int a) {
            }

            @Override
            public void onTextChanged(CharSequence sq, int st, int b, int c) {
            }

            @Override
            public void afterTextChanged(android.text.Editable e) {
                updateRegionsSummary();
            }
        });

        // 高级选项收起时也要看得出填没填：不然用户会忘了自己填过节点域名，
        // 更糟的是忘了填却以为填了 —— 那项直接决定结果能不能用。
        android.text.TextWatcher advancedWatcher = new android.text.TextWatcher() {
            @Override
            public void beforeTextChanged(CharSequence sq, int st, int c, int a) {
            }

            @Override
            public void onTextChanged(CharSequence sq, int st, int b, int c) {
            }

            @Override
            public void afterTextChanged(android.text.Editable e) {
                updateAdvancedSummary();
            }
        };
        editSNI.addTextChangedListener(advancedWatcher);
        editSpeedURL.addTextChangedListener(advancedWatcher);

        updatePortsSummary();
        updateRegionsSummary();
        updateAdvancedSummary();
        validateRanges();
    }

    /**
     * 预检 IP 段并更新提示与摘要。
     *
     * <p>校验逻辑不在这里重写一遍 —— 走核心层的 {@link Better#previewIPRanges}，
     * 保证「界面说没问题」和「扫描时真能解析」是同一套判断。两处各写一份
     * 必然会出现「界面放过了、扫描才报错」。
     */
    private void validateRanges() {
        if (editRanges == null || txtRangesHint == null) return;
        String raw = editRanges.getText().toString();
        String json = Better.previewIPRanges(raw);

        rangesValid = true;
        String hint;
        int color = R.color.text_secondary;
        try {
            org.json.JSONObject o = new org.json.JSONObject(json);
            if (o.optBoolean("ok", false)) {
                hint = o.optString("summary", "");
            } else {
                rangesValid = false;
                hint = o.optString("error", "IP 段格式不对");
                color = R.color.danger;
            }
        } catch (org.json.JSONException e) {
            // 预检本身出错不该把用户拦住：核心层解析时还会再校验一次
            hint = "";
        }
        txtRangesHint.setText(hint);
        txtRangesHint.setTextColor(getColor(color));
        updateRangesSummary();
    }

    /**
     * 收起状态下标题行右侧显示 IP 段状态。
     *
     * <p>折叠不能把信息藏掉：用户忘了自己填过段、然后奇怪为什么只扫了
     * 几十个子网，这种困惑必须避免。
     */
    private void updateRangesSummary() {
        if (txtRangesSummary == null) return;
        String raw = editRanges.getText().toString().trim();
        if (raw.isEmpty()) {
            txtRangesSummary.setText("官方全部段");
            return;
        }
        if (!rangesValid) {
            txtRangesSummary.setText("填写有误");
            return;
        }
        try {
            org.json.JSONObject o = new org.json.JSONObject(Better.previewIPRanges(raw));
            int v4 = o.optInt("v4", 0);
            int v6 = o.optInt("v6", 0);
            java.util.List<String> parts = new java.util.ArrayList<>();
            if (v4 > 0) parts.add("v4 " + v4 + " 段");
            if (v6 > 0) parts.add("v6 " + v6 + " 段");
            txtRangesSummary.setText(parts.isEmpty()
                    ? "已指定" : android.text.TextUtils.join(" · ", parts));
        } catch (org.json.JSONException e) {
            txtRangesSummary.setText("已指定");
        }
    }

    /**
     * 高级选项收起时的摘要。
     *
     * <p>按「影响结果可信度」排序显示：节点域名没填是最需要提醒的，
     * 它决定了会不会挡掉「握手成功但一发数据就断」的 IP；测速源其次，
     * 它决定速度数字测的是谁的链路；测速时长总是显示，因为它一直在生效。
     */
    private void updateAdvancedSummary() {
        if (txtAdvancedSummary == null) return;
        java.util.List<String> parts = new java.util.ArrayList<>();
        if (editSNI.getText().toString().trim().isEmpty()) {
            parts.add("未填域名");
        } else {
            parts.add("已填域名");
        }
        parts.add(speedSourceLabel());
        parts.add("测速 " + speedSeconds + " 秒");
        txtAdvancedSummary.setText(android.text.TextUtils.join(" · ", parts));
    }

    /**
     * 摘要里显示的测速源名称。
     *
     * <p>选了「手动输入」但地址还没填时说「手动测速未填」而不是「手动输入」——
     * 那种状态下点扫描会直接报错，摘要必须提前露出来。
     */
    private String speedSourceLabel() {
        if ("custom".equals(speedSource)) {
            if (editSpeedURL == null || editSpeedURL.getText().toString().trim().isEmpty()) {
                return "手动测速未填";
            }
            return "自定义测速";
        }
        int idx = speedSourceIds.indexOf(speedSource);
        java.util.List<String> labels = parseCsv(Better.speedSourceLabels());
        if (idx >= 0 && idx < labels.size()) {
            return labels.get(idx);
        }
        return "自动测速源";
    }

    private void toggleSection(View box, View arrow, String prefKey) {
        boolean willOpen = box.getVisibility() != View.VISIBLE;
        // 动画正在跑时 expand/collapse 会拒绝并返回 false；此时箭头和 prefs
        // 都不能改，否则连点两下箭头会停在和实际状态相反的方向。
        boolean accepted = willOpen ? Anim.expand(box) : Anim.collapse(box);
        if (!accepted) return;
        Anim.rotateArrow(arrow, willOpen);
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit().putBoolean(prefKey, willOpen).apply();
    }

    /**
     * 收起状态下标题行右侧显示当前选中的端口。
     *
     * <p>折叠不能把信息藏掉 —— 用户不展开也得知道现在测的是哪些端口，
     * 否则扫出来的 IP 接不上节点时根本无从排查。
     */
    private void updatePortsSummary() {
        if (txtPortsSummary == null) return;
        if (selectedPorts.isEmpty()) {
            txtPortsSummary.setText(checkTLS.isChecked() ? "443" : "80");
            return;
        }
        StringBuilder sb = new StringBuilder();
        int i = 0;
        for (int p : selectedPorts) {
            // 超过 3 个就省略，否则摘要会把标题挤掉
            if (i == 3) {
                sb.append(" +").append(selectedPorts.size() - 3);
                break;
            }
            if (i > 0) sb.append(", ");
            sb.append(p);
            i++;
        }
        txtPortsSummary.setText(sb.toString());
    }

    /** 同上，地区的摘要。不选时明确写「不限」而不是留空。 */
    private void updateRegionsSummary() {
        if (txtRegionsSummary == null) return;
        java.util.List<String> names = new java.util.ArrayList<>();
        for (String[] r : COMMON_REGIONS) {
            if (selectedRegions.contains(r[0])) names.add(r[1]);
        }
        String extra = editCountries == null ? "" : editCountries.getText().toString().trim();
        if (!extra.isEmpty()) names.add(extra.toUpperCase(Locale.ROOT));
        if (names.isEmpty()) {
            txtRegionsSummary.setText("不限");
            return;
        }
        if (names.size() > 3) {
            txtRegionsSummary.setText(String.join("、", names.subList(0, 3))
                    + " +" + (names.size() - 3));
        } else {
            txtRegionsSummary.setText(String.join("、", names));
        }
    }

    /**
     * 按当前 TLS 模式重建端口筹码。
     *
     * 端口列表从核心层取（Better.httpsPorts / httpPorts），不在界面里硬编码：
     * 两处各写一份，改一处忘另一处就会出现「界面能选、核心层不认」的端口。
     */
    private void rebuildPortChips() {
        boolean useTLS = checkTLS.isChecked();
        String csv = useTLS ? Better.httpsPorts() : Better.httpPorts();
        int defaultPort = useTLS ? 443 : 80;

        // 换模式后旧端口不再合法，只保留仍在新列表里的
        java.util.LinkedHashSet<Integer> valid = new java.util.LinkedHashSet<>();
        java.util.List<Integer> ports = new java.util.ArrayList<>();
        for (String s : csv.split(",")) {
            s = s.trim();
            if (s.isEmpty()) continue;
            try {
                int p = Integer.parseInt(s);
                ports.add(p);
                if (selectedPorts.contains(p)) valid.add(p);
            } catch (NumberFormatException ignored) {
            }
        }
        selectedPorts.clear();
        selectedPorts.addAll(valid);
        // 一个都没选就默认勾上标准端口，避免用户以为不选=不测
        if (selectedPorts.isEmpty() && ports.contains(defaultPort)) {
            selectedPorts.add(defaultPort);
        }

        layoutPorts.removeAllViews();
        for (int p : ports) {
            layoutPorts.addView(makeChip(String.valueOf(p), selectedPorts.contains(p), sel -> {
                if (sel) {
                    selectedPorts.add(p);
                } else {
                    selectedPorts.remove(p);
                }
                updatePortHint();
                updatePortsSummary();
                return true;
            }));
        }
        updatePortHint();
        updatePortsSummary();
    }

    /** 端口选得越多，候选数成倍增长，得让用户知道会变慢。 */
    private void updatePortHint() {
        int n = selectedPorts.size();
        if (n == 0) {
            txtPortHint.setText("未选端口，将只测标准端口");
        } else if (n == 1) {
            txtPortHint.setText("选你节点实际使用的端口，多选会变慢");
        } else {
            txtPortHint.setText("已选 " + n + " 个端口，候选数为单端口的 " + n + " 倍，会明显变慢");
        }
    }

    /** 渲染常用地区筹码。不选=不限，这一点靠上面的说明文字交代。 */
    private void renderRegionChips() {
        layoutRegions.removeAllViews();
        for (String[] r : COMMON_REGIONS) {
            final String code = r[0];
            layoutRegions.addView(makeChip(r[1], selectedRegions.contains(code), sel -> {
                if (sel) {
                    selectedRegions.add(code);
                } else {
                    selectedRegions.remove(code);
                }
                updateRegionsSummary();
                return true;
            }));
        }
    }

    /** 可点选的筹码。用 TextView + setSelected，不引入额外依赖。 */
    private TextView makeChip(String label, boolean selected, ChipToggle onToggle) {
        TextView chip = new TextView(this);
        // 间距交给 FlowLayout 统一管（原来靠 marginEnd，换行后行尾会多出空隙）
        chip.setLayoutParams(new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, dp(34)));
        chip.setBackgroundResource(R.drawable.chip_bg);
        chip.setTextColor(getResources().getColorStateList(R.color.chip_text, getTheme()));
        chip.setTextSize(13f);
        chip.setGravity(Gravity.CENTER);
        chip.setPadding(dp(14), 0, dp(14), 0);
        chip.setText(label);
        chip.setSelected(selected);
        chip.setClickable(true);
        chip.setFocusable(true);
        chip.setOnClickListener(v -> {
            boolean next = !v.isSelected();
            v.setSelected(next);
            // 选中时放大回落，取消时不弹：加选是"多了一个"，值得强调；
            // 取消只是回到默认，弹一下反而像操作失败了
            if (next) {
                Anim.segmentSelect(v);
            }
            onToggle.onToggle(next);
        });
        Anim.attachPressScale(chip, 0.93f);
        return chip;
    }

    private interface ChipToggle {
        boolean onToggle(boolean selected);
    }

    /**
     * 汇总地区筛选条件：常用筹码 + 自由输入，去重后拼成 CSV。
     *
     * 输入框允许写任意 cca2，是为了覆盖筹码没列的地区（如 DE、NL）。
     * 不做合法性校验 —— 写错的代码在核心层只会匹配不到，
     * 而错误信息已经会提示「放宽地区重试」。
     */
    private String collectCountries() {
        java.util.LinkedHashSet<String> all = new java.util.LinkedHashSet<>(selectedRegions);
        String extra = editCountries.getText().toString();
        for (String s : extra.split("[,，\\s]+")) {
            s = s.trim().toUpperCase(Locale.ROOT);
            if (!s.isEmpty()) all.add(s);
        }
        return String.join(",", all);
    }

    /** 汇总端口 CSV。空串表示让核心层用默认端口。 */
    private String collectPorts() {
        StringBuilder sb = new StringBuilder();
        for (int p : selectedPorts) {
            if (sb.length() > 0) sb.append(',');
            sb.append(p);
        }
        return sb.toString();
    }

    // cancelCurrentTask 取消扫描或数据更新。两者都走核心层同一个取消通道，
    // 下载过程中的取消检查也认这个信号。
    private void cancelCurrentTask() {
        if (!isRunning.get()) return;
        btnCancel.setEnabled(false);
        btnCancel.setText("正在停止...");
        txtProgress.setText("正在取消...");
        Better.cancelScan();
    }

    private void startScan() {
        if (isRunning.get()) return;

        final boolean v4 = segIPVersion.getSelectedIndex() == 0;
        final boolean useTLS = checkTLS.isChecked();
        final int bandwidth = normalizeBandwidthInput();
        final String ports = collectPorts();
        final String countries = collectCountries();
        final String sni = editSNI.getText().toString().trim();
        final int count = resultCount;
        final int speedSecs = speedSeconds;
        final String speedSrc = speedSource;
        final String speedURL = editSpeedURL.getText().toString().trim();
        final String ipRanges = editRanges.getText().toString().trim();

        // IP 段填错就别开始了：核心层会立刻返回错误，但用户已经看到
        // 「扫描中...」再变回来，不如现在就把光标送到出错的地方
        if (!rangesValid) {
            showToast("IP 段填写有误，请检查后再扫描");
            if (boxRanges.getVisibility() != View.VISIBLE) {
                toggleSection(boxRanges, arrowRanges, PREFS_RANGES_OPEN);
            }
            editRanges.requestFocus();
            return;
        }

        // 选了「手动输入」却没填地址：核心层会直接报错停下，与其让用户
        // 等一轮扫描再看到红字，不如现在就说清楚。
        if ("custom".equals(speedSrc) && speedURL.isEmpty()) {
            showToast("请先填写测速地址，或把测速源改回「自动选择」");
            return;
        }
        editBandwidth.clearFocus();
        hideKeyboard(editBandwidth);
        saveScanSettings();

        btnScan.setText("扫描中...");
        btnScan.setEnabled(false);
        showCancelButton("取消扫描");
        currentIp = "";

        showScanning();
        showProgressText("正在初始化...");

        btnUpdate.setEnabled(false);
        btnClearHistory.setEnabled(false);
        isRunning.set(true);

        // 必须在派发到后台线程之前调用：核心层会在任务开始时重建取消上下文，
        // 这中间用户点取消的话，不先占位就会被抹掉
        Better.beginTask();

        startProgressPolling();

        executor.execute(() -> {
            try {
                String resultJson = Better.getIPs(v4, useTLS, bandwidth, ports, countries, sni, count,
                        speedSecs, speedSrc, speedURL, ipRanges);
                mainHandler.post(() -> onScanResult(resultJson));
            } catch (Exception e) {
                mainHandler.post(() -> showResult("扫描出错: " + e.getMessage()));
            }
        });
    }

    private void startProgressPolling() {
        stopProgressPolling();
        progressPoller = new Runnable() {
            @Override
            public void run() {
                if (!isRunning.get()) return;
                try {
                    String p = Better.getProgress();
                    if (p != null && !p.isEmpty()) {
                        txtProgress.setText(p);
                    }
                } catch (Exception ignored) {
                }
                mainHandler.postDelayed(this, 500);
            }
        };
        mainHandler.postDelayed(progressPoller, 500);
    }

    private void stopProgressPolling() {
        if (progressPoller != null) {
            mainHandler.removeCallbacks(progressPoller);
            progressPoller = null;
        }
    }

    private void onScanResult(String resultJson) {
        stopProgressPolling();
        isRunning.set(false);
        resetButtons();

        if (resultJson == null || resultJson.isEmpty()) {
            showNotice("扫描已取消");
            return;
        }

        try {
            JSONObject json = new JSONObject(resultJson);
            String ip = json.optString("ip", "");
            String error = json.optString("error", "");
            boolean cancelled = json.optBoolean("cancelled", false);

            // 用户主动取消：不能显示成"未找到"，那是误导
            if (cancelled) {
                showNotice(error.isEmpty() ? "扫描已取消" : error);
                // 备选列表必须收掉：留着上一次的结果会让人以为这次也出了结果
                layoutMoreResults.setVisibility(View.GONE);
                return;
            }

            if (ip.isEmpty()) {
                layoutProgress.setVisibility(View.GONE);
                showResult(error.isEmpty() ? "未找到可用 IP" : error);
                return;
            }

            int bandwidth = json.optInt("bandwidth", 0);
            int realBandwidth = json.optInt("realBandwidth", 0);
            int maxSpeed = json.optInt("maxSpeed", 0);
            int latencyMs = json.optInt("latencyMs", 0);
            String dataCenter = json.optString("dataCenter", "");
            String country = json.optString("country", "");
            int port = json.optInt("port", 0);
            // address 是核心层拼好的 IP:端口。用它而不是自己拼，
            // 避免两处格式不一致（尤其 IPv6 要加方括号）
            String address = json.optString("address", "");
            if (address.isEmpty()) address = ip;
            int elapsed = json.optInt("elapsed", 0);
            // 测速配置也要回显：不知道「这个速度是拿什么地址、测了几秒得出的」，
            // 「优选很快、实际很慢」这个问题就永远查不下去
            int speedSecs = json.optInt("speedSeconds", 0);
            String speedTarget = json.optString("speedTarget", "");
            // 自动档探到的运营商：让用户看懂「为什么给我选了这个源」
            String speedISP = json.optString("speedSourceISP", "");
            // 数据源也要回显：用户忘了自己填过 IP 段、然后奇怪为什么
            // 只测了几十个子网，这种困惑必须避免
            boolean usedRanges = json.optBoolean("usingCustomRanges", false);
            String scanTime = formatNow();

            // 复制的必须是带端口的完整地址：只给 IP 就是原来那个
            // closed pipe 问题的来源 —— 用户拿 IP 去接一个跑在别的端口的节点
            currentIp = address;

            String resultText = "地址: " + address + "\n"
                    + "端口: " + (port > 0 ? String.valueOf(port) : "-") + "\n"
                    + "期望带宽: " + bandwidth + " Mbps\n"
                    + "实测带宽: " + realBandwidth + " Mbps\n"
                    + "峰值速度: " + maxSpeed + " kB/s\n"
                    + "往返延迟: " + latencyMs + " ms\n"
                    + "数据中心: " + displayValue(dataCenter)
                    + (country.isEmpty() ? "" : " (" + country + ")") + "\n"
                    + "总计用时: " + elapsed + " 秒"
                    + (speedSecs > 0 ? "\n测速时长: " + speedSecs + " 秒" : "")
                    + (speedTarget.isEmpty() ? "" : "\n测速地址: " + speedTarget)
                    + (speedISP.isEmpty() ? "" : "\n检测运营商: " + speedISP)
                    + "\n数据源: " + (usedRanges ? "你指定的 IP 段" : "官方 IP 段列表");

            showStructuredResult(address, bandwidth, realBandwidth, maxSpeed, latencyMs,
                    dataCenter, country, elapsed);
            renderMoreResults(json.optJSONArray("results"));

            // 有结果时也可能有话要说：未达标（IP 仍可用但没到期望带宽）、
            // 测速地址全部 404、测速文件太小等。这些都会影响用户怎么看
            // 这个速度数字，不能因为「拿到 IP 了」就吞掉。
            if (!error.isEmpty()) {
                showNotice(error);
            }

            saveHistory(scanTime, json, resultText);
            renderHistory();

        } catch (Exception e) {
            showResult("解析结果失败: " + e.getMessage());
        }
    }

    private void updateData() {
        if (isRunning.get()) return;
        isRunning.set(true);
        showScanning();
        showProgressText("正在更新数据...");
        btnScan.setText("更新中...");
        btnScan.setEnabled(false);
        btnUpdate.setEnabled(false);
        btnClearHistory.setEnabled(false);
        // 数据更新同样可以取消：走的是核心层同一个取消通道，
        // 下载卡住时用户得有办法脱身
        showCancelButton("取消更新");

        // 与扫描同理：先占住取消上下文，否则这段窗口里的取消会丢
        Better.beginTask();

        startProgressPolling();

        executor.execute(() -> {
            try {
                // 核心层返回空串表示成功，否则是失败原因。
                // 不能无条件报"数据已更新"——下载失败时那是谎报。
                String err = Better.updateData();
                mainHandler.post(() -> {
                    stopProgressPolling();
                    isRunning.set(false);
                    resetButtons();
                    if (err == null || err.isEmpty()) {
                        layoutProgress.setVisibility(View.GONE);
                        showToast("数据已更新");
                    } else {
                        txtProgressTitle.setText("说明");
                        progressBar.setVisibility(View.GONE);
                        txtProgress.setText(err);
                    }
                });
            } catch (Exception e) {
                mainHandler.post(() -> {
                    stopProgressPolling();
                    progressBar.setVisibility(View.GONE);
                    txtProgressTitle.setText("说明");
                    txtProgress.setText("更新失败: " + e.getMessage());
                    isRunning.set(false);
                    resetButtons();
                });
            }
        });
    }

    private void showCancelButton(String label) {
        btnCancel.setText(label);
        btnCancel.setEnabled(true);
        if (btnCancel.getVisibility() != View.VISIBLE) {
            Anim.fadeInUp(btnCancel, dp(6), 0);
        }
    }

    private void resetButtons() {
        btnScan.setText("开始扫描");
        btnScan.setBackgroundResource(R.drawable.btn_primary_bg);
        btnScan.setEnabled(true);
        btnCancel.setVisibility(View.GONE);
        btnCancel.setEnabled(true);
        btnUpdate.setEnabled(true);
        btnClearHistory.setEnabled(true);
    }

    private void showScanning() {
        // 扫描的进度和结果都在扫描页，从别的页触发时自动跳过去
        if (currentTab != 0) {
            selectTab(0);
        }
        txtProgressTitle.setText("执行状态");
        // 已经显示着就不再重播动画：轮询期间 showScanning 会被反复调到
        if (layoutProgress.getVisibility() != View.VISIBLE) {
            Anim.fadeInUp(layoutProgress, dp(10), 0);
        }
        progressBar.setVisibility(View.VISIBLE);
        progressBar.setIndeterminate(true);
        layoutResult.setVisibility(View.GONE);
    }

    private void showProgressText(String text) {
        txtProgress.setVisibility(View.VISIBLE);
        txtProgress.setText(text);
    }

    // showNotice 在原进度区域显示一条静态说明（取消、未达标等）。
    // 复用进度卡片而不是弹 Toast：这些信息用户需要停下来读，
    // Toast 两秒就没了。
    private void showNotice(String text) {
        if (layoutProgress.getVisibility() != View.VISIBLE) {
            Anim.fadeInUp(layoutProgress, dp(10), 0);
        }
        progressBar.setVisibility(View.GONE);
        txtProgressTitle.setText("说明");
        showProgressText(text);
    }

    private void showResult(String text) {
        layoutProgress.setVisibility(View.GONE);
        layoutMoreResults.setVisibility(View.GONE);
        Anim.fadeInUp(layoutResult, dp(10), 0);
        txtIpValue.setText("未找到可用 IP");
        txtIpValue.setEnabled(false);
        txtTargetBandwidth.setText("-");
        txtRealBandwidth.setText("-");
        txtMaxSpeed.setText("-");
        txtLatency.setText("-");
        txtDataCenter.setText("-");
        txtElapsed.setText("-");
        currentIp = "";
        txtResult.setVisibility(View.VISIBLE);
        txtResult.setText(text);
    }

    private void hideResult() {
        layoutResult.setVisibility(View.GONE);
    }

    private int parseBandwidth() {
        try {
            int val = Integer.parseInt(editBandwidth.getText().toString().trim());
            return val <= 0 ? 1 : val;
        } catch (NumberFormatException e) {
            return 1;
        }
    }

    private int normalizeBandwidthInput() {
        int bandwidth = parseBandwidth();
        String normalized = String.valueOf(bandwidth);
        if (!normalized.equals(editBandwidth.getText().toString())) {
            editBandwidth.setText(normalized);
            editBandwidth.setSelection(editBandwidth.getText().length());
        }
        return bandwidth;
    }

    private void loadScanSettings() {
        boolean useIPv4 = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_USE_IPV4, true);
        boolean useTLS = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getBoolean(PREFS_USE_TLS, false);
        int bandwidth = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getInt(PREFS_BANDWIDTH, 1);
        // 默认 1：多数人只要一个最快的，而多要几个会明显变慢
        resultCount = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getInt(PREFS_RESULT_COUNT, 1);
        // 默认 5 秒：与 v1.15 及以前行为一致，不给老用户改变默认结果
        speedSeconds = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getInt(PREFS_SPEED_SECONDS, 5);
        // 默认 auto：让核心层探运营商挑源，绝大多数人不需要动这个
        speedSource = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_SPEED_SOURCE, "auto");
        if (speedSource == null || speedSource.trim().isEmpty()) {
            speedSource = "auto";
        }

        // 静默设置：这是从 prefs 恢复初值，不该触发"用户选择了"的回调，
        // 否则 onCreate 阶段就会往 prefs 回写一次
        segIPVersion.setSelectedSilently(useIPv4 ? 0 : 1);
        checkTLS.setChecked(useTLS);
        editBandwidth.setText(String.valueOf(bandwidth <= 0 ? 1 : bandwidth));

        // 端口与地区也要记住：每次扫描都重填一遍太折磨人。
        // 注意此时不能调 rebuildPortChips —— 那要等 onCreate 里
        // 把 TLS 监听装好之后统一渲染一次，否则会渲染两遍。
        String ports = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_PORTS, "");
        selectedPorts.clear();
        for (String s : ports.split(",")) {
            s = s.trim();
            if (s.isEmpty()) continue;
            try {
                selectedPorts.add(Integer.parseInt(s));
            } catch (NumberFormatException ignored) {
            }
        }

        String regions = getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_REGIONS, "");
        selectedRegions.clear();
        for (String s : regions.split(",")) {
            s = s.trim();
            if (!s.isEmpty()) selectedRegions.add(s);
        }

        editCountries.setText(getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_COUNTRIES_EXTRA, ""));
        // 节点域名不再折叠，直接回填即可
        editSNI.setText(getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_SNI, ""));
        // 自定义测速地址同理：填过一次的人下次还想用同一个文件
        editSpeedURL.setText(getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_SPEED_URL, ""));
        // 指定的 IP 段更要记住：手打十几条 CIDR 一次就够了
        editRanges.setText(getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_IP_RANGES, ""));
    }

    /**
     * 切换分页。0=优选 1=历史。
     *
     * <p>两页始终存在，只切 visibility：切页不能丢掉用户已填的节点域名、
     * 端口选择和滚动位置，更不能打断正在跑的扫描。
     */
    private void selectTab(int tab) {
        int from = currentTab;
        boolean changed = from != tab || pageScan.getVisibility() == View.GONE;
        currentTab = tab;
        View show = tab == 0 ? pageScan : pageHistory;
        View hide = tab == 0 ? pageHistory : pageScan;
        hide.setVisibility(View.GONE);

        // 只在真的切页时做动画。重复点同一个 tab 还闪一下会显得界面在抽。
        //
        // 横向滑入而不是纵向：两个分页是左右并列的关系（底部导航一左一右），
        // 从下往上飞进来会让人以为是弹出了一个新层。方向跟着导航顺序 ——
        // 去历史页从右边进，回优选页从左边进，和底栏的左右位置对应。
        if (changed) {
            Anim.slideIn(show, tab > from ? dp(20) : dp(-20));
        } else {
            show.setVisibility(View.VISIBLE);
        }

        highlightNav(navScan, R.id.navScanIcon, R.id.navScanLabel, tab == 0);
        highlightNav(navHistory, R.id.navHistoryIcon, R.id.navHistoryLabel, tab == 1);

        if (tab == 0) {
            txtPageTitle.setText("官方优选");
        } else {
            txtPageTitle.setText("历史记录");
            // 进历史页时刷一次：扫描可能在切页之后才完成
            renderHistory();
        }

        // 分隔线要按新页面自己的滚动位置重算：两个页面的 scrollY 各自独立，
        // 不重算的话从滚到一半的页面切到停在顶部的页面，线会留着不消失
        SpringScrollView active = tab == 0 ? pageScan : pageHistory;
        Anim.fadeTo(topBarDivider, active.getScrollY() > dp(4));
    }

    /**
     * 高亮选中的导航项。
     *
     * <p>图标和文字都用 {@code @color/nav_icon} 这个 selector 着色，
     * 靠 {@code setSelected} 驱动 —— 和端口/地区筹码的选中态同一套机制，
     * 不用在代码里写死颜色。
     */
    private void highlightNav(View item, int iconId, int labelId, boolean active) {
        item.setSelected(active);
        ImageView icon = item.findViewById(iconId);
        TextView label = item.findViewById(labelId);
        icon.setSelected(active);
        label.setSelected(active);
        label.setTypeface(null, active ? android.graphics.Typeface.BOLD : android.graphics.Typeface.NORMAL);
    }

    private void saveScanSettings() {
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit()
                .putBoolean(PREFS_USE_IPV4, segIPVersion.getSelectedIndex() == 0)
                .putBoolean(PREFS_USE_TLS, checkTLS.isChecked())
                .putInt(PREFS_BANDWIDTH, normalizeBandwidthInput())
                .putString(PREFS_PORTS, collectPorts())
                .putString(PREFS_REGIONS, String.join(",", selectedRegions))
                .putString(PREFS_COUNTRIES_EXTRA, editCountries.getText().toString().trim())
                .putString(PREFS_SNI, editSNI.getText().toString().trim())
                .putInt(PREFS_RESULT_COUNT, resultCount)
                .putInt(PREFS_SPEED_SECONDS, speedSeconds)
                .putString(PREFS_SPEED_URL, editSpeedURL.getText().toString().trim())
                .putString(PREFS_SPEED_SOURCE, speedSource)
                .putString(PREFS_IP_RANGES, editRanges.getText().toString().trim())
                .apply();
    }

    private static int themeModeIndex = 0; // 0=system, 1=day, 2=night
    // 矢量图标而不是 emoji：emoji 的字形取决于系统字体，颜色和字重
    // 都不受控，跟这套自绘的扁平描边界面对不上。
    private static final int[] THEME_ICONS = {
            R.drawable.ic_theme_auto, R.drawable.ic_theme_day, R.drawable.ic_theme_night};
    /** 图标没有文字，切换后靠 Toast 告诉用户切到了哪一档。 */
    private static final String[] THEME_NAMES = {"跟随系统", "浅色", "深色"};

    private void updateThemeLabel() {
        txtThemeMode.setImageResource(THEME_ICONS[themeModeIndex]);
    }

    private void cycleThemeMode() {
        themeModeIndex = (themeModeIndex + 1) % 3;
        // 持久化
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit().putInt(PREFS_THEME, themeModeIndex).apply();
        switch (themeModeIndex) {
            case 1: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_NO); break;
            case 2: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_YES); break;
            default: AppCompatDelegate.setDefaultNightMode(AppCompatDelegate.MODE_NIGHT_FOLLOW_SYSTEM); break;
        }
        updateThemeLabel();
        showToast("主题：" + THEME_NAMES[themeModeIndex]);
    }

    private void showStructuredResult(String address, int bandwidth, int realBandwidth,
                                      int maxSpeed, int latencyMs, String dataCenter,
                                      String country, int elapsed) {
        layoutProgress.setVisibility(View.GONE);
        boolean firstShow = layoutResult.getVisibility() != View.VISIBLE;
        txtIpValue.setEnabled(true);
        txtIpValue.setText(address);
        txtTargetBandwidth.setText(bandwidth + " Mbps");
        txtRealBandwidth.setText(realBandwidth + " Mbps");
        txtMaxSpeed.setText(maxSpeed + " kB/s");
        txtLatency.setText(latencyMs + " ms");
        // 带上国家代码：地区筛选的结果得能核对，否则用户没法确认筛选生效了
        String dc = displayValue(dataCenter);
        txtDataCenter.setText(country.isEmpty() ? dc : dc + " " + country);
        txtElapsed.setText(elapsed + " 秒");
        txtResult.setVisibility(View.GONE);

        if (firstShow) {
            // 整卡淡入，六个指标块依次错开 40ms 冒出来。扫一次要等几十秒，
            // 结果出现的这一下值得给一点仪式感。
            Anim.fadeInUp(layoutResult, dp(12), 0);
            Anim.staggerIn(new View[]{txtTargetBandwidth, txtRealBandwidth, txtMaxSpeed,
                    txtLatency, txtDataCenter, txtElapsed}, 40);
        } else {
            // 已经显示着（连续扫两次）就只脉冲一下变化的数字，
            // 整卡重播淡入会让人以为界面刷新丢了状态。
            layoutResult.setVisibility(View.VISIBLE);
            Anim.pulse(txtRealBandwidth);
            Anim.pulse(txtLatency);
        }
    }

    /**
     * 渲染备选地址列表。
     *
     * <p>结果卡上方那一大块指标只展示最快的那个；这里列出剩下的。
     * 选「输出 1 个」时整块隐藏，界面和以前一字不差。
     *
     * <p>只显示地址 + 速度 + 延迟：备选是「第一个不好用就换下一个」的东西，
     * 机房和用时对这个决策没帮助，全列出来只会让人多划几屏。
     */
    private void renderMoreResults(JSONArray results) {
        layoutMoreList.removeAllViews();

        // 第 0 条是最快的那个，已经在上面的指标区展示过了，这里从 1 开始
        int extra = results == null ? 0 : results.length() - 1;
        if (extra <= 0) {
            layoutMoreResults.setVisibility(View.GONE);
            return;
        }

        txtMoreResultsTitle.setText("备选地址（" + extra + " 个，点击复制）");

        java.util.List<View> rows = new java.util.ArrayList<>();
        for (int i = 1; i < results.length(); i++) {
            JSONObject item = results.optJSONObject(i);
            if (item == null) continue;
            View row = createResultRow(item, i);
            layoutMoreList.addView(row);
            rows.add(row);
        }
        layoutMoreResults.setVisibility(View.VISIBLE);
        if (!rows.isEmpty()) {
            Anim.staggerIn(rows.toArray(new View[0]), 30);
        }
    }

    /** 备选列表里的一行：序号 + 地址 + 速度/延迟，整行可点复制。 */
    private View createResultRow(JSONObject item, int rank) {
        String ip = item.optString("ip", "");
        String address = item.optString("address", "");
        if (address.isEmpty()) address = ip;
        final String copyTarget = address;
        int maxSpeed = item.optInt("maxSpeed", 0);
        int latencyMs = item.optInt("latencyMs", 0);

        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setBackgroundResource(R.drawable.glass_metric);
        row.setPadding(dp(10), dp(8), dp(10), dp(8));
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.setMargins(0, dp(6), 0, 0);
        row.setLayoutParams(lp);
        row.setClickable(true);
        row.setFocusable(true);
        row.setOnClickListener(v ->
                copyToClipboard("CF-IP", copyTarget, "已复制: " + copyTarget));
        // 用按压反馈而不是点击后回弹：按压是"按住就有反应"，
        // 复制这种瞬时动作用它更贴合，点完才弹会感觉延迟
        Anim.attachPressScale(row, 0.97f);

        TextView rankView = new TextView(this);
        rankView.setText(String.valueOf(rank + 1));
        rankView.setTextColor(getColorCompat(R.color.text_muted));
        rankView.setTextSize(11);
        rankView.setGravity(Gravity.CENTER);
        // 不能用 fixedSizeParams：它会把高度也按 dp 换算，
        // 传 WRAP_CONTENT(-2) 会算成 -3px
        row.addView(rankView, new LinearLayout.LayoutParams(
                dp(18), ViewGroup.LayoutParams.WRAP_CONTENT));

        TextView addrView = new TextView(this);
        addrView.setText(address);
        // 等宽字体：一列地址对不齐的话扫读很费劲
        addrView.setTypeface(Typeface.MONOSPACE, Typeface.BOLD);
        addrView.setTextColor(getColorCompat(R.color.primary));
        addrView.setTextSize(13);
        addrView.setSingleLine(true);
        addrView.setEllipsize(android.text.TextUtils.TruncateAt.END);
        row.addView(addrView, weightedWrapParams());

        TextView metaView = new TextView(this);
        // 速度按 MB/s 显示，和上面指标区的口径一致
        metaView.setText(formatSpeed(maxSpeed) + " · " + latencyMs + "ms");
        metaView.setTextColor(getColorCompat(R.color.success_text));
        metaView.setTextSize(11);
        metaView.setGravity(Gravity.END);
        row.addView(metaView, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        return row;
    }

    /** kB/s 转成人看的单位。上千就用 MB/s，四位数字读起来太费劲。 */
    private String formatSpeed(int kbps) {
        if (kbps >= 1000) {
            return String.format(Locale.ROOT, "%.1f MB/s", kbps / 1024f);
        }
        return kbps + " kB/s";
    }

    private void copyCurrentIp() {
        if (currentIp == null || currentIp.isEmpty()) {
            showToast("暂无可复制的地址");
            return;
        }
        copyToClipboard("CF-IP", currentIp, "已复制: " + currentIp);
    }

    private void copyToClipboard(String label, String text, String toastText) {
        ClipboardManager clipboard = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        ClipData clip = ClipData.newPlainText(label, text);
        clipboard.setPrimaryClip(clip);
        showToast(toastText);
    }

    private void showToast(String message) {
        if (activeToast != null) {
            activeToast.cancel();
        }
        activeToast = Toast.makeText(getApplicationContext(), message, Toast.LENGTH_SHORT);
        activeToast.show();
    }

    /**
     * 写入历史。
     *
     * <p>选了输出 5/10 个时，全部结果都要进历史 —— 只记最快那一条的话，
     * 用户杀掉进程就再也找回不了另外几个备选，而"多拿几个备用"正是他
     * 选这个档位的目的。
     *
     * <p>MAX_HISTORY 是 10，所以选 10 个会正好占满整个历史。这是可以接受的：
     * 本次结果比上一次的旧结果更有用。
     */
    private void saveHistory(String scanTime, JSONObject source, String resultText) {
        try {
            JSONArray next = new JSONArray();
            JSONArray results = source.optJSONArray("results");

            if (results != null && results.length() > 0) {
                for (int i = 0; i < results.length() && next.length() < MAX_HISTORY; i++) {
                    JSONObject r = results.optJSONObject(i);
                    if (r == null) continue;
                    // resultText 是给最快那条用的详细文本，其余留空，
                    // 免得每条历史都塞一份几乎相同的长文本
                    next.put(historyItemOf(scanTime, source, r, i == 0 ? resultText : ""));
                }
            } else {
                // 老核心层不返回 results，回落到顶层字段
                next.put(historyItemOf(scanTime, source, source, resultText));
            }

            JSONArray old = loadHistory();
            for (int i = 0; i < old.length() && next.length() < MAX_HISTORY; i++) {
                next.put(old.getJSONObject(i));
            }
            getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                    .edit()
                    .putString(PREFS_HISTORY, next.toString())
                    .apply();
        } catch (Exception ignored) {
        }
    }

    /**
     * 拼一条历史记录。
     *
     * @param source 整个扫描结果，用来取 bandwidth / elapsed 这类整次共享的字段
     * @param one    单条结果，用来取 ip / 速度 / 延迟这类逐条不同的字段
     */
    private JSONObject historyItemOf(String scanTime, JSONObject source, JSONObject one,
                                     String resultText) throws Exception {
        JSONObject item = new JSONObject();
        item.put("time", scanTime);
        item.put("ip", one.optString("ip", ""));
        item.put("port", one.optInt("port", 0));
        // 历史里存完整地址：只存 IP 的话回看时还得重新猜端口
        item.put("address", one.optString("address", one.optString("ip", "")));
        // 期望带宽和总用时是整次扫描共有的，不在单条结果里
        item.put("bandwidth", source.optInt("bandwidth", 0));
        item.put("elapsed", source.optInt("elapsed", 0));
        item.put("realBandwidth", one.optInt("realBandwidth", 0));
        item.put("maxSpeed", one.optInt("maxSpeed", 0));
        item.put("latencyMs", one.optInt("latencyMs", 0));
        item.put("dataCenter", one.optString("dataCenter", ""));
        item.put("country", one.optString("country", ""));
        item.put("resultText", resultText);
        return item;
    }

    private JSONArray loadHistory() {
        String raw = getSharedPreferences(PREFS_NAME, MODE_PRIVATE).getString(PREFS_HISTORY, "[]");
        try {
            return new JSONArray(raw);
        } catch (Exception e) {
            return new JSONArray();
        }
    }

    private void renderHistory() {
        layoutHistoryList.removeAllViews();
        JSONArray history = loadHistory();
        txtEmptyHistory.setVisibility(history.length() == 0 ? View.VISIBLE : View.GONE);

        java.util.List<View> rows = new java.util.ArrayList<>();
        for (int i = 0; i < history.length(); i++) {
            JSONObject item = history.optJSONObject(i);
            if (item == null) {
                continue;
            }
            View row = createHistoryItem(item, i);
            layoutHistoryList.addView(row);
            rows.add(row);
        }
        // 列表项依次冒出。这里 stepMs 给 30 而不是结果卡的 40：
        // 历史最多 10 条，40ms 会让最后一条等到 400ms 才出现。
        if (!rows.isEmpty()) {
            Anim.staggerIn(rows.toArray(new View[0]), 30);
        }
    }

    private void clearScanHistory() {
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit()
                .remove(PREFS_HISTORY)
                .apply();
        renderHistory();
        showToast("历史记录已清空");
    }

    private void deleteHistoryItem(int index) {
        JSONArray history = loadHistory();
        if (index < 0 || index >= history.length()) {
            return;
        }

        JSONArray next = new JSONArray();
        for (int i = 0; i < history.length(); i++) {
            if (i == index) {
                continue;
            }
            JSONObject item = history.optJSONObject(i);
            if (item != null) {
                next.put(item);
            }
        }

        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit()
                .putString(PREFS_HISTORY, next.toString())
                .apply();
        renderHistory();
        showToast("已删除该条历史记录");
    }

    private View createHistoryItem(JSONObject item, int index) {
        String time = item.optString("time", "");
        String ip = item.optString("ip", "");
        // 老版本的历史没有 address 字段，回落到 ip，否则升级后历史全变空
        final String address = item.optString("address", ip).isEmpty()
                ? ip : item.optString("address", ip);
        int bandwidth = item.optInt("bandwidth", 0);
        int realBandwidth = item.optInt("realBandwidth", 0);
        int maxSpeed = item.optInt("maxSpeed", 0);
        int latencyMs = item.optInt("latencyMs", 0);
        String dataCenter = item.optString("dataCenter", "");
        String country = item.optString("country", "");
        int elapsed = item.optInt("elapsed", 0);

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundResource(R.drawable.glass_metric);
        root.setPadding(dp(12), dp(11), dp(12), dp(11));
        LinearLayout.LayoutParams rootParams = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
        );
        rootParams.setMargins(0, dp(8), 0, 0);
        root.setLayoutParams(rootParams);

        LinearLayout headerRow = new LinearLayout(this);
        headerRow.setOrientation(LinearLayout.HORIZONTAL);
        headerRow.setGravity(Gravity.CENTER_VERTICAL);

        TextView timeView = new TextView(this);
        timeView.setText(time);
        timeView.setTextColor(getColorCompat(R.color.text_secondary));
        timeView.setTextSize(12);
        headerRow.addView(timeView, weightedWrapParams());

        TextView deleteButton = new TextView(this);
        deleteButton.setBackgroundResource(R.drawable.btn_secondary_bg);
        deleteButton.setClickable(true);
        deleteButton.setFocusable(true);
        deleteButton.setGravity(Gravity.CENTER);
        deleteButton.setText("删除");
        deleteButton.setTextColor(getColorCompat(R.color.danger));
        deleteButton.setTextSize(12);
        deleteButton.setOnClickListener(v -> deleteHistoryItem(index));
        Anim.attachPressScale(deleteButton, 0.9f);
        headerRow.addView(deleteButton, fixedSizeParams(48, 30));

        root.addView(headerRow, matchWrapParams());

        TextView ipView = new TextView(this);
        ipView.setText(address);
        ipView.setTextColor(getColorCompat(R.color.primary));
        ipView.setTextSize(18);
        ipView.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        ipView.setGravity(Gravity.CENTER_VERTICAL);
        ipView.setMinHeight(dp(40));
        ipView.setPadding(0, dp(5), 0, dp(4));
        ipView.setClickable(true);
        ipView.setOnClickListener(v -> copyToClipboard("CF-IP", address, "已复制: " + address));
        Anim.attachPressScale(ipView, 0.96f);
        root.addView(ipView, matchWrapParams());

        TextView detailsView = new TextView(this);
        detailsView.setText("实测 " + realBandwidth + " Mbps / 目标 " + bandwidth + " Mbps\n"
                + "峰值 " + maxSpeed + " kB/s / 延迟 " + latencyMs + " ms\n"
                + "数据中心 " + displayValue(dataCenter)
                + (country.isEmpty() ? "" : " " + country)
                + " / 用时 " + elapsed + " 秒");
        detailsView.setTextColor(getColorCompat(R.color.text_secondary));
        detailsView.setTextSize(13);
        detailsView.setLineSpacing(dp(2), 1.0f);
        root.addView(detailsView, matchWrapParams());

        return root;
    }

    private LinearLayout.LayoutParams weightedWrapParams() {
        return new LinearLayout.LayoutParams(
                0,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                1
        );
    }

    private LinearLayout.LayoutParams fixedSizeParams(int widthDp, int heightDp) {
        return new LinearLayout.LayoutParams(
                dp(widthDp),
                dp(heightDp)
        );
    }

    private LinearLayout.LayoutParams matchWrapParams() {
        return new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
        );
    }

    private int getColorCompat(int colorRes) {
        return getResources().getColor(colorRes, getTheme());
    }

    private int dp(int value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }

    private String formatNow() {
        return new SimpleDateFormat("yyyy-MM-dd HH:mm:ss", Locale.getDefault()).format(new Date());
    }

    private String displayValue(String value) {
        return value == null || value.isEmpty() ? "-" : value;
    }

    @Override
    public boolean dispatchTouchEvent(MotionEvent ev) {
        if (ev.getAction() == MotionEvent.ACTION_DOWN && getCurrentFocus() instanceof EditText) {
            View focused = getCurrentFocus();
            int[] location = new int[2];
            focused.getLocationOnScreen(location);
            int x = (int) ev.getRawX();
            int y = (int) ev.getRawY();
            boolean outside = x < location[0]
                    || x > location[0] + focused.getWidth()
                    || y < location[1]
                    || y > location[1] + focused.getHeight();
            if (outside) {
                focused.clearFocus();
                hideKeyboard(focused);
            }
        }
        return super.dispatchTouchEvent(ev);
    }

    @Override
    protected void onPause() {
        super.onPause();
        saveScanSettings();
    }

    private void hideKeyboard(View view) {
        InputMethodManager imm = (InputMethodManager) getSystemService(Context.INPUT_METHOD_SERVICE);
        if (imm != null) {
            imm.hideSoftInputFromWindow(view.getWindowToken(), 0);
        }
    }

    @Override
    public void onBackPressed() {
        exitApp();
    }

    private void exitApp() {
        saveScanSettings();
        stopProgressPolling();
        if (isRunning.get()) {
            Better.cancelScan();
        }
        if (activeToast != null) {
            activeToast.cancel();
            activeToast = null;
        }
        executor.shutdownNow();
        finishAndRemoveTask();
        mainHandler.postDelayed(() -> {
            android.os.Process.killProcess(android.os.Process.myPid());
            System.exit(0);
        }, 150);
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        stopProgressPolling();
        executor.shutdownNow();
    }
}
