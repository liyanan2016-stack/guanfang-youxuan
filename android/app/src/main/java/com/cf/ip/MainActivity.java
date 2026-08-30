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

    private RadioGroup groupIPVersion;
    private RadioButton radioIPv4;
    private RadioButton radioIPv6;
    private Switch checkTLS;
    private EditText editBandwidth;
    private EditText editCountries;
    private EditText editSNI;
    private LinearLayout layoutPorts;
    private LinearLayout layoutRegions;
    private LinearLayout layoutAdvanced;
    private TextView txtAdvancedToggle;
    private TextView txtPortHint;
    private Button btnScan;
    private Button btnCancel;
    private Button btnUpdate;
    private Button btnClearHistory;
    private ProgressBar progressBar;
    private TextView txtProgressTitle;
    private TextView txtProgress;
    private TextView txtResult;
    private TextView txtThemeMode;
    private TextView txtIpValue;
    private TextView txtTargetBandwidth;
    private TextView txtRealBandwidth;
    private TextView txtMaxSpeed;
    private TextView txtLatency;
    private TextView txtDataCenter;
    private TextView txtElapsed;
    private TextView txtEmptyHistory;
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

        groupIPVersion = findViewById(R.id.groupIPVersion);
        radioIPv4 = findViewById(R.id.radioIPv4);
        radioIPv6 = findViewById(R.id.radioIPv6);
        checkTLS = findViewById(R.id.checkTLS);
        editBandwidth = findViewById(R.id.editBandwidth);
        editCountries = findViewById(R.id.editCountries);
        editSNI = findViewById(R.id.editSNI);
        layoutPorts = findViewById(R.id.layoutPorts);
        layoutRegions = findViewById(R.id.layoutRegions);
        layoutAdvanced = findViewById(R.id.layoutAdvanced);
        txtAdvancedToggle = findViewById(R.id.txtAdvancedToggle);
        txtPortHint = findViewById(R.id.txtPortHint);
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
        layoutHistoryList = findViewById(R.id.layoutHistoryList);

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

        txtThemeMode.setOnClickListener(v -> cycleThemeMode());
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
        btnScan.setOnClickListener(v -> startScan());
        btnCancel.setOnClickListener(v -> cancelCurrentTask());
        btnUpdate.setOnClickListener(v -> updateData());
        btnClearHistory.setOnClickListener(v -> clearScanHistory());

        txtAdvancedToggle.setOnClickListener(v -> toggleAdvanced());
        // TLS 开关切换时端口集合完全不同（80 系 vs 443 系），必须重建。
        // 已选的端口在新模式下不合法，留着会让扫描全灭。
        checkTLS.setOnCheckedChangeListener((v, checked) -> rebuildPortChips());
        renderRegionChips();
        rebuildPortChips();

        renderHistory();
    }

    /** 展开/收起高级选项。默认收起：SNI 填错会导致全部测不通。 */
    private void toggleAdvanced() {
        boolean show = layoutAdvanced.getVisibility() != View.VISIBLE;
        layoutAdvanced.setVisibility(show ? View.VISIBLE : View.GONE);
        txtAdvancedToggle.setText(show ? "▾ 高级选项" : "▸ 高级选项");
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
                return true;
            }));
        }
        updatePortHint();
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
                return true;
            }));
        }
    }

    /** 可点选的筹码。用 TextView + setSelected，不引入额外依赖。 */
    private TextView makeChip(String label, boolean selected, ChipToggle onToggle) {
        TextView chip = new TextView(this);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, dp(34));
        lp.setMarginEnd(dp(8));
        chip.setLayoutParams(lp);
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
            onToggle.onToggle(next);
        });
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

        final boolean v4 = radioIPv4.isChecked();
        final boolean useTLS = checkTLS.isChecked();
        final int bandwidth = normalizeBandwidthInput();
        final String ports = collectPorts();
        final String countries = collectCountries();
        final String sni = editSNI.getText().toString().trim();
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
                String resultJson = Better.getIPs(v4, useTLS, bandwidth, ports, countries, sni);
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
            boolean belowTarget = json.optBoolean("belowTarget", false);

            // 用户主动取消：不能显示成"未找到"，那是误导
            if (cancelled) {
                showNotice(error.isEmpty() ? "扫描已取消" : error);
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
                    + "总计用时: " + elapsed + " 秒";

            showStructuredResult(address, bandwidth, realBandwidth, maxSpeed, latencyMs,
                    dataCenter, country, elapsed);

            // 未达标时结果和说明同时给出：IP 仍然可用，
            // 但要让用户知道没到他要的带宽
            if (belowTarget && !error.isEmpty()) {
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
        btnCancel.setVisibility(View.VISIBLE);
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
        txtProgressTitle.setText("执行状态");
        layoutProgress.setVisibility(View.VISIBLE);
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
        layoutProgress.setVisibility(View.VISIBLE);
        progressBar.setVisibility(View.GONE);
        txtProgressTitle.setText("说明");
        showProgressText(text);
    }

    private void showResult(String text) {
        layoutProgress.setVisibility(View.GONE);
        layoutResult.setVisibility(View.VISIBLE);
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

        radioIPv4.setChecked(useIPv4);
        radioIPv6.setChecked(!useIPv4);
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
        editSNI.setText(getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .getString(PREFS_SNI, ""));
        // 上次填过 SNI 就直接展开高级选项，否则用户会以为设置丢了
        if (editSNI.getText().length() > 0) {
            layoutAdvanced.setVisibility(View.VISIBLE);
            txtAdvancedToggle.setText("▾ 高级选项");
        }
    }

    private void saveScanSettings() {
        getSharedPreferences(PREFS_NAME, MODE_PRIVATE)
                .edit()
                .putBoolean(PREFS_USE_IPV4, radioIPv4.isChecked())
                .putBoolean(PREFS_USE_TLS, checkTLS.isChecked())
                .putInt(PREFS_BANDWIDTH, normalizeBandwidthInput())
                .putString(PREFS_PORTS, collectPorts())
                .putString(PREFS_REGIONS, String.join(",", selectedRegions))
                .putString(PREFS_COUNTRIES_EXTRA, editCountries.getText().toString().trim())
                .putString(PREFS_SNI, editSNI.getText().toString().trim())
                .apply();
    }

    private static int themeModeIndex = 0; // 0=system, 1=day, 2=night
    private static final String[] THEME_LABELS = {"🌓", "☀️", "🌙"};

    private void updateThemeLabel() {
        txtThemeMode.setText(THEME_LABELS[themeModeIndex]);
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
        showToast("主题: " + THEME_LABELS[themeModeIndex]);
    }

    private void showStructuredResult(String address, int bandwidth, int realBandwidth,
                                      int maxSpeed, int latencyMs, String dataCenter,
                                      String country, int elapsed) {
        layoutProgress.setVisibility(View.GONE);
        layoutResult.setVisibility(View.VISIBLE);
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

    private void saveHistory(String scanTime, JSONObject source, String resultText) {
        try {
            JSONObject item = new JSONObject();
            item.put("time", scanTime);
            item.put("ip", source.optString("ip", ""));
            item.put("port", source.optInt("port", 0));
            // 历史里存完整地址：只存 IP 的话回看时还得重新猜端口
            item.put("address", source.optString("address", source.optString("ip", "")));
            item.put("bandwidth", source.optInt("bandwidth", 0));
            item.put("realBandwidth", source.optInt("realBandwidth", 0));
            item.put("maxSpeed", source.optInt("maxSpeed", 0));
            item.put("latencyMs", source.optInt("latencyMs", 0));
            item.put("dataCenter", source.optString("dataCenter", ""));
            item.put("country", source.optString("country", ""));
            item.put("elapsed", source.optInt("elapsed", 0));
            item.put("resultText", resultText);

            JSONArray next = new JSONArray();
            next.put(item);
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

        for (int i = 0; i < history.length(); i++) {
            JSONObject item = history.optJSONObject(i);
            if (item == null) {
                continue;
            }
            layoutHistoryList.addView(createHistoryItem(item, i));
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
        root.setBackgroundResource(R.drawable.metric_bg);
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
