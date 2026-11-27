// OpenListSync 引擎 — headless 双向同步核心（纯 CLI，无面板/托盘/挂载 UI）
// 由 OpenList Desktop 作为 sidecar 调用，也可独立常驻运行：
//
//	openlistsync -base <数据目录> -run  [-job <ID>]   执行一轮同步后退出
//	openlistsync -base <数据目录> -serve               常驻后台，按间隔自动同步
//
// 单文件 Go 应用，仅用标准库。管理引擎自己的 rclone 配置与定时任务，
// 不修改全局 rclone.conf。参数针对本机 OpenList(5244) 实测校准：
//   - 无哈希、modtime 不可靠 => --size-only
//   - WebDAV 并发 PUT 新建目录会竞态 => --transfers 1 --checkers 2
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 配置模型 ----------

type Job struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	LocalDir       string `json:"localDir"`       // Path1
	RemoteDir      string `json:"remoteDir"`      // Path2
	RemoteTrashDir string `json:"remoteTrashDir"` // backup-dir2（须与 RemoteDir 同级）
	LocalTrashDir  string `json:"localTrashDir"`  // backup-dir1
	AutoRemote     bool   `json:"autoRemote"`     // true=目标跟随总目录自动生成；false=手动指定
	Enabled        bool   `json:"enabled"`
}

type Globals struct {
	WebDAVURL        string `json:"webdavUrl"`
	User             string `json:"user"`
	Pass             string `json:"pass"`
	IntervalMin      int    `json:"intervalMin"`
	Transfers        int    `json:"transfers"`
	Checkers         int    `json:"checkers"`
	Retries          int    `json:"retries"`
	LowLevelRetries  int    `json:"lowLevelRetries"`
	Contimeout       string `json:"contimeout"`
	TimeoutStr       string `json:"timeout"`
	MaxDelete        int    `json:"maxDelete"`
	Compare          string `json:"compare"`
	RclonePath       string `json:"rclonePath"`
	Remote           string `json:"remote"`           // rclone 远端名，默认 openlist
	RemoteBase       string `json:"remoteBase"`       // 用户在网盘选的"上一级"目录，如 /ty2
	RemoteFolderName string `json:"remoteFolderName"` // 系统自动创建的固定子目录名，默认 agents-sy
	AutoEnabled      bool   `json:"autoEnabled"`      // 是否开启内置自动同步
	// 挂载配置（面板可控；含"开机自动挂载"）
	MountAuto         bool   `json:"mountAuto"`         // 程序启动时自动挂载（需先测试连接通过）
	MountAutoDisk     string `json:"mountAutoDisk"`     // 自动挂载用的盘符
	MountPlayMode     bool   `json:"mountPlayMode"`     // 播放模式：大预读+长缓存，为在线播放大视频优化
	MountBufferMB     int    `json:"mountBufferMB"`     // 预读缓冲（MB）
	MountDirCacheSec  int    `json:"mountDirCacheSec"`  // 目录缓存时间（秒）
	MountCacheDir     string `json:"mountCacheDir"`     // VFS 缓存目录（空=默认临时目录）
	MountCacheKeepSec int    `json:"mountCacheKeepSec"` // 缓存保留时长（秒），超时自动清理
	MountCacheMaxGB   int    `json:"mountCacheMaxGB"`   // 缓存目录上限（GB），超过自动清最久未访问
	MountRCAddr       string `json:"mountRCAddr"`       // rclone mount 的 rc 服务地址（同步后用它刷新挂载盘 VFS 缓存；空=不刷新）
	VerifyOnSync      bool   `json:"verifyOnSync"`      // 每轮同步后做深度校验（下载比对内容，修复同尺寸不同内容），较慢
}

type Config struct {
	Globals Globals `json:"globals"`
	Jobs    []Job   `json:"jobs"`
}

func defaultConfig() Config {
	return Config{
		Globals: Globals{
			WebDAVURL: "http://127.0.0.1:5244/dav", User: "admin", Pass: "",
			IntervalMin: 30, Transfers: 1, Checkers: 2, Retries: 10, LowLevelRetries: 20,
			Contimeout: "30s", TimeoutStr: "5m", MaxDelete: 50, Compare: "size",
			RclonePath: "rclone", Remote: "openlist", RemoteBase: "", RemoteFolderName: "agents-sy", AutoEnabled: true,
			MountRCAddr: "127.0.0.1:5572",
		},
		Jobs: []Job{},
	}
}

// ---------- 应用 ----------

type App struct {
	base      string
	confPath  string
	conf      Config
	rcloneLog string
	onlyJob   string // -job：只跑这一个任务（空=全部启用）
	mu        sync.Mutex
	running   map[string]bool
}

func main() {
	initJobObject() // 子进程(挂载)随主程序退出而终止，防死链
	runMode := flag.Bool("run", false, "执行一轮全部启用目录的同步后退出")
	verifyMode := flag.Bool("verify", false, "深度校验：下载比对本地与网盘文件内容，把内容不同（即使大小相同）的文件以本地为准推到网盘")
	prepareMode := flag.Bool("prepare", false, "仅在网盘创建各任务的目标目录与垃圾桶目录后退出")
	serveMode := flag.Bool("serve", false, "常驻后台：内置调度器按间隔持续同步（桌面端退出后不中断）")
	baseFlag := flag.String("base", "", "数据目录（config/state/logs/recycle 都放这里），默认是 exe 所在目录")
	jobFlag := flag.String("job", "", "只执行指定任务 ID（配合 -run 使用）")
	flag.Parse()
	base := mustExeDir()
	if strings.TrimSpace(*baseFlag) != "" {
		base = filepath.Clean(*baseFlag)
	}
	app := &App{base: base, confPath: filepath.Join(base, "config.json"), rcloneLog: filepath.Join(base, "rclone.log"), running: map[string]bool{}, onlyJob: strings.TrimSpace(*jobFlag)}

	app.conf = defaultConfig()
	if b, err := os.ReadFile(app.confPath); err == nil {
		_ = json.Unmarshal(b, &app.conf)
		// 旧配置里没有 autoEnabled 字段时，默认开启
		var rawTop map[string]json.RawMessage
		if json.Unmarshal(b, &rawTop) == nil {
			if g, ok := rawTop["globals"]; ok {
				var gm map[string]json.RawMessage
				if json.Unmarshal(g, &gm) == nil {
					if _, has := gm["autoEnabled"]; !has {
						app.conf.Globals.AutoEnabled = true
					}
				}
			}
		}
		if app.conf.Globals.RemoteFolderName == "" {
			app.conf.Globals.RemoteFolderName = "agents-sy"
		}
		if app.conf.Globals.Remote == "" {
			app.conf.Globals.Remote = "openlist"
		}
		if app.conf.Globals.MountRCAddr == "" {
			app.conf.Globals.MountRCAddr = "127.0.0.1:5572"
		}
		app.hardenAll()
	} else {
		_ = app.saveConf()
	}
	app.ensureDirs()
	// 启动时按各目录 .nosy 刷新过滤器，保证磁盘上的 filter 与规则一致
	for _, j := range app.conf.Jobs {
		_ = app.filterFor(j)
	}

	if *prepareMode {
		os.Exit(app.prepareAll())
	}
	if *verifyMode {
		app.runVerifyCLI()
		return
	}

	if *runMode {
		app.runAllCLI()
		return
	}

	// 常驻模式：内置调度器持续按间隔同步；桌面端退出后引擎继续运行
	if *serveMode {
		unlock := acquireServeLock(base)
		if unlock == nil {
			log.Printf("openlistsync -serve 已在运行（检测到 %s），跳过重复启动", serveLockFile)
			return
		}
		defer unlock()
		app.startScheduler()
		log.Printf("OpenListSync 引擎常驻运行：工作目录 %s（按配置间隔自动同步）", base)
		select {}
	}

	flag.Usage()
}

// runAllCLI 供计划任务调用：串行跑所有启用任务，任一失败退出码=1
func (a *App) runAllCLI() {
	rc := 0
	n := 0
	for _, j := range a.conf.Jobs {
		if !j.Enabled {
			continue
		}
		if a.onlyJob != "" && j.ID != a.onlyJob {
			continue
		}
		n++
		mode := "sync"
		if !a.hasListings(j) {
			mode = "resync" // 首次运行自动建立基线
			fmt.Printf("[%s] 首次运行，自动建立基线\n", j.Name)
		}
		out, err := a.runJob(j, mode)
		fmt.Print(out)
		if err != nil {
			rc = 1
		}
		if a.conf.Globals.VerifyOnSync {
			vout, verr := a.verifyJob(j)
			fmt.Print(vout)
			if verr != nil {
				rc = 1
			}
		}
	}
	if a.onlyJob != "" && n == 0 {
		fmt.Printf("[openlist-sync] 没有找到任务 %s（或该任务已停用）\n", a.onlyJob)
		rc = 2
	}
	fmt.Printf("\n[openlist-sync] 共执行 %d 个目录，退出码 %d\n", n, rc)
	os.Exit(rc)
}

// prepareAll 仅创建远端目标目录与垃圾桶目录，返回失败数
func (a *App) prepareAll() int {
	failed := 0
	for _, j := range a.conf.Jobs {
		if !j.Enabled {
			continue
		}
		if _, err := a.ownRcloneConf(); err != nil {
			fmt.Printf("[%s] 无法生成 rclone 配置: %v\n", j.Name, err)
			failed++
			continue
		}
		if strings.TrimSpace(j.RemoteDir) == "" {
			continue
		}
		if _, err := a.rclone("mkdir", j.RemoteDir); err != nil {
			fmt.Printf("[%s] 创建远端目录失败 %s: %v\n", j.Name, j.RemoteDir, err)
			failed++
		}
		if j.RemoteTrashDir != "" {
			if _, err := a.rclone("mkdir", j.RemoteTrashDir); err != nil {
				fmt.Printf("[%s] 创建远端垃圾桶失败 %s: %v\n", j.Name, j.RemoteTrashDir, err)
				failed++
			}
		}
	}
	fmt.Printf("[openlist-sync] 远端目录准备完成，失败 %d 个\n", failed)
	return failed
}

// runVerifyCLI -verify 模式入口：逐任务深度校验后退出
func (a *App) runVerifyCLI() {
	rc := 0
	n := 0
	for _, j := range a.conf.Jobs {
		if !j.Enabled {
			continue
		}
		if a.onlyJob != "" && j.ID != a.onlyJob {
			continue
		}
		n++
		out, err := a.verifyJob(j)
		fmt.Print(out)
		if err != nil {
			rc = 1
		}
	}
	if a.onlyJob != "" && n == 0 {
		fmt.Printf("[openlist-sync] 没有找到任务 %s（或该任务已停用）\n", a.onlyJob)
		rc = 2
	}
	fmt.Printf("\n[openlist-sync] 深度校验完成 %d 个目录，退出码 %d\n", n, rc)
	os.Exit(rc)
}

// verifyJob 深度校验：--checkfile 抓出"大小相同但内容不同"的文件，再用
// copy --size-only 把本地版本推到网盘（copy 对同尺寸文件默认跳过，
// 所以这里用 --no-... 不行，rclone copy 没有 ignore-size 选项；改为
// 用 check 找出差异文件后逐个 copyto 覆盖）。
func (a *App) verifyJob(j Job) (string, error) {
	if _, err := a.ownRcloneConf(); err != nil {
		return "", err
	}
	_ = os.MkdirAll(filepath.Dir(a.jobLog(j)), 0o755)
	_ = os.MkdirAll(filepath.Join(a.base, "jobs"), 0o755) // check --combined 输出目录
	logPath := a.jobLog(j)
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "\n===== [%s] %s 深度校验开始 =====\n", time.Now().Format("2006-01-02 15:04:05"), j.Name)
		f.Close()
	}

	header := fmt.Sprintf("【%s】深度校验 %s ↔ %s\n", j.Name, j.LocalDir, j.RemoteDir)

	// 1) 找出内容不同的文件（本地为基准计算 MD5，下载远端比对）
	checkArgs := []string{"check", j.LocalDir, j.RemoteDir,
		"--config", filepath.Join(a.base, "rclone.conf"),
		"--download",
		"--combined", filepath.Join(a.base, "jobs", j.ID+".checkdiff"),
		"--log-file", logPath, "--log-level", "INFO",
	}
	if fp := a.filterFor(j); fp != "" {
		checkArgs = append(checkArgs, "--filter-from", fp)
	}
	c := exec.Command(a.rcloneBin(), checkArgs...)
	hideWindow(c)
	_, checkErr := c.CombinedOutput()
	if checkErr != nil {
		// rclone check 有差异时退出码非 0，属预期；真正的命令错误也在这里
		header += fmt.Sprintf("· 校验发现差异或存在错误（详见日志）\n")
	}

	// 2) --combined 输出里 "==" 行表示 hash 不同（内容不同）
	diffList := filepath.Join(a.base, "jobs", j.ID+".checkdiff")
	var differing []string
	if b, err := os.ReadFile(diffList); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			// rclone --combined 单字符码："=" 一致，"*" 内容不同，"+" 仅本地，"-" 仅远端
			if strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "- ") {
				differing = append(differing, strings.TrimSpace(line[2:]))
			}
		}
		_ = os.Remove(diffList)
	}
	// 过滤：只保留相对路径里不在排除规则里的（check 已按 --filter-from 过滤）
	if len(differing) == 0 {
		header += "✅ 内容校验一致（或无可比文件）\n"
		if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
			fmt.Fprintf(f, "===== [%s] 深度校验结束：无差异 =====\n", j.Name)
			f.Close()
		}
		return header, nil
	}
	header += fmt.Sprintf("· 发现 %d 个内容不同（大小可能相同）的文件，以本地为准推送到网盘：\n", len(differing))
	var firstErr error
	pushed := 0
	for _, rel := range differing {
		rel = strings.ReplaceAll(rel, "\\", "/")
		src := filepath.Join(j.LocalDir, filepath.FromSlash(rel))
		st, err := os.Stat(src)
		if err != nil || st.IsDir() {
			continue // 本地已删的差异文件交给常规 bisync 处理
		}
		dst := j.RemoteDir + "/" + rel
		out, err := a.rclone("copyto", src, dst,
			"--no-check-dest", // 同尺寸文件 copyto 默认跳过；强制覆盖上传
			"--log-file", logPath, "--log-level", "INFO")
		if err != nil {
			header += fmt.Sprintf("  ! 推送失败 %s: %v %s\n", rel, err, strings.TrimSpace(out))
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pushed++
		header += fmt.Sprintf("  ↑ %s\n", rel)
	}
	header += fmt.Sprintf("✅ 深度校验完成：推送 %d/%d 个差异文件\n", pushed, len(differing))
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "===== [%s] 深度校验结束：推送 %d/%d =====\n", j.Name, pushed, len(differing))
		f.Close()
	}
	return header, firstErr
}

// refreshMountCache 通过 rclone mount 的 rc 接口刷新 VFS 目录/数据缓存。
// 同步把新文件拉到本地后，挂载盘上看到的还是旧缓存；dir-cache-time 到期前用户
// 只能读到旧内容。有 rc 地址配置时（默认 127.0.0.1:5572，mount 需加 --rc），
// 同步完成后调用 vfs/refresh 递归刷新目录缓存，挂载盘立即呈现最新内容。
// 失败静默（mount 未开 rc 时不影响同步本身）。
func (a *App) refreshMountCache() {
	addr := strings.TrimSpace(a.conf.Globals.MountRCAddr)
	if addr == "" {
		return
	}
	url := fmt.Sprintf("http://%s/rc/vfs/refresh", addr)
	body := strings.NewReader("{\"recursive\": true}")
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return // mount 未开启 rc 或未挂载，静默跳过
	}
	_ = resp.Body.Close()
}

func mustExeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

// 派生任务路径：垃圾桶若与目标同名目录冲突，自动改成 _trash_<base>
func hardenJob(j Job) Job {
	if j.ID == "" {
		j.ID = newID()
	}
	if j.Name == "" {
		j.Name = filepath.Base(filepath.Clean(j.LocalDir))
		if j.Name == "" || j.Name == "." || j.Name == `\` {
			j.Name = ""
		}
	}
	j.LocalDir = strings.TrimSpace(j.LocalDir)
	j.RemoteDir = strings.TrimSpace(j.RemoteDir)
	j.RemoteTrashDir = strings.TrimSpace(j.RemoteTrashDir)
	j.LocalTrashDir = strings.TrimSpace(j.LocalTrashDir)
	// 远端垃圾桶缺省：与目标同级
	if j.RemoteTrashDir == "" && j.RemoteDir != "" {
		j.RemoteTrashDir = deriveRemoteTrash(j.RemoteDir)
	}
	return j
}

func joinRemote(base, name string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	return base + "/" + name
}

// remoteBase 返回"网盘同步根目录"：用户在网盘选的上一级 + 自动建的固定子目录名。
// 例：RemoteBase="/ty2"，RemoteFolderName="agents-sy" → openlist:/ty2/agents-sy
func (a *App) remoteBase() string {
	remote := orDefault(a.conf.Globals.Remote, "openlist")
	pick := strings.TrimRight(strings.TrimSpace(a.conf.Globals.RemoteBase), "/")
	sub := strings.Trim(orDefault(a.conf.Globals.RemoteFolderName, "agents-sy"), "/")
	// 归一化 pick 为 "openlist:/a/b" 或 "openlist:"
	full := a.normalizeRemote(pick)
	if full == "" {
		full = remote + ":"
	}
	if sub == "" {
		return full
	}
	if strings.HasSuffix(full, ":") {
		return full + "/" + sub
	}
	return full + "/" + sub
}

// normalizeRemote 统一网盘路径：接受 "/ty2/x"、"ty2/x"、"openlist:/ty2/x"，
// 一律返回 "openlist:/ty2/x"（内部给 rclone 用）。空串返回空。
func (a *App) normalizeRemote(p string) string {
	remote := orDefault(a.conf.Globals.Remote, "openlist")
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if i := strings.Index(p, ":"); i >= 0 && !strings.Contains(p[:i], "/") {
		// 已带 "xxx:" 前缀（如 openlist:/ty2）
		rest := strings.TrimLeft(p[i+1:], "/")
		if rest == "" {
			return remote + ":"
		}
		return remote + ":/" + rest
	}
	rest := strings.TrimLeft(p, "/")
	if rest == "" {
		return remote + ":"
	}
	return remote + ":/" + rest
}

// displayRemote 去掉 remote: 前缀，返回 "/ty2/x"（界面显示用）
func (a *App) displayRemote(p string) string {
	a2 := a.normalizeRemote(p)
	i := strings.Index(a2, ":")
	if i >= 0 {
		return a2[i+1:]
	}
	return a2
}

// prepJob 补齐派生路径。一个本地文件夹 → 「系统总目录/<文件夹名>」。
func (a *App) prepJob(j Job) Job {
	j = hardenJob(j)
	// 尚未选择本地文件夹：不臆造任何网盘目标，保持为空，等选好后再派生
	if strings.TrimSpace(j.LocalDir) == "" {
		j.RemoteDir = ""
		j.RemoteTrashDir = ""
		j.AutoRemote = true
		return j
	}
	name := filepath.Base(filepath.Clean(j.LocalDir))
	if name == "" || name == "." || name == `\` {
		name = j.Name
	}
	auto := a.remoteBase() + "/" + name
	if strings.TrimSpace(j.RemoteDir) == "" {
		j.RemoteDir = auto
		j.AutoRemote = true
	} else {
		j.RemoteDir = a.normalizeRemote(j.RemoteDir) // 手填的也统一加前缀
		if j.AutoRemote {
			j.RemoteDir = auto // 跟随总目录变化刷新
		}
	}
	if j.RemoteTrashDir != "" {
		j.RemoteTrashDir = a.normalizeRemote(j.RemoteTrashDir)
	}
	if j.AutoRemote {
		j.RemoteTrashDir = deriveRemoteTrash(j.RemoteDir)
	} else if j.RemoteTrashDir == "" {
		j.RemoteTrashDir = deriveRemoteTrash(j.RemoteDir)
	}
	return j
}

// tagAuto 在保存单个任务前判定其目标是否等于"自动值"，据此设置 AutoRemote
func (a *App) tagAuto(j Job) Job {
	if strings.TrimSpace(j.LocalDir) == "" {
		j.AutoRemote = true
		return j
	}
	name := filepath.Base(filepath.Clean(j.LocalDir))
	if name == "" || name == "." || name == `\` {
		name = j.Name
	}
	auto := a.remoteBase() + "/" + name
	if strings.TrimSpace(j.RemoteDir) == "" {
		j.AutoRemote = true
	} else if strings.TrimRight(a.normalizeRemote(j.RemoteDir), "/") == strings.TrimRight(auto, "/") {
		j.AutoRemote = true
	} else {
		j.AutoRemote = false
	}
	return j
}

// deriveRemoteTrash 远端垃圾桶固定在总目录下的 .trash/<文件夹名>，各自独立。
// 例：remoteDir=openlist:/ty2/agents-sy/项目A → openlist:/ty2/agents-sy/.trash/项目A
func deriveRemoteTrash(remoteDir string) string {
	base := strings.TrimRight(remoteDir, "/")
	name := path_Base(base)
	// 去掉可能已有的 : 前缀部分再取父级
	parent := path_Dir(base)
	if parent == "" {
		return base + "/.trash/" + name
	}
	return parent + "/.trash/" + name
}

func path_Dir(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}
func path_Base(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (a *App) hardenAll() {
	for i := range a.conf.Jobs {
		a.conf.Jobs[i] = a.prepJob(a.conf.Jobs[i])
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("job%x", b)
}

func (a *App) job(id string) *Job {
	for i := range a.conf.Jobs {
		if a.conf.Jobs[i].ID == id {
			return &a.conf.Jobs[i]
		}
	}
	return nil
}

func (a *App) saveConf() error {
	b, _ := json.MarshalIndent(a.conf, "", "  ")
	_ = os.MkdirAll(filepath.Dir(a.confPath), 0o755)
	return os.WriteFile(a.confPath, b, 0o600)
}

func (a *App) ensureDirs() {
	for _, j := range a.conf.Jobs {
		for _, d := range []string{j.LocalDir, a.stateDir(j)} {
			_ = os.MkdirAll(d, 0o755)
		}
	}
}

func (a *App) stateDir(j Job) string { return filepath.Join(a.base, "state", j.ID) }
func (a *App) jobLog(j Job) string   { return filepath.Join(a.base, "logs", j.ID+".log") }

// localTrashDir 回收站不可用时的本地留档目录（绝不静默删除）。
func (a *App) localTrashDir(j Job) string { return filepath.Join(a.base, "trash", j.ID) }

// newStageDir 本次运行的 --backup-dir1 暂存区。必须在 LocalDir 之外，
// 否则暂存区会被当成待同步文件；LocalDir 与程序目录互相包含时直接跳过（退回旧的硬删除）。
func (a *App) newStageDir(j Job) string {
	root := filepath.Join(a.base, "recycle")
	if pathOverlaps(root, j.LocalDir) {
		return ""
	}
	return filepath.Join(root, j.ID, time.Now().Format("20060102-150405.000000"))
}

// pathOverlaps 判断两个目录相同或互为父子（Windows 路径不区分大小写）。
func pathOverlaps(a, b string) bool {
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	if a == "" || b == "" || a == "." || b == "." {
		return true
	}
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, b+sep) || strings.HasPrefix(b, a+sep)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// ---------- rclone ----------

func (a *App) ownRcloneConf() (string, error) {
	g := a.conf.Globals
	if g.User == "" || g.Pass == "" {
		return "", fmt.Errorf("请先填写并保存 WebDAV 账号密码")
	}
	obsc, err := a.rclone("obscure", g.Pass)
	if err != nil {
		return "", fmt.Errorf("rclone obscure 失败: %v", err)
	}
	obsc = strings.TrimSpace(obsc)
	path := filepath.Join(a.base, "rclone.conf")
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n", orDefault(g.Remote, "openlist"))
	b.WriteString("type = webdav\n")
	b.WriteString("url = " + g.WebDAVURL + "\n")
	b.WriteString("vendor = other\n")
	b.WriteString("user = " + g.User + "\n")
	b.WriteString("pass = " + obsc + "\n")
	b.WriteString("timeout = " + orDefault(g.TimeoutStr, "5m") + "\n")
	b.WriteString("contimeout = " + orDefault(g.Contimeout, "30s") + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// rcloneBin 返回要用的 rclone 可执行文件：优先用工具文件夹内自带的 rclone，
// 没有才回退到配置里的路径/系统 PATH，保证工具"自包含、免额外安装"。
func (a *App) rcloneBin() string {
	exe := "rclone"
	if runtime.GOOS == "windows" {
		exe = "rclone.exe"
	}
	bundled := filepath.Join(a.base, "rclone", exe)
	if st, err := os.Stat(bundled); err == nil && !st.IsDir() {
		return bundled
	}
	if b := strings.TrimSpace(a.conf.Globals.RclonePath); b != "" {
		return b
	}
	return "rclone"
}

func (a *App) rclone(args ...string) (string, error) {
	full := []string{"--config", filepath.Join(a.base, "rclone.conf"), "--log-file", a.rcloneLog, "--log-level", "INFO"}
	if len(args) > 0 && args[0] == "obscure" {
		full = []string{"--config", filepath.Join(a.base, "rclone.conf")}
	}
	full = append(full, args...)
	cmd := exec.Command(a.rcloneBin(), full...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// 为某任务构造 bisync 参数。stageDir 非空时作为 --backup-dir1：
// 网盘侧删除的本地文件先移进暂存区，跑完再由 stageAndRecycle 还原并送系统回收站。
func (a *App) bisyncArgs(j Job, mode, filterPath, stageDir string) []string {
	g := a.conf.Globals
	args := []string{"bisync", j.LocalDir, j.RemoteDir,
		"--config", filepath.Join(a.base, "rclone.conf"),
		"--backup-dir2", j.RemoteTrashDir,
		"--workdir", a.stateDir(j),
		"--recover",
		"--max-delete", strconv.Itoa(g.MaxDelete),
		"--retries", strconv.Itoa(g.Retries),
		"--low-level-retries", strconv.Itoa(g.LowLevelRetries),
		"--contimeout", orDefault(g.Contimeout, "30s"),
		"--timeout", orDefault(g.TimeoutStr, "5m"),
		"--transfers", strconv.Itoa(g.Transfers),
		"--checkers", strconv.Itoa(g.Checkers),
		"--log-file", a.jobLog(j),
		"--log-level", "INFO",
	}
	if stageDir != "" {
		args = append(args, "--backup-dir1", stageDir)
	}
	switch g.Compare {
	case "checksum":
		args = append(args, "--checksum")
	case "modtime":
		args = append(args, "--compare", "size,modtime")
	default:
		args = append(args, "--size-only")
	}
	if filterPath != "" {
		args = append(args, "--filter-from", filterPath)
	}
	effective := mode
	if mode == "dry-run" && !a.hasListings(j) {
		effective = "dry-run-resync"
	}
	switch effective {
	case "resync":
		args = append(args, "--resync")
		if mode != effective {
			args = append(args, "--dry-run")
		}
	case "dry-run":
		args = append(args, "--dry-run")
	case "dry-run-resync":
		args = append(args, "--resync", "--dry-run")
	}
	return append(args, "--stats-one-line")
}

func (a *App) hasListings(j Job) bool {
	entries, err := os.ReadDir(a.stateDir(j))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".lst") {
			return true
		}
	}
	return false
}

// filterFor 读取目录根的 .nosy，生成 rclone 过滤文件；无 .nosy 返回空串
func (a *App) filterFor(j Job) string {
	nosy := filepath.Join(j.LocalDir, ".nosy")
	b, err := os.ReadFile(nosy)
	if err != nil {
		return ""
	}
	p := filepath.Join(a.base, "jobs", j.ID+".filter")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(FiltersFileContent(string(b))), 0o644)
	return p
}

// runJob 执行一个任务；失败返回输出+错误
func (a *App) runJob(j Job, mode string) (string, error) {
	if _, err := a.ownRcloneConf(); err != nil {
		return "", err
	}
	_ = os.MkdirAll(a.stateDir(j), 0o755)
	_ = os.MkdirAll(filepath.Dir(a.jobLog(j)), 0o755)
	// bisync 不会自动建 Path2 根目录
	_, _ = a.rclone("mkdir", j.RemoteDir)
	if j.RemoteTrashDir != "" {
		_, _ = a.rclone("mkdir", j.RemoteTrashDir)
	}
	filterPath := a.filterFor(j)
	logPath := a.jobLog(j)

	// 每次运行前写分隔头，方便追踪本次进度
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "\n===== [%s] %s 开始 %s =====\n", time.Now().Format("2006-01-02 15:04:05"), j.Name, mode)
		f.Close()
	}

	// 网盘侧删除的文件会在这里被 bisync 移进暂存区；跑完还原原位并送系统回收站。
	stageDir := ""
	if !strings.HasPrefix(mode, "dry-run") {
		stageDir = a.newStageDir(j)
		if stageDir != "" {
			_ = os.MkdirAll(stageDir, 0o755)
		}
	}

	cmd := exec.Command(a.rcloneBin(), a.bisyncArgs(j, mode, filterPath, stageDir)...)
	hideWindow(cmd)
	procOut, err := cmd.CombinedOutput()

	body := strings.TrimSpace(string(procOut))
	header := fmt.Sprintf("【%s】%s ↔ %s\n", j.Name, j.LocalDir, j.RemoteDir)
	if err != nil {
		header += fmt.Sprintf("❌ %s 失败: %v\n", j.Name, err)
	} else {
		header += fmt.Sprintf("✅ %s 完成\n", j.Name)
	}
	if stageDir != "" {
		rec, kept, errs := stageAndRecycle(stageDir, j.LocalDir, a.localTrashDir(j))
		if rec > 0 {
			header += fmt.Sprintf("· 网盘侧删除：%d 个本地文件已移入系统回收站（可在资源管理器还原）\n", rec)
		}
		if kept > 0 {
			header += fmt.Sprintf("· %d 个文件进不了回收站，已留档到 %s\n", kept, a.localTrashDir(j))
		}
		for _, e := range errs {
			header += "  ! " + e + "\n"
		}
	}
	return header + body + "\n", err
}

// seedAndResync 首次同步快路径：并行 copy 铺底（容忍重试）→ bisync --resync 建基线。
// 直接 bisync 串行铺大目录极慢（天翼云盘单文件往返 ~2.6s），且高并发下 bisync 会因竞态中断，
// 故先并行 copy（copy 失败重试更宽容、不中断），再 bisync 建基线（此时几乎无需传输、秒级完成）。
func (a *App) seedAndResync(j Job) (string, error) {
	if _, err := a.ownRcloneConf(); err != nil {
		return "", err
	}
	_ = os.MkdirAll(a.stateDir(j), 0o755)
	_ = os.MkdirAll(filepath.Dir(a.jobLog(j)), 0o755)
	_, _ = a.rclone("mkdir", j.RemoteDir)
	_, _ = a.rclone("mkdir", j.RemoteTrashDir)
	filterPath := a.filterFor(j)

	logPath := a.jobLog(j)
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "\n===== [%s] %s 首次同步开始（铺底→建基线）=====\n", time.Now().Format("2006-01-02 15:04:05"), j.Name)
		f.Close()
	}

	// 步骤1：并行 copy 铺底（跳过已存在）
	copyArgs := []string{"copy", j.LocalDir, j.RemoteDir,
		"--config", filepath.Join(a.base, "rclone.conf"),
		"--size-only", "--ignore-existing",
		"--transfers", "4", "--checkers", "8",
		"--retries", "10", "--low-level-retries", "20",
		"--contimeout", orDefault(a.conf.Globals.Contimeout, "30s"),
		"--timeout", orDefault(a.conf.Globals.TimeoutStr, "5m"),
		"--log-file", logPath, "--log-level", "INFO",
	}
	if filterPath != "" {
		copyArgs = append(copyArgs, "--filter-from", filterPath)
	}
	c1 := exec.Command(a.rcloneBin(), copyArgs...)
	hideWindow(c1)
	_, _ = c1.CombinedOutput() // copy 的个别错误不致命，交给 resync 兜底

	// 步骤2：bisync --resync 建基线（仅补差异、写 listings）
	out, err := a.runJob(j, "resync")
	header := fmt.Sprintf("【%s】首次同步（已先并行铺底）\n", j.Name)
	return header + out, err
}

// runAll 后台串行跑所有已启用任务（不并发同一任务）
func (a *App) runAll() {
	a.mu.Lock()
	var js []Job
	for _, j := range a.conf.Jobs {
		if j.Enabled && !a.running[j.ID] {
			a.running[j.ID] = true
			js = append(js, j)
		}
	}
	a.mu.Unlock()
	// js 是快照；任务执行期间保存配置不会影响本轮已在跑的任务
	defer func() {
		a.mu.Lock()
		for _, j := range js {
			delete(a.running, j.ID)
		}
		a.mu.Unlock()
	}()
	for _, j := range js {
		if !a.hasListings(j) {
			a.seedAndResync(j) // 首次自动建基线
		} else {
			a.runJob(j, "sync")
		}
		if a.conf.Globals.VerifyOnSync {
			a.verifyJob(j) // size-only 补丁：内容比对，同尺寸不同内容以本地为准推网盘
		}
	}
	// 同步可能从网盘拉了新文件：刷新挂载盘的 VFS 缓存（若挂载开着 rc）
	a.refreshMountCache()
}

// startScheduler 常驻调度器：启动稍候即跑一次（若开启），之后按当前设置的间隔循环。
// 间隔与开关每轮实时读取，改了设置立即生效；关闭时不跑但继续等待开关打开。
func (a *App) startScheduler() {
	go func() {
		time.Sleep(5 * time.Second) // 给 OpenList 等外部 WebDAV 服务留出启动时间
		if a.conf.Globals.AutoEnabled {
			a.runAll()
		}
		for {
			mins := a.conf.Globals.IntervalMin
			if mins < 1 {
				mins = 30
			}
			time.Sleep(time.Duration(mins) * time.Minute)
			if a.conf.Globals.AutoEnabled {
				a.runAll()
			}
		}
	}()
}

// ---------- 单实例锁（防多个常驻引擎重复跑同一任务） ----------
// 用 base 目录下的锁文件实现：O_CREATE|O_EXCL 创建成功=拿到锁；
// 退出时删除。崩溃残留由 serve 启动时检测进程是否存活来容忍（简单策略：
// 直接覆盖旧锁文件，因为引擎自身有 running map 防 same-process 重入，
// 跨进程重复启动场景这里挡住最常见的"双击两次"即可）。

const serveLockFile = "serve.lock"

func acquireServeLock(base string) func() {
	path := filepath.Join(base, serveLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }
}
