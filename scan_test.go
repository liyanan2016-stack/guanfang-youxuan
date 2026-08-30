package better

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// ---------- 子网抽样器 ----------

func mkSubnets(n int) []string {
	list := make([]string, 0, n)
	for i := 0; i < n; i++ {
		// 每个子网必须唯一，否则测的是辅助函数的 bug 而不是抽样器
		list = append(list, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}
	return list
}

// 单次扫描内跨轮零重复 —— 本次改动的核心保证
func TestSamplerNoDuplicateAcrossRounds(t *testing.T) {
	pool := mkSubnets(6534) // 真实 ips-v4.txt 的规模
	s := newSubnetSampler(pool)

	seen := make(map[string]struct{})
	for round := 1; round <= maxScanRounds; round++ {
		batch := s.next(sampleSize)
		if batch == nil {
			t.Fatalf("第 %d 轮就取空了，6534 个子网跑 10 轮不该耗尽", round)
		}
		if len(batch) != sampleSize {
			t.Fatalf("第 %d 轮取到 %d 个，期望 %d", round, len(batch), sampleSize)
		}
		for _, sn := range batch {
			if _, dup := seen[sn]; dup {
				t.Fatalf("第 %d 轮重复取到 %s", round, sn)
			}
			seen[sn] = struct{}{}
		}
	}
	if len(seen) != sampleSize*maxScanRounds {
		t.Fatalf("10 轮共取 %d 个，期望 %d 个互不相同", len(seen), sampleSize*maxScanRounds)
	}
}

// 小池子应取完即耗尽，而不是把同一批反复测 10 遍
func TestSamplerExhausts(t *testing.T) {
	s := newSubnetSampler(mkSubnets(150))
	if n := len(s.next(100)); n != 100 {
		t.Fatalf("首轮取 %d，期望 100", n)
	}
	if n := len(s.next(100)); n != 50 {
		t.Fatalf("次轮取 %d，期望 50", n)
	}
	if got := s.next(100); got != nil {
		t.Fatalf("第三轮应耗尽返回 nil，实际拿到 %d 个", len(got))
	}
}

// 全池取完时每个子网恰好出现一次
func TestSamplerFullPassExactlyOnce(t *testing.T) {
	pool := mkSubnets(500)
	s := newSubnetSampler(pool)

	count := make(map[string]int)
	for {
		batch := s.next(100)
		if batch == nil {
			break
		}
		for _, sn := range batch {
			count[sn]++
		}
	}
	if len(count) != 500 {
		t.Fatalf("取完全池只覆盖 %d 个，期望 500", len(count))
	}
	for sn, c := range count {
		if c != 1 {
			t.Fatalf("%s 被取了 %d 次，期望恰好 1 次", sn, c)
		}
	}
}

func TestSamplerEdgeCases(t *testing.T) {
	s := newSubnetSampler(nil)
	if got := s.next(100); got != nil {
		t.Fatalf("空池应返回 nil，实际 %v", got)
	}
	s = newSubnetSampler(mkSubnets(1))
	if n := len(s.next(100)); n != 1 {
		t.Fatalf("单条池应取到 1 个，实际 %d", n)
	}
	if got := s.next(100); got != nil {
		t.Fatal("单条池第二次应耗尽")
	}
}

// ---------- IP 生成 ----------

func TestGetRandomIPv4s(t *testing.T) {
	in := []string{"1.0.0.0/24", "8.6.144.0/24", "  ", "bad", "104.16.0.0/24"}
	out := getRandomIPv4s(in)
	// 每个子网产出 ipsPerSubnet 个 IP：只取 1 个时，那一个恰好不响应
	// 整段就被误判为死，命中率被白白压低
	want := 3 * ipsPerSubnet
	if len(out) != want {
		t.Fatalf("3 个合法子网 × %d 应产出 %d 个 IP，实际 %d: %v",
			ipsPerSubnet, want, len(out), out)
	}
	for _, c := range out {
		p := net.ParseIP(c.IP)
		if p == nil || p.To4() == nil {
			t.Errorf("%q 不是合法 IPv4", c.IP)
		}
		// 必须记住来源子网，地区筛选靠它按段跳过
		if c.Subnet == "" {
			t.Errorf("%q 没有记录来源子网", c.IP)
		}
	}
	// 生成的 IP 必须落在原子网内
	if !strings.HasPrefix(out[0].IP, "1.0.0.") {
		t.Errorf("1.0.0.0/24 生成了 %s，超出子网范围", out[0].IP)
	}
	if out[0].Subnet != "1.0.0.0/24" {
		t.Errorf("来源子网应为 1.0.0.0/24，实际 %q", out[0].Subnet)
	}
}

// 同一子网内不该重复生成同一个地址：测两次同一个 IP 不提高命中率，
// 只是白浪费一次 RTT
func TestGetRandomIPv4sNoDuplicateInSubnet(t *testing.T) {
	for range 50 {
		out := getRandomIPv4s([]string{"104.16.0.0/24"})
		seen := make(map[string]struct{})
		for _, c := range out {
			if _, dup := seen[c.IP]; dup {
				t.Fatalf("同一子网内生成了重复 IP %s", c.IP)
			}
			seen[c.IP] = struct{}{}
		}
	}
}

// /48 是 CF 实际使用的前缀，前 3 段必须保持不变
func TestGetRandomIPv6sKeepsPrefix48(t *testing.T) {
	out := getRandomIPv6s([]string{"2400:cb00:2048::/48"})
	if len(out) != ipsPerSubnet {
		t.Fatalf("期望 %d 个结果，实际 %d", ipsPerSubnet, len(out))
	}
	_, subnet, _ := net.ParseCIDR("2400:cb00:2048::/48")
	for _, c := range out {
		ip := net.ParseIP(c.IP)
		if ip == nil || ip.To4() != nil {
			t.Fatalf("%q 不是合法 IPv6", c.IP)
		}
		if !subnet.Contains(ip) {
			t.Errorf("生成的 %s 不在 2400:cb00:2048::/48 内", c.IP)
		}
	}
}

// 前缀长度必须被尊重：原版固定保留 3 段，遇到 /64 会拼出不属于该子网的地址
func TestGetRandomIPv6sRespectsPrefixLen(t *testing.T) {
	cases := []string{
		"2400:cb00:2048::/48",
		"2606:4700::/32",
		"2803:f800:0050:0001::/64",
	}
	for _, cidr := range cases {
		out := getRandomIPv6s([]string{cidr})
		if len(out) != ipsPerSubnet {
			t.Fatalf("%s 期望 %d 个结果，实际 %d", cidr, ipsPerSubnet, len(out))
		}
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("测试用例 %s 本身不合法: %v", cidr, err)
		}
		for _, c := range out {
			ip := net.ParseIP(c.IP)
			if ip == nil {
				t.Fatalf("%s 生成了非法 IP %q", cidr, c.IP)
			}
			if !subnet.Contains(ip) {
				t.Errorf("%s 生成的 %s 不在子网内", cidr, c.IP)
			}
		}
	}
}

func TestGetRandomIPv6sSkipsGarbage(t *testing.T) {
	out := getRandomIPv6s([]string{"", "  ", "not-an-ip", "1.2.3.4/24"})
	for _, c := range out {
		if net.ParseIP(c.IP) == nil {
			t.Errorf("垃圾输入产出了非法 IP %q", c.IP)
		}
	}
}

// ---------- 结果结构 ----------

// 取消必须能和「没找到」区分开，否则界面会把取消显示成失败
func TestScanResultDistinguishesCancel(t *testing.T) {
	var r ScanResult
	if err := json.Unmarshal([]byte(`{"cancelled":true,"error":"扫描已取消"}`), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Cancelled {
		t.Error("cancelled 字段未正确反序列化")
	}

	b, err := json.Marshal(ScanResult{IP: "1.2.3.4", BelowTarget: true, RealBandwidth: 61})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"belowTarget":true`, `"cancelled":false`, `"realBandwidth":61`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("序列化结果缺少 %s: %s", want, b)
		}
	}
}

func TestExtractDataCenter(t *testing.T) {
	cases := map[string]string{
		"7d8d4f0a9c2e1b3a-LAX": "LAX",
		"abc-HKG":              "HKG",
		"":                     "",
		"noseparator":          "",
	}
	for in, want := range cases {
		if got := extractDataCenter(in); got != want {
			t.Errorf("extractDataCenter(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestParseIPList(t *testing.T) {
	got := parseIPList("1.0.0.0/24\n\n  8.6.144.0/24  \n\n")
	if len(got) != 2 {
		t.Fatalf("期望 2 条，实际 %d: %v", len(got), got)
	}
	if got[1] != "8.6.144.0/24" {
		t.Errorf("未去除首尾空白: %q", got[1])
	}
}

// ---------- 单轮规模 ----------

// 单轮候选数 = 子网数 × ipsPerSubnet × 端口数。
// 每子网多测 IP、多端口全测这两个改动会让候选数乘起来涨，
// 不缩减子网数的话一轮就要跑好几分钟。
func TestRoundBatchSizeCapsCandidates(t *testing.T) {
	const pool = 6534

	// 单端口：候选数 = n × ipsPerSubnet
	single := roundBatchSize(sampleSize, 1, pool)
	// 全部 TLS 端口（6 个）是最坏情况
	multi := roundBatchSize(sampleSize, len(cfHTTPSPorts), pool)

	if multi > single {
		t.Errorf("端口越多每轮子网数不应变大：单端口 %d，%d 端口 %d",
			single, len(cfHTTPSPorts), multi)
	}
	// 最坏情况的候选总数要留在可接受范围内。400 是经验上限：
	// taskNum=50 并发下大约一分钟内能测完
	if got := multi * ipsPerSubnet * len(cfHTTPSPorts); got > 400 {
		t.Errorf("%d 端口下单轮候选 %d 个，太多了", len(cfHTTPSPorts), got)
	}
	// 但也不能缩到没有统计意义
	if multi < 10 {
		t.Errorf("每轮子网数缩到了 %d，样本太小", multi)
	}
}

// 子网总数很少时不能要求超过总量，否则 sampler 直接取不到东西
func TestRoundBatchSizeRespectsSmallPool(t *testing.T) {
	if got := roundBatchSize(sampleSize, 1, 7); got != 7 {
		t.Errorf("池子只有 7 个子网时应取 7，实际 %d", got)
	}
	// poolSize<=0 表示未知，不做裁剪
	if got := roundBatchSize(sampleSize, 1, 0); got <= 0 {
		t.Errorf("未知池大小时应返回正数，实际 %d", got)
	}
}

// numPorts 传 0 不能导致除零 panic
func TestRoundBatchSizeZeroPorts(t *testing.T) {
	if got := roundBatchSize(sampleSize, 0, 100); got <= 0 {
		t.Errorf("端口数为 0 时应回落到 1 个端口，实际 %d", got)
	}
}

// ---------- 覆盖率下限 ----------

// 只靠 maxScanRounds 会让覆盖率极低：6534 个子网跑满 10 轮也只测到一小部分，
// 而最快的 IP 很可能就在没测到的部分里。minCoverageRatio 就是为此存在的。
func TestCoverageFloorExceedsRoundBudget(t *testing.T) {
	const pool = 6534
	batch := roundBatchSize(sampleSize, 1, pool)

	byRounds := batch * maxScanRounds
	poolF := float64(pool)
	floor := int(poolF * minCoverageRatio)

	if floor <= byRounds {
		t.Errorf("覆盖率下限 %d 没有超过轮次预算 %d，这个机制等于没生效", floor, byRounds)
	}
	// 也不能大到要求几乎全测完 —— 那就等于取消了轮次上限
	if floor > pool*3/4 {
		t.Errorf("覆盖率下限 %d/%d 过高，扫描会久到没人愿意等", floor, pool)
	}
}

// ---------- 测速提前放弃 ----------

// 提前放弃的门槛必须明显低于目标，否则会误杀慢启动后追上来的 IP
func TestSpeedTestGiveUpThresholdIsLenient(t *testing.T) {
	if speedTestGiveUpRatio <= 0 || speedTestGiveUpRatio >= 0.6 {
		t.Errorf("放弃比例 %v 不合理：太高会误杀波动的 IP", speedTestGiveUpRatio)
	}
	// 观察期太短会在 TCP 窗口涨起来之前就下结论
	if speedTestMinSampleMs < 1000 {
		t.Errorf("观察期 %dms 太短，会误杀慢启动的连接", speedTestMinSampleMs)
	}
	// "已经足够好"的倍数必须 >1，否则等于没有这个提前退出
	if speedTestGoodEnough <= 1 {
		t.Errorf("speedTestGoodEnough=%d 无意义", speedTestGoodEnough)
	}
}

// ---------- 两阶段测速 ----------

// 两阶段的意义就是省时间。如果预筛+决赛的总预算没比全量完整测速少，
// 这个机制就是纯粹的复杂度负担。
func TestTwoPhaseSpeedTestSavesTime(t *testing.T) {
	full := time.Duration(maxSpeedTestCount) * speedTestFullBudget
	twoPhase := time.Duration(maxSpeedTestCount)*speedTestProbeBudget +
		time.Duration(speedTestFinalists)*speedTestFullBudget

	if twoPhase >= full {
		t.Errorf("两阶段 %v 没比全量 %v 快，机制没有意义", twoPhase, full)
	}
	// 决赛名额至少 2 个，否则等于直接信任粗测结果 ——
	// 而粗测在慢启动阶段的排序并不完全可靠
	if speedTestFinalists < 2 {
		t.Errorf("决赛名额 %d 太少，粗测排序不足以单独定胜负", speedTestFinalists)
	}
	if speedTestFinalists >= maxSpeedTestCount {
		t.Errorf("决赛名额 %d 不小于候选数 %d，预筛白做",
			speedTestFinalists, maxSpeedTestCount)
	}
	// 预筛预算必须短于完整预算，否则不叫"快速"预筛
	if speedTestProbeBudget >= speedTestFullBudget {
		t.Errorf("预筛预算 %v 不短于完整预算 %v", speedTestProbeBudget, speedTestFullBudget)
	}
	// 预筛预算要短于放弃观察期：预筛阶段不应该触发提前放弃逻辑
	if speedTestProbeBudget >= speedTestMinSampleMs*time.Millisecond {
		t.Errorf("预筛预算 %v 不短于放弃观察期 %dms，预筛会误触发放弃",
			speedTestProbeBudget, speedTestMinSampleMs)
	}
}

// ---------- 机房多样性 ----------

func mkRTT(ip, colo string, latency int) RTTResult {
	return RTTResult{IP: ip, Port: 443, LatencyMs: latency, Colo: colo}
}

// 按延迟排出来的 top N 常常全在同一个 colo（都是最近的那个机房）。
// 那个机房一拥塞，整轮测速测的就是同一条链路，全部白费。
func TestCapPerColoEnforcesDiversity(t *testing.T) {
	var in []RTTResult
	// 8 个 HKG + 4 个 NRT，延迟递增
	for i := range 8 {
		in = append(in, mkRTT(fmt.Sprintf("1.1.1.%d", i), "HKG", 10+i))
	}
	for i := range 4 {
		in = append(in, mkRTT(fmt.Sprintf("2.2.2.%d", i), "NRT", 50+i))
	}

	kept, dropped := capPerColo(in, 10)

	perColo := map[string]int{}
	for _, r := range kept {
		perColo[r.Colo]++
	}
	if perColo["HKG"] > coloDiversityCap {
		t.Errorf("HKG 保留了 %d 个，超过上限 %d", perColo["HKG"], coloDiversityCap)
	}
	if perColo["NRT"] == 0 {
		t.Error("NRT 一个都没进，多样性没起作用")
	}
	if dropped == 0 {
		t.Error("8 个 HKG 应该有超额被剔除")
	}
	// 每个机房 3 个 → HKG 3 + NRT 3 = 6，不该回填到 10：
	// 同机房再多测几个 IP 测的还是同一条链路，白花时间
	if len(kept) != 2*coloDiversityCap {
		t.Errorf("2 个机房 × 上限 %d 应保留 %d 个，实际 %d",
			coloDiversityCap, 2*coloDiversityCap, len(kept))
	}
	// 最优候选必须还在最前面
	if kept[0].IP != "1.1.1.0" {
		t.Errorf("延迟最低的应排第一，实际 %s", kept[0].IP)
	}
}

// 机房单一时按配额只留少数几个。少测不是损失 —— 同一个机房里
// 测 5 个 IP 测的是同一条链路，不如快点进下一轮换新子网。
func TestCapPerColoDoesNotBackfillSingleColo(t *testing.T) {
	var in []RTTResult
	for i := range 6 {
		in = append(in, mkRTT(fmt.Sprintf("1.1.1.%d", i), "LAX", 10+i))
	}
	kept, dropped := capPerColo(in, 5)
	if len(kept) != coloDiversityCap {
		t.Fatalf("单机房应只留 %d 个，实际 %d", coloDiversityCap, len(kept))
	}
	if dropped != 6-coloDiversityCap {
		t.Errorf("应剔除 %d 个，实际 %d", 6-coloDiversityCap, dropped)
	}
	// 留下的必须是延迟最低的那几个
	if kept[0].IP != "1.1.1.0" {
		t.Errorf("最优候选应排第一，实际 %s", kept[0].IP)
	}
}

// colo 为空（locations.json 缺这个三字码）不能被当成同一个机房，
// 否则新上线的机房会被互相误伤
func TestCapPerColoIgnoresUnknownColo(t *testing.T) {
	var in []RTTResult
	for i := range 6 {
		in = append(in, mkRTT(fmt.Sprintf("1.1.1.%d", i), "", 10+i))
	}
	kept, dropped := capPerColo(in, 6)
	if len(kept) != 6 {
		t.Errorf("colo 未知的不该受配额限制，实际保留 %d", len(kept))
	}
	if dropped != 0 {
		t.Errorf("colo 未知的不该被剔除，实际剔除 %d", dropped)
	}
}

func TestCapPerColoEdgeCases(t *testing.T) {
	if kept, _ := capPerColo(nil, 5); kept != nil {
		t.Error("空输入应返回 nil")
	}
	if kept, _ := capPerColo([]RTTResult{mkRTT("1.1.1.1", "HKG", 10)}, 0); kept != nil {
		t.Error("limit=0 应返回 nil")
	}
}
