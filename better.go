package better

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ScanResult 扫描结果
type ScanResult struct {
	IP string `json:"ip"`
	// Port 实测通过的端口。
	//
	// 必须输出：原版只给 IP，用户拿去接一个跑在 2053 的节点，
	// 而工具验证的是 443 —— 握手能过、数据一发就被掐断，
	// 报 io: read/write on closed pipe。给出 IP:端口才是完整地址。
	Port int `json:"port"`
	// Address 直接可用的 IP:端口，省得界面自己拼
	Address       string `json:"address"`
	Bandwidth     int    `json:"bandwidth"`     // 期望带宽 Mbps
	RealBandwidth int    `json:"realBandwidth"` // 实测带宽 Mbps
	MaxSpeed      int    `json:"maxSpeed"`      // 峰值速度 kB/s
	LatencyMs     int    `json:"latencyMs"`
	DataCenter    string `json:"dataCenter"`
	// Country 实测落地国家代码。官方 IP 的落地机房与运营商线路有关，
	// 事先无法预测，只能测完才知道。
	Country string `json:"country"`
	Elapsed int    `json:"elapsed"` // 总计用时 秒
	// Cancelled 区分「用户主动取消」与「没找到」。
	// 原版两者都返回空 IP，界面只能显示"未找到"，会误导用户。
	Cancelled bool `json:"cancelled"`
	// BelowTarget 有结果但未达到期望带宽。此时 IP 仍然可用，
	// 界面应该同时显示结果和提示，而不是只给一个错误。
	BelowTarget bool   `json:"belowTarget"`
	Error       string `json:"error"`

	// Results 全部结果，按实测速度降序。用户选「输出 5 个 / 10 个」时
	// 这里就有多条；选 1 个时只有一条，内容和上面那些顶层字段相同。
	//
	// 顶层字段没有因此废弃：界面和历史记录原来就读顶层，保留它们意味着
	// 「只要 1 个」这条老路径的行为一字不变。
	Results []ScanItem `json:"results"`
	// WantCount 实际生效的输出数量（已收敛到 1/5/10 之一）。
	// 回传是因为界面填 6 会被收敛成 10，得让用户看到真实值。
	WantCount int `json:"wantCount"`
	// FoundCount 实际达标的结果个数。要 5 个只凑到 3 个时，
	// 界面得能说清「找到 3 个」而不是假装有 5 个。
	FoundCount int `json:"foundCount"`
}

// ScanItem 单条优选结果。字段与 ScanResult 的顶层同名字段一致。
type ScanItem struct {
	IP            string `json:"ip"`
	Port          int    `json:"port"`
	Address       string `json:"address"`
	RealBandwidth int    `json:"realBandwidth"`
	MaxSpeed      int    `json:"maxSpeed"`
	LatencyMs     int    `json:"latencyMs"`
	DataCenter    string `json:"dataCenter"`
	Country       string `json:"country"`
}

// ResultCounts 返回允许的输出数量 CSV（如 "1,5,10"），供界面构建选项。
//
// 和端口列表同理：不在界面里硬编码，两处各写一份改一处忘另一处
// 就会出现「界面能选、核心层不认」的档位。
func ResultCounts() string { return joinInts(allowedResultCounts) }

// Version 返回核心层版本号，供界面显示
func Version() string { return libVersion }

const libVersion = "1.15"

// HTTPPorts 返回明文模式可选端口的 CSV，供界面构建选项
func HTTPPorts() string { return joinInts(cfHTTPPorts) }

// HTTPSPorts 返回 TLS 模式可选端口的 CSV，供界面构建选项
func HTTPSPorts() string { return joinInts(cfHTTPSPorts) }

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

// SetCacheDir 设置缓存目录（Android 应用数据目录）
// gomobile 必须显式设置，Android 下通常设为 Context.getFilesDir()
func SetCacheDir(dir string) {
	dataDir = dir
}

// GetProgress 返回当前进度描述，供 Android 端轮询
func GetProgress() string {
	progressMu.Lock()
	defer progressMu.Unlock()
	return progress
}

func setProgress(s string) {
	progressMu.Lock()
	progress = s
	progressMu.Unlock()
}

// CancelScan 取消正在进行的任务（扫描或数据更新），立即中断所有网络操作
func CancelScan() {
	cancelMu.Lock()
	if cancelCancel != nil {
		cancelCancel()
	}
	cancelMu.Unlock()
	// 措辞要中性：数据更新也走这个取消通道，
	// 写成"已取消扫描"会让用户以为点错了按钮。
	// 具体是取消了什么，由各任务结束时的收尾消息覆盖。
	setProgress("正在取消...")
}

func isCancelled() bool {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	if cancelCtx == nil {
		return false
	}
	select {
	case <-cancelCtx.Done():
		return true
	default:
		return false
	}
}

// BeginTask 在把任务派发到后台线程之前同步调用一次。
//
// 为什么需要：GetIPs / UpdateData 会在后台线程里重建取消上下文。
// 如果用户在「点了开始」和「任务真正跑起来」之间点取消，
// 那次取消会被重建动作抹掉，取消按钮看起来就是失灵了。
// 先在前台 BeginTask()，这段窗口里到达的取消就能保留下来。
func BeginTask() {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	cancelCtx, cancelCancel = context.WithCancel(context.Background())
	taskRequested = true
}

// enterTask 任务真正开始时取用取消上下文。
// BeginTask 已经建好就沿用（保留这期间到达的取消），否则新建一个。
// 后者覆盖「上一个任务留下的已取消上下文」，避免旧取消泄漏到新任务。
func enterTask() {
	cancelMu.Lock()
	defer cancelMu.Unlock()
	if !taskRequested {
		cancelCtx, cancelCancel = context.WithCancel(context.Background())
	}
	taskRequested = false
}

// GetIPs 运行 Cloudflare IP 优选，返回结果 JSON。
//
// v4: true=IPv4 false=IPv6
// useTLS: 是否启用 TLS 握手
// bandwidth: 期望带宽（Mbps），设为 0 则使用默认 1 Mbps
// ports: 要测的端口 CSV（如 "443,2053"），空则只测默认端口（TLS 443 / 明文 80）
// countries: 允许的落地国家代码 CSV（如 "HK,JP"），空则不限
// sni: 自定义 SNI/Host，空则用 cloudflare.com
// wantCount: 要输出几个结果，收敛到 1/5/10 之一（见 ResultCounts）。
//
// 关于 wantCount —— 它直接决定扫描时长：每个结果都要占一次完整测速预算
// （speedTestFullBudget），要 10 个就意味着单轮测速时间翻几倍。所以只开放
// 三档而不是任意数字，避免用户填个 50 然后等十分钟以为程序卡死。
//
// 关于 countries —— 和反代优选有本质区别：反代节点列表自带国家标签，
// 可以先筛后测；官方 IP 的落地机房取决于运营商线路，同一个 IP 在
// 电信和联通下可能落到不同国家，事先无从得知。所以这里是「测完再筛」，
// 筛得越窄要试的子网就越多，耗时会明显变长。
//
// 阻塞调用，需在后台线程执行。
// 界面派发到后台线程之前应先调 BeginTask()，否则这段窗口里的取消会丢。
func GetIPs(v4 bool, useTLS bool, bandwidth int, ports string, countries string, sni string,
	wantCount int) string {
	enterTask()
	wantCount = normalizeResultCount(wantCount)
	setProgress("正在初始化...")

	ipType := 4
	if !v4 {
		ipType = 6
	}

	if bandwidth <= 0 {
		bandwidth = 1
	}
	// 带宽上限保护：填个 999999 除了让扫描永远达不到目标、
	// 白跑满 10 轮之外没有任何意义
	if bandwidth > maxBandwidthMbps {
		bandwidth = maxBandwidthMbps
	}

	filter := scanFilter{
		Ports:     parsePortsCSV(ports, useTLS),
		Countries: parseCountriesCSV(countries),
	}

	// 转为 kB/s
	speedTarget := bandwidth * 128

	startTime := timeNow()

	out := cloudflareTest(ipType, useTLS, defaultTaskNum, speedTarget, filter, strings.TrimSpace(sni),
		wantCount)

	realBandwidth := out.MaxSpeed / 128
	elapsed := int(timeSince(startTime).Seconds())

	result := ScanResult{
		IP:            out.IP,
		Port:          out.Port,
		Bandwidth:     bandwidth,
		RealBandwidth: realBandwidth,
		MaxSpeed:      out.MaxSpeed,
		LatencyMs:     out.LatencyMs,
		DataCenter:    out.DataCenter,
		Country:       out.Country,
		Elapsed:       elapsed,
		WantCount:     wantCount,
	}
	if out.IP != "" && out.Port > 0 {
		result.Address = net.JoinHostPort(out.IP, strconv.Itoa(out.Port))
	}
	for _, it := range out.Results {
		item := ScanItem{
			IP:            it.IP,
			Port:          it.Port,
			RealBandwidth: it.MaxSpeed / 128,
			MaxSpeed:      it.MaxSpeed,
			LatencyMs:     it.LatencyMs,
			DataCenter:    it.DataCenter,
			Country:       it.Country,
		}
		if it.IP != "" && it.Port > 0 {
			item.Address = net.JoinHostPort(it.IP, strconv.Itoa(it.Port))
		}
		result.Results = append(result.Results, item)
		if it.MaxSpeed >= speedTarget {
			result.FoundCount++
		}
	}

	switch {
	case isCancelled():
		// 用户主动取消：不能报"未找到"，否则界面会误导用户
		result.Cancelled = true
		result.IP = ""
		result.Address = ""
		// 列表也要清空：只清 IP 会让界面显示"未找到"却又列出一串结果
		result.Results = nil
		result.FoundCount = 0
		result.Error = "扫描已取消"
		setProgress("扫描已取消")

	case out.IP == "":
		// 地区筛选下要说清是「筛没了」还是「都不通」，
		// 否则用户会以为工具坏了，而实际上只是条件太窄
		scope := ""
		if len(filter.Countries) > 0 {
			scope = "（限定地区：" + strings.ToUpper(countries) + "）"
		}
		// 区分「全部子网测完」与「轮次用尽」，不谎报轮数
		if out.PoolExhausted {
			result.Error = fmt.Sprintf("%d 个子网已全部测过，没有一个 IP 符合条件%s（用时 %d 秒）",
				out.PoolSize, scope, elapsed)
		} else {
			result.Error = fmt.Sprintf("已测试 %d 个子网（共 %d 轮），未找到可用 IP%s（用时 %d 秒）",
				out.Tested, out.RoundsRun, scope, elapsed)
		}
		if len(filter.Countries) > 0 {
			result.Error += "。官方 IP 的落地地区取决于运营商线路，缩小地区会大幅降低命中率，可放宽后重试"
		}
		setProgress(fmt.Sprintf("扫描结束，用时 %d 秒", elapsed))

	case out.BelowTarget:
		// 有结果但未达标：IP 仍然可用，同时给出说明
		result.BelowTarget = true
		if out.PoolExhausted {
			result.Error = fmt.Sprintf("%d 个子网已全部测过，均未达到 %d Mbps，返回最佳结果 %d Mbps",
				out.PoolSize, bandwidth, realBandwidth)
		} else {
			result.Error = fmt.Sprintf("已测试 %d 个子网（共 %d 轮）未达到 %d Mbps，返回最佳结果 %d Mbps",
				out.Tested, out.RoundsRun, bandwidth, realBandwidth)
		}
		if wantCount > 1 {
			result.Error += fmt.Sprintf("；共返回 %d 个结果", len(result.Results))
		}
		setProgress(fmt.Sprintf("扫描完成，用时 %d 秒", elapsed))

	case wantCount > 1 && result.FoundCount < wantCount:
		// 达标但数量没凑够。不能沉默 —— 用户选了 10 个却只拿到 4 个，
		// 得知道是子网测完了还是轮次用尽了，而不是以为界面吞了结果。
		if out.PoolExhausted {
			result.Error = fmt.Sprintf("%d 个子网已全部测过，只找到 %d 个达标 IP（要求 %d 个）",
				out.PoolSize, result.FoundCount, wantCount)
		} else {
			result.Error = fmt.Sprintf("已测试 %d 个子网（共 %d 轮），只找到 %d 个达标 IP（要求 %d 个）",
				out.Tested, out.RoundsRun, result.FoundCount, wantCount)
		}
		setProgress(fmt.Sprintf("扫描完成，找到 %d 个，用时 %d 秒", result.FoundCount, elapsed))

	default:
		if wantCount > 1 {
			setProgress(fmt.Sprintf("扫描完成，找到 %d 个，用时 %d 秒", result.FoundCount, elapsed))
		} else {
			setProgress(fmt.Sprintf("扫描完成，用时 %d 秒", elapsed))
		}
	}

	b, _ := json.Marshal(result)
	return string(b)
}

// dataFiles 需要下载与缓存的数据文件
var dataFiles = []string{"locations.json", "ips-v4.txt", "ips-v6.txt", "url.txt"}

// UpdateData 重新下载所有数据文件（清空缓存后重新下载）。
// 返回空字符串表示成功，否则是给用户看的失败原因。
// 返回值不能省：界面无法从 void 里判断成败，只能无条件报"更新完成"，
// 那就是谎报。
//
// 阻塞调用，需在后台线程执行。界面派发前应先调 BeginTask()。
func UpdateData() string {
	// 必须取一个干净的取消上下文。否则用户「取消扫描 → 点更新数据」时，
	// downloadAllData 会在第一个 isCancelled() 检查处直接返回，
	// 但进度照样显示"数据更新完成" —— 删掉了旧文件却什么都没下载，
	// 下次扫描才发现数据没了。
	enterTask()

	setProgress("正在更新数据...")
	for _, f := range dataFiles {
		removeFile(dataPath(f))
	}
	initLocations()

	// 不能无条件报成功：下载失败时 downloadAllData 已经写了错误原因，
	// 这里再盖一层"更新完成"就是谎报。
	if isCancelled() {
		setProgress("数据更新已取消")
		return "数据更新已取消"
	}
	if missing := missingDataFiles(); len(missing) > 0 {
		msg := fmt.Sprintf("数据更新未完成，缺少 %s（检查网络后重试）",
			strings.Join(missing, "、"))
		setProgress(msg)
		return msg
	}
	setProgress("数据更新完成")
	return ""
}

// missingDataFiles 返回仍然缺失的数据文件名
func missingDataFiles() []string {
	var missing []string
	for _, f := range dataFiles {
		if !fileExists(dataPath(f)) {
			missing = append(missing, f)
		}
	}
	return missing
}

// ClearCache 清除缓存的数据文件
func ClearCache() {
	setProgress("正在清除缓存...")
	for _, f := range dataFiles {
		removeFile(dataPath(f))
	}
	setProgress("缓存已清除")
}
