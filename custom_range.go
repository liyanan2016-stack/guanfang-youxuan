package better

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// ----------------------- 指定 IP 段 -----------------------
//
// 用户自己填 IP 段，替代官方下发的 ips-v4.txt / ips-v6.txt 作为数据源。
//
// 为什么需要：官方列表是 CF 全部对外宣告的段（6500+ 个 /24），里面绝大
// 部分和用户所在网络的实际质量无关，而且 104.16/104.21 这些「大家都在扫」
// 的段早被用烂。有明确目标时（别人分享的优质段、自己 Cloudflare 账户
// 对应的段、某个特定 ASN 宣告的段），直接指定能把候选池从六千多个子网
// 压到几十个，扫描时间从几分钟降到几十秒，命中率也高得多。
//
// 设计要点：
//   - 只换数据源，不动后面任何一环。RTT、地区侦察、测速、排序全都照跑，
//     所以「指定段 + 筛地区 + 自定义测速源」可以叠加使用。
//   - 必须切成 /24（v6 切成 /48）交给抽样器。抽样器是按「子网」批次推进的，
//     每个子网出 ipsPerSubnet 个候选 —— 直接把一个 /13 当成一个子网塞进去，
//     整轮就只会测 2 个 IP。
//   - 解析失败必须报错停下，不能悄悄回落官方列表：用户以为在扫自己指定的
//     段，拿到的却是全网结果，那比直接报错糟糕得多。

// maxCustomSubnets 展开后的子网数量上限。
//
// 官方列表是 6534 个 /24，这里给到 20000 —— 比官方列表还宽松，
// 足够覆盖「几个 /13 加起来」这种正常用法（CF 的 104.16.0.0/13 = 2048 个
// /24）。再大就没意义了：那已经等于扫全网，不如不填直接用官方列表，
// 而且展开出来的字符串切片会占掉几 MB 内存。
const maxCustomSubnets = 20000

// customV4SplitPrefix / customV6SplitPrefix 切分粒度。
//
// v4 切到 /24：CF 的一个 /24 通常整段落在同一个 colo，这也是地区侦察
// 「探一个 IP 就能判定整段」这个前提的来源。切得更细（比如 /28）会让
// 侦察开销成倍上升却换不到额外信息。
//
// v6 切到 /48：CF 对外宣告的 v6 段就是按 /48 组织的。
const (
	customV4SplitPrefix = 24
	customV6SplitPrefix = 48
)

// customRanges 解析后的用户 IP 段。
type customRanges struct {
	// V4 / V6 展开后的子网列表，格式与 ips-v4.txt / ips-v6.txt 一致
	V4 []string
	V6 []string
	// Truncated 是否因为超出 maxCustomSubnets 被截断
	Truncated bool
	// InputCount 用户实际填了几条（去空白后），用于提示文案
	InputCount int
}

// empty 用户什么都没填。
func (c customRanges) empty() bool {
	return len(c.V4) == 0 && len(c.V6) == 0
}

// listFor 返回指定协议族的子网列表。
func (c customRanges) listFor(ipType int) []string {
	if ipType == 6 {
		return c.V6
	}
	return c.V4
}

// customSplitPrefixFor 返回该协议族的切分粒度，供进度文案使用。
func customSplitPrefixFor(ipType int) int {
	if ipType == 6 {
		return customV6SplitPrefix
	}
	return customV4SplitPrefix
}

// parseCustomRanges 解析用户填写的 IP 段。
//
// 支持的写法（可用逗号、分号、空格、换行任意混合分隔）：
//
//	104.16.0.0/24          CIDR
//	104.16.0.0/13          大段，自动切成 2048 个 /24
//	104.16.1.5             单个 IP，等价 /32
//	104.16.0.0-104.16.3.255  起止范围（仅 IPv4）
//	2606:4700::/48         IPv6 CIDR
//	# 开头或 // 开头的行按注释忽略
//
// 空输入返回零值且不报错 —— 那表示「不指定，用官方列表」。
func parseCustomRanges(raw string) (customRanges, error) {
	var out customRanges
	tokens := splitRangeTokens(raw)
	out.InputCount = len(tokens)
	if len(tokens) == 0 {
		return out, nil
	}

	// 去重用：同一个 /24 被两条输入覆盖时只保留一份，否则它被测到的
	// 概率会翻倍，而用户看不出为什么
	seen := make(map[string]struct{}, len(tokens)*4)
	budget := maxCustomSubnets

	for _, tok := range tokens {
		prefixes, err := parseRangeToken(tok)
		if err != nil {
			return customRanges{}, err
		}
		for _, p := range prefixes {
			target := customV4SplitPrefix
			if p.Addr().Is6() {
				target = customV6SplitPrefix
			}
			parts, truncated := splitPrefix(p, target, budget)
			if truncated {
				out.Truncated = true
			}
			for _, sn := range parts {
				s := sn.String()
				if _, dup := seen[s]; dup {
					continue
				}
				seen[s] = struct{}{}
				budget--
				if sn.Addr().Is6() {
					out.V6 = append(out.V6, s)
				} else {
					out.V4 = append(out.V4, s)
				}
			}
			if budget <= 0 {
				out.Truncated = true
				break
			}
		}
		if budget <= 0 {
			break
		}
	}

	if out.empty() {
		return customRanges{}, fmt.Errorf("没能从你填的内容里解析出任何 IP 段")
	}

	// 按地址数值排序让展开结果稳定且可读。抽样器自己会洗牌，所以顺序
	// 不影响扫描；但不能用 sort.Strings —— 字典序会把 104.23.99.0 排在
	// 104.23.255.0 后面（'9' > '2'），日志和单测里一眼看不出对不对。
	sortSubnets(out.V4)
	sortSubnets(out.V6)
	return out, nil
}

// sortSubnets 按地址数值给子网列表排序。解析不了的排到最后。
func sortSubnets(list []string) {
	sort.SliceStable(list, func(i, j int) bool {
		pi, ei := netip.ParsePrefix(list[i])
		pj, ej := netip.ParsePrefix(list[j])
		if ei != nil || ej != nil {
			return ei == nil
		}
		if c := pi.Addr().Compare(pj.Addr()); c != 0 {
			return c < 0
		}
		return pi.Bits() < pj.Bits()
	})
}

// splitRangeTokens 把用户输入切成一条条待解析的记号。
//
// 分隔符给得很宽松（逗号、中文逗号、分号、空格、制表、换行）：用户会从
// 各种地方复制粘贴，为分隔符格式报错纯属添乱。
func splitRangeTokens(raw string) []string {
	replaced := strings.NewReplacer(
		"，", ",", "、", ",", ";", ",", "；", ",",
		"\r", "\n", "\t", " ",
	).Replace(raw)

	var out []string
	for _, line := range strings.Split(replaced, "\n") {
		line = strings.TrimSpace(line)
		// 注释行：分享出来的 IP 段列表经常带说明
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		for _, part := range strings.Split(line, ",") {
			for _, tok := range strings.Fields(part) {
				tok = strings.TrimSpace(tok)
				if tok != "" {
					out = append(out, tok)
				}
			}
		}
	}
	return out
}

// parseRangeToken 解析单条记号，返回它覆盖的前缀列表。
//
// 起止范围会被换算成若干个 CIDR（一个任意范围通常需要多个前缀才能精确
// 覆盖），所以返回的是切片而不是单个前缀。
func parseRangeToken(tok string) ([]netip.Prefix, error) {
	// 带 "/" 的按 CIDR 解析
	if strings.Contains(tok, "/") {
		p, err := netip.ParsePrefix(tok)
		if err != nil {
			return nil, fmt.Errorf("IP 段 %q 格式不对：%v", tok, err)
		}
		// Masked 把 104.16.1.5/24 归一成 104.16.1.0/24。
		// 不归一的话 netip 的 Contains 仍然可用，但字符串形式会带上
		// 无意义的主机位，展开出来的子网名不干净。
		return []netip.Prefix{p.Masked()}, nil
	}

	// 起止范围
	if idx := strings.Index(tok, "-"); idx > 0 {
		startStr := strings.TrimSpace(tok[:idx])
		endStr := strings.TrimSpace(tok[idx+1:])
		start, err := netip.ParseAddr(startStr)
		if err != nil {
			return nil, fmt.Errorf("范围起点 %q 不是合法 IP：%v", startStr, err)
		}
		end, err := netip.ParseAddr(endStr)
		if err != nil {
			return nil, fmt.Errorf("范围终点 %q 不是合法 IP：%v", endStr, err)
		}
		if start.Is4() != end.Is4() {
			return nil, fmt.Errorf("范围 %q 的起点和终点不是同一种 IP", tok)
		}
		// IPv6 范围不支持：地址空间太大，一个看似很短的范围能展开成天文
		// 数字个前缀。v6 请直接写 CIDR —— 实际使用中也没人用 v6 起止范围。
		if !start.Is4() {
			return nil, fmt.Errorf("IPv6 请用 CIDR 写法（如 2606:4700::/48），不支持起止范围")
		}
		if start.Compare(end) > 0 {
			return nil, fmt.Errorf("范围 %q 的起点比终点大", tok)
		}
		return v4RangeToPrefixes(start, end)
	}

	// 单个 IP
	addr, err := netip.ParseAddr(tok)
	if err != nil {
		return nil, fmt.Errorf("%q 既不是 IP 也不是 IP 段：%v", tok, err)
	}
	bits := 32
	if addr.Is6() {
		bits = 128
	}
	return []netip.Prefix{netip.PrefixFrom(addr, bits)}, nil
}

// v4RangeToPrefixes 把 IPv4 起止范围换算成最少个数的 CIDR 前缀。
//
// 标准算法：从起点开始，每步取「起点对齐允许的最大块」与「剩余长度允许
// 的最大块」中较小的那个，然后前进。任意 v4 范围最多产出 62 个前缀
// （每侧最多 31 个），所以这里不需要数量上限 —— 真正的规模控制在
// splitPrefix 的 budget 上：一个 /2 会被切成 /24 时按 budget 截断。
func v4RangeToPrefixes(start, end netip.Addr) ([]netip.Prefix, error) {
	lo := v4ToUint(start)
	hi := v4ToUint(end)

	var out []netip.Prefix
	for lo <= hi {
		// 起点的对齐能力：末尾有几个 0 位就最多能开多大的块
		size := uint64(1)
		bits := 32
		for size*2 <= uint64(hi)-uint64(lo)+1 && lo&uint32(size*2-1) == 0 {
			size *= 2
			bits--
		}
		out = append(out, netip.PrefixFrom(uintToV4(lo), bits))
		next := uint64(lo) + size
		if next > uint64(^uint32(0)) {
			break
		}
		lo = uint32(next)
	}
	return out, nil
}

func v4ToUint(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func uintToV4(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	})
}

// splitPrefix 把前缀切成粒度为 target 的子前缀。
//
// 已经等于或长于 target 的（比如用户填了 /28 或单个 /32）原样返回 ——
// 不能反过来放宽到 /24，那样会测到用户没指定的地址。
//
// budget 是还能产出多少个子网。超了就截断并返回 truncated=true，
// 由调用方告诉用户「只取了前 N 个」。
func splitPrefix(p netip.Prefix, target, budget int) (out []netip.Prefix, truncated bool) {
	if budget <= 0 {
		return nil, true
	}
	if p.Bits() >= target {
		return []netip.Prefix{p}, false
	}

	shift := target - p.Bits()
	// 1<<shift 可能天文数字（/8 切 /24 = 65536，v6 的 /16 切 /48 更夸张），
	// 先跟 budget 比再决定循环次数，避免为了算个总数就溢出
	var count uint64 = 1
	if shift < 63 {
		count = uint64(1) << shift
	} else {
		count = uint64(budget) + 1
	}
	if count > uint64(budget) {
		count = uint64(budget)
		truncated = true
	}

	addr := p.Masked().Addr()
	// 每块的大小 = 2^(地址位数 - target)
	totalBits := 32
	if addr.Is6() {
		totalBits = 128
	}
	step := totalBits - target

	for i := uint64(0); i < count; i++ {
		out = append(out, netip.PrefixFrom(addr, target))
		next, ok := addrAddPow2(addr, step)
		if !ok {
			// 地址空间到顶：正常输入不会走到这里（/24 的最后一块之后
			// 就是 255.255.255.0 之外），但不能因此死循环
			break
		}
		addr = next
	}
	return out, truncated
}

// addrAddPow2 给地址加上 2^exp，溢出返回 ok=false。
//
// 用字节数组做大端加法而不是转成整数：v6 是 128 位，任何整数类型都装不下。
func addrAddPow2(a netip.Addr, exp int) (netip.Addr, bool) {
	if a.Is4() {
		v := v4ToUint(a)
		if exp >= 32 {
			return netip.Addr{}, false
		}
		delta := uint64(1) << exp
		sum := uint64(v) + delta
		if sum > uint64(^uint32(0)) {
			return netip.Addr{}, false
		}
		return uintToV4(uint32(sum)), true
	}

	b := a.As16()
	if exp >= 128 {
		return netip.Addr{}, false
	}
	// 第 exp 位（从最低位数）落在倒数第 exp/8 个字节的 exp%8 位上
	byteIdx := 15 - exp/8
	carry := uint16(1) << (exp % 8)
	for i := byteIdx; i >= 0; i-- {
		sum := uint16(b[i]) + carry
		b[i] = byte(sum & 0xff)
		carry = sum >> 8
		if carry == 0 {
			break
		}
	}
	if carry != 0 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom16(b), true
}
