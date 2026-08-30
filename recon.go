package better

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------------- 地区侦察 -----------------------
//
// 只在用户选了地区时启用。
//
// 为什么需要它：官方优选是从 CF 官方子网随机拼 IP，落地机房事先不可知，
// 地区筛选只能「测完再丢」。原来这个丢弃发生在 runRTTTest 内部——
// 拿到 colo 发现国家不对，就把该 /24 标记跳过。但这个优化有两个硬伤：
//
//  1. skip 表是在 RTT 跑的过程中懒构建的。一批候选会被快速并发派发出去，
//     等第一个结果回来时同批大部分已经在跑，跳过救不回同批的开销。
//  2. 跨轮不复用。每轮换一批子网就从零重新发现哪些段不匹配，地区筛得窄时
//     （比如只要 HK）每轮几百个候选可能只有个位数有用，然后反复「换下一批」。
//
// 而每个候选的发现成本是 rttProbes 次 TCP + 一次 TLS+HTTP —— 用一次完整
// RTT 测量去换「这段不要」这一个比特的信息，太贵。
//
// 侦察阶段把这件事拆开：每个子网只拼 1 个 IP，只做一次 TCP+TLS+HTTP 拿
// CF-RAY，不测延迟、不算抖动、不测速。得到 subnet → country 的索引后，
// 后续所有轮次都只从命中的子网里取，索引跨轮复用。
//
// 索引只存内存、不落盘：colo 取决于用户 ISP 的 BGP 路由，切运营商或开
// VPN 后整张表立刻失效，持久化只会让下次扫描拿着错的表跑。

// batchSampler 子网批次来源。
//
// subnetSampler（不筛地区）和 regionSampler（筛地区、带侦察）都实现它，
// 让 cloudflareTest 的主循环不必关心底下有没有侦察这一层。
type batchSampler interface {
	next(n int) []string
	total() int
	used() int
}

// reconChunkSize 每次侦察一批多少个子网。
//
// 太小会让「攒够一批可用子网」要跑很多次侦察，每次都有并发爬坡的开销；
// 太大则在地区命中率高时白探一堆用不上的子网。200 在 6500 子网的池子上
// 大约是 3% 一刀，命中率 1% 的窄地区也能在几刀内攒出一批。
const reconChunkSize = 200

// reconMaxChunksPerBatch 一次 next() 最多连续侦察几刀就必须交货。
//
// 为什么需要：原来 next() 会一直侦察到攒满整批（sampleSize=100 个命中
// 子网）才返回。选了冷门地区时命中率可能低于 1%，攒满要把六千多个子网
// 几乎探一遍 —— 期间 RTT 一个没跑、轮次号停在 1、界面只有侦察计数在动，
// 用户看着就是「卡在第一轮」。
//
// 改成探够 3 刀（600 个子网）就先拿现有命中去测：哪怕只有十几个子网，
// 也能立刻进 RTT 和测速，用户几十秒内就看到实质进展。攒不满不影响正确性，
// 外层本来就是多轮循环，下一轮会继续从剩下的子网里侦察。
const reconMaxChunksPerBatch = 3

// reconTimeout 单次侦察探测的超时。
//
// 比 RTT 的 1s + 3s 更短：侦察只要「这个 IP 属于哪个机房」，不需要精确
// 计时也不容忍慢链路——慢到 1.5 秒还没握完手的 IP，本来也进不了优选。
// 探测量是 RTT 阶段的数倍（每个子网都要探），超时给太松会让侦察比
// 它省下来的时间还长。
const reconTimeout = 1500 * time.Millisecond

// subnetVerdict 一个子网的侦察结论。
//
// 必须是三态而不是「匹配/不匹配」两态：官方优选随机拼出来的 IP 大约只有
// 1/3 能响应（这是数据源特性，不是 bug）。探测失败的子网如果被当成
// 「地区不符」排除，会误杀掉大量本来可用的好子网 —— 命中率 1/3 意味着
// 每 3 个好子网里有 2 个会被冤枉。
type subnetVerdict int

const (
	// verdictUnknown 探测失败，或拿到 colo 但 locations.json 里查不到国家。
	// 不能据此排除，只能降级为候补。
	verdictUnknown subnetVerdict = iota
	// verdictMatched colo 对应的国家在用户所选范围内。
	verdictMatched
	// verdictMismatched colo 拿到了、国家也查到了，但不在所选范围内。
	// 这是唯一可以放心排除的情况。
	verdictMismatched
)

// probeColo 探测单个 IP 的 colo 三字码。返回 (colo, 状态码)。
// colo 为空表示探不出来，此时状态码无意义。
//
// 与 testRTT 的区别：只连一次、不计时、不重试。目的只有两个——拿 CF-RAY，
// 以及（填了 SNI 时）拿状态码判断能不能回源。
// 单次失败就返回空（判为 unknown 而非 mismatched），由调用方降级处理。
func probeColo(ip string, port int, useTLS bool, sni string) (string, int) {
	if sni == "" {
		if speedTestDomain != "" {
			sni = speedTestDomain
		} else {
			sni = "cloudflare.com"
		}
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))

	d := net.Dialer{Timeout: reconTimeout}
	conn, err := d.DialContext(scanCtx(), "tcp", addr)
	if err != nil {
		return "", 0
	}
	conn.SetDeadline(time.Now().Add(reconTimeout * 2))

	var rwc net.Conn = conn
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return "", 0
		}
		rwc = tlsConn
	}

	reqStr := "GET / HTTP/1.1\r\nHost: " + sni + "\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n\r\n"
	if _, err := rwc.Write([]byte(reqStr)); err != nil {
		rwc.Close()
		return "", 0
	}

	resp, err := http.ReadResponse(bufio.NewReader(rwc), nil)
	rwc.Close()
	if err != nil {
		return "", 0
	}
	resp.Body.Close()

	cfRay := resp.Header.Get("CF-RAY")
	if cfRay == "" {
		return "", 0
	}
	return extractDataCenter(cfRay), resp.StatusCode
}

// reconOne 侦察一个子网，返回结论和探到的 colo（探不到时 colo 为空）。
func reconOne(subnet string, ipType, port int, useTLS bool, sni string, filter scanFilter) (subnetVerdict, string) {
	ip := randomIPFromSubnet(subnet, ipType)
	if ip == "" {
		return verdictUnknown, ""
	}
	colo, status := probeColo(ip, port, useTLS, sni)
	if colo == "" {
		return verdictUnknown, ""
	}
	// 填了 SNI 且 CF 明确回了「到不了你的源」（521/522/523 等）时排除整段。
	//
	// 免费套餐下部分 CF IP 段对某些运营商回源不通，而回源路径是按段路由的，
	// 同一个 /24 的表现基本一致。既然侦察已经拿到了确定的错误码，就没必要
	// 让这一段的每个 IP 都去 RTT 阶段各撞一次。
	//
	// 依据是「拿到了 CF-RAY + 明确的错误码」这个确定证据，不是探测失败的
	// 猜测 —— 探测失败仍然走 unknown 降级，不会误杀。
	if strings.TrimSpace(sni) != "" && !originReachable(status) {
		return verdictMismatched, colo
	}
	country := countryOfColo(colo)
	if country == "" {
		// 拿到了 colo 但位置数据里没有这个机房（CF 时不时上线新机房）。
		// 与 allowsCountry 的 fail-open 语义保持一致：不排除，降级候补。
		return verdictUnknown, colo
	}
	if filter.allowsCountry(country) {
		return verdictMatched, colo
	}
	return verdictMismatched, colo
}

// randomIPFromSubnet 从子网里随机取 1 个 IP。取不出返回空串。
//
// 复用 getRandomIPv4s / getRandomIPv6s 而不是重写一遍拼装逻辑：
// 它们会按 ipsPerSubnet 生成多个，这里只取第一个。多生成一个字符串的
// 开销远小于「两处拼 IP 的逻辑各自演化然后不一致」的风险。
func randomIPFromSubnet(subnet string, ipType int) string {
	var cands []candidateIP
	if ipType == 6 {
		cands = getRandomIPv6s([]string{subnet})
	} else {
		cands = getRandomIPv4s([]string{subnet})
	}
	if len(cands) == 0 {
		return ""
	}
	return cands[0].IP
}

// reconStats 侦察的累计计数，用于进度展示。
type reconStats struct {
	probed     int
	matched    int
	mismatched int
	unknown    int
}

// reconChunk 并发侦察一批子网，把结论分别追加到 matched / unknown。
//
// mismatched 的子网直接丢弃 —— 那是这整个机制的收益所在：一次廉价探测
// 换掉后续所有轮次对这个子网的完整 RTT + 可能的测速。
func reconChunk(
	subnets []string, ipType, port int, useTLS bool, sni string,
	filter scanFilter, taskNum int, st *reconStats,
) (matched, unknown []string) {
	if len(subnets) == 0 {
		return nil, nil
	}
	if taskNum <= 0 {
		taskNum = 1
	}
	if taskNum > len(subnets) {
		taskNum = len(subnets)
	}

	type outcome struct {
		subnet  string
		verdict subnetVerdict
	}

	var wg sync.WaitGroup
	thread := make(chan struct{}, taskNum)
	results := make(chan outcome, len(subnets))

	for _, sn := range subnets {
		if isCancelled() {
			break
		}
		wg.Add(1)
		// 并发满时这里阻塞，取消后必须能立刻退出，
		// 否则用户点了取消还要等排队的子网全部探完
		select {
		case thread <- struct{}{}:
		case <-scanCtx().Done():
			wg.Done()
			goto collect
		}
		go func(subnet string) {
			defer func() {
				<-thread
				wg.Done()
			}()
			if isCancelled() {
				return
			}
			v, _ := reconOne(subnet, ipType, port, useTLS, sni, filter)
			results <- outcome{subnet: subnet, verdict: v}
		}(sn)
	}

collect:
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		st.probed++
		switch r.verdict {
		case verdictMatched:
			st.matched++
			matched = append(matched, r.subnet)
		case verdictMismatched:
			st.mismatched++
		default:
			st.unknown++
			unknown = append(unknown, r.subnet)
		}
	}
	return matched, unknown
}

// regionSampler 带地区侦察的子网抽样器。
//
// 对外和 subnetSampler 行为一致（next / total / used），但内部多了一层：
// 每次要不出足够的命中子网时，就从底层抽样器再取一刀做侦察，直到攒够
// 一批或者子网池见底。
//
// total() 报的是整个子网池的大小、used() 报的是已经侦察过的数量 ——
// 侦察本身就是「已检视」，覆盖率语义因此保持不变：侦察掉的子网算已覆盖，
// 不会因为引入侦察就让覆盖率下限永远达不到。
//
// 命中池和候补池都空、且底层取完时才返回 nil。候补池（unknown）是
// 防误杀的兜底：地区筛得极窄又赶上探测运气差时，宁可让用户拿到几个
// 「可能不在该地区」的结果去 RTT 阶段复检，也不要空手而归 ——
// runRTTTest 里的地区判定仍然生效，错的会在那里被拦下。
type regionSampler struct {
	base    *subnetSampler
	ipType  int
	port    int
	useTLS  bool
	sni     string
	filter  scanFilter
	taskNum int

	matched []string
	unknown []string
	stats   reconStats

	// maxChunks 一次 next() 最多连续侦察几刀。默认取
	// reconMaxChunksPerBatch，测试里会调小以便验证提前交货的行为。
	maxChunks int

	// usedUnknown 记录是否已经开始动用候补池，只为了提示文案不重复刷。
	usedUnknown bool
}

func newRegionSampler(
	base *subnetSampler, ipType, port int, useTLS bool, sni string,
	filter scanFilter, taskNum int,
) *regionSampler {
	return &regionSampler{
		base: base, ipType: ipType, port: port, useTLS: useTLS,
		sni: sni, filter: filter, taskNum: taskNum,
		maxChunks: reconMaxChunksPerBatch,
	}
}

func (r *regionSampler) total() int { return r.base.total() }
func (r *regionSampler) used() int  { return r.base.used() }

// next 取下一批最多 n 个命中子网。返回 nil 表示彻底没有了。
func (r *regionSampler) next(n int) []string {
	if n <= 0 {
		return nil
	}

	// 命中池不够就继续侦察，但最多连续探 reconMaxChunksPerBatch 刀就交货：
	// 攒不满也要让 RTT 尽快跑起来，否则窄地区下界面长时间只有侦察在动。
	//
	// 注意配额只在「已经有命中」时才生效。一个都没命中就交货会直接掉进
	// 下面的候补池分支，把待定子网当命中用 —— 那是子网池探完时的兜底，
	// 不该因为提前交货而提前触发。
	chunks := 0
	limit := r.maxChunks
	if limit <= 0 {
		limit = reconMaxChunksPerBatch
	}
	for len(r.matched) < n {
		if len(r.matched) > 0 && chunks >= limit {
			break
		}
		if isCancelled() {
			break
		}
		chunk := r.base.next(reconChunkSize)
		if chunk == nil {
			break
		}
		chunks++
		setScanProgress(fmt.Sprintf("地区侦察：正在探测 %d 个子网的落地机房（累计 %d/%d）...",
			len(chunk), r.base.used(), r.base.total()))

		m, u := reconChunk(chunk, r.ipType, r.port, r.useTLS, r.sni, r.filter, r.taskNum, &r.stats)
		r.matched = append(r.matched, m...)
		r.unknown = append(r.unknown, u...)

		// 命中率让用户对「还要攒多久」有预期：命中的子网越多，
		// 剩余需要侦察的量就越少，扫完地区的总时长是可以大致估的。
		rate := ""
		if r.stats.probed > 0 {
			rate = fmt.Sprintf("，命中率 %.1f%%", float64(r.stats.matched)/float64(r.stats.probed)*100)
		}
		setScanProgress(fmt.Sprintf("地区侦察：已探测 %d/%d 个子网，命中 %d 个，排除 %d 个，待定 %d 个%s",
			r.stats.probed, r.base.total(), r.stats.matched, r.stats.mismatched, r.stats.unknown, rate))
	}

	if len(r.matched) > 0 {
		take := n
		if take > len(r.matched) {
			take = len(r.matched)
		}
		batch := r.matched[:take]
		r.matched = r.matched[take:]
		return batch
	}

	// 命中池空了：动用候补池（探测失败或机房位置未知的子网）
	if len(r.unknown) > 0 {
		if !r.usedUnknown {
			r.usedUnknown = true
			setScanProgress(fmt.Sprintf("所选地区命中的子网已用尽，改用 %d 个待定子网继续（地区仍会在 RTT 阶段复检）...",
				len(r.unknown)))
		}
		take := n
		if take > len(r.unknown) {
			take = len(r.unknown)
		}
		batch := r.unknown[:take]
		r.unknown = r.unknown[take:]
		return batch
	}

	return nil
}
