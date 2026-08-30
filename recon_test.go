package better

import (
	"strings"
	"sync"
	"testing"
)

// ---- 三态判定 ----

func TestReconOneUnknownOnBadSubnet(t *testing.T) {
	freshTask(t)
	// 拼不出 IP 的输入必须判 unknown，绝不能判 mismatched：
	// mismatched 会让子网被永久排除
	for _, bad := range []string{"", "   ", "not-an-ip", "1.2.3"} {
		v, colo := reconOne(bad, 4, 443, true, "", scanFilter{
			Countries: parseCountriesCSV("HK"),
		})
		if v != verdictUnknown {
			t.Errorf("reconOne(%q) verdict=%v want verdictUnknown", bad, v)
		}
		if colo != "" {
			t.Errorf("reconOne(%q) colo=%q want empty", bad, colo)
		}
	}
}

func TestProbeColoUnreachable(t *testing.T) {
	freshTask(t)
	// 保留地址，连不通。必须返回空串而不是 panic 或阻塞
	if colo := probeColo("192.0.2.1", 443, true, "example.com"); colo != "" {
		t.Errorf("probeColo unreachable = %q want empty", colo)
	}
}

// ---- 侦察索引与 batchSampler 契约 ----

func TestSubnetSamplerImplementsBatchSampler(t *testing.T) {
	var _ batchSampler = newSubnetSampler([]string{"1.1.1.0/24"})
	var _ batchSampler = newRegionSampler(
		newSubnetSampler([]string{"1.1.1.0/24"}), 4, 443, true, "",
		scanFilter{Countries: parseCountriesCSV("HK")}, 10,
	)
}

// regionSampler 在所有子网都探不通（全 unknown）时，必须把候补池全部吐出来，
// 而不是返回 nil —— 否则「地区筛选 + 网络差」会直接扫不出任何结果。
func TestRegionSamplerFallsBackToUnknown(t *testing.T) {
	freshTask(t)
	// 全是 TEST-NET-1 保留地址，探测必然失败 => 全部 unknown
	subnets := []string{
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
	}
	rs := newRegionSampler(
		newSubnetSampler(subnets), 4, 443, true, "example.com",
		scanFilter{Countries: parseCountriesCSV("HK")}, 8,
	)

	got := map[string]bool{}
	for {
		batch := rs.next(2)
		if batch == nil {
			break
		}
		for _, s := range batch {
			if got[s] {
				t.Fatalf("subnet %s returned twice", s)
			}
			got[s] = true
		}
	}
	if len(got) != len(subnets) {
		t.Fatalf("got %d subnets from fallback, want %d (%v)", len(got), len(subnets), got)
	}
	if rs.stats.probed != len(subnets) {
		t.Errorf("probed=%d want %d", rs.stats.probed, len(subnets))
	}
	if rs.stats.mismatched != 0 {
		t.Errorf("mismatched=%d want 0 (unreachable must not be mismatched)", rs.stats.mismatched)
	}
	if rs.stats.unknown != len(subnets) {
		t.Errorf("unknown=%d want %d", rs.stats.unknown, len(subnets))
	}
}

// 池子取完后必须稳定返回 nil，不能无限产出（主循环靠它终止）
func TestRegionSamplerTerminates(t *testing.T) {
	freshTask(t)
	rs := newRegionSampler(
		newSubnetSampler([]string{"192.0.2.0/24"}), 4, 443, true, "example.com",
		scanFilter{Countries: parseCountriesCSV("JP")}, 4,
	)
	if b := rs.next(5); len(b) != 1 {
		t.Fatalf("first next = %v want 1 subnet", b)
	}
	for i := 0; i < 3; i++ {
		if b := rs.next(5); b != nil {
			t.Fatalf("exhausted next #%d = %v want nil", i, b)
		}
	}
}

func TestRegionSamplerTotalUsedReflectBase(t *testing.T) {
	freshTask(t)
	subnets := []string{"192.0.2.0/24", "198.51.100.0/24"}
	base := newSubnetSampler(subnets)
	rs := newRegionSampler(base, 4, 443, true, "example.com",
		scanFilter{Countries: parseCountriesCSV("SG")}, 4)

	if rs.total() != len(subnets) {
		t.Fatalf("total=%d want %d", rs.total(), len(subnets))
	}
	if rs.used() != 0 {
		t.Fatalf("used before scan = %d want 0", rs.used())
	}
	rs.next(1)
	// 侦察本身就算「已检视」，覆盖率语义依赖这一点
	if rs.used() != len(subnets) {
		t.Errorf("used after recon = %d want %d (recon must count as covered)", rs.used(), len(subnets))
	}
}

func TestRegionSamplerNonPositiveN(t *testing.T) {
	freshTask(t)
	rs := newRegionSampler(
		newSubnetSampler([]string{"192.0.2.0/24"}), 4, 443, true, "example.com",
		scanFilter{Countries: parseCountriesCSV("HK")}, 4,
	)
	if b := rs.next(0); b != nil {
		t.Errorf("next(0) = %v want nil", b)
	}
	if b := rs.next(-3); b != nil {
		t.Errorf("next(-3) = %v want nil", b)
	}
	if rs.stats.probed != 0 {
		t.Errorf("next(0) must not probe, probed=%d", rs.stats.probed)
	}
}

// ---- 取消行为 ----

func TestRegionSamplerRespectsCancel(t *testing.T) {
	freshTask(t)
	subnets := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		subnets = append(subnets, "192.0.2.0/24")
	}
	rs := newRegionSampler(
		newSubnetSampler(subnets), 4, 443, true, "example.com",
		scanFilter{Countries: parseCountriesCSV("HK")}, 4,
	)
	CancelScan()
	// 已取消时不应该长时间阻塞，且不能 panic
	rs.next(10)
	freshTask(t)
}

func TestReconChunkEmptyInput(t *testing.T) {
	freshTask(t)
	var st reconStats
	m, u := reconChunk(nil, 4, 443, true, "", scanFilter{}, 8, &st)
	if m != nil || u != nil {
		t.Errorf("reconChunk(nil) = %v,%v want nil,nil", m, u)
	}
	if st.probed != 0 {
		t.Errorf("probed=%d want 0", st.probed)
	}
}

// taskNum 非法值不能导致 panic 或死锁
func TestReconChunkClampsTaskNum(t *testing.T) {
	freshTask(t)
	subnets := []string{"192.0.2.0/24", "198.51.100.0/24"}
	for _, tn := range []int{-1, 0, 1, 999} {
		var st reconStats
		m, u := reconChunk(subnets, 4, 443, true, "example.com", scanFilter{
			Countries: parseCountriesCSV("HK"),
		}, tn, &st)
		if st.probed != len(subnets) {
			t.Errorf("taskNum=%d probed=%d want %d", tn, st.probed, len(subnets))
		}
		if len(m)+len(u) != len(subnets) {
			t.Errorf("taskNum=%d matched+unknown=%d want %d", tn, len(m)+len(u), len(subnets))
		}
	}
}

// ---- 与地区筛选的一致性 ----

// 侦察的判定必须和 runRTTTest 用的 allowsCountry 完全一致，
// 否则会出现「侦察放行、RTT 拦下」或反之的自相矛盾
func TestReconVerdictMatchesAllowsCountry(t *testing.T) {
	f := scanFilter{Countries: parseCountriesCSV("hk, jp")}
	if !f.allowsCountry("HK") {
		t.Error("HK should be allowed")
	}
	if !f.allowsCountry("JP") {
		t.Error("JP should be allowed (case-insensitive parse)")
	}
	if f.allowsCountry("US") {
		t.Error("US should be rejected")
	}
	// 空国家码 fail-open，侦察对应 verdictUnknown 而非 mismatched
	if !f.allowsCountry("") {
		t.Error("empty country must fail open")
	}
}

func TestRandomIPFromSubnet(t *testing.T) {
	ip := randomIPFromSubnet("104.16.0.0/24", 4)
	if ip == "" {
		t.Fatal("randomIPFromSubnet returned empty for valid v4 subnet")
	}
	if strings.Count(ip, ".") != 3 {
		t.Errorf("v4 ip=%q malformed", ip)
	}
	if !strings.HasPrefix(ip, "104.16.0.") {
		t.Errorf("v4 ip=%q not inside 104.16.0.0/24", ip)
	}
	if got := randomIPFromSubnet("garbage", 4); got != "" {
		t.Errorf("garbage subnet = %q want empty", got)
	}
	if v6 := randomIPFromSubnet("2400:cb00:2049:1::/64", 6); v6 == "" {
		t.Error("randomIPFromSubnet returned empty for valid v6 subnet")
	}
}

// ---- 并发安全 ----

func TestReconChunkConcurrentStatsNoRace(t *testing.T) {
	freshTask(t)
	subnets := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		subnets = append(subnets, "192.0.2.0/24")
	}
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var st reconStats
			reconChunk(subnets, 4, 443, true, "example.com", scanFilter{
				Countries: parseCountriesCSV("HK"),
			}, 8, &st)
			if st.probed != len(subnets) {
				t.Errorf("probed=%d want %d", st.probed, len(subnets))
			}
		}()
	}
	wg.Wait()
}
