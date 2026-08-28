package better

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
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
	if len(out) != 3 {
		t.Fatalf("3 个合法子网应产出 3 个 IP，实际 %d: %v", len(out), out)
	}
	for _, ip := range out {
		p := net.ParseIP(ip)
		if p == nil || p.To4() == nil {
			t.Errorf("%q 不是合法 IPv4", ip)
		}
	}
	// 生成的 IP 必须落在原子网内
	if !strings.HasPrefix(out[0], "1.0.0.") {
		t.Errorf("1.0.0.0/24 生成了 %s，超出子网范围", out[0])
	}
}

// /48 是 CF 实际使用的前缀，前 3 段必须保持不变
func TestGetRandomIPv6sKeepsPrefix48(t *testing.T) {
	out := getRandomIPv6s([]string{"2400:cb00:2048::/48"})
	if len(out) != 1 {
		t.Fatalf("期望 1 个结果，实际 %d", len(out))
	}
	ip := net.ParseIP(out[0])
	if ip == nil || ip.To4() != nil {
		t.Fatalf("%q 不是合法 IPv6", out[0])
	}
	_, subnet, _ := net.ParseCIDR("2400:cb00:2048::/48")
	if !subnet.Contains(ip) {
		t.Errorf("生成的 %s 不在 2400:cb00:2048::/48 内", out[0])
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
		if len(out) != 1 {
			t.Fatalf("%s 期望 1 个结果，实际 %d", cidr, len(out))
		}
		ip := net.ParseIP(out[0])
		if ip == nil {
			t.Fatalf("%s 生成了非法 IP %q", cidr, out[0])
		}
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("测试用例 %s 本身不合法: %v", cidr, err)
		}
		if !subnet.Contains(ip) {
			t.Errorf("%s 生成的 %s 不在子网内", cidr, out[0])
		}
	}
}

func TestGetRandomIPv6sSkipsGarbage(t *testing.T) {
	out := getRandomIPv6s([]string{"", "  ", "not-an-ip", "1.2.3.4/24"})
	for _, ip := range out {
		if net.ParseIP(ip) == nil {
			t.Errorf("垃圾输入产出了非法 IP %q", ip)
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
