package better

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ----------------------- 测速源 -----------------------
//
// 为什么要有「测速源」这个概念，而不是一个固定地址：
//
// 之前测速地址硬编码取自 url.txt（cloudflaremirrors.com 上的 Oracle Linux
// ISO）。那是个静态大文件，问题有三个：
//   1. 依赖边缘缓存是否命中。没命中时 CF 要回源到镜像站，那一段链路
//      跟被测 IP 完全无关，测出来的数字忽高忽低。
//   2. 上游随时可能改版或删文件，一 404 全部候选测速归零。
//   3. 单一域名，运营商对它的 QoS 策略就是唯一策略。中国移动国际出口
//      对不同域名/不同段限速差别很大，这直接导致「优选很快、实际很慢」。
//
// 改用 __down 这类「按需生成流」的端点：不吃缓存、字节数可控、不会失效。
// 再按 ISP 挑选对该运营商友好的源，测出来的才接近用户实际能跑到的速度。

// 内置测速源。三个都实测可用（非直连、指定任意 CF IP 均返回 200）。
const (
	// cloudflareSpeedURL Cloudflare 官方测速端点，通用首选。
	cloudflareSpeedURL = "speed.cloudflare.com/__down?bytes=99999999"
	// cmSpeedURL 社区提供的 CF Worker 测速端点，移动线路表现较好。
	cmSpeedURL = "cf.090227.xyz/__down?bytes=99999999"
	// mobileDedicatedSpeedURL 移动专属测速源，直接回 100MB octet-stream。
	mobileDedicatedSpeedURL = "speed.okl.abrdns.com/"
)

// ispProbeURL ISP 探测端点，返回 CF 视角看到的 asn / asOrganization。
//
// 用 CF 自己的 cf.json 而不是第三方 IP 库：这里要的不是「用户真实归属」，
// 而是「CF 边缘认为这个客户端来自哪个 AS」—— 决定限速策略的正是后者。
//
// var 而非 const：单测要指向本地 httptest 服务器，不然每跑一次测试
// 都要访问外网。生产代码不改它。
var ispProbeURL = "https://cf.090227.xyz/cf.json"

// SpeedSourceAuto 等值：界面传这个（或空串）表示自动挑选。
const SpeedSourceAuto = "auto"

// 测速源标识。界面用这些值，核心层负责翻译成实际地址。
const (
	SpeedSourceCloudflare = "cloudflare"
	SpeedSourceCM         = "cm"
	SpeedSourceMobile     = "mobile"
	// SpeedSourceCustom 用户手动填地址。此时以 customSpeedURL 为准，
	// 这个标识只是让界面知道该显示输入框。
	SpeedSourceCustom = "custom"
)

// SpeedSources 返回可选测速源标识的 CSV，供界面构建选项。
//
// 同 Ports/ResultCounts/SpeedSeconds：选项定义只留核心层一份，
// 否则界面能选、核心层不认。
func SpeedSources() string {
	return strings.Join([]string{
		SpeedSourceAuto, SpeedSourceCloudflare, SpeedSourceCM,
		SpeedSourceMobile, SpeedSourceCustom,
	}, ",")
}

// SpeedSourceLabels 返回与 SpeedSources() 一一对应的中文名 CSV。
func SpeedSourceLabels() string {
	return strings.Join([]string{
		"自动选择", "Cloudflare", "CM提供", "移动专属", "手动输入",
	}, ",")
}

// autoSpeedSource 自动档实际选中的地址。
//
// 由 ISP 探测填写，未探测或探测失败时保持 Cloudflare 官方端点 ——
// 探测只是优化，失败不该让测速不可用。
var autoSpeedSource = struct {
	sync.RWMutex
	value string
	// isp 探测到的 AS 组织名，用于在结果里向用户交代「为什么选了这个源」
	isp string
}{value: cloudflareSpeedURL}

func setAutoSpeedSource(addr, isp string) {
	autoSpeedSource.Lock()
	defer autoSpeedSource.Unlock()
	if strings.TrimSpace(addr) == "" {
		autoSpeedSource.value = cloudflareSpeedURL
	} else {
		autoSpeedSource.value = addr
	}
	autoSpeedSource.isp = isp
}

func currentAutoSpeedSource() (string, string) {
	autoSpeedSource.RLock()
	defer autoSpeedSource.RUnlock()
	if strings.TrimSpace(autoSpeedSource.value) == "" {
		return cloudflareSpeedURL, autoSpeedSource.isp
	}
	return autoSpeedSource.value, autoSpeedSource.isp
}

// ispProbeInfo cf.json 里我们关心的两个字段。
type ispProbeInfo struct {
	ASN            int    `json:"asn"`
	ASOrganization string `json:"asOrganization"`
}

// ispProbeClient ISP 探测专用客户端。
//
// 必须禁用代理（Proxy: nil）。与地区判定同一套约定：挂着代理探出来的是
// 代理出口的 AS，据此选测速源等于按别人的网络环境优化，纯属帮倒忙。
var ispProbeClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy:             nil,
		DialContext:       (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
	},
}

// detectISP 探测 CF 边缘看到的客户端 AS 信息。
func detectISP(ctx context.Context) (ispProbeInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ispProbeURL, nil)
	if err != nil {
		return ispProbeInfo{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := ispProbeClient.Do(req)
	if err != nil {
		return ispProbeInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ispProbeInfo{}, fmt.Errorf("ISP 探测返回 HTTP %d", resp.StatusCode)
	}

	var info ispProbeInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return ispProbeInfo{}, err
	}
	return info, nil
}

// chinaMobileASNs 中国移动及其国际出口的 AS 号。
//
// 9808  = 中国移动骨干（CMNET）
// 24400 = 中国移动上海
// 56040/56041/56044 = 各省移动
var chinaMobileASNs = map[int]bool{
	9808: true, 24400: true, 56040: true, 56041: true, 56044: true,
}

// isChinaMobile 判断是否移动线路。
//
// 组织名和 ASN 双路判断：组织名格式各地不统一（CMI / CMNET / China Mobile /
// 中国移动都见过），ASN 更可靠但覆盖不全（虚商、专线会挂在别的号下）。
func isChinaMobile(info ispProbeInfo) bool {
	org := strings.ToLower(info.ASOrganization)
	for _, kw := range []string{
		"cmi", "cmnet", "cmcc", "chinamobile", "china mobile",
		"mobile communications", "移动",
	} {
		if strings.Contains(org, kw) {
			return true
		}
	}
	return chinaMobileASNs[info.ASN]
}

// pickMobileSpeedURL 在移动友好的两个源里随机挑一个。
//
// 随机而不是固定：这两个都是社区资源，全部用户都压同一个会把它打挂。
// 用 crypto/rand 而非 math/rand 只是图省事 —— 不用管种子。
func pickMobileSpeedURL() string {
	urls := []string{cmSpeedURL, mobileDedicatedSpeedURL}
	idx := 0
	if n, err := rand.Int(rand.Reader, big.NewInt(int64(len(urls)))); err == nil {
		idx = int(n.Int64())
	}
	return urls[idx]
}

// refreshAutoSpeedSource 探测 ISP 并更新自动档选中的地址。
//
// 返回选中的地址与 AS 组织名。探测失败时回落 Cloudflare 官方端点并返回
// 错误 —— 调用方只需记一条日志，不该因此中断扫描。
func refreshAutoSpeedSource(ctx context.Context) (string, string, error) {
	info, err := detectISP(ctx)
	if err != nil {
		setAutoSpeedSource(cloudflareSpeedURL, "")
		return cloudflareSpeedURL, "", err
	}
	addr := cloudflareSpeedURL
	if isChinaMobile(info) {
		addr = pickMobileSpeedURL()
	}
	setAutoSpeedSource(addr, info.ASOrganization)
	return addr, info.ASOrganization, nil
}

// resolveSpeedSource 把「测速源标识 + 手动地址」翻译成实际测速地址。
//
// 返回 (域名, 路径含查询串, 错误)。空标识按 auto 处理，让老版本前端
// 不传这个字段也能工作。
func resolveSpeedSource(source, customURL string) (string, string, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	custom := strings.TrimSpace(customURL)

	// 填了手动地址就以它为准，不管标识是什么。
	// 界面上「选了手动输入却没填」和「填了却没切标识」都是常见操作，
	// 按用户实际输入走比按标识走更符合预期。
	if custom != "" {
		return parseSpeedURL(custom)
	}

	switch source {
	case SpeedSourceCustom:
		return "", "", fmt.Errorf("选择了手动输入测速地址，但地址是空的")
	case SpeedSourceCloudflare:
		return parseSpeedURL(cloudflareSpeedURL)
	case SpeedSourceCM:
		return parseSpeedURL(cmSpeedURL)
	case SpeedSourceMobile:
		return parseSpeedURL(mobileDedicatedSpeedURL)
	case "", SpeedSourceAuto:
		addr, _ := currentAutoSpeedSource()
		return parseSpeedURL(addr)
	default:
		return "", "", fmt.Errorf("未知的测速源: %q", source)
	}
}

// parseSpeedURL 解析测速地址，返回 (域名, 路径含查询串)。
//
// 用 net/url 解析而不是手工切字符串：要支持带端口（a.com:8443/x.bin）、
// 带查询串（a.com/__down?bytes=99999999）、协议省略、// 前缀等写法。
// 手工切的版本把带 ":" 的域名一律判非法，__down 这类端点根本填不进来。
//
// 返回的域名不含端口。测速连的是被测 IP 加用户选的端口，URL 里的端口
// 没有意义，留着只会让 TLS SNI 带上端口导致握手失败。
func parseSpeedURL(raw string) (string, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", nil
	}

	// 补协议。scheme 只影响解析，实际用哪个由被测端口是否 TLS 决定。
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	} else if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		value = "https://" + value
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", fmt.Errorf("测速地址格式不对：%v", err)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", "", fmt.Errorf("测速地址缺少域名")
	}
	// 域名里不该有空格：多半是粘贴时带进来的，早点说清楚
	if strings.ContainsAny(host, " \t") {
		return "", "", fmt.Errorf("测速域名格式不对：%q", host)
	}

	// 路径连查询串一起带上：__down?bytes=... 的字节数在查询串里，
	// 丢了它就变成下载 0 字节。
	path := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		// 允许只填域名：像 speed.okl.abrdns.com 这样根路径就是大文件的
		// 源确实存在。真拿到首页 HTML 会被 speedTestMinBytes 诊断兜住，
		// 收尾时提示「文件太小，换个大文件」。
		path = ""
	}
	return host, path, nil
}
