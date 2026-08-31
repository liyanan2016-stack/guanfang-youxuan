# 官方优选

Cloudflare 官方 IP 段优选工具（Android + Windows/Linux）。测 RTT 与下载带宽，挑出你当前网络下最快的那个 IP。

> 第一次用看 [使用教程.md](使用教程.md)｜每版改了什么看 [更新日志.md](更新日志.md)

<!--
  两张截图并排。
  用 table 而不是把两个 <img> 摞在一起：GitHub 渲染 Markdown 时会把
  连续的图片各占一行，宽度写多少都不影响换行。表格是唯一能在 README 里
  可靠并排的手段（GitHub 的 CSS 沙箱不认 flex、float、display 这些内联样式）。

  宽度写在 <img width> 上，改这个数就能调尺寸；高度留空让它按原图比例
  （1264×2800）自适应，写死高度会把截图压变形。
-->
<table>
  <tr>
    <td align="center" width="50%">
      <img width="330" alt="扫描前的参数界面"
           src="https://github.com/user-attachments/assets/b0647dfe-a08a-4b5b-ba18-9db4df5cd891" />
      <br /><sub>扫描参数</sub>
    </td>
    <td align="center" width="50%">
      <img width="330" alt="扫描完成后的结果界面"
           src="https://github.com/user-attachments/assets/d5ac4c1e-23db-4de3-bc0b-ffa2be595b1c" />
      <br /><sub>扫描结果</sub>
    </td>
  </tr>
</table>

## 特色

**指定 IP 段。** 有明确目标时直接填 `104.17.168.0/22`，候选池从六千多个子网压到几十个 —— 实测 4 秒出结果，扫官方全部段要 14 秒。指定段时不读也不下载官方列表，数据源挂了照样能用。

**按运营商自动挑测速源。** 中国移动国际出口对不同域名的限速差别很大，这是「优选出来很快、实际用起来很慢」的头号原因。自动档探 `cf.json` 拿 ASN，判定是移动就换用移动友好源。也可以手动填自己域名下的大文件，测的就是你真正会用的那条链路。

**测的是带宽，不是延迟。** 同城机房延迟 20ms 但可能已被打满，300ms 的远端反而跑得开。所有候选测完再选最快的，速度用 EWMA 平均而非突发峰值，只接受 200/206 状态码。

**端口全覆盖。** CF 边缘不只监听 80/443 —— 明文还有 8080/8880/2052/2082/2086/2095，TLS 还有 2053/2083/2087/2096/8443。选错端口的后果是优选出的 IP 接上节点就报 `closed pipe`。

**校验回源是否真的通。** 只看响应带 `CF-RAY` 会把「CF 到不了你的服务器」的 IP 当成可用报出来（521–526 同样带这个头），用户侧表现为连上就断，免费套餐尤其常见。改成状态码白名单判定。填了自己的节点域名，验证的就是真实链路。

**地区筛选。** 官方 IP 的落地机房取决于运营商线路，事先无从得知，只能测完从 `CF-RAY` 反查。选了地区会先做一轮廉价侦察（每子网 1 个 IP、只拿机房代码），把发现成本压到约 1/8。

**输出数量可调。** 1 / 5 / 10 个，要备选地址时不用重复扫。

## 下载

到 [Releases](../../releases) 页下载：

- **Android**：`-universal.apk`（通用）或 `-arm64-v8a.apk`（2016 年后的手机，省一半空间）
- **Windows**：`-windows-amd64.exe`
- **Linux**：`-linux-amd64`

APK 只申请 `INTERNET` 一个权限，没有后台服务、没有开机自启。桌面版双击即用，会打开浏览器界面，只监听 `127.0.0.1`。

## 不做什么

**不做任何形式的代理或加速。** 它只输出一个 IP 地址。

优选结果与网络环境强相关，换 WiFi、换运营商、过几小时都可能变 —— 这是「用时再扫」的工具。

名字里的「官方」指**数据源是 CF 官方公布的 IP 段**，不是「官方出品」。子网列表与机房位置表来自第三方公开接口 `baipiao.eu.org`，本项目不托管这些数据。Cloudflare 是 Cloudflare, Inc. 的商标，本项目与其无任何关联。

## 与「反代优选」的区别

两个独立项目，可同时安装。

| | 官方优选 | 反代优选 |
|---|---|---|
| 数据源 | CF 官方子网列表 | 已验证的 `IP:PORT` 列表 |
| 候选 IP | 从子网随机拼 | 直接用现成条目 |
| 地区筛选 | 测完再筛，会明显变慢 | 列表自带标签，先筛后测 |
| 包名 | `com.gf.youxuan` | `com.cf.ip` |
| 使用门槛 | 无 | 密码 |

## 已知限制

- **候选 IP 大部分是死的**，所以扫描时会反复看到「这批 IP 都存在 RTT 丢包」。数据源决定的，不是 bug。指定 IP 段可以绕开。
- **地区筛得越窄，耗时增长越明显。** 随机拼的 IP 大部分不通，能连通的里面再筛地区，命中率是乘法关系。
- **机房位置数据可能滞后**，极少数结果的国家可能显示为空。这种情况选择放行而不是拒绝。
- **测速源是公共端点**，短时间内反复扫描会撞 429。想要完全可控的数字就用「手动输入」。
- **回源校验未在真实的「免费套餐 + 回源不通」环境端到端验证过。** 按 CF 官方错误码规范实现；如果 CF 那边是直接 TCP RST 而不是返回 523，这层判断抓不到。

## 构建

```bash
./build-apk.sh                     # Android debug
./build-apk.sh release             # Android release，需签名配置
./build-desktop.sh                 # 桌面版，交叉编译，不需要 Windows 机器
```

工具链目录自动探测，也可以用 `GF_SDK_ROOT=/opt/toolchain` 指定。两个脚本出包前都会跑 `gofmt` / `go vet` / `go test`，任一失败即中止。

release 签名配置写在 `android/store.properties`（不要提交到 Git）：

```properties
storeFile=guanfang-release.jks
storePassword=***
keyAlias=guanfang
keyPassword=***
```

**签名密钥必须单独备份** —— 丢了之后用户没法覆盖安装新版，而且无法补救。

```
better.go            对外 API（gomobile 绑定入口，桌面版也调它）
scan.go              核心扫描逻辑
recon.go             地区侦察
speedsource.go       测速源选择
custom_range.go      指定 IP 段解析
android/             Android 界面（XML + Java）
cmd/guanfang/        桌面版：本地 HTTP 服务 + 内嵌网页
```

两端共用同一份 Go 核心层，行为一致。

> 每个版本改了什么、为什么这么改，见 [更新日志.md](更新日志.md)。

## 鸣谢

- **[股神](https://t.me/CF_NAT)** —— 测速源与优选思路。v1.19 的「CM提供」（`cf.090227.xyz/__down`）与「移动专属」（`speed.okl.abrdns.com`）两档来自这条线上长期积累的实践，也是「按运营商挑测速源」这个方向的出处。
- **[PoemMisty/CFData-WEB](https://github.com/PoemMisty/CFData-WEB)** —— 按运营商分流测速源的具体做法。三源结构、探 `cf.json` 读 `asn` / `asOrganization` 判断中国移动、自动档的选源规则都参考了它。
- **[cmliu/edgetunnel](https://github.com/cmliu/edgetunnel)** —— CF 边缘节点的实际使用场景。端口选择、回源状态码校验都是从「优选出来的 IP 拿去接节点却报 `closed pipe`」这类真实问题倒推出来的。
- **Cloudflare** —— 公开的 IP 段数据。

上述项目与本项目代码独立，没有直接引用其源码，参考的是思路、数据源地址与判定口径。如有署名或许可上的疑问，开 issue 说明即可。

## 许可

[MIT](LICENSE)。

代码基于一份早期的 Cloudflare IP 优选实现重写而来，核心扫描逻辑经过大幅修改。如果你是原始代码的作者并希望补充署名或调整许可方式，开 issue 说明即可。

## 免责声明

本工具仅测量网络连通性与下载速度，输出 IP 地址。使用者需自行确保用途符合所在地法律法规与相关服务条款。作者不对使用后果承担责任。
