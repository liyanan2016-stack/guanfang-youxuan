package better

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSpeedDiagRateLimitTrip 连续 429 达到阈值才熔断。
//
// 不能一撞就停：偶发一次限流很常见，停了会白丢一整轮候选。
func TestSpeedDiagRateLimitTrip(t *testing.T) {
	var d speedDiagnostics
	for i := 0; i < speedRateLimitTrip-1; i++ {
		d.recordAttempt()
		d.recordStatus(http.StatusTooManyRequests)
		if d.rateLimitTripped() {
			t.Fatalf("第 %d 次 429 就熔断了，阈值是 %d", i+1, speedRateLimitTrip)
		}
	}
	d.recordAttempt()
	d.recordStatus(http.StatusTooManyRequests)
	if !d.rateLimitTripped() {
		t.Fatalf("连续 %d 次 429 应熔断", speedRateLimitTrip)
	}
}

// TestSpeedDiagSuccessResetsStreak 成功一次就清掉连续计数。
//
// 否则「429、成功、429、成功、429」会被误判成连续三次限流，
// 明明只是偶发。
func TestSpeedDiagSuccessResetsStreak(t *testing.T) {
	var d speedDiagnostics
	for i := 0; i < 10; i++ {
		d.recordAttempt()
		d.recordStatus(http.StatusTooManyRequests)
		d.recordSuccess()
	}
	if d.rateLimitTripped() {
		t.Fatal("每次 429 之间都有成功，不该熔断")
	}
}

// TestSpeedDiagOtherStatusResetsStreak 别的状态码也打断连续 429。
func TestSpeedDiagOtherStatusResetsStreak(t *testing.T) {
	var d speedDiagnostics
	d.recordAttempt()
	d.recordStatus(http.StatusTooManyRequests)
	d.recordAttempt()
	d.recordStatus(http.StatusNotFound)
	d.recordAttempt()
	d.recordStatus(http.StatusTooManyRequests)
	d.recordAttempt()
	d.recordStatus(http.StatusTooManyRequests)
	if d.rateLimitTripped() {
		t.Fatal("中间夹了个 404，连续计数应被打断")
	}
}

// TestSpeedDiagHintPrioritisesRateLimit 限流提示要优先于其他提示。
//
// 限流会让后面所有判断失真（速度全部偏低、状态码全是 429），
// 而且用户能立刻采取行动（等几分钟 / 换源）。
func TestSpeedDiagHintPrioritisesRateLimit(t *testing.T) {
	var d speedDiagnostics
	for i := 0; i < speedRateLimitTrip; i++ {
		d.recordAttempt()
		d.recordStatus(http.StatusTooManyRequests)
	}
	d.recordTooSmall()

	hint := d.hint(false)
	if hint == "" {
		t.Fatal("熔断后必须给提示")
	}
	if !contains(hint, "429") {
		t.Errorf("提示应点明 429，实际 %q", hint)
	}
}

// TestSpeedDiagHintMentionsPartialRateLimit 有限流但没熔断时也要提醒。
// 速度数字可能偏低，用户得知道这不是 IP 的问题。
func TestSpeedDiagHintMentionsPartialRateLimit(t *testing.T) {
	var d speedDiagnostics
	// 一次 429，其余成功
	d.recordAttempt()
	d.recordStatus(http.StatusTooManyRequests)
	for i := 0; i < 5; i++ {
		d.recordAttempt()
		d.recordSuccess()
	}
	hint := d.hint(false)
	if !contains(hint, "429") {
		t.Errorf("应提醒有限流，实际 %q", hint)
	}
}

// TestSpeedDiagResetClearsRateLimit reset 要把限流状态一起清掉。
// 不清的话上一次扫描的熔断会让下一次扫描一个候选都不测。
func TestSpeedDiagResetClearsRateLimit(t *testing.T) {
	var d speedDiagnostics
	for i := 0; i < speedRateLimitTrip; i++ {
		d.recordAttempt()
		d.recordStatus(http.StatusTooManyRequests)
	}
	d.reset()
	if d.rateLimitTripped() {
		t.Fatal("reset 后不该还处于熔断状态")
	}
	if d.hint(false) != "" {
		t.Fatal("reset 后不该还有提示")
	}
}

// TestRunSpeedTestRecordsRateLimit 429 响应要被计入限流而非普通状态失败。
func TestRunSpeedTestRecordsRateLimit(t *testing.T) {
	freshTask(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	withTestSpeedURL(t, "127.0.0.1", "x")

	speedDiag.reset()
	t.Cleanup(func() { speedDiag.reset() })

	port := testPort(t, srv)
	for i := 0; i < speedRateLimitTrip; i++ {
		speedDiag.recordAttempt()
		runSpeedTestSimple("127.0.0.1", port, false, 0, 500*time.Millisecond)
	}
	if !speedDiag.rateLimitTripped() {
		t.Fatal("连续 429 应触发熔断")
	}
}

// TestRunSpeedTestHonoursBudgetOnStalledBody 预算必须是硬边界。
//
// 之前是在主循环里直接 Read，读完一次才检查预算：一个「TCP 通、TLS 握手过、
// 但数据一个字节都不来」的半死连接能把 Read 挂到 client.Timeout（预算+3 秒）。
// 15 秒预算下最坏 18 秒卡在一个候选上，一轮 10 个就是三分钟。
func TestRunSpeedTestHonoursBudgetOnStalledBody(t *testing.T) {
	freshTask(t)
	// 响应头发出去，body 一个字节都不给，撑住直到测试结束
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "999999999")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-stop
	}))
	// 顺序要紧：srv.Close() 会等所有 handler 返回，而 handler 卡在 <-stop 上。
	// 先放 handler 再 Close，反了就死锁（defer 是 LIFO）。
	defer srv.Close()
	defer close(stop)
	withTestSpeedURL(t, "127.0.0.1", "x")

	budget := 600 * time.Millisecond
	start := time.Now()
	speed, _, _ := runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, budget)
	elapsed := time.Since(start)

	if speed != 0 {
		t.Errorf("一个字节都没来，速度应为 0，实际 %d", speed)
	}
	// 留足余量给建连和调度，但必须远小于「预算 + client.Timeout 3 秒」
	if elapsed > budget+1500*time.Millisecond {
		t.Fatalf("预算 %v 却耗了 %v，预算不是硬边界", budget, elapsed)
	}
}

// TestRunSpeedTestCancelStopsImmediately 取消要立刻生效，不等预算走完。
func TestRunSpeedTestCancelStopsImmediately(t *testing.T) {
	freshTask(t)
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-stop
	}))
	// 同 StalledBody：先放 handler 再 Close，否则 Close 等 handler、
	// handler 等 stop，死锁。
	defer srv.Close()
	defer close(stop)
	withTestSpeedURL(t, "127.0.0.1", "x")

	go func() {
		time.Sleep(300 * time.Millisecond)
		CancelScan()
	}()

	start := time.Now()
	runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, 10*time.Second)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("取消后应立刻返回，实际耗了 %v", elapsed)
	}
}

// TestRunSpeedTestNoGzipInflation 不能让 gzip 透明解压把速度虚高。
//
// Go 默认会加 Accept-Encoding: gzip 并透明解压，totalBytes 数的是解压后的
// 字节数 —— 遇到高度可压缩的内容（一堆 0），速度会被放大几十倍。
func TestRunSpeedTestNoGzipInflation(t *testing.T) {
	freshTask(t)
	var sawGzipRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ae := r.Header.Get("Accept-Encoding"); contains(ae, "gzip") {
			sawGzipRequest = true
		}
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, 32*1024)
		for i := 0; i < 64; i++ {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	withTestSpeedURL(t, "127.0.0.1", "x")

	runSpeedTestSimple("127.0.0.1", testPort(t, srv), false, 0, 500*time.Millisecond)

	if sawGzipRequest {
		t.Fatal("测速请求不该声明接受 gzip，否则解压后的字节数会让速度虚高")
	}
}

// TestSpeedTestGapIsReasonable 候选间隔要在合理区间。
//
// 太小挡不住速率限制，太大会让一轮扫描明显变慢（10 个候选 × 间隔）。
func TestSpeedTestGapIsReasonable(t *testing.T) {
	if speedTestGapMs < 500 {
		t.Errorf("间隔 %dms 太短，挡不住速率限制", speedTestGapMs)
	}
	if speedTestGapMs > 3000 {
		t.Errorf("间隔 %dms 太长，一轮扫描会慢 %d 秒以上",
			speedTestGapMs, speedTestGapMs*10/1000)
	}
}

// contains 是 strings.Contains 的短名字，避免测试里到处 import。
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
