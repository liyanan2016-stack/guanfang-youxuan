package better

import (
	"reflect"
	"testing"
)

// 这些测试盯住新加的端口与地区筛选最容易出错的地方：
//  1. 端口必须落在 CF 实际支持的集合内 —— 填一个 CF 不监听的端口，
//     测出来必然全灭，用户会以为工具坏了；
//  2. 端口要跟着 TLS 模式走 —— 明文模式填 2053 是无意义的；
//  3. 地区筛选在 colo 查不到时必须放行 —— CF 新机房上线时
//     locations.json 会滞后，不能因此让用户拿不到结果。

func TestParsePortsCSVAcceptsSupportedPorts(t *testing.T) {
	got := parsePortsCSV("443,2053,8443", true)
	want := []int{443, 2053, 8443}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestParsePortsCSVRejectsUnsupported(t *testing.T) {
	// 22 和 3000 不是 CF 端口，1234 也不是；全都该被丢掉
	if got := parsePortsCSV("22,3000,1234", true); got != nil {
		t.Fatalf("不支持的端口应全部丢弃，got %v", got)
	}
	// 443 是 TLS 端口，明文模式下不该接受
	if got := parsePortsCSV("443", false); got != nil {
		t.Fatalf("明文模式不该接受 443，got %v", got)
	}
	// 反之 80 在 TLS 模式下也不该接受
	if got := parsePortsCSV("80", true); got != nil {
		t.Fatalf("TLS 模式不该接受 80，got %v", got)
	}
}

func TestParsePortsCSVDedupAndTrim(t *testing.T) {
	got := parsePortsCSV(" 443 , 443 ,2053, ,x,", true)
	want := []int{443, 2053}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestPortListFallsBackToDefault(t *testing.T) {
	var f scanFilter
	if got := f.portList(true); !reflect.DeepEqual(got, []int{443}) {
		t.Fatalf("TLS 默认端口应为 443，got %v", got)
	}
	if got := f.portList(false); !reflect.DeepEqual(got, []int{80}) {
		t.Fatalf("明文默认端口应为 80，got %v", got)
	}
}

func TestAllowsCountry(t *testing.T) {
	f := scanFilter{Countries: parseCountriesCSV("hk, jp ")}

	if !f.allowsCountry("HK") {
		t.Fatal("HK 应通过（大小写不敏感）")
	}
	if !f.allowsCountry("JP") {
		t.Fatal("JP 应通过")
	}
	if f.allowsCountry("US") {
		t.Fatal("US 不该通过")
	}
	// 关键：查不到国家时放行。CF 上线新机房时 locations.json 会滞后，
	// 一律拒绝会让用户在这段时间里完全拿不到结果。
	if !f.allowsCountry("") {
		t.Fatal("国家未知时应放行，否则新机房上线期间用户拿不到结果")
	}
}

func TestAllowsCountryEmptyFilterAllowsAll(t *testing.T) {
	var f scanFilter
	for _, c := range []string{"US", "HK", ""} {
		if !f.allowsCountry(c) {
			t.Fatalf("未设置地区筛选时 %q 应放行", c)
		}
	}
}

func TestCountryOfColo(t *testing.T) {
	locationMu.Lock()
	locationMap = map[string]location{
		"HKG": {Iata: "HKG", Cca2: "HK", City: "Hong Kong"},
		"LAX": {Iata: "LAX", Cca2: "us", City: "Los Angeles"},
	}
	locationMu.Unlock()
	defer func() {
		locationMu.Lock()
		locationMap = nil
		locationMu.Unlock()
	}()

	if got := countryOfColo("HKG"); got != "HK" {
		t.Fatalf("want HK, got %q", got)
	}
	// 数据源里出现小写也要归一化，否则筛选会莫名失配
	if got := countryOfColo("LAX"); got != "US" {
		t.Fatalf("want US（应统一大写）, got %q", got)
	}
	if got := countryOfColo("XXX"); got != "" {
		t.Fatalf("未知 colo 应返回空串, got %q", got)
	}
	if got := countryOfColo(""); got != "" {
		t.Fatalf("空 colo 应返回空串, got %q", got)
	}
}

func TestPortsForMatchesTLSMode(t *testing.T) {
	https := portsFor(true)
	if len(https) == 0 || https[0] != 443 {
		t.Fatalf("TLS 端口列表首项应为 443, got %v", https)
	}
	http := portsFor(false)
	if len(http) == 0 || http[0] != 80 {
		t.Fatalf("明文端口列表首项应为 80, got %v", http)
	}
	// 两组不能有交集，否则 parsePortsCSV 的模式校验就失去意义
	set := make(map[int]struct{}, len(https))
	for _, p := range https {
		set[p] = struct{}{}
	}
	for _, p := range http {
		if _, dup := set[p]; dup {
			t.Fatalf("端口 %d 同时出现在两组里", p)
		}
	}
}

func TestExportedPortListsAreParseable(t *testing.T) {
	// 界面拿这两个字符串构建选项，必须能被 parsePortsCSV 全量接受
	if got := parsePortsCSV(HTTPSPorts(), true); len(got) != len(cfHTTPSPorts) {
		t.Fatalf("HTTPSPorts() 应能被全量解析, got %v", got)
	}
	if got := parsePortsCSV(HTTPPorts(), false); len(got) != len(cfHTTPPorts) {
		t.Fatalf("HTTPPorts() 应能被全量解析, got %v", got)
	}
}
