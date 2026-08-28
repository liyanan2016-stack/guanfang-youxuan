package better

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------------- 包级全局变量 -----------------------

// maxScanRounds 单次扫描的最大轮次。
//
// 原版内外两层 for{} 都没有上限：只要达不到目标带宽就无限重试，
// 用户把期望带宽填高一点（比如 900Mbps）就等于永久扫描，只能靠点取消。
// 加上限之后轮次用尽会返回历轮最佳结果，而不是空手而归。
const maxScanRounds = 10

// sampleSize 每轮从子网列表中抽取多少个子网（每个子网生成 1 个测试 IP）
const sampleSize = 100

// maxSpeedTestCount 每轮最多对多少个低延迟 IP 做测速。
// 测速比 RTT 慢得多，不设上限会让单轮耗时失控。
const maxSpeedTestCount = 10

// defaultTaskNum RTT 测试的并发数。
// 原版硬编码在 GetIPs 里，提出来便于按平台调整——
// 手机上并发过高会导致大量连接超时，反而更慢。
const defaultTaskNum = 50

// maxBandwidthMbps 期望带宽上限。填得再高也只是白跑满 10 轮。
const maxBandwidthMbps = 1000

var (
	dataDir         string
	randomMu        sync.Mutex
	randomGenerator = rand.New(rand.NewSource(time.Now().UnixNano()))
	locationMap     map[string]location
	locationMu      sync.RWMutex
	speedTestDomain string
	speedTestFile   string
	progress        string
	progressMu      sync.Mutex
	cancelCtx       context.Context
	cancelCancel    context.CancelFunc
	cancelMu        sync.Mutex
	// taskRequested 表示 BeginTask 已经建好上下文、任务还没接手。
	// 用来保住「点了开始但任务还没跑起来」这段窗口里到达的取消。
	taskRequested bool
)

func scanCtx() context.Context {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	if cancelCtx != nil {
		return cancelCtx
	}
	return context.Background()
}

type location struct {
	Iata   string  `json:"iata"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Cca2   string  `json:"cca2"`
	Region string  `json:"region"`
	City   string  `json:"city"`
}

// ----------------------- 工具函数 -----------------------

func dataPath(name string) string {
	if dataDir == "" {
		return name
	}
	return filepath.Join(dataDir, name)
}

var downloadClient = &http.Client{Timeout: 8 * time.Second}

func timeNow() time.Time {
	return time.Now()
}

func timeSince(t time.Time) time.Duration {
	return time.Since(t)
}

func getURLContent(targetURL string) (string, error) {
	req, _ := http.NewRequestWithContext(scanCtx(), "GET", targetURL, nil)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func getFileContent(filename string) (string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func saveToFile(filename, content string) error {
	dir := filepath.Dir(filename)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(filename, []byte(content), 0644)
}

func removeFile(path string) {
	os.Remove(path)
}

// fileExists 判断文件是否存在且非空。
// 只判存在是不够的：下载中断可能留下 0 字节的空文件，
// 那种文件解析时才报错，不如在这里就当作缺失。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

func parseIPList(content string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var ipList []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			ipList = append(ipList, line)
		}
	}
	return ipList
}

func nextRandomIntn(n int) int {
	randomMu.Lock()
	defer randomMu.Unlock()
	return randomGenerator.Intn(n)
}

func getRandomIPv4s(ipList []string) []string {
	var randomIPs []string
	for _, subnet := range ipList {
		subnet = strings.TrimSpace(subnet)
		if subnet == "" {
			continue
		}
		if idx := strings.Index(subnet, "/"); idx >= 0 {
			subnet = subnet[:idx]
		}
		octets := strings.Split(subnet, ".")
		if len(octets) == 4 {
			octets[3] = fmt.Sprintf("%d", nextRandomIntn(256))
			randomIPs = append(randomIPs, strings.Join(octets, "."))
		}
	}
	return randomIPs
}

func getRandomIPv6s(ipList []string) []string {
	var randomIPs []string
	for _, subnet := range ipList {
		subnet = strings.TrimSpace(subnet)
		if subnet == "" {
			continue
		}

		// 前缀长度决定能随机多少位。原版固定保留前 3 段（48 位）
		// 并随机后 5 段，对 CF 实际使用的 /48 子网来说恰好正确，
		// 但一旦上游给出 /32 或 /64，随机范围就会错——
		// /64 会随机掉子网自己的位，拼出根本不属于 CF 的地址。
		prefixLen := 48
		if idx := strings.Index(subnet, "/"); idx >= 0 {
			if n, err := strconv.Atoi(subnet[idx+1:]); err == nil && n > 0 && n <= 128 {
				prefixLen = n
			}
			subnet = subnet[:idx]
		}

		// 展开 :: 压缩，确保有 8 段
		if strings.Contains(subnet, "::") {
			parts := strings.Split(subnet, "::")
			left := strings.Split(parts[0], ":")
			var right []string
			if len(parts) > 1 && parts[1] != "" {
				right = strings.Split(parts[1], ":")
			}
			missing := 8 - len(left) - len(right)
			sections := left
			for range missing {
				sections = append(sections, "0")
			}
			sections = append(sections, right...)
			subnet = strings.Join(sections, ":")
		}

		sections := strings.Split(subnet, ":")
		if len(sections) != 8 {
			continue
		}

		// 保留前缀覆盖到的完整段（向上取整到 16 位边界），其余随机。
		// 至少保留 1 段，至多保留 7 段（留一段可随机）。
		keep := (prefixLen + 15) / 16
		if keep < 1 {
			keep = 1
		}
		if keep > 7 {
			keep = 7
		}
		for i := keep; i < 8; i++ {
			sections[i] = fmt.Sprintf("%x", nextRandomIntn(65536))
		}
		randomIPs = append(randomIPs, strings.Join(sections, ":"))
	}
	return randomIPs
}

// randomSample 从列表中随机抽取 n 个元素
func randomSample(list []string, n int) []string {
	shuffled := make([]string, len(list))
	copy(shuffled, list)
	randomMu.Lock()
	randomGenerator.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	randomMu.Unlock()
	if n > len(shuffled) {
		n = len(shuffled)
	}
	return shuffled[:n]
}

// ----------------------- 子网抽样器 -----------------------

// subnetSampler 单次扫描内的子网抽样器。
//
// 原版每轮调 randomSample 重新洗牌取前 100，轮与轮之间没有记忆，
// 会反复抽到同一批子网。改成「洗一次牌 + 游标推进」后，
// 同一次扫描内不会重复测同一个子网，跨轮零重复是结构保证。
//
// 生命周期只有一次扫描：由 cloudflareTest 局部创建，返回即释放。
// 不落盘、不跨扫描——重新点扫描时全部子网重新可选，
// 不会因为某个子网这次没测出好结果就永久排除它。
type subnetSampler struct {
	order  []string
	cursor int
}

func newSubnetSampler(list []string) *subnetSampler {
	order := make([]string, len(list))
	copy(order, list)

	randomMu.Lock()
	randomGenerator.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})
	randomMu.Unlock()

	return &subnetSampler{order: order}
}

// next 取下一批最多 n 个子网。返回 nil 表示全部子网已取完。
func (s *subnetSampler) next(n int) []string {
	if s.cursor >= len(s.order) {
		return nil
	}
	end := s.cursor + n
	if end > len(s.order) {
		end = len(s.order)
	}
	batch := s.order[s.cursor:end]
	s.cursor = end
	return batch
}

func (s *subnetSampler) total() int { return len(s.order) }
func (s *subnetSampler) used() int  { return s.cursor }

// ----------------------- 数据下载 -----------------------

// downloadAllData 确保所有数据文件存在，缺失则自动下载
func downloadAllData() {
	urlFilename := dataPath("url.txt")
	if _, err := os.Stat(urlFilename); os.IsNotExist(err) {
		if isCancelled() {
			return
		}
		setProgress("正在下载测速 URL...")
		content, err := getURLContent("https://www.baipiao.eu.org/cloudflare/url")
		if err != nil {
			setProgress("下载测速 URL 失败: " + err.Error())
			return
		}
		if err := saveToFile(urlFilename, content); err != nil {
			setProgress("保存测速 URL 失败: " + err.Error())
			return
		}
	}

	if isCancelled() {
		return
	}

	content, err := getFileContent(urlFilename)
	if err != nil {
		setProgress("读取测速 URL 失败: " + err.Error())
		return
	}
	content = strings.TrimSpace(content)
	parts := strings.SplitN(content, "/", 2)
	if len(parts) == 2 {
		speedTestDomain = parts[0]
		speedTestFile = parts[1]
	}

	for _, item := range []struct{ file, url string }{
		{"ips-v4.txt", "https://www.baipiao.eu.org/cloudflare/ips-v4"},
		{"ips-v6.txt", "https://www.baipiao.eu.org/cloudflare/ips-v6"},
	} {
		if isCancelled() {
			return
		}
		fp := dataPath(item.file)
		if _, err := os.Stat(fp); os.IsNotExist(err) {
			setProgress("正在下载 IP 列表: " + item.file)
			c, err := getURLContent(item.url)
			if err != nil {
				setProgress("下载 IP 列表失败: " + err.Error())
				return
			}
			if err := saveToFile(fp, c); err != nil {
				setProgress("保存 IP 列表失败: " + err.Error())
				return
			}
		}
	}

	if isCancelled() {
		return
	}
	fp := dataPath("locations.json")
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		setProgress("正在下载位置信息...")
		req, _ := http.NewRequestWithContext(scanCtx(), "GET", "https://www.baipiao.eu.org/cloudflare/locations", nil)
		resp, err := downloadClient.Do(req)
		if err != nil {
			setProgress("获取位置信息失败: " + err.Error())
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
		resp.Body.Close()
		if err != nil {
			setProgress("读取响应内容失败: " + err.Error())
			return
		}
		if err := saveToFile(fp, string(body)); err != nil {
			setProgress("保存位置信息失败: " + err.Error())
			return
		}
	}
}

// initLocations 初始化数据中心位置信息
func initLocations() {
	downloadAllData()

	if isCancelled() {
		return
	}
	fp := dataPath("locations.json")
	body, err := os.ReadFile(fp)
	if err != nil {
		setProgress("读取位置文件失败: " + err.Error())
		return
	}

	var locations []location
	if err := json.Unmarshal(body, &locations); err != nil {
		setProgress("解析位置信息 JSON 失败: " + err.Error())
		return
	}

	loadedMap := make(map[string]location)
	for _, loc := range locations {
		loadedMap[loc.Iata] = loc
	}

	locationMu.Lock()
	locationMap = loadedMap
	locationMu.Unlock()

	setProgress(fmt.Sprintf("已加载 %d 个数据中心位置信息", len(loadedMap)))
}

// ----------------------- RTT 测试 -----------------------

// RTTResult RTT 测试结果
type RTTResult struct {
	IP        string
	LatencyMs int
}

// testRTT 测试单个 IP 的 RTT（TCP 连接 + 验证 CF-RAY）
func testRTT(ip string, useTLS bool) int {
	port := 80
	if useTLS {
		port = 443
	}

	var totalMs int
	for range 3 {
		start := time.Now()
		var d = net.Dialer{Timeout: 1 * time.Second}
		conn, err := d.DialContext(scanCtx(), "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err != nil {
			return 0
		}
		tcpDuration := time.Since(start)

		conn.SetDeadline(start.Add(1 * time.Second))

		var rwc net.Conn = conn
		if useTLS {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: "cloudflare.com", InsecureSkipVerify: true})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return 0
			}
			rwc = tlsConn
		}

		reqStr := "GET / HTTP/1.1\r\nHost: cloudflare.com\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n\r\n"
		_, err = rwc.Write([]byte(reqStr))
		if err != nil {
			rwc.Close()
			return 0
		}

		reader := bufio.NewReader(rwc)
		resp, err := http.ReadResponse(reader, nil)
		rwc.Close()
		if err != nil {
			return 0
		}
		resp.Body.Close()

		if resp.Header.Get("CF-RAY") == "" {
			return 0
		}

		totalMs += int(tcpDuration.Milliseconds())
	}

	return totalMs / 3
}

// runRTTTest 运行 RTT 测试（并发，带进度显示）
func runRTTTest(ipList []string, taskNum int, useTLS bool) []RTTResult {
	if len(ipList) < taskNum {
		taskNum = len(ipList)
	}

	var wg sync.WaitGroup
	resultChan := make(chan RTTResult, len(ipList))
	thread := make(chan struct{}, taskNum)
	var count int
	var mu sync.Mutex
	total := len(ipList)

	for _, ip := range ipList {
		if isCancelled() {
			break
		}
		wg.Add(1)
		// 并发满时这里会阻塞，取消后必须能立刻退出，
		// 否则用户点了取消还要等已排队的 IP 全部测完
		select {
		case thread <- struct{}{}:
		case <-scanCtx().Done():
			wg.Done()
			goto collect
		}
		go func(ip string) {
			defer func() {
				<-thread
				wg.Done()
				mu.Lock()
				count++
				current := count
				mu.Unlock()
				if current%10 == 0 || current == total {
					setProgress(fmt.Sprintf("RTT 测试进度: %d/%d", current, total))
				}
			}()

			if isCancelled() {
				return
			}
			avgMs := testRTT(ip, useTLS)
			if avgMs > 0 {
				resultChan <- RTTResult{IP: ip, LatencyMs: avgMs}
			}
		}(ip)
	}

collect:
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var results []RTTResult
	for r := range resultChan {
		results = append(results, r)
	}

	if isCancelled() {
		return nil
	}
	// 按最小延迟排序，最多保留前 maxSpeedTestCount 个进入速度测试
	sort.Slice(results, func(i, j int) bool {
		return results[i].LatencyMs < results[j].LatencyMs
	})

	if len(results) > maxSpeedTestCount {
		setProgress(fmt.Sprintf("RTT 测试完成，%d/%d 个 IP 有效，保留延迟最低的 %d 个",
			len(results), total, maxSpeedTestCount))
		results = results[:maxSpeedTestCount]
	} else {
		setProgress(fmt.Sprintf("RTT 测试完成，%d/%d 个 IP 有效", len(results), total))
	}
	return results
}

// ----------------------- 速度测试 -----------------------

// runSpeedTestSimple 简单速度测试，返回 (峰值速度 kB/s, TCP延迟ms, 三字码头)
func runSpeedTestSimple(ip string, port int, useTLS bool) (int, int, string) {
	var tcpMs int
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			start := time.Now()
			conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
			if err == nil {
				tcpMs = int(time.Since(start).Milliseconds())
			}
			return conn, err
		},
	}
	if useTLS {
		transport.TLSClientConfig = &tls.Config{ServerName: speedTestDomain}
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	testURL := fmt.Sprintf("%s://%s/%s", scheme, speedTestDomain, speedTestFile)

	req, _ := http.NewRequestWithContext(scanCtx(), "GET", testURL, nil)
	reqStart := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, ""
	}
	defer resp.Body.Close()

	cfRay := resp.Header.Get("CF-RAY")
	dataCenter := extractDataCenter(cfRay)

	buf := make([]byte, 32*1024)
	var totalBytes int64
	var windowBytes int64
	windowStart := time.Now()
	maxSpeed := 0
	for {
		n, err := resp.Body.Read(buf)
		totalBytes += int64(n)
		windowBytes += int64(n)

		// 先结算窗口再判断错误。
		// 原版在 err != nil 时直接 break，导致最后一个不满 1 秒的窗口
		// 永远不会被统计——如果整个下载在 1 秒内结束（小文件或提前断流），
		// maxSpeed 会是 0，明明下载成功却报速度为零。
		elapsed := time.Since(windowStart).Seconds()
		if elapsed >= 1.0 || err != nil {
			if elapsed > 0 && windowBytes > 0 {
				speedKB := int(float64(windowBytes) / 1024 / elapsed)
				if speedKB > maxSpeed {
					maxSpeed = speedKB
				}
			}
			windowBytes = 0
			windowStart = time.Now()
		}

		if err != nil {
			break
		}
	}

	// 整个下载不足 1 秒时上面的窗口结算可能因 elapsed 太小而失真，
	// 用总量兜底算一次平均速度，取两者较大值。
	if totalBytes > 0 {
		if avg := int(float64(totalBytes) / 1024 / time.Since(reqStart).Seconds()); avg > maxSpeed {
			maxSpeed = avg
		}
	}

	return maxSpeed, tcpMs, dataCenter
}

// extractDataCenter 从 CF-RAY 头提取三字码头
func extractDataCenter(cfRay string) string {
	if cfRay == "" {
		return ""
	}
	parts := strings.Split(cfRay, "-")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// lookupDataCenter 查找数据中心名称
func lookupDataCenter(colo string) string {
	locationMu.RLock()
	loc := locationMap[colo]
	locationMu.RUnlock()

	if loc.City != "" {
		return loc.City
	}
	return colo
}

// ----------------------- 核心测试逻辑 -----------------------

// testOutcome 一次扫描的结果。
// 除了优选 IP 本身，还带上「实际测了多少」的统计，
// 这样界面能给出准确文案，而不是笼统地说"未找到"。
type testOutcome struct {
	IP         string
	MaxSpeed   int
	LatencyMs  int
	DataCenter string

	RoundsRun     int  // 实际跑了几轮
	Tested        int  // 实际测过多少个子网
	PoolSize      int  // 子网总数
	PoolExhausted bool // true = 全部子网已测完（而非轮次用尽）
	BelowTarget   bool // true = 有结果但未达到期望带宽
}

// cloudflareTest 核心测试逻辑
func cloudflareTest(ipType int, useTLS bool, taskNum int, speed int) testOutcome {
	initLocations()
	if isCancelled() {
		return testOutcome{}
	}
	filename := dataPath("ips-v4.txt")
	if ipType == 6 {
		filename = dataPath("ips-v6.txt")
	}
	content, err := getFileContent(filename)
	if err != nil {
		setProgress("读取 IP 列表失败: " + err.Error())
		return testOutcome{}
	}
	ipList := parseIPList(content)
	if len(ipList) == 0 {
		setProgress("子网列表为空，请点击「更新数据」重新下载")
		return testOutcome{}
	}
	setProgress(fmt.Sprintf("正在从 %d 个子网中随机生成 IP...", len(ipList)))

	batchSize := sampleSize
	if len(ipList) < batchSize {
		batchSize = len(ipList)
	}

	// 一次洗牌 + 游标推进：同一次扫描内不会重复测同一个子网
	sampler := newSubnetSampler(ipList)

	// 记录历轮中实测最快的 IP。轮次用尽仍未达标时把它返回，
	// 至少让用户拿到一个可用结果，而不是空手而归。
	var best testOutcome

	roundsRun := 0
	poolExhausted := false

	for round := 1; round <= maxScanRounds; round++ {
		if isCancelled() {
			setProgress("扫描已取消")
			return testOutcome{}
		}

		var rttResults []RTTResult
		// 整批 RTT 全丢包时继续取下一批，直到有结果或子网取完。
		// 随机拼出来的 IP 大部分是死的，这个重试是必要的，
		// 但必须有出口——原版这里是 for{} 无上限。
		for {
			sampled := sampler.next(batchSize)
			if sampled == nil {
				poolExhausted = true
				break
			}
			roundsRun = round

			var testIPs []string
			if ipType == 6 {
				testIPs = getRandomIPv6s(sampled)
			} else {
				testIPs = getRandomIPv4s(sampled)
			}

			setProgress(fmt.Sprintf("第 %d/%d 轮：已生成 %d 个测试 IP（累计 %d/%d 子网），开始 RTT 测试...",
				round, maxScanRounds, len(testIPs), sampler.used(), sampler.total()))

			rttResults = runRTTTest(testIPs, taskNum, useTLS)
			if isCancelled() {
				break
			}
			if len(rttResults) > 0 {
				break
			}
			setProgress("当前这批 IP 都存在 RTT 丢包，换下一批子网继续...")
		}

		if isCancelled() {
			setProgress("扫描已取消")
			return testOutcome{}
		}
		if poolExhausted {
			break
		}

		// 速度测试：依次测试，满足带宽目标立即返回
		for _, r := range rttResults {
			if isCancelled() {
				setProgress("扫描已取消")
				return testOutcome{}
			}

			setProgress(fmt.Sprintf("正在测速 %s (延迟 %dms)", r.IP, r.LatencyMs))
			speedPort := 80
			if useTLS {
				speedPort = 443
			}
			maxSpeed, tcpMs, dc := runSpeedTestSimple(r.IP, speedPort, useTLS)
			dcName := dc
			if dc != "" {
				dcName = lookupDataCenter(dc)
			}
			setProgress(fmt.Sprintf("%s 峰值速度 %d kB/s, 数据中心 %s", r.IP, maxSpeed, dcName))

			// 带宽达标 → 立即返回
			if maxSpeed >= speed {
				setProgress(fmt.Sprintf("找到优选 IP: %s, 速度 %d kB/s, 延迟 %dms", r.IP, maxSpeed, tcpMs))
				return testOutcome{
					IP: r.IP, MaxSpeed: maxSpeed, LatencyMs: tcpMs, DataCenter: dcName,
					RoundsRun: round, Tested: sampler.used(), PoolSize: sampler.total(),
				}
			}

			if maxSpeed > best.MaxSpeed {
				best = testOutcome{IP: r.IP, MaxSpeed: maxSpeed, LatencyMs: tcpMs, DataCenter: dcName}
			}
		}

		// 子网刚好取完，没有下一批可测，不必再走剩下的轮次
		if sampler.used() >= sampler.total() {
			poolExhausted = true
			break
		}

		setProgress(fmt.Sprintf("第 %d/%d 轮未达到期望带宽，继续下一轮...", round, maxScanRounds))
	}

	best.RoundsRun = roundsRun
	best.Tested = sampler.used()
	best.PoolSize = sampler.total()
	best.PoolExhausted = poolExhausted
	best.BelowTarget = best.IP != ""

	if best.IP != "" {
		if poolExhausted {
			setProgress(fmt.Sprintf("%d 个子网已全部测完仍未达标，返回最佳结果 %s (%d kB/s)",
				sampler.total(), best.IP, best.MaxSpeed))
		} else {
			setProgress(fmt.Sprintf("已尝试 %d 轮仍未达标，返回最佳结果 %s (%d kB/s)",
				roundsRun, best.IP, best.MaxSpeed))
		}
	}
	return best
}
