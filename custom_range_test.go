package better

import (
	"encoding/json"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// ----------------------- 解析 -----------------------

// TestParseCustomRangesEmpty 空输入不是错误：表示「不指定，用官方列表」。
func TestParseCustomRangesEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n", "# 只有注释\n// 也是注释"} {
		r, err := parseCustomRanges(in)
		if err != nil {
			t.Errorf("parseCustomRanges(%q) 不该报错: %v", in, err)
		}
		if !r.empty() {
			t.Errorf("parseCustomRanges(%q) 应为空，实际 v4=%d v6=%d", in, len(r.V4), len(r.V6))
		}
	}
}

// TestParseCustomRangesCIDR /24 原样保留，不能被改动。
func TestParseCustomRangesCIDR(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 || r.V4[0] != "104.16.0.0/24" {
		t.Fatalf("v4 = %v, want [104.16.0.0/24]", r.V4)
	}
}

// TestParseCustomRangesSplitsBigBlock 大段必须切成 /24。
//
// 这是整个功能能跑起来的前提：抽样器按「子网」批次推进，每个子网出
// ipsPerSubnet 个候选。直接把一个 /13 当成一个子网塞进去，整轮只会测 2 个 IP。
func TestParseCustomRangesSplitsBigBlock(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/13")
	if err != nil {
		t.Fatal(err)
	}
	// /13 → /24 = 2^11 = 2048 个
	if len(r.V4) != 2048 {
		t.Fatalf("/13 应切成 2048 个 /24，实际 %d", len(r.V4))
	}
	if r.V4[0] != "104.16.0.0/24" {
		t.Errorf("第一个应为 104.16.0.0/24，实际 %q", r.V4[0])
	}
	// 排序后最后一个是 104.23.255.0/24（104.16.0.0/13 覆盖 104.16-104.23）
	last := r.V4[len(r.V4)-1]
	if last != "104.23.255.0/24" {
		t.Errorf("最后一个应为 104.23.255.0/24，实际 %q", last)
	}
	// 每个都必须落在原段内
	for _, sn := range r.V4 {
		if !strings.HasPrefix(sn, "104.1") && !strings.HasPrefix(sn, "104.2") {
			t.Fatalf("%q 不在 104.16.0.0/13 内", sn)
		}
	}
}

// TestParseCustomRangesKeepsNarrowerThanSplit 比 /24 更细的段不能被放宽。
//
// 放宽到 /24 会测到用户没指定的地址 —— 他填 /28 就是只想测那 16 个。
func TestParseCustomRangesKeepsNarrowerThanSplit(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/28")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 || r.V4[0] != "104.16.0.0/28" {
		t.Fatalf("v4 = %v, want [104.16.0.0/28]", r.V4)
	}
}

// TestParseCustomRangesSingleIP 单个 IP 等价 /32。
func TestParseCustomRangesSingleIP(t *testing.T) {
	r, err := parseCustomRanges("104.17.168.20")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 || r.V4[0] != "104.17.168.20/32" {
		t.Fatalf("v4 = %v, want [104.17.168.20/32]", r.V4)
	}
}

// TestParseCustomRangesMasksHostBits 带主机位的 CIDR 要归一。
// 104.16.1.5/24 应变成 104.16.1.0/24，否则子网名带无意义的主机位。
func TestParseCustomRangesMasksHostBits(t *testing.T) {
	r, err := parseCustomRanges("104.16.1.5/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 || r.V4[0] != "104.16.1.0/24" {
		t.Fatalf("v4 = %v, want [104.16.1.0/24]", r.V4)
	}
}

// TestParseCustomRangesIPv4Range 起止范围换算成 CIDR。
func TestParseCustomRangesIPv4Range(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0-104.16.3.255")
	if err != nil {
		t.Fatal(err)
	}
	// 覆盖 104.16.0.0/22 = 4 个 /24
	if len(r.V4) != 4 {
		t.Fatalf("应得 4 个 /24，实际 %d: %v", len(r.V4), r.V4)
	}
	want := []string{"104.16.0.0/24", "104.16.1.0/24", "104.16.2.0/24", "104.16.3.0/24"}
	for i, w := range want {
		if r.V4[i] != w {
			t.Errorf("第 %d 个 = %q, want %q", i, r.V4[i], w)
		}
	}
}

// TestParseCustomRangesUnalignedRange 不对齐的范围也要精确覆盖。
//
// 104.16.0.5-104.16.1.10 跨两个 /24，两端都不在边界上。算法必须能拆成
// 多个前缀精确覆盖，而不是简单地把两端各扩到 /24。
func TestParseCustomRangesUnalignedRange(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.5-104.16.1.10")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) == 0 {
		t.Fatal("不该为空")
	}
	// 所有产出的前缀合起来必须覆盖 .0.5 到 .1.10，且不超出
	// 用 parseRangeToken 直接验前缀（parseCustomRanges 会再切一次 /24）
	prefixes, err := parseRangeToken("104.16.0.5-104.16.1.10")
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, p := range prefixes {
		total += uint64(1) << (32 - p.Bits())
		// 每个前缀都不能越界
		lo := v4ToUint(p.Masked().Addr())
		hi := lo + uint32((uint64(1)<<(32-p.Bits()))-1)
		if lo < 0x68100005 || hi > 0x6810010A {
			t.Errorf("前缀 %v 越界（应在 104.16.0.5 ~ 104.16.1.10 内）", p)
		}
	}
	// 地址总数 = 0x6810010A - 0x68100005 + 1 = 262
	if total != 262 {
		t.Errorf("覆盖地址数 = %d, want 262", total)
	}
}

// TestParseCustomRangesIPv6 IPv6 CIDR 走 /48 粒度。
func TestParseCustomRangesIPv6(t *testing.T) {
	r, err := parseCustomRanges("2606:4700::/48")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V6) != 1 {
		t.Fatalf("v6 = %v, want 1 个", r.V6)
	}
	if len(r.V4) != 0 {
		t.Errorf("不该产出 v4，实际 %v", r.V4)
	}
	// /47 切 /48 = 2 个
	r2, err := parseCustomRanges("2606:4700::/47")
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.V6) != 2 {
		t.Fatalf("/47 应切成 2 个 /48，实际 %d: %v", len(r2.V6), r2.V6)
	}
}

// TestParseCustomRangesMixed v4 与 v6 混填要分别归类。
func TestParseCustomRangesMixed(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/24, 2606:4700::/48")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 || len(r.V6) != 1 {
		t.Fatalf("v4=%d v6=%d，应各 1 个", len(r.V4), len(r.V6))
	}
}

// TestParseCustomRangesSeparators 分隔符要给得宽松。
//
// 用户会从各种地方复制粘贴，为分隔符格式报错纯属添乱。
func TestParseCustomRangesSeparators(t *testing.T) {
	inputs := []string{
		"104.16.0.0/24,104.17.0.0/24",
		"104.16.0.0/24 104.17.0.0/24",
		"104.16.0.0/24\n104.17.0.0/24",
		"104.16.0.0/24;104.17.0.0/24",
		"104.16.0.0/24，104.17.0.0/24",       // 中文逗号
		"104.16.0.0/24、104.17.0.0/24",       // 中文顿号
		"104.16.0.0/24\t104.17.0.0/24",      // 制表
		"104.16.0.0/24\r\n104.17.0.0/24",    // CRLF
		" 104.16.0.0/24 ,\n 104.17.0.0/24 ", // 混合 + 多余空白
	}
	for _, in := range inputs {
		r, err := parseCustomRanges(in)
		if err != nil {
			t.Errorf("parseCustomRanges(%q) 报错: %v", in, err)
			continue
		}
		if len(r.V4) != 2 {
			t.Errorf("parseCustomRanges(%q) 得到 %d 个，应为 2: %v", in, len(r.V4), r.V4)
		}
	}
}

// TestParseCustomRangesComments 注释行要跳过。
// 分享出来的 IP 段列表经常带说明。
func TestParseCustomRangesComments(t *testing.T) {
	in := "# 移动优选\n104.16.0.0/24\n// 电信优选\n104.17.0.0/24\n"
	r, err := parseCustomRanges(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 2 {
		t.Fatalf("应得 2 个，实际 %d: %v", len(r.V4), r.V4)
	}
}

// TestParseCustomRangesDedup 重复覆盖的子网只保留一份。
//
// 不去重的话它被测到的概率翻倍，而用户看不出为什么。
func TestParseCustomRangesDedup(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/24, 104.16.0.0/24, 104.16.0.5/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 1 {
		t.Fatalf("三条都指向同一个 /24，应去重成 1 个，实际 %d: %v", len(r.V4), r.V4)
	}
}

// TestParseCustomRangesDedupAcrossBlocks 大段与小段重叠时也要去重。
func TestParseCustomRangesDedupAcrossBlocks(t *testing.T) {
	// /22 展开出 4 个 /24，其中一个与后面单独写的那条重复
	r, err := parseCustomRanges("104.16.0.0/22, 104.16.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.V4) != 4 {
		t.Fatalf("应去重成 4 个，实际 %d: %v", len(r.V4), r.V4)
	}
}

// TestParseCustomRangesErrors 各种填错都要报错而不是静默忽略。
//
// 静默忽略最糟：用户以为在扫自己的段，实际扫的是别的（或者全网）。
func TestParseCustomRangesErrors(t *testing.T) {
	bad := []string{
		"104.16/13",                   // 不是合法 CIDR
		"104.16.0.0/33",               // 前缀越界
		"not-an-ip",                   // 完全不是 IP
		"104.16.0.0-",                 // 范围缺终点
		"104.16.3.0-104.16.0.0",       // 起点大于终点
		"104.16.0.0-2606:4700::",      // 混协议范围
		"2606:4700::-2606:4700::ffff", // v6 范围不支持
		"104.16.0.0/24, garbage",      // 一条对一条错，整体也要报错
	}
	for _, in := range bad {
		if _, err := parseCustomRanges(in); err == nil {
			t.Errorf("parseCustomRanges(%q) 应报错", in)
		}
	}
}

// TestParseCustomRangesTruncates 超出上限要截断并标记。
//
// 不截断的话 /8 会展开出 65536 个子网，那已经等于扫全网，
// 而且展开的字符串切片会占掉几 MB。
func TestParseCustomRangesTruncates(t *testing.T) {
	r, err := parseCustomRanges("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated {
		t.Error("应标记为已截断")
	}
	if len(r.V4) > maxCustomSubnets {
		t.Fatalf("截断后仍有 %d 个，上限 %d", len(r.V4), maxCustomSubnets)
	}
}

// TestParseCustomRangesHugeRangeTruncates 巨大的起止范围要截断而不是耗尽内存。
//
// 与 /8 同一套处理：能算出来就算，超过上限就截断并标记，让界面告诉用户
// 「只取了前 N 个」。报错反而不好 —— 用户填个大范围只是想「多测点」，
// 直接拒绝等于让他自己去算该拆成几条。
func TestParseCustomRangesHugeRangeTruncates(t *testing.T) {
	r, err := parseCustomRanges("1.0.0.1-200.0.0.1")
	if err != nil {
		t.Fatalf("超大范围应截断而不是报错: %v", err)
	}
	if !r.Truncated {
		t.Error("应标记为已截断")
	}
	if len(r.V4) > maxCustomSubnets {
		t.Fatalf("截断后仍有 %d 个，上限 %d", len(r.V4), maxCustomSubnets)
	}
	if len(r.V4) == 0 {
		t.Fatal("不该为空")
	}
}

// TestCustomRangesListFor listFor 按协议族取列表。
func TestCustomRangesListFor(t *testing.T) {
	r, err := parseCustomRanges("104.16.0.0/24, 2606:4700::/48")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.listFor(4); len(got) != 1 || !strings.Contains(got[0], ".") {
		t.Errorf("listFor(4) = %v", got)
	}
	if got := r.listFor(6); len(got) != 1 || !strings.Contains(got[0], ":") {
		t.Errorf("listFor(6) = %v", got)
	}
}

// ----------------------- 地址运算 -----------------------

// TestAddrAddPow2V4 v4 加 2^exp。
func TestAddrAddPow2V4(t *testing.T) {
	a := mustAddr(t, "104.16.0.0")
	got, ok := addrAddPow2(a, 8) // 加 256 = 下一个 /24
	if !ok {
		t.Fatal("不该溢出")
	}
	if got.String() != "104.16.1.0" {
		t.Fatalf("= %v, want 104.16.1.0", got)
	}
}

// TestAddrAddPow2V4Overflow v4 到顶要返回失败而不是绕回。
// 绕回会让 splitPrefix 死循环。
func TestAddrAddPow2V4Overflow(t *testing.T) {
	a := mustAddr(t, "255.255.255.0")
	if _, ok := addrAddPow2(a, 8); ok {
		t.Fatal("255.255.255.0 + 256 应溢出")
	}
}

// TestAddrAddPow2V6 v6 用字节数组做大端加法（128 位装不进任何整数类型）。
func TestAddrAddPow2V6(t *testing.T) {
	a := mustAddr(t, "2606:4700::")
	// 加 2^80 = 下一个 /48
	got, ok := addrAddPow2(a, 128-48)
	if !ok {
		t.Fatal("不该溢出")
	}
	if got.String() != "2606:4700:1::" {
		t.Fatalf("= %v, want 2606:4700:1::", got)
	}
}

// TestAddrAddPow2V6Carry 进位要跨字节正确传播。
func TestAddrAddPow2V6Carry(t *testing.T) {
	a := mustAddr(t, "2606:4700:ffff::")
	got, ok := addrAddPow2(a, 128-48)
	if !ok {
		t.Fatal("不该溢出")
	}
	if got.String() != "2606:4701::" {
		t.Fatalf("= %v, want 2606:4701::", got)
	}
}

// ----------------------- IP 生成 -----------------------

// TestGetRandomIPv4sRespectsNarrowPrefix 比 /24 细的段不能拼出段外地址。
//
// 这是「指定 IP 段」引入的新情况：官方列表清一色 /24，随机整个末位八位组
// 恰好正确；用户填 /28 后还随机 0-255 就会测到他没指定的地址。
func TestGetRandomIPv4sRespectsNarrowPrefix(t *testing.T) {
	// 跑多次：随机取值，单次通过说明不了问题
	for range 200 {
		for _, c := range []struct {
			cidr   string
			lo, hi int
		}{
			{"104.16.0.0/28", 0, 15},
			{"104.16.0.16/28", 16, 31},
			{"104.16.0.0/30", 0, 3},
			{"104.16.0.128/25", 128, 255},
		} {
			for _, cand := range getRandomIPv4s([]string{c.cidr}) {
				last := lastOctet(t, cand.IP)
				if last < c.lo || last > c.hi {
					t.Fatalf("%s 拼出 %s，末位 %d 不在 [%d,%d]",
						c.cidr, cand.IP, last, c.lo, c.hi)
				}
			}
		}
	}
}

// TestGetRandomIPv4sSlash32 /32 只有一个地址，应原样返回而不是随机。
func TestGetRandomIPv4sSlash32(t *testing.T) {
	out := getRandomIPv4s([]string{"104.17.168.20/32"})
	if len(out) != 1 {
		t.Fatalf("应只产出 1 个，实际 %d: %v", len(out), out)
	}
	if out[0].IP != "104.17.168.20" {
		t.Fatalf("= %q, want 104.17.168.20", out[0].IP)
	}
}

// TestGetRandomIPv4sSlash31 /31 只有 2 个地址，取满不能死循环。
//
// 原实现用「固定 4 次重试」躲重复，span 小到 2 时很容易 4 次全撞，
// 结果一个 IP 都不产出。现在用 len(used) 做循环条件，必须取满。
func TestGetRandomIPv4sSlash31(t *testing.T) {
	out := getRandomIPv4s([]string{"104.16.0.0/31"})
	if len(out) != 2 {
		t.Fatalf("/31 应产出 2 个，实际 %d: %v", len(out), out)
	}
	seen := map[string]bool{}
	for _, c := range out {
		if seen[c.IP] {
			t.Fatalf("产出重复 IP %q", c.IP)
		}
		seen[c.IP] = true
	}
}

// TestGetRandomIPv4sSlash24Unchanged /24 的行为不能变。
// 官方列表全是 /24，这是绝大多数用户的路径。
func TestGetRandomIPv4sSlash24Unchanged(t *testing.T) {
	out := getRandomIPv4s([]string{"104.16.0.0/24"})
	if len(out) != ipsPerSubnet {
		t.Fatalf("应产出 %d 个，实际 %d", ipsPerSubnet, len(out))
	}
	for _, c := range out {
		if !strings.HasPrefix(c.IP, "104.16.0.") {
			t.Errorf("%q 不在 104.16.0.0/24 内", c.IP)
		}
		if c.Subnet != "104.16.0.0/24" {
			t.Errorf("Subnet = %q, want 104.16.0.0/24", c.Subnet)
		}
	}
}

// ----------------------- 对外接口 -----------------------

// TestPreviewIPRangesOK 预检成功时给出可读摘要。
func TestPreviewIPRangesOK(t *testing.T) {
	var p struct {
		OK        bool   `json:"ok"`
		V4        int    `json:"v4"`
		V6        int    `json:"v6"`
		Truncated bool   `json:"truncated"`
		Summary   string `json:"summary"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(PreviewIPRanges("104.16.0.0/13")), &p); err != nil {
		t.Fatal(err)
	}
	if !p.OK {
		t.Fatalf("应成功，实际 error=%q", p.Error)
	}
	if p.V4 != 2048 {
		t.Errorf("v4 = %d, want 2048", p.V4)
	}
	if !strings.Contains(p.Summary, "2048") {
		t.Errorf("摘要应含数量，实际 %q", p.Summary)
	}
}

// TestPreviewIPRangesEmpty 空输入要明确说「用官方列表」而不是报错。
func TestPreviewIPRangesEmpty(t *testing.T) {
	var p struct {
		OK      bool   `json:"ok"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(PreviewIPRanges("  ")), &p); err != nil {
		t.Fatal(err)
	}
	if !p.OK {
		t.Fatal("空输入不该判为错误")
	}
	if !strings.Contains(p.Summary, "官方") {
		t.Errorf("摘要应说明会用官方列表，实际 %q", p.Summary)
	}
}

// TestPreviewIPRangesError 预检失败要带上具体错误。
func TestPreviewIPRangesError(t *testing.T) {
	var p struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(PreviewIPRanges("104.16/13")), &p); err != nil {
		t.Fatal(err)
	}
	if p.OK {
		t.Fatal("应判为错误")
	}
	if p.Error == "" {
		t.Fatal("错误信息不能为空")
	}
	// 错误信息要点出是哪一条出问题，否则填了十几条时无从下手
	if !strings.Contains(p.Error, "104.16/13") {
		t.Errorf("错误信息应包含出错的那条，实际 %q", p.Error)
	}
}

// TestMaxIPRangeSubnets 上限要暴露给界面做提示。
func TestMaxIPRangeSubnets(t *testing.T) {
	if MaxIPRangeSubnets() != maxCustomSubnets {
		t.Fatalf("= %d, want %d", MaxIPRangeSubnets(), maxCustomSubnets)
	}
}

// TestCustomSplitPrefixFor 切分粒度按协议族返回。
func TestCustomSplitPrefixFor(t *testing.T) {
	if got := customSplitPrefixFor(4); got != 24 {
		t.Errorf("v4 = %d, want 24", got)
	}
	if got := customSplitPrefixFor(6); got != 48 {
		t.Errorf("v6 = %d, want 48", got)
	}
}

// ----------------------- 测试辅助 -----------------------

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func lastOctet(t *testing.T, ip string) int {
	t.Helper()
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		t.Fatalf("%q 不是 IPv4", ip)
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		t.Fatal(err)
	}
	return n
}
