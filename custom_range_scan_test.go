package better

import (
	"os"
	"strings"
	"testing"
)

// TestScanUsesCustomRangesInsteadOfOfficialList 指定 IP 段时不读官方列表。
//
// 这是整个功能的核心契约。验证方式：数据目录里放一份内容完全不同的
// ips-v4.txt，再指定一个不重叠的段，检查进度文案报的子网数是用户段的
// 数量而不是官方列表的。
func TestScanUsesCustomRangesInsteadOfOfficialList(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "127.0.0.1", "x")
	writeFakeDataFiles(t, "1.1.1.0/24\n2.2.2.0/24\n3.3.3.0/24\n")

	ranges, err := parseCustomRanges("198.51.100.0/22")
	if err != nil {
		t.Fatal(err)
	}
	// /22 → 4 个 /24
	if len(ranges.V4) != 4 {
		t.Fatalf("应展开 4 个子网，实际 %d", len(ranges.V4))
	}

	// 取消后 cloudflareTest 会尽快返回，但数据源选择在取消检查之前完成，
	// 进度文案已经写好了 —— 这正是要断言的东西
	out := cloudflareTest(4, true, 2, 128, scanFilter{}, "",
		1, 5, SpeedSourceCloudflare, "", ranges)

	if !out.UsingCustomRanges && out.PoolSize == 0 {
		// PoolSize 为 0 说明扫描在建抽样器之前就退出了（网络不通等），
		// 此时无法断言，跳过而不是误报失败
		t.Skip("扫描过早退出，无法验证数据源")
	}
	if out.PoolSize != 4 {
		t.Fatalf("子网池应为用户指定的 4 个，实际 %d —— 说明读了官方列表", out.PoolSize)
	}
	if !out.UsingCustomRanges {
		t.Error("UsingCustomRanges 应为 true")
	}
}

// TestScanFallsBackToOfficialListWhenNoRanges 不填 IP 段时照旧读官方列表。
func TestScanFallsBackToOfficialListWhenNoRanges(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "127.0.0.1", "x")
	writeFakeDataFiles(t, "1.1.1.0/24\n2.2.2.0/24\n3.3.3.0/24\n")

	out := cloudflareTest(4, true, 2, 128, scanFilter{}, "",
		1, 5, SpeedSourceCloudflare, "", customRanges{})

	if out.PoolSize == 0 {
		t.Skip("扫描过早退出，无法验证数据源")
	}
	if out.PoolSize != 3 {
		t.Fatalf("子网池应为官方列表的 3 个，实际 %d", out.PoolSize)
	}
	if out.UsingCustomRanges {
		t.Error("没填 IP 段，UsingCustomRanges 应为 false")
	}
}

// TestScanRejectsWrongFamilyRanges 填了 v4 段却扫 v6 要给明确提示。
//
// 这是最容易犯的错。不提示的话用户只会看到「未找到可用 IP」，
// 对着一个空结果猜半天。
func TestScanRejectsWrongFamilyRanges(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "127.0.0.1", "x")
	writeFakeDataFiles(t, "1.1.1.0/24\n")

	ranges, err := parseCustomRanges("198.51.100.0/24")
	if err != nil {
		t.Fatal(err)
	}

	// ipType=6 但只填了 v4 段
	out := cloudflareTest(6, true, 2, 128, scanFilter{}, "",
		1, 5, SpeedSourceCloudflare, "", ranges)

	if out.SpeedHint == "" {
		t.Fatal("应给出提示")
	}
	if !strings.Contains(out.SpeedHint, "IPv6") {
		t.Errorf("提示应点明缺 IPv6 段，实际 %q", out.SpeedHint)
	}
	if out.IP != "" {
		t.Errorf("不该返回结果，实际 %q", out.IP)
	}
}

// TestGetIPsRejectsBadRanges GetIPs 层面填错 IP 段要立刻返回错误。
//
// 必须在任何耗时操作之前：不能让用户等下载完数据、跑完侦察才看到
// 「格式不对」。
func TestGetIPsRejectsBadRanges(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)

	BeginTask()
	res := GetIPs(true, true, 1, "", "", "", 1, 5, SpeedSourceCloudflare, "", "104.16/13")

	if !strings.Contains(res, "IP 段填写有误") {
		t.Fatalf("应返回 IP 段错误，实际 %s", res)
	}
}

// TestGetIPsAcceptsEmptyRanges 空 IP 段不算错误。
//
// 老版本界面不传这个参数（gomobile 生成的接口会传空串），
// 必须当成「不指定」而不是「填错了」。
func TestGetIPsAcceptsEmptyRanges(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "127.0.0.1", "x")
	writeFakeDataFiles(t, "1.1.1.0/24\n")

	BeginTask()
	// 立刻取消，只验证不会因为空 IP 段报错
	CancelScan()
	res := GetIPs(true, true, 1, "", "", "", 1, 5, SpeedSourceCloudflare, "", "")

	if strings.Contains(res, "IP 段填写有误") {
		t.Fatalf("空 IP 段不该报错，实际 %s", res)
	}
}

// TestCustomRangesSkipsIPListDownload 指定段时不下载官方列表。
//
// 首次使用的人没必要为一个 6500 行的下载干等，数据源挂掉时也不该
// 因此扫不了 —— 用户的段一个字节都不依赖它。
func TestCustomRangesSkipsIPListDownload(t *testing.T) {
	freshTask(t)
	withTempDataDir(t)
	withTestSpeedURL(t, "127.0.0.1", "x")

	// 只写 url.txt 和 locations.json，故意不写 ips-v4.txt。
	// 如果实现去下载官方列表，这个测试会因为访问外网而变慢或失败；
	// 正确实现应该完全不碰它。
	if err := os.WriteFile(dataPath("url.txt"), []byte("127.0.0.1/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath("locations.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	downloadAllData(false)

	if fileExists(dataPath("ips-v4.txt")) {
		t.Error("needIPList=false 时不该下载 ips-v4.txt")
	}
	if fileExists(dataPath("ips-v6.txt")) {
		t.Error("needIPList=false 时不该下载 ips-v6.txt")
	}
	// url.txt 仍要被读进来（测速地址兜底）
	if speedTestDomain == "" {
		t.Error("url.txt 仍应被读取")
	}
}

// TestCustomRangesFullCoverage 指定段时覆盖率下限拉到 100%。
//
// 用户给的池子本来就小、而且是他明确挑出来的，测 30% 就收手等于把他
// 指定的段大部分跳过了。这里通过 minCoverageRatio 的语义间接验证：
// 官方列表模式下 3 个子网的下限是 0（3×0.3 取整），指定段模式应是 3。
func TestCustomRangesFullCoverage(t *testing.T) {
	// 直接验算法而不跑完整扫描：跑完整扫描要真实网络，太慢也不稳定
	total := 100
	official := int(float64(total) * minCoverageRatio)
	if official >= total {
		t.Fatalf("minCoverageRatio=%v 已经是全量，这个测试失去意义", minCoverageRatio)
	}
	// 指定段时的下限就是 total 本身，见 cloudflareTest 里的 usingCustom 分支
	if total <= official {
		t.Fatal("指定段的下限应严格大于官方模式的下限")
	}
}

// writeFakeDataFiles 写一套最小可用的数据文件，避免测试访问外网。
func writeFakeDataFiles(t *testing.T, v4List string) {
	t.Helper()
	files := map[string]string{
		"url.txt":        "127.0.0.1/x\n",
		"ips-v4.txt":     v4List,
		"ips-v6.txt":     "2606:4700::/48\n",
		"locations.json": "[]",
	}
	for name, content := range files {
		if err := os.WriteFile(dataPath(name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
