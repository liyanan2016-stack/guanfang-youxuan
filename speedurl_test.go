package better

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ----------------------- 自定义测速地址解析 -----------------------

func TestParseSpeedURL(t *testing.T) {
	cases := []struct {
		in         string
		wantDomain string
		wantFile   string
		wantErr    bool
	}{
		// 三种前缀写法都要认：用户从浏览器地址栏复制过来的多半带 scheme
		{"https://a.com/files/100mb.bin", "a.com", "files/100mb.bin", false},
		{"http://a.com/files/100mb.bin", "a.com", "files/100mb.bin", false},
		{"a.com/files/100mb.bin", "a.com", "files/100mb.bin", false},
		// 空串 = 没填，用默认地址，不算错误
		{"", "", "", false},
		{"   ", "", "", false},
		// 查询串必须完整保留：__down?bytes=N 的字节数在里面，
		// 丢了它就变成下载 0 字节
		{"speed.cloudflare.com/__down?bytes=99999999",
			"speed.cloudflare.com", "__down?bytes=99999999", false},
		{"https://cf.090227.xyz/__down?bytes=99999999",
			"cf.090227.xyz", "__down?bytes=99999999", false},
		// 端口要能填，但域名里不能留端口 —— 留着会让 TLS SNI 带上端口
		// 导致握手失败。测速连的是被测 IP + 用户选的端口，URL 里的
		// 端口本来就没意义。
		{"a.com:8443/x.bin", "a.com", "x.bin", false},
		// 只填域名：speed.okl.abrdns.com 这类根路径就是大文件的源确实
		// 存在，放过。真拿到首页 HTML 会被 speedTestMinBytes 诊断兜住。
		{"speed.okl.abrdns.com", "speed.okl.abrdns.com", "", false},
		{"a.com/", "a.com", "", false},
		// 域名缺失
		{"https:///x.bin", "", "", true},
	}
	for _, c := range cases {
		d, f, err := parseSpeedURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSpeedURL(%q) 应报错，实际得到 %q/%q", c.in, d, f)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSpeedURL(%q) 意外报错: %v", c.in, err)
			continue
		}
		if d != c.wantDomain || f != c.wantFile {
			t.Errorf("parseSpeedURL(%q) = %q/%q, want %q/%q", c.in, d, f, c.wantDomain, c.wantFile)
		}
	}
}

// 多余的前导斜杠不该把域名吃掉
func TestParseSpeedURLTrimsExtraSlashes(t *testing.T) {
	d, f, err := parseSpeedURL("//a.com/x/y.bin")
	if err != nil {
		t.Fatalf("意外报错: %v", err)
	}
	if d != "a.com" || f != "x/y.bin" {
		t.Fatalf("= %q/%q, want a.com/x/y.bin", d, f)
	}
}

// ----------------------- 测速地址优先级 -----------------------

// 用户填了自定义地址就必须用它，不能再用 url.txt 下发的公共地址 ——
// 那是「优选很快、实际很慢」的根源：测的是别人域名的速度。
func TestSpeedTestTargetPrefersUserURL(t *testing.T) {
	withTestSpeedURL(t, "public.example", "public/file.iso")

	if d, f := speedTestTarget(); d != "public.example" || f != "public/file.iso" {
		t.Fatalf("未填自定义地址时应用公共地址，实际 %q/%q", d, f)
	}

	userSpeedDomain, userSpeedFile = "mine.example", "big.bin"
	t.Cleanup(func() { userSpeedDomain, userSpeedFile = "", "" })

	if d, f := speedTestTarget(); d != "mine.example" || f != "big.bin" {
		t.Fatalf("填了自定义地址时应优先，实际 %q/%q", d, f)
	}
}

// 只设了域名没设路径是合法的：speed.okl.abrdns.com 这类根路径就是
// 大文件的源确实存在，不能因为路径空就当「没设置」而回落公共地址。
func TestSpeedTestTargetAcceptsRootPath(t *testing.T) {
	withTestSpeedURL(t, "public.example", "public/file.iso")
	userSpeedDomain, userSpeedFile = "mine.example", ""
	t.Cleanup(func() { userSpeedDomain, userSpeedFile = "", "" })

	if d, f := speedTestTarget(); d != "mine.example" || f != "" {
		t.Fatalf("根路径地址应生效，实际 %q/%q", d, f)
	}
}

// 测速实际请求的必须是自定义地址的路径。
// 这是本次修复的核心断言：RTT 验证和测速要打同一个目标。
func TestSpeedTestRequestsUserPath(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "public.example", "public/should-not-be-used")

	payload := strings.Repeat("x", 512*1024)
	srv, paths := speedTestServerRecordingPaths(t, http.StatusOK, payload, 5*time.Millisecond)

	userSpeedDomain, userSpeedFile = "127.0.0.1", "mine/big.bin"
	t.Cleanup(func() { userSpeedDomain, userSpeedFile = "", "" })

	runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, 500*time.Millisecond)

	got := paths()
	if len(got) == 0 {
		t.Fatal("测速没有发出任何请求")
	}
	for _, p := range got {
		if p != "/mine/big.bin" {
			t.Fatalf("请求路径 = %q，want /mine/big.bin（自定义地址必须生效）", p)
		}
	}
}

// ----------------------- 测速时长档位 -----------------------

func TestNormalizeSpeedSeconds(t *testing.T) {
	cases := map[int]int{
		// 0 和负数 = 没指定，用默认 5 秒（与 v1.15 及以前行为一致）
		0: 5, -1: 5,
		// 取「不小于输入的最小档位」：宁可慢也别测不准
		1: 5, 5: 5, 6: 10, 8: 10, 10: 10, 11: 15, 15: 15,
		// 超过最大档位取最大，不让用户踩「填 60 秒扫半小时」的坑
		30: 15, 600: 15,
	}
	for in, want := range cases {
		if got := normalizeSpeedSeconds(in); got != want {
			t.Errorf("normalizeSpeedSeconds(%d) = %d, want %d", in, got, want)
		}
	}
}

// 档位定义只留核心层一份，界面靠这个函数构建选项
func TestSpeedSecondsCSV(t *testing.T) {
	if got := SpeedSeconds(); got != "5,10,15" {
		t.Errorf("SpeedSeconds() = %q, want \"5,10,15\"", got)
	}
}

// 测速时长必须真的作用到下载预算上：这是「跨过运营商突发窗口」的前提。
// 只验证长预算确实下载了更多数据，不断言具体速度（本地环回速度不可控）。
func TestSpeedBudgetAffectsDownloadDuration(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "127.0.0.1", "speedtest")

	payload := strings.Repeat("x", 4*1024*1024)
	srv := speedTestServer(t, http.StatusOK, payload, 10*time.Millisecond)
	port := testPort(t, srv)

	start := time.Now()
	runSpeedTestSimple("127.0.0.1", port, false, 0, 300*time.Millisecond)
	shortElapsed := time.Since(start)

	start = time.Now()
	runSpeedTestSimple("127.0.0.1", port, false, 0, 1200*time.Millisecond)
	longElapsed := time.Since(start)

	if longElapsed <= shortElapsed {
		t.Fatalf("长预算耗时 %v 应明显大于短预算 %v（预算未生效）", longElapsed, shortElapsed)
	}
}

// ----------------------- 测速诊断 -----------------------

// 测速地址全部返回 4xx 时必须直说，而不是让用户对着「未找到可用 IP」猜。
func TestSpeedDiagReportsStatusFailure(t *testing.T) {
	speedDiag.reset()
	for range 3 {
		speedDiag.recordAttempt()
		speedDiag.recordStatus(http.StatusNotFound)
	}
	hint := speedDiag.hint(true)
	if !strings.Contains(hint, "404") {
		t.Fatalf("提示应含状态码，实际 %q", hint)
	}
	if !strings.Contains(hint, "你填的测速地址") {
		t.Fatalf("自定义地址时提示应指向用户输入，实际 %q", hint)
	}
}

func TestSpeedDiagReportsTooSmall(t *testing.T) {
	speedDiag.reset()
	for range 2 {
		speedDiag.recordAttempt()
		speedDiag.recordTooSmall()
	}
	hint := speedDiag.hint(false)
	if !strings.Contains(hint, "太小") {
		t.Fatalf("提示应说明文件太小，实际 %q", hint)
	}
}

// 部分失败不该报「地址有问题」：那是 IP 本身的差异，不是配置错误。
func TestSpeedDiagSilentOnPartialFailure(t *testing.T) {
	speedDiag.reset()
	for range 4 {
		speedDiag.recordAttempt()
	}
	speedDiag.recordStatus(http.StatusForbidden)
	if hint := speedDiag.hint(true); hint != "" {
		t.Fatalf("部分失败不应给出配置类提示，实际 %q", hint)
	}
}

// 一次没测过就没什么可说的
func TestSpeedDiagSilentWithoutAttempts(t *testing.T) {
	speedDiag.reset()
	if hint := speedDiag.hint(true); hint != "" {
		t.Fatalf("无测速尝试时应静默，实际 %q", hint)
	}
}
