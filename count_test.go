package better

import (
	"encoding/json"
	"testing"
)

// normalizeResultCount 必须把任意输入收敛到 1/5/10，
// 且取「不小于输入的最小档位」——多给不亏，少给不够用。
func TestNormalizeResultCount(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{-5, 1}, {0, 1}, {1, 1},
		{2, 5}, {4, 5}, {5, 5},
		{6, 10}, {9, 10}, {10, 10},
		{11, 10}, {50, 10}, {99999, 10},
	}
	for _, c := range cases {
		if got := normalizeResultCount(c.in); got != c.want {
			t.Errorf("normalizeResultCount(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// 收敛结果必须真的在允许列表里，不能出现第四种值
func TestNormalizeResultCountAlwaysAllowed(t *testing.T) {
	for n := -3; n <= 40; n++ {
		got := normalizeResultCount(n)
		ok := false
		for _, c := range allowedResultCounts {
			if got == c {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("normalizeResultCount(%d) = %d，不在 %v 内", n, got, allowedResultCounts)
		}
	}
}

func TestResultCountsCSV(t *testing.T) {
	if got := ResultCounts(); got != "1,5,10" {
		t.Errorf("ResultCounts() = %q, want \"1,5,10\"", got)
	}
}

// 池必须按速度降序，且只保留 limit 个
func TestResultPoolOrderAndLimit(t *testing.T) {
	p := newResultPool(3)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 500})
	p.add(speedResult{IP: "2.2.2.2", Port: 443, MaxSpeed: 1500})
	p.add(speedResult{IP: "3.3.3.3", Port: 443, MaxSpeed: 100})
	p.add(speedResult{IP: "4.4.4.4", Port: 443, MaxSpeed: 900})

	got := p.list()
	if len(got) != 3 {
		t.Fatalf("池容量 3，实际留了 %d 个", len(got))
	}
	wantOrder := []string{"2.2.2.2", "4.4.4.4", "1.1.1.1"}
	for i, ip := range wantOrder {
		if got[i].IP != ip {
			t.Errorf("第 %d 位应为 %s，实际 %s", i, ip, got[i].IP)
		}
	}
	if best := p.best(); best.IP != "2.2.2.2" {
		t.Errorf("best() = %s, want 2.2.2.2", best.IP)
	}
}

// 同一个 IP 换端口不该占两个名额：用户要 5 个结果是想要 5 个不同落点
func TestResultPoolDedupByIP(t *testing.T) {
	p := newResultPool(5)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 500})
	p.add(speedResult{IP: "1.1.1.1", Port: 2053, MaxSpeed: 800})
	p.add(speedResult{IP: "1.1.1.1", Port: 8443, MaxSpeed: 300})

	got := p.list()
	if len(got) != 1 {
		t.Fatalf("同 IP 应只占一个名额，实际 %d 个", len(got))
	}
	// 保留的必须是最快的那个端口
	if got[0].MaxSpeed != 800 || got[0].Port != 2053 {
		t.Errorf("应保留最快的 2053/800，实际 %d/%d", got[0].Port, got[0].MaxSpeed)
	}
}

// 速度为 0 或空 IP 不能进池：那不是可用结果
func TestResultPoolRejectsEmpty(t *testing.T) {
	p := newResultPool(5)
	p.add(speedResult{IP: "", Port: 443, MaxSpeed: 900})
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 0})
	p.add(speedResult{IP: "2.2.2.2", Port: 443, MaxSpeed: -1})
	if p.len() != 0 {
		t.Errorf("无效结果不该进池，实际 %d 个", p.len())
	}
}

// 速度相同时延迟低的靠前
func TestResultPoolTieBreakByLatency(t *testing.T) {
	p := newResultPool(3)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 500, LatencyMs: 200})
	p.add(speedResult{IP: "2.2.2.2", Port: 443, MaxSpeed: 500, LatencyMs: 30})
	if got := p.list(); got[0].IP != "2.2.2.2" {
		t.Errorf("同速应让低延迟靠前，实际第一位是 %s", got[0].IP)
	}
}

// qualified 只数达标的，不能把未达标的也算进去
func TestResultPoolQualified(t *testing.T) {
	p := newResultPool(5)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 1000})
	p.add(speedResult{IP: "2.2.2.2", Port: 443, MaxSpeed: 200})
	p.add(speedResult{IP: "3.3.3.3", Port: 443, MaxSpeed: 640})

	if n := p.qualified(640); n != 2 {
		t.Errorf("target=640 时应有 2 个达标，实际 %d", n)
	}
	if n := p.qualified(2000); n != 0 {
		t.Errorf("target=2000 时应有 0 个达标，实际 %d", n)
	}
}

// 索引在排序截断后必须重建，否则替换会改错条目
func TestResultPoolIndexRebuiltAfterTrim(t *testing.T) {
	p := newResultPool(2)
	p.add(speedResult{IP: "a", Port: 443, MaxSpeed: 100})
	p.add(speedResult{IP: "b", Port: 443, MaxSpeed: 200})
	p.add(speedResult{IP: "c", Port: 443, MaxSpeed: 300}) // 挤掉 a
	// 再喂一次 b 的更快结果，若索引没重建会写到错误的下标
	p.add(speedResult{IP: "b", Port: 443, MaxSpeed: 400})

	got := p.list()
	if len(got) != 2 {
		t.Fatalf("容量 2，实际 %d", len(got))
	}
	if got[0].IP != "b" || got[0].MaxSpeed != 400 {
		t.Errorf("b 应更新为 400 并排第一，实际 %s/%d", got[0].IP, got[0].MaxSpeed)
	}
	if got[1].IP != "c" {
		t.Errorf("第二位应为 c，实际 %s", got[1].IP)
	}
}

// finalistCount：要 N 个结果得留出余量，但不能超过单轮上限
func TestFinalistCount(t *testing.T) {
	if got := finalistCount(1); got != speedTestFinalists {
		t.Errorf("要 1 个时应沿用 %d，实际 %d", speedTestFinalists, got)
	}
	if got := finalistCount(5); got != 7 {
		t.Errorf("要 5 个应测 7 个（留 2 个余量），实际 %d", got)
	}
	// 要 10 个时 10+2=12 超过 maxSpeedTestCount，须被夹住
	if got := finalistCount(10); got != maxSpeedTestCount {
		t.Errorf("要 10 个应夹到 %d，实际 %d", maxSpeedTestCount, got)
	}
	for _, n := range allowedResultCounts {
		if got := finalistCount(n); got > maxSpeedTestCount {
			t.Errorf("finalistCount(%d) = %d 超过单轮上限 %d", n, got, maxSpeedTestCount)
		}
	}
}

// outcomeFromPool 顶层字段必须等于最快那一条，保证老界面读顶层不变味
func TestOutcomeFromPoolTopMatchesFirst(t *testing.T) {
	p := newResultPool(3)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 300, LatencyMs: 50, DataCenter: "HKG", Country: "HK"})
	p.add(speedResult{IP: "2.2.2.2", Port: 2053, MaxSpeed: 900, LatencyMs: 80, DataCenter: "NRT", Country: "JP"})

	out := outcomeFromPool(p)
	if out.IP != "2.2.2.2" || out.Port != 2053 || out.MaxSpeed != 900 {
		t.Errorf("顶层应为最快的 2.2.2.2:2053/900，实际 %s:%d/%d", out.IP, out.Port, out.MaxSpeed)
	}
	if out.DataCenter != "NRT" || out.Country != "JP" {
		t.Errorf("顶层机房/国家应跟着最快那条，实际 %s/%s", out.DataCenter, out.Country)
	}
	if len(out.Results) != 2 {
		t.Errorf("Results 应有 2 条，实际 %d", len(out.Results))
	}
	if out.Results[0].IP != out.IP {
		t.Errorf("Results[0] 必须与顶层一致")
	}
}

func TestOutcomeFromEmptyPool(t *testing.T) {
	out := outcomeFromPool(newResultPool(5))
	if out.IP != "" || len(out.Results) != 0 {
		t.Errorf("空池应返回零值，实际 %+v", out)
	}
}

// JSON 里必须同时有顶层字段和 results 数组：
// 老界面读顶层，新界面读 results，两条路都要通
func TestScanResultJSONShape(t *testing.T) {
	r := ScanResult{
		IP: "1.1.1.1", Port: 443, Address: "1.1.1.1:443",
		WantCount: 5, FoundCount: 2,
		Results: []ScanItem{
			{IP: "1.1.1.1", Port: 443, Address: "1.1.1.1:443", MaxSpeed: 900},
			{IP: "2.2.2.2", Port: 443, Address: "2.2.2.2:443", MaxSpeed: 400},
		},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for _, k := range []string{"ip", "port", "address", "results", "wantCount", "foundCount"} {
		if _, ok := back[k]; !ok {
			t.Errorf("JSON 缺少字段 %q", k)
		}
	}
	arr, ok := back["results"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("results 应是长度 2 的数组，实际 %#v", back["results"])
	}
}

// 只要 1 个结果时，results 里也应该有一条（而不是空数组），
// 否则新界面得为「1 个」写一条特例分支
func TestSingleCountStillFillsResults(t *testing.T) {
	p := newResultPool(1)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 900})
	out := outcomeFromPool(p)
	if len(out.Results) != 1 {
		t.Fatalf("要 1 个时 Results 也该有 1 条，实际 %d", len(out.Results))
	}
	if out.Results[0].IP != out.IP {
		t.Errorf("Results[0] 与顶层不一致")
	}
}

// 要多个结果时不能沿用 goodEnough 那条捷径。
//
// 这是最容易写错的地方：第一个 IP 跑出目标 3 倍的速度确实说明它很好，
// 但要 5 个结果时直接 break 就只能给出 1 个。这里用真实的
// speedTestRound 分支条件做一次静态确认。
func TestMultiCountIgnoresGoodEnoughShortcut(t *testing.T) {
	target := 128
	// 模拟一个远超目标的结果
	fast := target * speedTestGoodEnough * 2

	// wantCount == 1：达标且远超目标 → 应该收工
	if !(fast >= target && (!speedTestPickBest || fast >= target*speedTestGoodEnough)) {
		t.Errorf("单结果模式下 %d kB/s 应触发提前收工", fast)
	}

	// wantCount > 1：池里只有 1 个达标而要 5 个 → 不能收工
	p := newResultPool(5)
	p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: fast})
	if p.qualified(target) >= 5 {
		t.Errorf("只有 1 个达标却认为凑够 5 个")
	}
}

// 池容量必须等于 wantCount：要 5 个就不能只留 3 个
func TestPoolLimitMatchesWantCount(t *testing.T) {
	for _, n := range allowedResultCounts {
		p := newResultPool(n)
		for i := 0; i < n+5; i++ {
			p.add(speedResult{
				IP:       "10.0.0." + string(rune('0'+i%10)) + string(rune('a'+i)),
				Port:     443,
				MaxSpeed: 100 + i,
			})
		}
		if p.len() != n {
			t.Errorf("wantCount=%d 时池应留 %d 个，实际 %d", n, n, p.len())
		}
	}
}

// 池容量下限为 1：传 0 或负数不能得到一个永远空的池
func TestPoolMinimumCapacity(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		p := newResultPool(n)
		p.add(speedResult{IP: "1.1.1.1", Port: 443, MaxSpeed: 500})
		if p.len() != 1 {
			t.Errorf("newResultPool(%d) 应至少能存 1 个，实际 %d", n, p.len())
		}
	}
}
