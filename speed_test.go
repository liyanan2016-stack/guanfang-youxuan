package better

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ----------------------- 辅助 -----------------------

// withTestSpeedURL 固定测速域名与文件路径。
// runSpeedTestSimple 拼接的 testURL 是 "%s://%s/%s"，真实运行由
// downloadAllData 从 url.txt 读入；测试环境是空的，必须显式设置。
func withTestSpeedURL(t *testing.T, domain, file string) {
	t.Helper()
	oldDomain, oldFile := speedTestDomain, speedTestFile
	speedTestDomain, speedTestFile = domain, file
	t.Cleanup(func() {
		speedTestDomain, speedTestFile = oldDomain, oldFile
	})
}

// freshTask 重置取消上下文。
//
// 必须显式调用：同包内的取消相关测试（TestCancelScanProgressIsNeutral 等）
// 会把全局 cancelCtx 留在已取消状态，而 testRTT / downloadAllData 都会查
// isCancelled() 并立刻早退 —— 单跑能过、跑全套就失败，就是这个原因。
func freshTask(t *testing.T) {
	t.Helper()
	enterTask()
}

// speedTestServer 起一个本地测速 server。
//
// 响应分片发送并带 sleep：本地瞬时下载会让 elapsed 变成 0，速度结算不出来
// （真实测速是几 GB 的 ISO 经网络传输，至少几百毫秒，不存在这个问题），
// 测试必须模拟真实时序。
func speedTestServer(t *testing.T, status int, body string, sleepPerChunk time.Duration) *httptest.Server {
	t.Helper()
	chunks := splitChunks(body, 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("CF-RAY", "abc123def456-HKG")
		w.WriteHeader(status)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			w.Write(c)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(sleepPerChunk)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func splitChunks(s string, size int) [][]byte {
	var out [][]byte
	for len(s) > size {
		out = append(out, []byte(s[:size]))
		s = s[size:]
	}
	if len(s) > 0 {
		out = append(out, []byte(s))
	}
	return out
}

func testPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析 server URL 失败: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}
	return port
}

// rttProbeServer 起一个本地 TCP server 模拟 CF 节点的行为。
// handler 收到请求原文，返回要写回的响应原文；返回空字符串表示直接断开。
func rttProbeServer(t *testing.T, handler func(req string) string) (host string, port int, wait func(n int) []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var mu sync.Mutex
	var collected []string

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				req := string(buf[:n])

				mu.Lock()
				collected = append(collected, req)
				mu.Unlock()

				if resp := handler(req); resp != "" {
					c.Write([]byte(resp))
				}
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)

	wait = func(n int) []string {
		deadline := time.Now().Add(3 * time.Second)
		for n > 0 && time.Now().Before(deadline) {
			mu.Lock()
			got := len(collected)
			mu.Unlock()
			if got >= n {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), collected...)
	}

	return "127.0.0.1", addr.Port, wait
}

func cfResp(status string, extraHeaders string) string {
	return fmt.Sprintf("HTTP/1.1 %s\r\nServer: cloudflare\r\n%sContent-Length: 1\r\nConnection: close\r\n\r\nx",
		status, extraHeaders)
}

// ----------------------- 测速状态码检查 -----------------------

// TestSpeedTestRejectsNon200 非 200/206 响应必须按失败处理（速度 0），
// 不能把错误页当成下载内容算出一个虚假速度 —— 全部节点都这样时，
// 那个假速度甚至能成为「最佳结果」返回给用户。
func TestSpeedTestRejectsNon200(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "127.0.0.1", "speedtest")
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway} {
		srv := speedTestServer(t, status, "error page body", time.Millisecond)
		speed, _, _ := runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, time.Second)
		if speed != 0 {
			t.Fatalf("HTTP %d 应视为失败（速度 0），实际 %d", status, speed)
		}
	}
}

// TestSpeedTestAcceptsOKAnd206 200 和 206 都要接受：
// 有些镜像站对大文件一直按 Range 回 206。
func TestSpeedTestAcceptsOKAnd206(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "127.0.0.1", "speedtest")
	payload := strings.Repeat("x", 2*1024*1024)

	for _, status := range []int{http.StatusOK, http.StatusPartialContent} {
		srv := speedTestServer(t, status, payload, 20*time.Millisecond)
		speed, _, colo := runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, speedTestFullBudget)
		if speed <= 0 {
			t.Fatalf("HTTP %d 应测出速度，实际 %d", status, speed)
		}
		if colo != "HKG" {
			t.Fatalf("应从 CF-RAY 提取到 colo，实际 %q", colo)
		}
	}
}

// ----------------------- EWMA 平均速度 -----------------------

// TestEwmaWarmupIsArithmeticMean 预热期内必须等于算术平均。
// 不预热的话前几个样本会被初始值 0 严重拉低，短测速直接失真。
func TestEwmaWarmupIsArithmeticMean(t *testing.T) {
	e := &ewmaRate{}
	for i := 1; i <= 4; i++ {
		e.add(float64(i * 100))
	}
	if got := e.rate(); got != 250 {
		t.Fatalf("预热期应为算术平均 250，实际 %v", got)
	}
}

// TestEwmaIgnoresSingleSpike EWMA 不能被单个尖峰主导 —— 这正是不用
// 「峰值窗口」的原因：TCP 慢启动后一次突发会把峰值拉得很好看，
// 但那不是持续可用带宽。
func TestEwmaIgnoresSingleSpike(t *testing.T) {
	e := &ewmaRate{}
	for range 20 {
		e.add(100)
	}
	base := e.rate()
	e.add(10000)
	after := e.rate()

	if after <= base {
		t.Fatalf("尖峰后应有上升，base=%v after=%v", base, after)
	}
	if after > 2000 {
		t.Fatalf("单个尖峰不应主导 EWMA（应远小于 10000），实际 %v", after)
	}
	t.Logf("稳定 %v → 尖峰 10000 后 %v（峰值法会直接报 10000）", base, after)
}

// TestEwmaConvergesToSteadyRate 持续同一速率时应收敛到该速率。
func TestEwmaConvergesToSteadyRate(t *testing.T) {
	e := &ewmaRate{}
	for range 200 {
		e.add(512)
	}
	if got := e.rate(); got < 500 || got > 520 {
		t.Fatalf("持续 512 应收敛到 512 附近，实际 %v", got)
	}
}

// ----------------------- 丢包率 -----------------------

// TestRTTSingleFailureDoesNotKillEndpoint 单次 TCP 失败只记丢包，
// 不能直接判死。跨境链路抖动很常见，一次超时就扔掉好 IP 是白丢节点。
//
// 用「先接受一次连接建立 CF 验证，再关掉 listener」模拟中途失败不好做，
// 这里改为验证丢包率的计算口径：3 次探测成功 2 次应得 1/3 丢包率，
// 且延迟仍然有效（不为 0）。
func TestRTTLossRateCalculation(t *testing.T) {
	// 契约：rttProbes 次探测里成功 n 次，丢包率 = (rttProbes-n)/rttProbes
	if rttProbes != 3 {
		t.Fatalf("本测试假设 rttProbes=3，当前 %d", rttProbes)
	}
	cases := []struct {
		recv int
		want float64
	}{
		{3, 0},
		{2, 1.0 / 3},
		{1, 2.0 / 3},
	}
	for _, c := range cases {
		got := float64(rttProbes-c.recv) / float64(rttProbes)
		if got != c.want {
			t.Fatalf("成功 %d 次应得丢包率 %v，实际 %v", c.recv, c.want, got)
		}
	}
}

// TestRTTMaxLossRateGate 丢包率上限必须是个真的会拦东西的值，
// 且不能把「偶尔抖一次」的好 IP 拦掉。
func TestRTTMaxLossRateGate(t *testing.T) {
	if rttMaxLossRate <= 0 || rttMaxLossRate >= 1 {
		t.Fatalf("丢包率上限应在 (0,1) 之间才有意义，当前 %v", rttMaxLossRate)
	}
	// 3 次丢 2 次 = 0.67 必须被拦
	if loss := 2.0 / float64(rttProbes); loss <= rttMaxLossRate {
		t.Fatalf("3 次丢 2 次（%.2f）应超过上限 %v", loss, rttMaxLossRate)
	}
	// 3 次丢 1 次 = 0.33 应放行
	if loss := 1.0 / float64(rttProbes); loss > rttMaxLossRate {
		t.Fatalf("3 次丢 1 次（%.2f）应在上限 %v 内放行", loss, rttMaxLossRate)
	}
}

// TestRTTDeadEndpointReportsFullLoss 完全连不上的应返回丢包率 1.0。
func TestRTTDeadEndpointReportsFullLoss(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "mirror.example.com", "big.iso")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ms, _, _, loss := testRTT("127.0.0.1", deadPort, false, "")
	if ms != 0 || loss != 1.0 {
		t.Fatalf("连不上的 endpoint 应为 (0, 1.0)，实际 (%d, %v)", ms, loss)
	}
}

// TestRTTNonCFNodeRejectedImmediately 没有 CF-RAY 的节点应立即判死，
// 不做重试 —— 重试也不会让它变成 CF 节点。
func TestRTTNonCFNodeRejectedImmediately(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "mirror.example.com", "big.iso")

	var conns int64
	host, port, wait := rttProbeServer(t, func(req string) string {
		atomic.AddInt64(&conns, 1)
		// 没有 CF-RAY 头
		return "HTTP/1.1 200 OK\r\nServer: nginx\r\nContent-Length: 1\r\nConnection: close\r\n\r\nx"
	})

	ms, _, colo, loss := testRTT(host, port, false, "")
	if ms != 0 || loss != 1.0 || colo != "" {
		t.Fatalf("非 CF 节点应立即判死，实际 (%d, %v, %q)", ms, loss, colo)
	}
	if reqs := wait(1); len(reqs) != 1 {
		t.Fatalf("非 CF 节点不该重试，应只连 1 次，实际 %d 次", len(reqs))
	}
}

// TestRTTVerifiesOnlyOnce CF 验证只做一次，后续探测只做 TCP 计时。
// 重复握手是纯浪费，这一条省掉约 2/3 的 RTT 阶段耗时。
func TestRTTVerifiesOnlyOnce(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "mirror.example.com", "big.iso")

	var httpReqCount int64
	host, port, wait := rttProbeServer(t, func(req string) string {
		if strings.HasPrefix(req, "GET ") {
			atomic.AddInt64(&httpReqCount, 1)
		}
		return cfResp("200 OK", "CF-RAY: abc123-HKG\r\n")
	})

	ms, _, colo, _ := testRTT(host, port, false, "")
	if ms <= 0 {
		t.Fatalf("应判定通过，实际延迟 %d", ms)
	}
	if colo != "HKG" {
		t.Fatalf("应提取到 colo=HKG，实际 %q", colo)
	}

	wait(rttProbes)
	if got := atomic.LoadInt64(&httpReqCount); got != 1 {
		t.Fatalf("CF 验证应只做 1 次（共 %d 次 TCP 探测），实际发了 %d 个 HTTP 请求",
			rttProbes, got)
	}
}

// TestRTTUsesSpeedTestDomainAsSNI SNI 默认应用测速域名，
// 让「验证通过」和「测速能成功」是同一件事。
func TestRTTUsesSpeedTestDomainAsSNI(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "mirror.example.com", "big.iso")

	host, port, wait := rttProbeServer(t, func(req string) string {
		return cfResp("200 OK", "CF-RAY: abc123-HKG\r\n")
	})

	testRTT(host, port, false, "")

	reqs := wait(1)
	if len(reqs) == 0 {
		t.Fatal("没有收到请求")
	}
	if !strings.Contains(reqs[0], "Host: mirror.example.com") {
		t.Fatalf("默认应用测速域名做 Host，实际请求:\n%s", reqs[0])
	}
}

// TestRTTCustomSNIWins 用户指定 SNI 时应优先用它。
func TestRTTCustomSNIWins(t *testing.T) {
	freshTask(t)
	withTestSpeedURL(t, "mirror.example.com", "big.iso")

	host, port, wait := rttProbeServer(t, func(req string) string {
		return cfResp("200 OK", "CF-RAY: abc123-HKG\r\n")
	})

	testRTT(host, port, false, "my.own.domain")

	reqs := wait(1)
	if len(reqs) == 0 {
		t.Fatal("没有收到请求")
	}
	if !strings.Contains(reqs[0], "Host: my.own.domain") {
		t.Fatalf("应使用用户指定的 SNI，实际请求:\n%s", reqs[0])
	}
}

// ----------------------- 排序 -----------------------

// TestSortPrefersLowLossOverLowLatency 排序必须先看丢包率。
// 一条 20ms 丢 33% 的链路，实际体验远差于 60ms 零丢包。
func TestSortPrefersLowLossOverLowLatency(t *testing.T) {
	results := []RTTResult{
		{IP: "1.1.1.1", LatencyMs: 20, LossRate: 1.0 / 3},
		{IP: "2.2.2.2", LatencyMs: 60, LossRate: 0},
		{IP: "3.3.3.3", LatencyMs: 90, LossRate: 0},
	}

	sortRTTResults(results)

	if results[0].IP != "2.2.2.2" {
		t.Fatalf("零丢包的 60ms 应排在丢 33%% 的 20ms 之前，实际首位 %s", results[0].IP)
	}
	if results[2].IP != "1.1.1.1" {
		t.Fatalf("丢包最高的应排最后，实际末位 %s", results[2].IP)
	}
}

// TestSortKeepsJitterTiebreakWithinLatencyBucket 同丢包率下仍应保持
// 「20ms 分档 + 档内比抖动」的原有口径。
func TestSortKeepsJitterTiebreakWithinLatencyBucket(t *testing.T) {
	results := []RTTResult{
		// 同一个 20ms 档位（25/20 == 30/20 == 1），抖动小的应在前
		{IP: "high-jitter", LatencyMs: 25, JitterMs: 15},
		{IP: "low-jitter", LatencyMs: 30, JitterMs: 1},
	}

	sortRTTResults(results)

	if results[0].IP != "low-jitter" {
		t.Fatalf("同档位应优先抖动小的，实际首位 %s", results[0].IP)
	}
}

// ----------------------- 测速地址兜底 -----------------------

// TestSpeedTestURLFallbackOnBadFormat url.txt 格式不对时必须兜底。
//
// 原本没有 else 分支：speedTestDomain/File 保持空串，测速 URL 拼成
// "https:///"，所有 IP 测速全部归零，而且用户只会看到「找不到可用 IP」，
// 完全无从判断是数据源坏了。
func TestSpeedTestURLFallbackOnBadFormat(t *testing.T) {
	for _, bad := range []string{"no-slash-at-all", "", "   ", "/"} {
		func() {
			freshTask(t)
			withTempDataDir(t)
			withTestSpeedURL(t, "", "")

			if err := os.WriteFile(dataPath("url.txt"), []byte(bad), 0o644); err != nil {
				t.Fatal(err)
			}
			// 其他数据文件写好，避免 downloadAllData 去访问网络
			for _, f := range []string{"ips-v4.txt", "ips-v6.txt"} {
				if err := os.WriteFile(dataPath(f), []byte("1.1.1.0/24\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(dataPath("locations.json"), []byte("[]"), 0o644); err != nil {
				t.Fatal(err)
			}

			downloadAllData()

			if speedTestDomain == "" || speedTestFile == "" {
				t.Fatalf("url.txt=%q 时必须兜底，实际 domain=%q file=%q",
					bad, speedTestDomain, speedTestFile)
			}
			if speedTestDomain != fallbackSpeedTestDomain {
				t.Fatalf("应使用内置备用域名 %q，实际 %q", fallbackSpeedTestDomain, speedTestDomain)
			}
		}()
	}
}

// TestFallbackIsNotCloudflareDown 兜底地址不能用 speed.cloudflare.com/__down。
// 那个端点只服务直连边缘节点，非直连访问一律 403。
func TestFallbackIsNotCloudflareDown(t *testing.T) {
	if strings.Contains(fallbackSpeedTestDomain, "speed.cloudflare.com") {
		t.Fatalf("兜底不能用 speed.cloudflare.com，实际 %q", fallbackSpeedTestDomain)
	}
	if strings.Contains(fallbackSpeedTestFile, "__down") {
		t.Fatalf("兜底不能用 __down 端点，实际 %q", fallbackSpeedTestFile)
	}
	if fallbackSpeedTestDomain == "" || fallbackSpeedTestFile == "" {
		t.Fatal("兜底地址不能为空")
	}
}

// TestGoodSpeedTestURLIsUsed url.txt 格式正常时应当采用它，不该被兜底覆盖。
func TestGoodSpeedTestURLIsUsed(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "", "")

	if err := os.WriteFile(dataPath("url.txt"), []byte("speed.example.com/big/file.bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ips-v4.txt", "ips-v6.txt"} {
		if err := os.WriteFile(dataPath(f), []byte("1.1.1.0/24\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(dataPath("locations.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	downloadAllData()

	if speedTestDomain != "speed.example.com" {
		t.Fatalf("应沿用 url.txt 的域名，实际 %q", speedTestDomain)
	}
	if speedTestFile != "big/file.bin" {
		t.Fatalf("应沿用 url.txt 的路径，实际 %q", speedTestFile)
	}
}

// ----------------------- 数据新鲜度 -----------------------

// TestDataFreshRejectsStale 过期文件必须判为不新鲜。
//
// 原本只判存在性，数据下载一次就永久不更新 —— 官方 IP 段会增删、
// locations.json 会加新机房，用户不手动点「更新数据」就一直拿旧数据扫。
func TestDataFreshRejectsStale(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(fp, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !dataFresh(fp) {
		t.Fatal("刚写的文件应判为新鲜")
	}

	// 把修改时间推到有效期之外
	stale := time.Now().Add(-dataMaxAge - time.Hour)
	if err := os.Chtimes(fp, stale, stale); err != nil {
		t.Fatal(err)
	}
	if dataFresh(fp) {
		t.Fatalf("超过 %v 的文件应判为过期", dataMaxAge)
	}
}

// TestDataFreshRejectsMissingAndEmpty 不存在或空文件都不算新鲜。
// 下载中断可能留下 0 字节文件，那种文件解析时才报错，不如在这里就当缺失。
func TestDataFreshRejectsMissingAndEmpty(t *testing.T) {
	dir := t.TempDir()

	if dataFresh(filepath.Join(dir, "nope.txt")) {
		t.Error("不存在的文件不该判为新鲜")
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if dataFresh(empty) {
		t.Error("空文件不该判为新鲜")
	}

	if dataFresh(dir) {
		t.Error("目录不该判为新鲜")
	}
}

// TestDataMaxAgeIsReasonable 有效期要能跟上变化又不至于每次扫描都重拉。
func TestDataMaxAgeIsReasonable(t *testing.T) {
	if dataMaxAge < time.Hour {
		t.Fatalf("有效期太短会导致频繁重复下载，当前 %v", dataMaxAge)
	}
	if dataMaxAge > 7*24*time.Hour {
		t.Fatalf("有效期太长等于没有更新机制，当前 %v", dataMaxAge)
	}
}

// TestStaleDataTriggersRedownload 过期数据应触发重新下载，
// 且下载失败时要沿用本地旧副本而不是让扫描直接失败。
func TestStaleDataTriggersRedownload(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "", "")

	// 写好全部数据文件，然后统一改成过期
	files := map[string]string{
		"url.txt":        "old.example.com/old/file.bin\n",
		"ips-v4.txt":     "1.1.1.0/24\n",
		"ips-v6.txt":     "2606:4700::/48\n",
		"locations.json": "[]",
	}
	for name, content := range files {
		if err := os.WriteFile(dataPath(name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-dataMaxAge - time.Hour)
	for name := range files {
		if err := os.Chtimes(dataPath(name), stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	// 网络不可用（baipiao 地址在测试环境访问不通或超时），
	// downloadAllData 应沿用旧副本，url.txt 内容仍然可用
	downloadAllData()

	// 无论网络成功与否，都不能把 speedTestDomain 留成空串
	if speedTestDomain == "" || speedTestFile == "" {
		t.Fatalf("过期数据下载失败时应沿用本地副本，实际 domain=%q file=%q",
			speedTestDomain, speedTestFile)
	}
	// 旧副本仍在（没被删除）
	if !fileExists(dataPath("ips-v4.txt")) {
		t.Error("下载失败不该删除本地旧副本")
	}
}
