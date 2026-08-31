package better

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSpeedSourcesMatchLabels 选项与中文名必须一一对应。
//
// 界面按索引把标识和显示名配对，两个列表长度不一致时会出现
// 「选了 Cloudflare 实际用的是移动源」这种错位，而且不报错。
func TestSpeedSourcesMatchLabels(t *testing.T) {
	ids := strings.Split(SpeedSources(), ",")
	labels := strings.Split(SpeedSourceLabels(), ",")
	if len(ids) != len(labels) {
		t.Fatalf("标识 %d 个、中文名 %d 个，必须一样多\nids=%v\nlabels=%v",
			len(ids), len(labels), ids, labels)
	}
	for i, id := range ids {
		if strings.TrimSpace(id) == "" {
			t.Errorf("第 %d 个标识是空串", i)
		}
		if strings.TrimSpace(labels[i]) == "" {
			t.Errorf("第 %d 个中文名是空串（标识 %q）", i, id)
		}
	}
	// auto 必须是第一个：界面默认选中第一项，默认就该是自动
	if ids[0] != SpeedSourceAuto {
		t.Errorf("第一项应为 %q，实际 %q", SpeedSourceAuto, ids[0])
	}
}

// TestResolveSpeedSourceBuiltins 每个内置标识都要能解析出可用地址。
func TestResolveSpeedSourceBuiltins(t *testing.T) {
	for _, id := range []string{
		SpeedSourceCloudflare, SpeedSourceCM, SpeedSourceMobile,
	} {
		d, _, err := resolveSpeedSource(id, "")
		if err != nil {
			t.Errorf("resolveSpeedSource(%q) 报错: %v", id, err)
			continue
		}
		if d == "" {
			t.Errorf("resolveSpeedSource(%q) 解析出空域名", id)
		}
		// 域名里不能残留端口/协议，否则会被当 TLS SNI 用导致握手失败
		if strings.ContainsAny(d, ":/ ") {
			t.Errorf("resolveSpeedSource(%q) 域名不干净: %q", id, d)
		}
	}
}

// TestResolveSpeedSourceAutoDefault 自动档在没探测过时回落 Cloudflare。
//
// 探测是优化，失败或没跑过都不该让测速不可用。
func TestResolveSpeedSourceAutoDefault(t *testing.T) {
	setAutoSpeedSource(cloudflareSpeedURL, "")
	t.Cleanup(func() { setAutoSpeedSource(cloudflareSpeedURL, "") })

	for _, id := range []string{"", SpeedSourceAuto} {
		d, f, err := resolveSpeedSource(id, "")
		if err != nil {
			t.Fatalf("resolveSpeedSource(%q) 报错: %v", id, err)
		}
		if d != "speed.cloudflare.com" || !strings.Contains(f, "__down") {
			t.Errorf("resolveSpeedSource(%q) = %q/%q，应回落 Cloudflare __down", id, d, f)
		}
	}
}

// TestResolveSpeedSourceAutoFollowsProbe 自动档要跟随探测结果。
func TestResolveSpeedSourceAutoFollowsProbe(t *testing.T) {
	setAutoSpeedSource(mobileDedicatedSpeedURL, "China Mobile")
	t.Cleanup(func() { setAutoSpeedSource(cloudflareSpeedURL, "") })

	d, _, err := resolveSpeedSource(SpeedSourceAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if d != "speed.okl.abrdns.com" {
		t.Errorf("自动档应使用探测选中的源，实际 %q", d)
	}
	if _, isp := currentAutoSpeedSource(); isp != "China Mobile" {
		t.Errorf("ISP 未记录，实际 %q", isp)
	}
}

// TestResolveSpeedSourceCustomWins 填了手动地址就以它为准。
//
// 「选了手动输入却没切标识」和「切了标识却没填」都是常见操作，
// 按用户实际输入走比按标识走更符合预期。
func TestResolveSpeedSourceCustomWins(t *testing.T) {
	d, f, err := resolveSpeedSource(SpeedSourceCloudflare, "mine.example/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if d != "mine.example" || f != "big.bin" {
		t.Errorf("手动地址应优先，实际 %q/%q", d, f)
	}
}

// TestResolveSpeedSourceCustomEmpty 选了手动输入但没填地址必须报错。
//
// 不能悄悄回落默认地址：用户会以为测的是自己的域名，
// 拿到一个和实际使用无关的速度。
func TestResolveSpeedSourceCustomEmpty(t *testing.T) {
	if _, _, err := resolveSpeedSource(SpeedSourceCustom, "   "); err == nil {
		t.Fatal("选了手动输入但地址为空，应报错")
	}
}

// TestResolveSpeedSourceUnknown 未知标识要报错而不是静默用默认。
func TestResolveSpeedSourceUnknown(t *testing.T) {
	if _, _, err := resolveSpeedSource("no-such-source", ""); err == nil {
		t.Fatal("未知测速源应报错")
	}
}

// TestIsChinaMobile 移动线路识别。
//
// 组织名格式各地不统一（CMI/CMNET/China Mobile 都见过），ASN 更可靠
// 但覆盖不全，所以两路都要判。
func TestIsChinaMobile(t *testing.T) {
	yes := []ispProbeInfo{
		{ASOrganization: "China Mobile Communications Corporation"},
		{ASOrganization: "CMNET"},
		{ASOrganization: "cmi"},
		{ASOrganization: "中国移动"},
		{ASN: 9808},
		{ASN: 56040},
		{ASN: 24400, ASOrganization: "unknown"},
	}
	for _, info := range yes {
		if !isChinaMobile(info) {
			t.Errorf("%+v 应判为移动", info)
		}
	}
	no := []ispProbeInfo{
		{ASN: 4134, ASOrganization: "Chinanet"},
		{ASN: 4837, ASOrganization: "CHINA UNICOM China169 Backbone"},
		{ASN: 13335, ASOrganization: "Cloudflare, Inc."},
		{},
	}
	for _, info := range no {
		if isChinaMobile(info) {
			t.Errorf("%+v 不该判为移动", info)
		}
	}
}

// TestPickMobileSpeedURL 移动源随机挑选只能落在预期集合里。
func TestPickMobileSpeedURL(t *testing.T) {
	allowed := map[string]bool{cmSpeedURL: true, mobileDedicatedSpeedURL: true}
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		u := pickMobileSpeedURL()
		if !allowed[u] {
			t.Fatalf("挑出了预期外的源: %q", u)
		}
		seen[u] = true
	}
	// 60 次两个都没碰到说明随机没生效（概率约 2^-59，可以断言）
	if len(seen) < 2 {
		t.Errorf("60 次只挑到 %d 个不同的源，随机可能没生效: %v", len(seen), seen)
	}
}

// TestSetAutoSpeedSourceRejectsEmpty 空地址要被换成默认值。
//
// 让它留空的话 resolveSpeedSource 会解析出空域名，
// 测速 URL 拼成 "https:///"，全部候选归零。
func TestSetAutoSpeedSourceRejectsEmpty(t *testing.T) {
	t.Cleanup(func() { setAutoSpeedSource(cloudflareSpeedURL, "") })
	setAutoSpeedSource("   ", "x")
	if v, _ := currentAutoSpeedSource(); v != cloudflareSpeedURL {
		t.Errorf("空地址应回落默认，实际 %q", v)
	}
}

// TestDetectISPParsesResponse 探测能正确解析 cf.json 字段。
//
// 用本地 httptest 而不是真访问 cf.090227.xyz：单测不该依赖外网。
// 通过临时替换 ispProbeURL 变量做到。
func TestDetectISPParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"asn":9808,"asOrganization":"China Mobile Communications Corporation","colo":"HKG"}`))
	}))
	defer srv.Close()

	old := ispProbeURL
	ispProbeURL = srv.URL
	t.Cleanup(func() { ispProbeURL = old })

	info, err := detectISP(nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.ASN != 9808 {
		t.Errorf("ASN = %d, want 9808", info.ASN)
	}
	if !isChinaMobile(info) {
		t.Errorf("%+v 应判为移动", info)
	}
}

// TestDetectISPRejectsNon200 非 200 响应要报错。
// 中间设备返回的登录页、错误页解析出来是零值，会被误判成非移动。
func TestDetectISPRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	old := ispProbeURL
	ispProbeURL = srv.URL
	t.Cleanup(func() { ispProbeURL = old })

	if _, err := detectISP(nil); err == nil {
		t.Fatal("非 200 应报错")
	}
}

// TestRefreshAutoSpeedSourceFallsBackOnError 探测失败必须回落而非留空。
func TestRefreshAutoSpeedSourceFallsBackOnError(t *testing.T) {
	old := ispProbeURL
	// 指向一个必然连不上的地址
	ispProbeURL = "http://127.0.0.1:1/cf.json"
	t.Cleanup(func() {
		ispProbeURL = old
		setAutoSpeedSource(cloudflareSpeedURL, "")
	})

	addr, isp, err := refreshAutoSpeedSource(nil)
	if err == nil {
		t.Fatal("探测应失败")
	}
	if addr != cloudflareSpeedURL {
		t.Errorf("失败时应回落 Cloudflare，实际 %q", addr)
	}
	if isp != "" {
		t.Errorf("失败时不该有 ISP，实际 %q", isp)
	}
	if v, _ := currentAutoSpeedSource(); v != cloudflareSpeedURL {
		t.Errorf("全局值也应回落，实际 %q", v)
	}
}

// TestISPProbeClientDisablesProxy ISP 探测必须禁用代理。
//
// 与地区判定同一套约定：挂着代理探出来的是代理出口的 AS，
// 据此选测速源等于按别人的网络环境优化。
func TestISPProbeClientDisablesProxy(t *testing.T) {
	tr, ok := ispProbeClient.Transport.(*http.Transport)
	if !ok {
		t.Fatal("ispProbeClient.Transport 应为 *http.Transport")
	}
	if tr.Proxy != nil {
		t.Fatal("ISP 探测必须禁用代理（Proxy 必须为 nil）")
	}
}
