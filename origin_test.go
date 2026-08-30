package better

import "testing"

// originReachable 的分类是 closed pipe 修复的核心：判错一个码，用户就会
// 拿到握手能过但一发数据就断的 IP。逐码固定住语义。
func TestOriginReachableAcceptsOriginResponses(t *testing.T) {
	// 这些响应都是「用户的源真的回话了」，回源链路通
	for _, code := range []int{
		101, // WebSocket 已升级 —— 隧道确实建立，最硬的证据
		200, 201, 204, 206,
		301, 302, 304, 307, 308,
		400, // 源说这个请求不合法，但它确实回话了
		404, // WS/gRPC 节点根路径无内容，正常
		405, // 源不接受 GET
		410,
	} {
		if !originReachable(code) {
			t.Errorf("originReachable(%d) = false, want true (response came from origin)", code)
		}
	}
}

func TestOriginReachableRejectsCloudflareErrors(t *testing.T) {
	// 52x 全部是 CF 自己生成的回源错误页，正是用户遇到的情况：
	// 免费套餐部分 IP 段对电信回源不通
	for _, code := range []int{
		521, // Web Server Is Down
		522, // Connection Timed Out
		523, // Origin Is Unreachable
		524, // A Timeout Occurred
		525, // SSL Handshake Failed
		526, // Invalid SSL Certificate
		530, // 通常伴随 1xxx 错误
	} {
		if originReachable(code) {
			t.Errorf("originReachable(%d) = true, want false (CF-generated origin error)", code)
		}
	}
}

func TestOriginReachableRejects403AndServerErrors(t *testing.T) {
	for _, code := range []int{
		403, // WAF 拦截或 zone 配置问题，不该当可用节点
		500, 502, 503, 504,
		0, // 没拿到响应
	} {
		if originReachable(code) {
			t.Errorf("originReachable(%d) = true, want false", code)
		}
	}
}

// 关键不变量：404 放行、523 判死。两者都带 CF-RAY，区别只在谁生成的响应。
// 这一条错了会导致「所有 WS 节点被误杀」或「回源不通的 IP 被放行」。
func TestOriginReachable404VsCloudflareError(t *testing.T) {
	if !originReachable(404) {
		t.Error("404 must pass: it proves CF reached the origin (WS nodes 404 on /)")
	}
	if originReachable(523) {
		t.Error("523 must fail: CF generated it because the origin was unreachable")
	}
}

// 白名单语义：未列出的码一律判不可用（偏保守）。
// 漏放一个可用 IP 的代价远小于放行一个会 closed pipe 的 IP。
func TestOriginReachableUnknownCodesAreRejected(t *testing.T) {
	for _, code := range []int{418, 451, 499, 599, 999, -1} {
		if originReachable(code) {
			t.Errorf("originReachable(%d) = true, want false (unlisted codes reject)", code)
		}
	}
}

// 没填 SNI 时 testRTT 不查状态码 —— 那时用的是测速域名，
// 回源是 CF 自己的事，状态码说明不了用户节点的任何情况。
// 这里固定住「未填 SNI 时连不通的 IP 仍然判死」这个基本行为。
func TestTestRTTUnreachableStillFails(t *testing.T) {
	freshTask(t)
	rtt, _, colo, loss := testRTT("192.0.2.1", 443, true, "")
	if rtt != 0 || colo != "" || loss != 1.0 {
		t.Errorf("testRTT unreachable = rtt %d colo %q loss %v, want 0/empty/1.0", rtt, colo, loss)
	}
}
