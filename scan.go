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
//
// 注意轮次不是唯一出口：还有 minCoverageRatio 保证覆盖率下限，
// 只靠 10 轮 × sampleSize 在 6500 个子网上只能覆盖约 15%。
const maxScanRounds = 10

// sampleSize 每轮从子网列表中抽取多少个子网
const sampleSize = 100

// ipsPerSubnet 每个子网生成几个测试 IP。
//
// 原版每个 /24 只随机拼 1 个地址。CF 的 /24 通常整段都是活的边缘节点，
// 但随机挑中的那一个恰好不响应（防火墙、未启用、临时故障）整段就被放弃了 ——
// 命中率被白白压低。取 2 个把「整段误判为死」的概率降到接近平方级。
//
// 不取更多是因为候选数直接线性增长：3 个就意味着单轮 300 次 RTT，
// 而同一子网内的 IP 落在同一机房、速度高度相关，边际收益递减。
const ipsPerSubnet = 2

// maxSpeedTestCount 每轮最多对多少个低延迟 IP 做测速。
// 测速比 RTT 慢得多，不设上限会让单轮耗时失控。
const maxSpeedTestCount = 10

// speedTestPickBest 测速阶段是否测完全部候选再选最优。
//
// 原版遇到第一个达标的就立即返回。问题是默认期望带宽是 1 Mbps，
// 几乎任何活着的 CF 节点都能过 —— 于是「优选」实际退化成
// 「延迟最低的那个 IP」，测速只是走了个形式。
//
// 而延迟低不等于带宽高：同城机房延迟 20ms 但可能已被打满，
// 300ms 的远端反而跑得开。所以现在测完 top N 再选实测最快的。
//
// 代价本来是每轮固定 10 × 5 秒，用两阶段预筛压回到约 30 秒
// （见 speedTestProbeBudget）；再用 speedTestGoodEnough 兜住极端情况。
const speedTestPickBest = true

// speedTestGoodEnough 达到期望带宽的这个倍数就不再往下测。
//
// 纯粹为了避免「明明已经很好了还在傻测」：用户填 1 Mbps 却测出
// 50 Mbps，继续测剩下 9 个几乎不可能改变选择，白等而已。
const speedTestGoodEnough = 3

// minCoverageRatio 至少要测过这么大比例的子网才允许因轮次用尽退出。
//
// 只看轮次会让覆盖率非常低：ips-v4.txt 有 6500+ 个子网，
// 10 轮 × 100 = 1000 个，也就是 15%。剩下 85% 从来没机会被测到，
// 而"最快的 IP"很可能就在里面。
//
// 0.3 是权衡：6500 子网下约 2000 个，配合 ipsPerSubnet=2
// 意味着约 4000 个候选 IP，仍在可接受的耗时内。
const minCoverageRatio = 0.3

// defaultTaskNum RTT 测试的并发数。
// 原版硬编码在 GetIPs 里，提出来便于按平台调整——
// 手机上并发过高会导致大量连接超时，反而更慢。
const defaultTaskNum = 50

// maxBandwidthMbps 期望带宽上限。填得再高也只是白跑满 10 轮。
const maxBandwidthMbps = 1000

// speedTestMinSampleMs 测速提前终止前至少要观察这么久。
//
// 太早判断会误杀慢启动的连接（TCP 窗口还没涨上来、TLS 刚握完手），
// 那种 IP 稳定后其实很快。
const speedTestMinSampleMs = 2000

// speedTestGiveUpRatio 观察期结束时，若实测速度低于目标的这个比例就放弃。
//
// 一个跑 200 kB/s 的 IP 原本要占满整个 5 秒超时；提前放弃能把这些时间
// 让给后面的候选。单轮 10 个 IP 最坏可省 20~30 秒。
//
// 0.4 留了较大余量：带宽会波动，卡在 0.5 附近的 IP 后半段可能追上来，
// 砍太狠会误杀。
const speedTestGiveUpRatio = 0.4

// speedTestFullBudget 正式测速的时间预算。
const speedTestFullBudget = 5 * time.Second

// speedTestProbeBudget 快速预筛的时间预算。
//
// 「测完 top N 再选最优」把每轮的测速成本推到了 10 × 5 秒 = 50 秒 ——
// 质量上去了但慢得让人不想用。两阶段可以两者兼得：
// 先用 1.5 秒粗测所有候选，再只对最有希望的几个做完整测速。
//
// 1.5 秒足够区分「几百 kB/s」和「几 MB/s」这种量级差异，
// 而量级差异正是我们要找的。粗测的绝对值不准（TCP 窗口还没涨满，
// 会系统性偏低），所以只用来排序，绝不用来判定达标。
const speedTestProbeBudget = 1500 * time.Millisecond

// speedTestProbeThreshold 候选超过这个数量才启用两阶段预筛。
//
// 候选本来就少的时候，预筛省不下多少时间，还多了一次连接开销。
const speedTestProbeThreshold = 4

// speedTestFinalists 预筛后进入完整测速的候选数。
//
// 3 个是权衡：粗测的排序不完美（慢启动阶段的相对表现和稳定后不完全一致），
// 留 3 个容错足够；再多就把省下来的时间还回去了。
const speedTestFinalists = 3

// coloDiversityCap 同一个数据中心最多有几个 IP 进入测速。
//
// 按延迟排出来的 top 10 经常全在同一个 colo —— 都是离用户最近的那个机房。
// 一旦那个机房正好拥塞，整轮 10 次测速测的其实是同一条链路，全部白费。
// 限制每个 colo 的名额能保证候选里有真正不同的选择。
const coloDiversityCap = 3

// rttMaxLossRate 丢包率上限，超过就不进测速。
//
// 0.5 = 3 次里失败 2 次以上。这种链路即使测出速度也不稳定，
// 拿来当优选结果只会让用户体验更差。
//
// 有这个上限的前提是「单次失败不再直接判死」：跨境链路抖动很常见，
// 一次超时就把好 IP 扔掉会白丢可用节点。
const rttMaxLossRate = 0.5

// dataMaxAge 数据文件的有效期。
//
// 原本只判断文件是否存在，下载一次就永久不再更新 —— 官方 IP 段会
// 增删、locations.json 会加新机房，用户不手动点「更新数据」就一直
// 拿着几个月前的旧数据扫。一天一次足够跟上变化，也不至于每次扫描都拉。
const dataMaxAge = 24 * time.Hour

// fallbackSpeedTestDomain / fallbackSpeedTestFile 测速地址兜底。
//
// url.txt 内容格式不对（不含 "/"）时，原本两个变量会保持空串，
// 测速 URL 拼成 "https:///"，所有 IP 测速全部归零而且没有任何提示。
//
// 用 cloudflaremirrors.com：Cloudflare 自家的公共镜像站，文件是几 GB
// 的 ISO，测速预算内下载不完。不用 speed.cloudflare.com/__down ——
// 那个端点只服务直连边缘节点，实测非直连访问一律 403。
const (
	fallbackSpeedTestDomain = "cloudflaremirrors.com"
	fallbackSpeedTestFile   = "oracle/OL9/u1/x86_64/OracleLinux-R9-U1-x86_64-dvd.iso"
)

// ewmaAvgAge EWMA 的平均年龄参数，decay = 2/(age+1)。
// 30 是 VividCortex/ewma 的默认值，CloudflareSpeedTest 用的就是它。
const ewmaAvgAge = 30.0

// ewmaWarmupSamples 预热样本数。预热期内用算术平均，
// 之后才切到指数加权 —— 否则前几个样本会被初始值 0 严重拉低。
const ewmaWarmupSamples = 10

// ----------------------- 端口 -----------------------
//
// Cloudflare 边缘节点在多个端口上提供服务，不只是 80/443。
// 原版只测 80/443，于是「优选出来的 IP 拿去接一个跑在 2053 的节点」
// 会握手过了但数据通道对不上，报 io: read/write on closed pipe ——
// 工具说这个 IP 可用，但它验证的根本不是用户要用的那个端口。

// cfHTTPPorts Cloudflare 支持的明文 HTTP 端口
var cfHTTPPorts = []int{80, 8080, 8880, 2052, 2082, 2086, 2095}

// cfHTTPSPorts Cloudflare 支持的 TLS 端口
var cfHTTPSPorts = []int{443, 2053, 2083, 2087, 2096, 8443}

// defaultPortFor 未指定端口时的默认值
func defaultPortFor(useTLS bool) int {
	if useTLS {
		return 443
	}
	return 80
}

// portsFor 返回指定 TLS 模式下的可选端口列表，供界面展示
func portsFor(useTLS bool) []int {
	if useTLS {
		return cfHTTPSPorts
	}
	return cfHTTPPorts
}

// parsePortsCSV 解析界面传来的端口 CSV（如 "443,2053"）。
// 返回空表示不限，由调用方回落到默认端口。
//
// 只接受 CF 实际支持的端口：填一个 CF 不监听的端口，
// 测出来必然全部失败，用户会以为是工具坏了。
func parsePortsCSV(s string, useTLS bool) []int {
	allowed := make(map[int]struct{})
	for _, p := range portsFor(useTLS) {
		allowed[p] = struct{}{}
	}

	var out []int
	seen := make(map[int]struct{})
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		if _, ok := allowed[p]; !ok {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// ----------------------- 地区筛选 -----------------------
//
// 这里和反代优选有本质区别，值得写清楚：
//
// 反代优选的节点列表自带国家标签，可以先筛后测。
// 官方优选是从 CF 官方子网随机拼 IP，落地机房事先无从得知 ——
// 而且同一个 IP 在不同运营商（电信/联通/移动）下会落到不同的
// 数据中心，所以「预测某个 IP 属于哪个地区」这件事本身就是错的。
//
// 只能反过来做：测完之后从 CF-RAY 头拿到 colo 三字码，
// 查 locations.json 得到国家，再决定要不要这个结果。
// 过滤放在 RTT 之后、测速之前 —— RTT 便宜、测速贵，
// 不匹配的 IP 不该浪费一次下载。

// parseCountriesCSV 解析国家代码 CSV（如 "HK,JP,SG"），统一大写。
// 返回空表示不限。
func parseCountriesCSV(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(s, ",") {
		part = strings.ToUpper(strings.TrimSpace(part))
		if part != "" {
			out[part] = struct{}{}
		}
	}
	return out
}

// countryOfColo 查 colo 三字码对应的国家代码（cca2）。
// 查不到返回空串。
func countryOfColo(colo string) string {
	if colo == "" {
		return ""
	}
	locationMu.RLock()
	loc := locationMap[colo]
	locationMu.RUnlock()
	return strings.ToUpper(loc.Cca2)
}

// scanFilter 一次扫描的筛选条件
type scanFilter struct {
	// Ports 要测的端口。空表示只用默认端口。
	Ports []int
	// Countries 允许的国家代码。空表示不限。
	Countries map[string]struct{}
}

// portList 返回实际要测的端口列表
func (f scanFilter) portList(useTLS bool) []int {
	if len(f.Ports) > 0 {
		return f.Ports
	}
	return []int{defaultPortFor(useTLS)}
}

// allowsCountry 判断某个国家代码是否通过筛选。
//
// colo 查不到国家时（locations.json 里没有这个三字码）放行：
// CF 时不时会上线新机房，位置数据滞后不该让用户拿不到结果。
func (f scanFilter) allowsCountry(cca2 string) bool {
	if len(f.Countries) == 0 {
		return true
	}
	if cca2 == "" {
		return true
	}
	_, ok := f.Countries[cca2]
	return ok
}

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

// dataFresh 判断数据文件是否存在且未过期。
//
// 原本只判断存在性（os.Stat），下载一次就永久不再更新 —— 官方 IP 段
// 会增删、locations.json 会加新机房，用户不手动点「更新数据」就一直
// 拿着几个月前的旧数据扫，命中率越来越差。
func dataFresh(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	return timeSince(st.ModTime()) < dataMaxAge
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

// candidateIP 一个待测 IP，连带它来自哪个子网。
//
// 记住来源子网是为了地区筛选能按段跳过：同一个 /24 里的 IP 基本落在
// 同一个 colo，测到一个不匹配就没必要再测同段的其他 IP。
type candidateIP struct {
	IP     string
	Subnet string
}

func getRandomIPv4s(ipList []string) []candidateIP {
	var out []candidateIP
	for _, raw := range ipList {
		subnet := strings.TrimSpace(raw)
		if subnet == "" {
			continue
		}
		base := subnet
		if idx := strings.Index(base, "/"); idx >= 0 {
			base = base[:idx]
		}
		octets := strings.Split(base, ".")
		if len(octets) != 4 {
			continue
		}
		// 同一子网内取多个不重复的末位。重复没有意义 ——
		// 测两次同一个地址不会提高命中率，只是浪费一次 RTT。
		used := make(map[int]struct{}, ipsPerSubnet)
		for range ipsPerSubnet {
			var last int
			// 256 个取值里挑 2 个，冲突概率极低；给几次机会就够，
			// 死循环风险不值得为此承担
			for range 4 {
				last = nextRandomIntn(256)
				if _, dup := used[last]; !dup {
					break
				}
			}
			if _, dup := used[last]; dup {
				continue
			}
			used[last] = struct{}{}
			parts := make([]string, 4)
			copy(parts, octets)
			parts[3] = strconv.Itoa(last)
			out = append(out, candidateIP{IP: strings.Join(parts, "."), Subnet: subnet})
		}
	}
	return out
}

func getRandomIPv6s(ipList []string) []candidateIP {
	var out []candidateIP
	for _, raw := range ipList {
		subnet := strings.TrimSpace(raw)
		if subnet == "" {
			continue
		}
		base := subnet

		// 前缀长度决定能随机多少位。原版固定保留前 3 段（48 位）
		// 并随机后 5 段，对 CF 实际使用的 /48 子网来说恰好正确，
		// 但一旦上游给出 /32 或 /64，随机范围就会错——
		// /64 会随机掉子网自己的位，拼出根本不属于 CF 的地址。
		prefixLen := 48
		if idx := strings.Index(base, "/"); idx >= 0 {
			if n, err := strconv.Atoi(base[idx+1:]); err == nil && n > 0 && n <= 128 {
				prefixLen = n
			}
			base = base[:idx]
		}

		// 展开 :: 压缩，确保有 8 段
		if strings.Contains(base, "::") {
			parts := strings.Split(base, "::")
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
			base = strings.Join(sections, ":")
		}

		sections := strings.Split(base, ":")
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
		// IPv6 地址空间极大，同段内重复的概率可以忽略，不做去重
		for range ipsPerSubnet {
			parts := make([]string, 8)
			copy(parts, sections)
			for i := keep; i < 8; i++ {
				parts[i] = fmt.Sprintf("%x", nextRandomIntn(65536))
			}
			out = append(out, candidateIP{IP: strings.Join(parts, ":"), Subnet: subnet})
		}
	}
	return out
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
	if !dataFresh(urlFilename) {
		if isCancelled() {
			return
		}
		setProgress("正在下载测速 URL...")
		content, err := getURLContent("https://www.baipiao.eu.org/cloudflare/url")
		if err != nil {
			// 有旧文件就先用着：拿过期数据能扫，拿不到数据只能干等
			if !fileExists(urlFilename) {
				setProgress("下载测速 URL 失败: " + err.Error())
				return
			}
			setProgress("测速 URL 更新失败，暂用本地副本")
		} else if err := saveToFile(urlFilename, content); err != nil {
			if !fileExists(urlFilename) {
				setProgress("保存测速 URL 失败: " + err.Error())
				return
			}
			setProgress("测速 URL 保存失败，暂用本地副本")
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
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		speedTestDomain = parts[0]
		speedTestFile = parts[1]
	} else {
		// 必须兜底。原本没有这个 else：url.txt 内容格式不对（不含 "/"、
		// 空文件、被中间设备替换成错误页）时两个变量保持空串，测速 URL
		// 拼成 "https:///"，所有 IP 测速全部归零 —— 而且没有任何提示，
		// 用户只会看到「找不到可用 IP」。
		speedTestDomain = fallbackSpeedTestDomain
		speedTestFile = fallbackSpeedTestFile
		setProgress("测速地址格式异常，已改用内置备用地址")
	}

	for _, item := range []struct{ file, url string }{
		{"ips-v4.txt", "https://www.baipiao.eu.org/cloudflare/ips-v4"},
		{"ips-v6.txt", "https://www.baipiao.eu.org/cloudflare/ips-v6"},
	} {
		if isCancelled() {
			return
		}
		fp := dataPath(item.file)
		if !dataFresh(fp) {
			setProgress("正在下载 IP 列表: " + item.file)
			c, err := getURLContent(item.url)
			if err != nil {
				// 数据源挂了但本地有旧副本时不该让扫描直接失败
				if !fileExists(fp) {
					setProgress("下载 IP 列表失败: " + err.Error())
					return
				}
				setProgress(item.file + " 更新失败，暂用本地副本")
			} else if err := saveToFile(fp, c); err != nil {
				if !fileExists(fp) {
					setProgress("保存 IP 列表失败: " + err.Error())
					return
				}
				setProgress(item.file + " 保存失败，暂用本地副本")
			}
		}
	}

	if isCancelled() {
		return
	}
	fp := dataPath("locations.json")
	if !dataFresh(fp) {
		setProgress("正在下载位置信息...")
		if err := fetchLocations(fp); err != nil {
			// locations 只影响机房名显示，缺了不该阻断扫描
			if !fileExists(fp) {
				setProgress("获取位置信息失败: " + err.Error())
				return
			}
			setProgress("位置信息更新失败，暂用本地副本")
		}
	}
}

// fetchLocations 下载机房位置信息并落盘。
// 单独拆出来是为了让 downloadAllData 的「过期则更新、失败则用旧副本」
// 逻辑对三种数据文件保持一致。
func fetchLocations(fp string) error {
	req, _ := http.NewRequestWithContext(scanCtx(), "GET", "https://www.baipiao.eu.org/cloudflare/locations", nil)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
	resp.Body.Close()
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("返回内容为空")
	}
	return saveToFile(fp, string(body))
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
	Port      int
	LatencyMs int
	// JitterMs 多次测量的平均绝对偏差。
	//
	// 低延迟高抖动的 IP 实际体验很差（表现为卡顿），只看平均值挑不出来。
	// 用它做排序的次要键，同延迟档位里优先选稳的。
	JitterMs int
	// Colo CF-RAY 头里的数据中心三字码。
	//
	// 在 RTT 阶段就取出来，是为了让地区筛选能在测速之前生效：
	// RTT 是一次 TCP 连接，测速要下载几 MB，把不匹配的 IP 拦在
	// 测速之前能省掉绝大部分无用流量。
	Colo string
	// LossRate 丢包率 0.0~1.0。
	//
	// 单独成一项而不是「失败就丢弃」：跨境链路抖动很常见，一次超时
	// 就把好 IP 判死会白扔掉可用节点。排序时优先于延迟 —— 一条 20ms
	// 但丢 33% 的链路，实际体验远差于 60ms 零丢包。
	LossRate float64
}

// rttProbes 每个目标测几次延迟。
//
// 只有第 1 次做完整的 TLS + HTTP 验证（拿 CF-RAY / colo），
// 后续几次只做 TCP 握手计时 —— 见 testRTT 的说明。
const rttProbes = 3

// testRTT 测试单个 IP:端口 的延迟，并验证它确实是 CF 节点。
// 返回 (平均延迟毫秒, 抖动毫秒, colo 三字码, 丢包率)，延迟为 0 表示不可用。
//
// sni 为空时用测速域名（拿不到测速域名才退回 cloudflare.com）。
// 用测速域名验证是为了让「验证通过」和「测速能成功」是同一件事：
// 验证一个域名、测速另一个域名，就可能出现验证通过但测速必然拿 0 的
// IP，白占一次测速预算。允许自定义 SNI 是因为有些节点要求特定 SNI，
// 用用户自己的域名去测，结果才和实际使用一致。
//
// 只有第一次探测做完整流程（TLS 握手 + HTTP 请求 + 读响应头验 CF-RAY），
// 后两次只做 TCP 握手计时。原版三次都跑完整流程，但延迟统计只用了
// TCP 握手时间 —— TLS 和 HTTP 那部分纯属白做，在 TLS 模式下浪费掉
// 约 2/3 的 RTT 阶段耗时。CF-RAY 也只需要验一次。
//
// 单次 TCP 失败只记丢包、继续探测剩下的次数，不再直接判死：跨境链路
// 抖动很常见，一次超时就扔掉好 IP 是白丢可用节点。只有反代/CF 验证
// 本身失败（TLS 握手失败、没有 CF-RAY 等）才立即返回不可用 —— 那说明
// 这个 IP 根本不是能用的 CF 节点，不是网络抖动。
//
// 同时返回抖动（各次测量与均值的平均绝对偏差）：低延迟高抖动的 IP
// 实际体验很差，只看平均值选不出来。
func testRTT(ip string, port int, useTLS bool, sni string) (int, int, string, float64) {
	// userSNI 记住这次验证用的是不是用户自己的域名。
	//
	// 这个区分决定了状态码怎么判：用用户域名请求时，响应码反映的是
	// 「CF 能不能回源到用户的服务器」；用测速域名请求时，回源是 CF
	// 自己的事，状态码说明不了用户节点的任何情况。
	userSNI := strings.TrimSpace(sni) != ""
	if sni == "" {
		// 优先用测速域名，让验证与测速指向同一个目标
		if speedTestDomain != "" {
			sni = speedTestDomain
		} else {
			sni = "cloudflare.com"
		}
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))

	samples := make([]int, 0, rttProbes)
	var colo string
	verified := false

	for range rttProbes {
		if isCancelled() {
			break
		}

		start := time.Now()
		var d = net.Dialer{Timeout: 1 * time.Second}
		conn, err := d.DialContext(scanCtx(), "tcp", addr)
		if err != nil {
			// 单次失败只记丢包，继续探测剩下的次数
			continue
		}
		tcpMs := int(time.Since(start).Milliseconds())

		// CF 验证只做一次，之后只需要握手耗时，握完就关
		if verified {
			conn.Close()
			samples = append(samples, tcpMs)
			continue
		}

		// 第一次成功连上：完整验证这是不是真的 CF 节点
		conn.SetDeadline(start.Add(3 * time.Second))

		var rwc net.Conn = conn
		if useTLS {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				// 握手失败说明这个 IP 不提供 TLS 服务，不是网络抖动
				return 0, 0, "", 1.0
			}
			rwc = tlsConn
		}

		reqStr := "GET / HTTP/1.1\r\nHost: " + sni + "\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n\r\n"
		if _, err := rwc.Write([]byte(reqStr)); err != nil {
			rwc.Close()
			return 0, 0, "", 1.0
		}

		reader := bufio.NewReader(rwc)
		resp, err := http.ReadResponse(reader, nil)
		rwc.Close()
		if err != nil {
			return 0, 0, "", 1.0
		}
		resp.Body.Close()

		cfRay := resp.Header.Get("CF-RAY")
		if cfRay == "" {
			// 不是 CF 节点，重试也不会变成 CF 节点
			return 0, 0, "", 1.0
		}
		// 用用户自己的域名验证时必须查状态码。
		//
		// 这是 closed pipe 的真正来源：CF 回源失败时返回 521/522/523，
		// 这些响应同样带 CF-RAY —— 只判 CF-RAY 存不存在，回源不通的 IP
		// 会被报成"可用"，用户拿去接节点，握手过了、数据一发就断。
		//
		// 免费套餐尤其常见：部分 CF IP 段对某些运营商（用户实测是电信）
		// 回源不通，而付费套餐和 Argo 隧道不受影响。同一个 IP 用
		// cloudflaremirrors.com 测是 200，用用户域名测就是 523。
		if userSNI && !originReachable(resp.StatusCode) {
			return 0, 0, "", 1.0
		}
		colo = extractDataCenter(cfRay)
		verified = true
		samples = append(samples, tcpMs)
	}

	if len(samples) == 0 {
		return 0, 0, "", 1.0
	}

	sum := 0
	for _, s := range samples {
		sum += s
	}
	avg := sum / len(samples)
	// 至少记 1ms：0 是「不可用」的信号值，而亚毫秒延迟算出来就是 0，
	// 会让一个其实很好的 IP 被当成死的丢掉
	if avg < 1 {
		avg = 1
	}

	// 抖动用平均绝对偏差：比标准差便宜，量级上也更直观
	dev := 0
	for _, s := range samples {
		d := s - avg
		if d < 0 {
			d = -d
		}
		dev += d
	}
	jitter := dev / len(samples)

	lossRate := float64(rttProbes-len(samples)) / float64(rttProbes)

	return avg, jitter, colo, lossRate
}

// originReachable 判断「用用户域名请求」拿到的状态码是否说明 CF 能回源。
//
// 只在用户填了 SNI 时使用。分类依据是「这个响应是 CF 生成的，还是用户
// 的源生成的」——只要响应来自源，回源链路就是通的，具体内容不重要。
//
//   - 2xx / 3xx：源正常响应或重定向 → 通
//   - 101：WebSocket 已升级，隧道确实建立起来了 → 通（最硬的证据）
//   - 400/404/405/410：源明确回答了「这个请求我不接」。对 WS/gRPC 节点
//     来说根路径返回 404 是正常的 —— 恰恰证明 CF 摸到了源。
//   - 403：可能是 WAF 拦、也可能是 zone 配置问题，两种都不该当可用节点
//   - 521/522/523/524/525/526/530：CF 自己生成的回源错误页，回源断了 → 不通
//   - 其他 5xx：源有问题，即使链路通也不适合作为优选目标
//
// 注意 404 必须放行：它和 52x 的区别是「谁生成的这个响应」。404 来自源，
// 52x 来自 CF。把 404 判死会让所有 WS 节点（根路径无内容）全军覆没。
func originReachable(code int) bool {
	switch {
	case code == 101:
		return true
	case code >= 200 && code < 400:
		return true
	case code == 400 || code == 404 || code == 405 || code == 410:
		return true
	default:
		// 403、52x、其他 5xx 以及一切未列出的都判不可用。
		// 白名单而非黑名单：漏掉一个 CF 新增的错误码只会让判定偏保守
		// （少给一个 IP），漏掉的如果是错误码却当成可用，用户又要遇到
		// closed pipe —— 两种代价不对称。
		return false
	}
}

// runRTTTest 对候选 IP × 端口做 RTT 测试。
//
// 多端口时逐个端口都测：同一个 IP 在 443 通、在 2053 不通是常见的，
// 只测一个端口然后假定其他端口也一样，就是原版那个 bug 的根源。
//
// filter 的地区条件在这里就应用：RTT 阶段已经能从 CF-RAY 拿到 colo，
// 不匹配的直接丢掉，不让它进入昂贵的测速环节。
//
// 地区不匹配时还会把整个来源子网标记为跳过：同一个 /24 内的 IP 基本
// 落在同一机房，逐个测完再逐个丢弃是纯浪费。地区筛得窄时这个优化
// 直接决定能不能在可接受时间内出结果。
//
// wantCount 是用户要的结果个数，只用来决定保留多少候选进测速阶段：
// 要 10 个结果却只留 10 个候选的话，一部分候选测速归零就凑不满了。
func runRTTTest(cands []candidateIP, ports []int, taskNum int, useTLS bool, sni string, filter scanFilter, wantCount int) []RTTResult {
	// 候选是 IP 与端口的组合
	type target struct {
		ip     string
		subnet string
		port   int
	}
	var targets []target
	for _, c := range cands {
		for _, p := range ports {
			targets = append(targets, target{c.IP, c.Subnet, p})
		}
	}

	if len(targets) < taskNum {
		taskNum = len(targets)
	}
	if taskNum <= 0 {
		return nil
	}

	var wg sync.WaitGroup
	resultChan := make(chan RTTResult, len(targets))
	thread := make(chan struct{}, taskNum)
	var count int
	var mu sync.Mutex
	total := len(targets)

	// 地区不匹配的子网。只在启用地区筛选时才有意义。
	var skipMu sync.RWMutex
	skipSubnet := make(map[string]struct{})
	filterByRegion := len(filter.Countries) > 0
	var skipped int

	for _, t := range targets {
		if isCancelled() {
			break
		}
		// 同段已判定为地区不匹配，直接跳过，不占用并发额度
		if filterByRegion {
			skipMu.RLock()
			_, skip := skipSubnet[t.subnet]
			skipMu.RUnlock()
			if skip {
				mu.Lock()
				skipped++
				mu.Unlock()
				continue
			}
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
		go func(ip, subnet string, port int) {
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
			avgMs, jitterMs, colo, lossRate := testRTT(ip, port, useTLS, sni)
			if avgMs <= 0 {
				return
			}
			// 丢包太多的链路即使能测出速度也不稳定，直接不要。
			// 注意不标记整段跳过：丢包是链路瞬时状态，同段其他 IP
			// 未必一样（地区不匹配才是整段的属性）。
			if lossRate > rttMaxLossRate {
				return
			}
			// 地区不匹配：丢掉这个结果，并把整段标记为跳过
			if !filter.allowsCountry(countryOfColo(colo)) {
				if filterByRegion && colo != "" {
					skipMu.Lock()
					skipSubnet[subnet] = struct{}{}
					skipMu.Unlock()
				}
				return
			}
			resultChan <- RTTResult{
				IP: ip, Port: port, LatencyMs: avgMs, JitterMs: jitterMs, Colo: colo,
				LossRate: lossRate,
			}
		}(t.ip, t.subnet, t.port)
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
	// 排序：先丢包率，再延迟档位，同档内优先选抖动小的。
	//
	// 丢包排最前面是因为它比延迟更能决定体验：一条 20ms 但丢 33% 的
	// 链路会明显卡顿，远差于 60ms 零丢包。
	//
	// 延迟不做精确排序而是按 20ms 分档，因为 5ms 的差别在实际使用中
	// 感知不到，而抖动大的 IP 会明显卡顿 —— 把"差不多快"的归到一起，
	// 再让稳定性决定谁优先。
	sortRTTResults(results)

	skipNote := ""
	if skipped > 0 {
		skipNote = fmt.Sprintf("，跳过 %d 个同段地区不符的目标", skipped)
	}

	// 要几个结果就得留够候选。RTT 候选不足时后面的完整测速没东西可测，
	// 用户要 10 个只能拿到 3 个。留 2 倍余量是因为一部分候选测速会归零
	// （403、连接被 reset、慢启动上不来）。
	keepCount := maxSpeedTestCount
	if wantCount > 1 {
		keepCount = max(maxSpeedTestCount, wantCount*2)
	}
	kept, dropped := capPerColo(results, keepCount)
	diversityNote := ""
	if dropped > 0 {
		diversityNote = fmt.Sprintf("，同机房超额剔除 %d 个", dropped)
	}
	if len(results) > len(kept) {
		setProgress(fmt.Sprintf("RTT 测试完成，%d/%d 个 IP 有效%s%s，保留 %d 个进入测速",
			len(results), total, skipNote, diversityNote, len(kept)))
	} else {
		setProgress(fmt.Sprintf("RTT 测试完成，%d/%d 个 IP 有效%s", len(results), total, skipNote))
	}
	return kept
}

// sortRTTResults 按「先丢包率，再延迟档位，同档内抖动小的优先」排序。
//
// 丢包优先于延迟：一条 20ms 但丢 33% 的链路会明显卡顿，实际体验远差于
// 60ms 零丢包。这也是 CloudflareSpeedTest 的排序口径（PingDelaySet.Less
// 先比 lossRate 再比 Delay）。
//
// 延迟按 20ms 分档而不精确排序：5ms 的差别在实际使用中感知不到，
// 而同档位里抖动小的那个体验明显更好。
func sortRTTResults(results []RTTResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].LossRate != results[j].LossRate {
			return results[i].LossRate < results[j].LossRate
		}
		bi, bj := results[i].LatencyMs/20, results[j].LatencyMs/20
		if bi != bj {
			return bi < bj
		}
		if results[i].JitterMs != results[j].JitterMs {
			return results[i].JitterMs < results[j].JitterMs
		}
		return results[i].LatencyMs < results[j].LatencyMs
	})
}

// capPerColo 从已排序的结果里挑出最多 limit 个，且同一 colo 不超过
// coloDiversityCap 个。返回 (保留的结果, 因同机房超额而被剔除的数量)。
//
// 按延迟排出来的 top N 经常全在同一个 colo —— 都是离用户最近的那个机房。
// 一旦那个机房正好拥塞，整轮测速测的其实是同一条链路，全部白费。
// 保证候选里有真正不同的机房，才有"选"的意义。
//
// 名额凑不满时不回填。少测几个不是损失：同一个机房里再测 5 个 IP，
// 测的还是那条链路，纯属白花时间。宁可这轮候选少、快点进下一轮换新子网。
func capPerColo(results []RTTResult, limit int) ([]RTTResult, int) {
	if limit <= 0 || len(results) == 0 {
		return nil, 0
	}

	perColo := make(map[string]int)
	kept := make([]RTTResult, 0, min(limit, len(results)))
	dropped := 0

	for _, r := range results {
		if len(kept) >= limit {
			break
		}
		// colo 未知的不参与配额：无法判断是否同机房，
		// 按配额处理会把它们全归到同一个空 key 上，误伤新上线的机房
		if r.Colo == "" {
			kept = append(kept, r)
			continue
		}
		if perColo[r.Colo] >= coloDiversityCap {
			dropped++
			continue
		}
		perColo[r.Colo]++
		kept = append(kept, r)
	}

	return kept, dropped
}

// ----------------------- 速度测试 -----------------------

// ewmaRate 指数加权移动平均。
//
// 用来算下载速度而不是取「峰值窗口」：峰值会被 TCP 慢启动后的一次突发
// 拉高，测出来的数字看着漂亮但不代表持续可用带宽。EWMA 让近期样本权重
// 更高又不至于被单点尖峰主导，这也是 CloudflareSpeedTest 的做法
// （task/download.go 里的 ewma.NewMovingAverage）。
//
// 自己实现而不引入 VividCortex/ewma：本项目要用 gomobile 打包，
// 依赖越少越省事，而这段逻辑只有二十行。
type ewmaRate struct {
	value float64
	count int
	sum   float64
}

func (e *ewmaRate) add(v float64) {
	if e.count < ewmaWarmupSamples {
		e.count++
		e.sum += v
		e.value = e.sum / float64(e.count)
		return
	}
	decay := 2 / (ewmaAvgAge + 1)
	e.value = v*decay + e.value*(1-decay)
}

func (e *ewmaRate) rate() float64 { return e.value }

// runSpeedTestSimple 速度测试，返回 (平均速度 kB/s, TCP延迟ms, 三字码头)
//
// 速度是 EWMA 平均值而非峰值：峰值会被单次突发拉高，不代表持续带宽。
//
// target 是期望带宽（kB/s），用于提前放弃明显达不到的 IP；传 0 表示不提前放弃。
// budget 是下载时间预算：正式测速给 speedTestFullBudget，
// 两阶段预筛时给 speedTestProbeBudget。
func runSpeedTestSimple(ip string, port int, useTLS bool, target int, budget time.Duration) (int, int, string) {
	if budget <= 0 {
		budget = speedTestFullBudget
	}
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
		// 预算只覆盖下载，连接和握手另算：短预算下如果把建连时间也算进去，
		// 稍慢的握手就会挤掉全部下载时间，测出来永远是 0
		Timeout: budget + 3*time.Second,
	}

	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	testURL := fmt.Sprintf("%s://%s/%s", scheme, speedTestDomain, speedTestFile)

	req, _ := http.NewRequestWithContext(scanCtx(), "GET", testURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, ""
	}
	defer resp.Body.Close()

	// 必须查状态码。否则 4xx/5xx 的错误页会被当成下载内容算速度：
	// 小错误页读完就 EOF，兜底逻辑会估出一个虚假的小速度，
	// 全部节点都这样时它甚至能成为「最佳结果」返回给用户。
	//
	// 206 也接受：有些镜像站对大文件一直按 Range 回。
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, 0, ""
	}

	cfRay := resp.Header.Get("CF-RAY")
	dataCenter := extractDataCenter(cfRay)

	buf := make([]byte, 32*1024)
	var totalBytes int64
	var sliceBytes int64
	// 下载计时从首字节开始，不含建连和握手 —— 否则短预算下
	// 握手慢一点就没有下载时间了，测出来永远是 0
	dlStart := time.Now()

	// 时间片切成预算的 1/100（对齐 CFST 的 timeSlice = Timeout/100），
	// 每片结算一次喂给 EWMA。片太大采样点不够 EWMA 没意义，
	// 太小则单片字节数受 Read 缓冲影响抖动过大。
	timeSlice := budget / 100
	if timeSlice < 10*time.Millisecond {
		timeSlice = 10 * time.Millisecond
	}
	e := &ewmaRate{}
	sliceStart := dlStart

	for {
		n, err := resp.Body.Read(buf)
		totalBytes += int64(n)
		sliceBytes += int64(n)

		// 先结算时间片再判断错误：末尾那个不满一片的残片也要算进去，
		// 否则整个下载在一片内结束时速度会算成 0。
		elapsed := time.Since(sliceStart)
		if elapsed >= timeSlice || err != nil {
			if secs := elapsed.Seconds(); secs > 0 && sliceBytes > 0 {
				// 换算成每秒字节数再喂给 EWMA，
				// 这样残片不会因为时间短而被低估
				e.add(float64(sliceBytes) / secs)
			}
			sliceBytes = 0
			sliceStart = time.Now()
		}

		if err != nil {
			break
		}

		sinceStart := time.Since(dlStart)

		// 预算用完就收工。不靠 client.Timeout 是因为那个超时会让
		// Read 返回错误，而这里是正常结束，语义不同。
		if sinceStart >= budget {
			break
		}

		// 提前放弃明显达不到目标的 IP。
		//
		// 没有这个的话，一个跑 200 kB/s 的 IP 也要占满整个 5 秒预算；
		// 单轮 10 个候选最坏白等 20~30 秒。观察期后如果速度连目标的
		// speedTestGiveUpRatio 都没到，后半段追上来的可能性极低。
		//
		// target <= 0 表示调用方不关心目标（只想知道能跑多快），此时不放弃。
		// 预筛阶段（预算短于观察期）也不放弃：那时本来就没测到稳定速度，
		// 谁快谁慢由调用方排序决定，不该在这里下结论。
		if target > 0 {
			if sinceStart >= speedTestMinSampleMs*time.Millisecond && totalBytes > 0 {
				avgKB := float64(totalBytes) / 1024 / sinceStart.Seconds()
				if avgKB < float64(target)*speedTestGiveUpRatio {
					break
				}
			}
		}
	}

	if totalBytes == 0 {
		return 0, tcpMs, dataCenter
	}

	speedKB := int(e.rate() / 1024)

	// EWMA 在样本极少时（下载几乎瞬间结束）可能失真，
	// 用总量平均兜底，取较大值 —— 有数据下来就不该报 0。
	if secs := time.Since(dlStart).Seconds(); secs > 0 {
		if avg := int(float64(totalBytes) / 1024 / secs); avg > speedKB {
			speedKB = avg
		}
	}

	return speedKB, tcpMs, dataCenter
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

// speedTestRound 对一批 RTT 候选做测速，把结果收进 pool。
//
// 两阶段：候选多时先用 speedTestProbeBudget 粗测全部，再只对前
// finalistCount(wantCount) 个做完整测速。
//
// 为什么要两阶段：「测完 top N 再选最优」把每轮成本推到 10 × 5 秒 = 50 秒，
// 质量上去了但慢得没人愿意用。粗测 1.5 秒足够区分「几百 kB/s」和「几 MB/s」
// 这种量级差异，而量级差异正是要找的东西。于是 10 × 1.5 + 3 × 5 ≈ 30 秒，
// 比全量完整测速快近一半，选出的仍是实测最快的。
//
// 粗测的绝对值不能用：TCP 窗口还没涨满，测出来系统性偏低。只用来排序。
//
// wantCount > 1 时不能测到第一个达标就收工 —— 那样永远只能凑出一个结果。
// 改成「池里达标的数量够了才收工」。
//
// 返回本轮实测最快的那个（供进度文案用）和是否被取消。
func speedTestRound(cands []RTTResult, useTLS bool, target int, wantCount int,
	pool *resultPool) (testOutcome, bool) {
	if len(cands) == 0 {
		return testOutcome{}, false
	}

	wantFinalists := finalistCount(wantCount)
	finalists := cands

	// 候选够多才值得预筛：本来就少的时候省不下多少，还多一次连接开销。
	// 要多个结果时决赛名额也多，候选没比名额多出多少就别预筛了。
	if speedTestPickBest && len(cands) > speedTestProbeThreshold && len(cands) > wantFinalists {
		type probe struct {
			r     RTTResult
			speed int
		}
		probes := make([]probe, 0, len(cands))

		for i, r := range cands {
			if isCancelled() {
				return testOutcome{}, true
			}
			setProgress(fmt.Sprintf("快速预筛 %d/%d：%s:%d (延迟 %dms 抖动 %dms)",
				i+1, len(cands), r.IP, r.Port, r.LatencyMs, r.JitterMs))
			// target 传 0：预筛不做「达不到就放弃」的判断，
			// 这么短的观察期内谁快谁慢还说不准
			sp, _, _ := runSpeedTestSimple(r.IP, r.Port, useTLS, 0, speedTestProbeBudget)
			probes = append(probes, probe{r, sp})
		}

		// 粗测速度降序；完全测不出速度的排最后
		sort.SliceStable(probes, func(i, j int) bool {
			return probes[i].speed > probes[j].speed
		})

		finalists = make([]RTTResult, 0, wantFinalists)
		for _, p := range probes {
			if len(finalists) >= wantFinalists {
				break
			}
			// 粗测彻底跑不动的没必要再花 5 秒确认
			if p.speed <= 0 {
				continue
			}
			finalists = append(finalists, p.r)
		}
		// 全部粗测都是 0（网络抖动或测速源故障）时不能放弃整轮，
		// 退回按延迟顺序做完整测速
		if len(finalists) == 0 {
			finalists = cands[:min(wantFinalists, len(cands))]
		}
		setProgress(fmt.Sprintf("预筛完成，%d 个候选中挑出 %d 个做完整测速",
			len(cands), len(finalists)))
	}

	var best testOutcome
	for i, r := range finalists {
		if isCancelled() {
			return testOutcome{}, true
		}

		if wantCount > 1 {
			setProgress(fmt.Sprintf("正在测速 %d/%d：%s:%d (已找到 %d/%d 个达标)",
				i+1, len(finalists), r.IP, r.Port, pool.qualified(target), wantCount))
		} else {
			setProgress(fmt.Sprintf("正在测速 %d/%d：%s:%d (延迟 %dms 抖动 %dms)",
				i+1, len(finalists), r.IP, r.Port, r.LatencyMs, r.JitterMs))
		}
		// 用 RTT 阶段测通的那个端口测速，不能回落到 80/443 ——
		// 否则测的是另一个端口的速度，结果对不上用户实际要用的端口。
		// 传入 target 让明显达不到目标的 IP 能提前放弃，不必占满预算。
		maxSpeed, tcpMs, dc := runSpeedTestSimple(r.IP, r.Port, useTLS, target, speedTestFullBudget)
		// 测速响应也带 CF-RAY，但以 RTT 阶段拿到的 colo 为准：
		// 地区筛选是基于它做的，两处不一致会让结果自相矛盾
		if dc == "" {
			dc = r.Colo
		}
		dcName := dc
		if dc != "" {
			dcName = lookupDataCenter(dc)
		}
		cca2 := countryOfColo(dc)
		setProgress(fmt.Sprintf("%s:%d 峰值速度 %d kB/s, 数据中心 %s", r.IP, r.Port, maxSpeed, dcName))

		pool.add(speedResult{
			IP: r.IP, Port: r.Port, MaxSpeed: maxSpeed, LatencyMs: tcpMs,
			DataCenter: dcName, Country: cca2,
		})

		if maxSpeed > best.MaxSpeed {
			best = testOutcome{
				IP: r.IP, Port: r.Port, MaxSpeed: maxSpeed, LatencyMs: tcpMs,
				DataCenter: dcName, Country: cca2,
			}
		}

		// 什么时候不必再往下测：
		// - 关掉「测完选最优」时，达标即收工（旧行为）
		// - 只要一个结果时，只有远超目标才提前收工：用户填 1 Mbps 却测出
		//   50 Mbps，再测剩下的几乎不可能改变选择，纯粹白等
		// - 要多个结果时，凑够达标数量才收工。这里不能再用 goodEnough
		//   那条捷径 —— 第一个 IP 跑出 50 Mbps 说明它很好，但另外
		//   4 个还没测，直接收工就只能给一个结果。
		if wantCount > 1 {
			if pool.qualified(target) >= wantCount {
				break
			}
			continue
		}
		if maxSpeed >= target {
			if !speedTestPickBest || maxSpeed >= target*speedTestGoodEnough {
				break
			}
		}
	}

	return best, false
}

// finalistCount 算出该有多少个候选进入完整测速。
//
// 要 N 个结果至少得完整测 N 个，但不能刚好等于 N：一部分候选测速会归零
// （403、连接被 reset、慢启动上不来），刚好够就凑不满。多给 2 个余量。
//
// 上限是 maxSpeedTestCount：再多单轮就要 10 × 5 秒以上，用户会以为卡死。
// 凑不满的缺口交给下一轮补。
func finalistCount(wantCount int) int {
	if wantCount <= 1 {
		return speedTestFinalists
	}
	return min(max(speedTestFinalists, wantCount+2), maxSpeedTestCount)
}

// roundBatchSize 算出每轮该抽多少个子网。
//
// 单轮实际候选数 = 子网数 × ipsPerSubnet × 端口数。不按后两个因子缩减的话，
// 「每子网 2 个 IP + 6 个端口」会让一轮变成 1200 次 RTT，单轮跑好几分钟。
//
// 留 2 倍余量而不是严格等比缩减：单轮候选多一些能提高每轮命中好 IP 的概率，
// 而 RTT 是并发跑的，耗时增长远小于线性。
//
// 下限 10 个子网：缩得太小会让每轮样本失去统计意义，覆盖率也推进得过慢。
func roundBatchSize(base, numPorts, poolSize int) int {
	if numPorts < 1 {
		numPorts = 1
	}
	n := base
	if perSubnet := ipsPerSubnet * numPorts; perSubnet > 1 {
		n = max(base/perSubnet*2, 10)
	}
	if poolSize > 0 && n > poolSize {
		n = poolSize
	}
	return n
}

// testOutcome 一次扫描的结果。
// 除了优选 IP 本身，还带上「实际测了多少」的统计，
// 这样界面能给出准确文案，而不是笼统地说"未找到"。
type testOutcome struct {
	IP         string
	Port       int
	MaxSpeed   int
	LatencyMs  int
	DataCenter string
	// Country 落地国家代码（cca2），从 colo 查 locations.json 得到。
	//
	// 必须实测才知道：官方 IP 的落地机房和运营商线路有关，
	// 同一个 IP 在电信和联通下可能落到不同国家，无法事先预测。
	Country string

	RoundsRun     int  // 实际跑了几轮
	Tested        int  // 实际测过多少个子网
	PoolSize      int  // 子网总数
	PoolExhausted bool // true = 全部子网已测完（而非轮次用尽）
	BelowTarget   bool // true = 有结果但未达到期望带宽

	// Results 按速度降序的全部结果（含 IP/Port/MaxSpeed 等字段）。
	//
	// 用户要 5 或 10 个结果时，第一个和 IP/Port/MaxSpeed 那几个顶层字段
	// 是同一个 —— 顶层字段保留下来是为了不动界面已有的读取逻辑，
	// 老版本界面读顶层照样能跑。
	Results []speedResult
}

// cloudflareTest 核心测试逻辑
func cloudflareTest(ipType int, useTLS bool, taskNum int, speed int, filter scanFilter, sni string,
	wantCount int) testOutcome {
	wantCount = normalizeResultCount(wantCount)
	pool := newResultPool(wantCount)
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
	base := newSubnetSampler(ipList)

	ports := filter.portList(useTLS)
	batchSize = roundBatchSize(batchSize, len(ports), base.total())

	// 选了地区时，在正式测试之前先做一轮廉价侦察：每个子网只探 1 个 IP、
	// 只拿 CF-RAY 里的 colo，据此排除落地国家不符的整个子网。
	//
	// 不选地区时不做侦察 —— 那种情况下侦察纯属额外开销，没有任何可排除的东西。
	var sampler batchSampler = base
	if len(filter.Countries) > 0 {
		sampler = newRegionSampler(base, ipType, ports[0], useTLS, sni, filter, taskNum)
	}

	// 历轮的结果都攒在 pool 里（见上面的 newResultPool）。轮次用尽仍未凑够时
	// 把池里已有的返回，至少让用户拿到可用结果，而不是空手而归。

	roundsRun := 0
	poolExhausted := false

	// 覆盖率下限：只看轮次会让覆盖率非常低（6500 子网只测 1000 个约 15%），
	// 而"最快的 IP"很可能就在剩下的 85% 里。达标结果会立即返回，
	// 所以这个下限只在"一直没达标"时才会真正延长扫描。
	minSubnets := int(float64(sampler.total()) * minCoverageRatio)

	// 绝对上限。理论上"子网取完"和"覆盖率达标"都能终止循环，
	// 但这是个会跑很久的用户可见循环，留一道硬闸比事后调试死循环便宜。
	hardMaxRounds := maxScanRounds
	if batchSize > 0 {
		hardMaxRounds = max(maxScanRounds, sampler.total()/batchSize+maxScanRounds)
	}

	for round := 1; round <= hardMaxRounds; round++ {
		// 轮次用尽后，若覆盖率还没到下限就继续 —— 但子网取完必然停
		if round > maxScanRounds {
			if sampler.used() >= minSubnets || sampler.used() >= sampler.total() {
				break
			}
			setProgress(fmt.Sprintf("已跑满 %d 轮但仅覆盖 %d/%d 个子网，继续扫描以提高覆盖率...",
				maxScanRounds, sampler.used(), sampler.total()))
		}
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

			var testIPs []candidateIP
			if ipType == 6 {
				testIPs = getRandomIPv6s(sampled)
			} else {
				testIPs = getRandomIPv4s(sampled)
			}

			setProgress(fmt.Sprintf("第 %d 轮：%d 个子网 × %d IP × %d 端口 = %d 个候选（累计 %d/%d 子网），开始 RTT 测试...",
				round, len(sampled), ipsPerSubnet, len(ports), len(testIPs)*len(ports),
				sampler.used(), sampler.total()))

			rttResults = runRTTTest(testIPs, ports, taskNum, useTLS, sni, filter, wantCount)
			if isCancelled() {
				break
			}
			if len(rttResults) > 0 {
				break
			}
			if len(filter.Countries) > 0 {
				setProgress("当前这批 IP 都不可用或不在所选地区，换下一批子网继续...")
			} else {
				setProgress("当前这批 IP 都存在 RTT 丢包，换下一批子网继续...")
			}
		}

		if isCancelled() {
			setProgress("扫描已取消")
			return testOutcome{}
		}
		if poolExhausted {
			break
		}

		// 测速：先粗测排序，再对最有希望的做完整测速，最后选最快的。
		//
		// 原版遇到第一个达标的就返回，但默认目标是 1 Mbps —— 几乎人人达标，
		// 于是"优选"退化成"延迟最低"。延迟低不代表带宽高：同城机房延迟 20ms
		// 却可能已被打满，300ms 的远端反而跑得开。
		roundBest, cancelled := speedTestRound(rttResults, useTLS, speed, wantCount, pool)
		if cancelled {
			setProgress("扫描已取消")
			return testOutcome{}
		}

		// 达标数量凑够了就收工。要 1 个时和以前完全一样；要 5/10 个时
		// 会继续跑下一轮去补，而不是拿到第一个达标的就返回。
		if pool.qualified(speed) >= wantCount {
			top := pool.best()
			if wantCount > 1 {
				setProgress(fmt.Sprintf("已找到 %d 个达标 IP，最快 %s:%d (%d kB/s)",
					pool.qualified(speed), top.IP, top.Port, top.MaxSpeed))
			} else {
				setProgress(fmt.Sprintf("找到优选 IP: %s:%d, 速度 %d kB/s, 延迟 %dms",
					top.IP, top.Port, top.MaxSpeed, top.LatencyMs))
			}
			out := outcomeFromPool(pool)
			out.RoundsRun = round
			out.Tested = sampler.used()
			out.PoolSize = sampler.total()
			return out
		}
		// 子网刚好取完，没有下一批可测，不必再走剩下的轮次
		if sampler.used() >= sampler.total() {
			poolExhausted = true
			break
		}

		if wantCount > 1 {
			setProgress(fmt.Sprintf("第 %d 轮结束（本轮最快 %d kB/s，累计达标 %d/%d 个），继续下一轮...",
				round, roundBest.MaxSpeed, pool.qualified(speed), wantCount))
		} else {
			setProgress(fmt.Sprintf("第 %d 轮未达到期望带宽（本轮最快 %d kB/s），继续下一轮...",
				round, roundBest.MaxSpeed))
		}
	}

	// 轮次/子网用尽：把池里攒到的全部结果返回，而不是只给最快那一个。
	// 用户要 5 个但只凑到 3 个时，3 个也比 1 个有用。
	best := outcomeFromPool(pool)
	best.RoundsRun = roundsRun
	best.Tested = sampler.used()
	best.PoolSize = sampler.total()
	best.PoolExhausted = poolExhausted
	best.BelowTarget = best.IP != ""

	if best.IP != "" {
		if poolExhausted {
			setProgress(fmt.Sprintf("%d 个子网已全部测完仍未达标，返回最佳结果 %s:%d (%d kB/s)",
				sampler.total(), best.IP, best.Port, best.MaxSpeed))
		} else {
			setProgress(fmt.Sprintf("已尝试 %d 轮仍未达标，返回最佳结果 %s:%d (%d kB/s)",
				roundsRun, best.IP, best.Port, best.MaxSpeed))
		}
	}
	return best
}

// ----------------------- 多结果输出 -----------------------

// allowedResultCounts 允许的输出数量。
//
// 只给三档而不是任意数字：这个值直接决定扫描时长（每个结果都要占一次
// 完整测速预算），开放输入等于让用户自己踩「填 50 然后扫十分钟」的坑。
// 1 = 只要最快的一个（默认，最快出结果）
// 5 = 一把备选，够应付某个 IP 被墙的情况
// 10 = 尽量多拿，代价是明显变慢
var allowedResultCounts = []int{1, 5, 10}

// normalizeResultCount 把任意输入收敛到 allowedResultCounts 里的某一档。
//
// 取「不小于输入的最小档位」而不是四舍五入：用户想要 6 个时给 10 个
// （多给不亏），而不是给 5 个（少了不够用）。超过最大档位就取最大。
func normalizeResultCount(n int) int {
	if n <= 1 {
		return allowedResultCounts[0]
	}
	for _, c := range allowedResultCounts {
		if n <= c {
			return c
		}
	}
	return allowedResultCounts[len(allowedResultCounts)-1]
}

// speedResult 一个测速完成的候选。
type speedResult struct {
	IP         string
	Port       int
	MaxSpeed   int
	LatencyMs  int
	DataCenter string
	Country    string
}

// resultPool 按速度降序保留最快的若干个结果。
//
// 按 IP 去重而不是按 IP:端口：同一个 IP 在 443 和 2053 上的速度基本一样，
// 都留着等于用三个名额换一个选择。用户要 5 个结果是想要 5 个不同的落点，
// 拿去做备用节点或者分散使用，同 IP 换端口起不到这个作用。
type resultPool struct {
	limit int
	items []speedResult
	byIP  map[string]int
}

func newResultPool(limit int) *resultPool {
	if limit < 1 {
		limit = 1
	}
	return &resultPool{limit: limit, byIP: make(map[string]int, limit)}
}

// add 收入一个结果。同 IP 已存在时只在更快的情况下替换。
func (p *resultPool) add(r speedResult) {
	if r.IP == "" || r.MaxSpeed <= 0 {
		return
	}
	if idx, ok := p.byIP[r.IP]; ok {
		if r.MaxSpeed <= p.items[idx].MaxSpeed {
			return
		}
		p.items[idx] = r
		p.sortAndTrim()
		return
	}
	p.items = append(p.items, r)
	p.sortAndTrim()
}

func (p *resultPool) sortAndTrim() {
	sort.SliceStable(p.items, func(i, j int) bool {
		if p.items[i].MaxSpeed != p.items[j].MaxSpeed {
			return p.items[i].MaxSpeed > p.items[j].MaxSpeed
		}
		// 速度相同就让延迟低的靠前
		return p.items[i].LatencyMs < p.items[j].LatencyMs
	})
	if len(p.items) > p.limit {
		p.items = p.items[:p.limit]
	}
	// 索引必须整体重建：排序和截断都会让旧下标失效
	p.byIP = make(map[string]int, len(p.items))
	for i, it := range p.items {
		p.byIP[it.IP] = i
	}
}

// qualified 返回达到目标速度的结果个数。
func (p *resultPool) qualified(target int) int {
	n := 0
	for _, it := range p.items {
		if it.MaxSpeed >= target {
			n++
		}
	}
	return n
}

func (p *resultPool) len() int { return len(p.items) }

// best 返回最快的那个；池为空时返回零值。
func (p *resultPool) best() speedResult {
	if len(p.items) == 0 {
		return speedResult{}
	}
	return p.items[0]
}

// list 返回按速度降序排好的副本。
func (p *resultPool) list() []speedResult {
	out := make([]speedResult, len(p.items))
	copy(out, p.items)
	return out
}

// outcomeFromPool 把结果池转成 testOutcome。
//
// 顶层的 IP/Port/MaxSpeed 等字段填最快的那一个：界面和历史记录原来就读这些，
// 保持不变意味着「只要 1 个结果」这条路径的行为和以前完全一致。
func outcomeFromPool(pool *resultPool) testOutcome {
	items := pool.list()
	if len(items) == 0 {
		return testOutcome{}
	}
	top := items[0]
	return testOutcome{
		IP:         top.IP,
		Port:       top.Port,
		MaxSpeed:   top.MaxSpeed,
		LatencyMs:  top.LatencyMs,
		DataCenter: top.DataCenter,
		Country:    top.Country,
		Results:    items,
	}
}
