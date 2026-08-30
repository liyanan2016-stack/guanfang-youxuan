package better

import (
	"net"
	"os"
	"strings"
	"testing"
)

// 用真实的 ips-v4.txt / ips-v6.txt 验证 IP 生成，
// 而不是只测构造数据。本地没有缓存文件时跳过。
func TestRealSubnetLists(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		v6   bool
	}{
		{"IPv4", "/tmp/ips-v4.txt", false},
		{"IPv6", "/tmp/ips-v6.txt", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := os.ReadFile(tc.path)
			if err != nil {
				t.Skipf("没有 %s，跳过：%v", tc.path, err)
			}
			subnets := parseIPList(string(content))
			if len(subnets) < 100 {
				t.Skipf("样本太小（%d 条）", len(subnets))
			}
			t.Logf("%s 共 %d 个子网", tc.name, len(subnets))

			// 抽 200 个子网生成 IP，逐个验证落在原子网内
			s := newSubnetSampler(subnets)
			batch := s.next(200)

			var ips []candidateIP
			if tc.v6 {
				ips = getRandomIPv6s(batch)
			} else {
				ips = getRandomIPv4s(batch)
			}
			if len(ips) == 0 {
				t.Fatal("一个 IP 都没生成")
			}
			t.Logf("%d 个子网 -> %d 个 IP", len(batch), len(ips))

			// 每个候选自带来源子网，直接按它校验，不再依赖下标配对
			checked := 0
			for _, c := range ips {
				_, subnet, err := net.ParseCIDR(c.Subnet)
				if err != nil {
					t.Errorf("候选 %s 记录的来源子网 %q 不合法", c.IP, c.Subnet)
					continue
				}
				ip := net.ParseIP(c.IP)
				if ip == nil {
					t.Errorf("%s 生成了非法 IP %q", c.Subnet, c.IP)
					continue
				}
				if !subnet.Contains(ip) {
					t.Errorf("%s 生成的 %s 不在子网内", c.Subnet, c.IP)
				}
				checked++
			}
			t.Logf("校验了 %d 个 IP 全部落在对应子网内", checked)

			// 生成的 IP 不应大量重复（说明随机源工作正常）
			uniq := make(map[string]struct{}, len(ips))
			for _, c := range ips {
				uniq[c.IP] = struct{}{}
			}
			if len(uniq) < len(ips)*9/10 {
				t.Errorf("%d 个 IP 只有 %d 个唯一值，随机性可疑", len(ips), len(uniq))
			}
		})
	}
}

// 前缀长度分布检查：确认真实数据里的前缀和代码假设一致
func TestRealPrefixLengths(t *testing.T) {
	for _, path := range []string{"/tmp/ips-v4.txt", "/tmp/ips-v6.txt"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("没有 %s", path)
		}
		lens := make(map[string]int)
		for _, line := range parseIPList(string(content)) {
			if i := strings.Index(line, "/"); i >= 0 {
				lens[line[i+1:]]++
			} else {
				lens["(无前缀)"]++
			}
		}
		t.Logf("%s 前缀分布: %v", path, lens)
	}
}
