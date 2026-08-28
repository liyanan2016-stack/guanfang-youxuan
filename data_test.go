package better

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 数据文件的下载/缓存状态相关的行为。桌面版多了「取消扫描后点更新数据」
// 这条路径，Android 版没有独立取消按钮时不容易触发。

// TestUpdateDataResetsCancel 上一个任务留下的取消状态不能泄漏到下一个任务。
// 否则 downloadAllData 会在第一个 isCancelled() 检查处直接返回：
// 旧文件已删、新文件没下，进度却显示"数据更新完成"。
func TestUpdateDataResetsCancel(t *testing.T) {
	withTempDataDir(t)

	// 模拟「扫描完成/被取消」之后的遗留状态
	enterTask()
	CancelScan()
	if !isCancelled() {
		t.Fatal("前置条件不成立：CancelScan 后 isCancelled 应为 true")
	}

	UpdateData()

	if isCancelled() {
		t.Error("旧的取消状态泄漏到了新任务，下载会被静默跳过")
	}
}

// TestBeginTaskPreservesCancel 「点开始 → 立刻点取消 → 任务才真正跑起来」
// 这段窗口里的取消不能丢。BeginTask 在前台建好上下文，
// 任务接手时必须沿用它而不是重建，否则取消按钮看起来就是失灵。
func TestBeginTaskPreservesCancel(t *testing.T) {
	withTempDataDir(t)

	BeginTask()  // 界面点了开始
	CancelScan() // 任务还没跑起来，用户就取消了

	if !isCancelled() {
		t.Fatal("前置条件不成立：CancelScan 应立即生效")
	}

	// 任务此刻才真正开始
	enterTask()

	if !isCancelled() {
		t.Error("BeginTask 之后到达的取消被任务启动抹掉了")
	}
}

// TestEnterTaskWithoutBeginTaskClearsStaleCancel 没走 BeginTask 时
// （例如桌面版直接调），仍然要清掉上一个任务的取消状态
func TestEnterTaskWithoutBeginTaskClearsStaleCancel(t *testing.T) {
	withTempDataDir(t)

	enterTask()
	CancelScan()
	if !isCancelled() {
		t.Fatal("前置条件不成立")
	}

	enterTask() // 新任务，没有先 BeginTask

	if isCancelled() {
		t.Error("上一个任务的取消状态泄漏到了新任务")
	}
}

// TestUpdateDataDoesNotLieOnFailure 保存失败时不能报"数据更新完成"，
// 而且必须把失败原因返回给调用方——界面拿不到返回值就只能无条件报成功。
// 制造失败的办法是让 dataDir 的父级是一个普通文件，
// MkdirAll 会因 ENOTDIR 失败（单纯用不存在的目录不行，saveToFile 会自己创建）。
func TestUpdateDataDoesNotLieOnFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "iamafile")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := swapDataDir(t, filepath.Join(blocker, "sub"))
	defer restore()

	errMsg := UpdateData()

	if errMsg == "" {
		t.Error("写入不可能成功，UpdateData 却返回空串（表示成功）")
	}
	if got := GetProgress(); got == "数据更新完成" {
		t.Errorf("写入不可能成功却报了成功，progress=%q", got)
	}
	if missing := missingDataFiles(); len(missing) == 0 {
		t.Error("文件都没写成功，missingDataFiles 却认为齐备")
	}
}

// TestUpdateDataReportsCancellation 更新前就被取消时，
// 返回值要说明是取消而不是空串（空串会被界面当成成功）
func TestUpdateDataReportsCancellation(t *testing.T) {
	withTempDataDir(t)

	BeginTask()
	CancelScan()

	errMsg := UpdateData()

	if errMsg == "" {
		t.Fatal("任务在开始前已被取消，却返回空串（表示成功）")
	}
	if !strings.Contains(errMsg, "取消") {
		t.Errorf("返回值没说明是取消: %q", errMsg)
	}
}

// TestCancelScanProgressIsNeutral 取消提示不能写死"扫描"。
// 数据更新走同一个取消通道，说成"已取消扫描"会让用户以为点错了按钮。
func TestCancelScanProgressIsNeutral(t *testing.T) {
	enterTask()
	CancelScan()

	if got := GetProgress(); strings.Contains(got, "扫描") {
		t.Errorf("取消提示写死了「扫描」，数据更新时会误导用户: %q", got)
	}
}

// TestMissingDataFilesTreatsEmptyAsMissing 0 字节文件要算缺失。
// 下载中断会留下空文件，如果当成"已存在"，问题会拖到解析阶段才暴露，
// 报出来的错误也和真正的原因无关。
func TestMissingDataFilesTreatsEmptyAsMissing(t *testing.T) {
	withTempDataDir(t)

	for _, f := range dataFiles {
		if err := os.WriteFile(dataPath(f), nil, 0o644); err != nil {
			t.Fatalf("创建空文件失败: %v", err)
		}
	}

	missing := missingDataFiles()
	if len(missing) != len(dataFiles) {
		t.Errorf("空文件应全部算缺失，got %d 个: %v", len(missing), missing)
	}
}

// TestMissingDataFilesEmptyWhenAllPresent 文件齐备时不应报缺失
func TestMissingDataFilesEmptyWhenAllPresent(t *testing.T) {
	withTempDataDir(t)

	for _, f := range dataFiles {
		if err := os.WriteFile(dataPath(f), []byte("x"), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}

	if missing := missingDataFiles(); len(missing) != 0 {
		t.Errorf("文件齐备却报缺失: %v", missing)
	}
}

// TestClearCacheRemovesAllDataFiles 清缓存要清掉所有数据文件，
// 漏掉任何一个都会让"清除缓存后重新下载"变成部分生效
func TestClearCacheRemovesAllDataFiles(t *testing.T) {
	withTempDataDir(t)

	for _, f := range dataFiles {
		if err := os.WriteFile(dataPath(f), []byte("x"), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}

	ClearCache()

	for _, f := range dataFiles {
		if fileExists(dataPath(f)) {
			t.Errorf("%s 没有被清除", f)
		}
	}
}

// TestFileExistsRejectsDirAndEmpty fileExists 的边界：
// 目录和空文件都不能算"文件存在"
func TestFileExistsRejectsDirAndEmpty(t *testing.T) {
	dir := t.TempDir()

	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if fileExists(sub) {
		t.Error("目录被当成了文件")
	}

	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if fileExists(empty) {
		t.Error("0 字节文件被当成了有效文件")
	}

	ok := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(ok, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(ok) {
		t.Error("正常文件被判为不存在")
	}
}

// ---------- 辅助 ----------

// withTempDataDir 把 dataDir 指向临时目录，测试结束自动还原。
// 全局变量必须还原，否则会污染同包内其他测试。
func withTempDataDir(t *testing.T) {
	t.Helper()
	restore := swapDataDir(t, t.TempDir())
	t.Cleanup(restore)
}

func swapDataDir(t *testing.T, dir string) func() {
	t.Helper()
	old := dataDir
	dataDir = dir
	return func() { dataDir = old }
}
