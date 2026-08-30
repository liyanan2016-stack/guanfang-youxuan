// Windows/桌面版入口：启动本地 Web 服务并打开浏览器。
// 复用 better 包的扫描逻辑，界面与 Android 版对齐。
//
// 官方优选的数据源是 CF 官方子网列表，不含地理位置与端口信息，
// 所以这里没有地区/端口筛选，也没有使用门槛。
package main

import (
	"better"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

const appName = "guanfang-youxuan"

// appVersion 复用核心库版本，保证桌面版与 Android 版显示一致
var appVersion = better.Version()

var (
	stateMu    sync.Mutex
	scanning   bool
	lastResult string
	lastNotice string // 数据更新等非扫描任务的结果说明
	dataDir    string
	histPath   string
)

// ---------- 数据目录 ----------

func initDataDir() {
	base, err := os.UserConfigDir() // Windows: %AppData%；Linux: ~/.config
	if err != nil || base == "" {
		base, _ = os.UserHomeDir()
	}
	dataDir = filepath.Join(base, appName)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("警告：无法创建数据目录 %s: %v", dataDir, err)
		// 退回到当前目录，至少不至于直接跑不起来
		dataDir = "."
	}
	histPath = filepath.Join(dataDir, "history.json")
	better.SetCacheDir(dataDir)
}

// ---------- 历史记录 ----------

type histItem struct {
	IP string `json:"ip"`
	// Port 实测通过的端口。历史记录必须带上它，
	// 否则回看时只有 IP，还得重新猜端口
	Port          int    `json:"port"`
	Address       string `json:"address"`
	Bandwidth     int    `json:"bandwidth"`
	RealBandwidth int    `json:"realBandwidth"`
	MaxSpeed      int    `json:"maxSpeed"`
	LatencyMs     int    `json:"latencyMs"`
	DataCenter    string `json:"dataCenter"`
	Country       string `json:"country"`
	Elapsed       int    `json:"elapsed"`
	Time          string `json:"time"`
}

func loadHistory() []histItem {
	var items []histItem
	b, err := os.ReadFile(histPath)
	if err != nil {
		return items
	}
	_ = json.Unmarshal(b, &items)
	return items
}

func appendHistory(it histItem) {
	items := append([]histItem{it}, loadHistory()...)
	if len(items) > 10 { // 与 Android 版一致：保留最近 10 条
		items = items[:10]
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(histPath, b, 0o644)
}

// ---------- HTTP handlers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		V4        bool   `json:"v4"`
		UseTLS    bool   `json:"useTLS"`
		Bandwidth int    `json:"bandwidth"`
		Ports     string `json:"ports"`
		Countries string `json:"countries"`
		SNI       string `json:"sni"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "参数解析失败: " + err.Error()})
		return
	}

	stateMu.Lock()
	if scanning {
		stateMu.Unlock()
		writeJSON(w, map[string]string{"error": "已有扫描任务在进行"})
		return
	}
	scanning = true
	lastResult = ""
	lastNotice = ""
	// 在启动 goroutine 之前占住取消上下文：否则用户在 goroutine 真正跑起来
	// 之前点取消，那次取消会被任务启动时的重建抹掉
	better.BeginTask()
	stateMu.Unlock()

	go func() {
		res := better.GetIPs(req.V4, req.UseTLS, req.Bandwidth, req.Ports, req.Countries, req.SNI)

		stateMu.Lock()
		lastResult = res
		scanning = false
		stateMu.Unlock()

		// 有结果才写历史；用户取消的扫描不记录
		var parsed struct {
			IP            string `json:"ip"`
			Port          int    `json:"port"`
			Address       string `json:"address"`
			Bandwidth     int    `json:"bandwidth"`
			RealBandwidth int    `json:"realBandwidth"`
			MaxSpeed      int    `json:"maxSpeed"`
			LatencyMs     int    `json:"latencyMs"`
			DataCenter    string `json:"dataCenter"`
			Country       string `json:"country"`
			Elapsed       int    `json:"elapsed"`
			Cancelled     bool   `json:"cancelled"`
		}
		if json.Unmarshal([]byte(res), &parsed) == nil && parsed.IP != "" && !parsed.Cancelled {
			appendHistory(histItem{
				IP:            parsed.IP,
				Port:          parsed.Port,
				Address:       parsed.Address,
				Bandwidth:     parsed.Bandwidth,
				RealBandwidth: parsed.RealBandwidth,
				MaxSpeed:      parsed.MaxSpeed,
				LatencyMs:     parsed.LatencyMs,
				DataCenter:    parsed.DataCenter,
				Country:       parsed.Country,
				Elapsed:       parsed.Elapsed,
				Time:          time.Now().Format("2006-01-02 15:04"),
			})
		}
	}()

	writeJSON(w, map[string]bool{"started": true})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	sc, res, notice := scanning, lastResult, lastNotice
	stateMu.Unlock()
	writeJSON(w, map[string]any{
		"scanning": sc,
		"progress": better.GetProgress(),
		"result":   res,
		"notice":   notice,
	})
}

func handleCancel(w http.ResponseWriter, r *http.Request) {
	better.CancelScan()
	writeJSON(w, map[string]bool{"ok": true})
}

// handleUpdateData 后台重新下载数据。这里也要占住 scanning 标记，
// 否则用户可以在更新数据的同时发起扫描，读到写了一半的 IP 列表。
func handleUpdateData(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	if scanning {
		stateMu.Unlock()
		writeJSON(w, map[string]string{"error": "已有任务在进行"})
		return
	}
	scanning = true
	lastResult = ""
	lastNotice = ""
	better.BeginTask() // 同扫描：先占住取消上下文
	stateMu.Unlock()

	go func() {
		// 空串表示成功，否则是失败原因，前端要能看到
		err := better.UpdateData()
		stateMu.Lock()
		lastNotice = err
		scanning = false
		stateMu.Unlock()
	}()
	writeJSON(w, map[string]bool{"ok": true})
}

func handleClearCache(w http.ResponseWriter, r *http.Request) {
	stateMu.Lock()
	busy := scanning
	stateMu.Unlock()
	if busy {
		writeJSON(w, map[string]string{"error": "任务进行中，请先停止再清除缓存"})
		return
	}
	better.ClearCache()
	writeJSON(w, map[string]bool{"ok": true})
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		_ = os.Remove(histPath)
		writeJSON(w, []histItem{})
		return
	}
	items := loadHistory()
	if items == nil {
		items = []histItem{}
	}
	writeJSON(w, items)
}

// handleMeta 返回版本号与数据目录，供前端页脚显示
func handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version": appVersion,
		"dataDir": dataDir,
		// 端口列表由核心层给出，前端不硬编码 —— 两处各写一份，
		// 改一处忘另一处就会出现「界面能选、核心层不认」的端口
		"httpPorts":  better.HTTPPorts(),
		"httpsPorts": better.HTTPSPorts(),
	})
}

// ---------- 浏览器 ----------

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器，请手动访问 %s", url)
	}
}

// listenLocal 只监听回环地址，不把服务暴露到局域网。
// 端口被占用时自动向后尝试。
func listenLocal(startPort int) (net.Listener, int, error) {
	for p := startPort; p < startPort+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, p, nil
		}
	}
	return nil, 0, fmt.Errorf("127.0.0.1:%d-%d 全部被占用", startPort, startPort+19)
}

func main() {
	port := flag.Int("port", 8788, "本地监听端口")
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器")
	flag.Parse()

	initDataDir()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/meta", handleMeta)
	mux.HandleFunc("/api/scan", handleScan)
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/cancel", handleCancel)
	mux.HandleFunc("/api/update-data", handleUpdateData)
	mux.HandleFunc("/api/clear-cache", handleClearCache)
	mux.HandleFunc("/api/history", handleHistory)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := webFS.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})

	ln, actualPort, err := listenLocal(*port)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", actualPort)
	fmt.Printf("官方优选 - 桌面版 v%s\n", appVersion)
	fmt.Println("服务地址:", url)
	fmt.Println("数据目录:", dataDir)
	fmt.Println("按 Ctrl+C 退出（或直接关闭此窗口）")

	if !*noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}

	srv := &http.Server{Handler: mux}

	// 优雅退出：先取消扫描再关 HTTP 服务，
	// 否则正在跑的 goroutine 会把进程拖到 TCP 超时才退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Println("\n正在退出...")
		better.CancelScan()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
