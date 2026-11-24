// OpenList 双向同步 — 本地配置面板（多目录版）
// 单文件 Go 应用，仅用标准库。管理本工具自己的 rclone 配置与定时任务，
// 不修改全局 rclone.conf。参数针对本机 OpenList(5244) 实测校准：
//   - 无哈希、modtime 不可靠 => --size-only
//   - WebDAV 并发 PUT 新建目录会竞态 => --transfers 1 --checkers 2
package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

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
		},
		Jobs: []Job{},
	}
}

// ---------- 应用 ----------

type App struct {
	base        string
	confPath    string
	conf        Config
	rcloneLog   string
	mu          sync.Mutex
	running     map[string]bool
	nextRunUnix atomic.Int64
}

func main() {
	initDPIAwareness()
	initJobObject() // 子进程(挂载)随主程序退出而终止，防死链
	addr := flag.String("addr", "127.0.0.1:5399", "监听地址")
	runMode := flag.Bool("run", false, "无界面执行一次全部启用目录的同步后退出（供计划任务调用）")
	flag.Parse()
	base := mustExeDir()
	app := &App{base: base, confPath: filepath.Join(base, "config.json"), rcloneLog: filepath.Join(base, "rclone.log"), running: map[string]bool{}}

	// 单实例锁：已在运行则只打开面板、不启动第二个
	if !*runMode && !acquireSingleInstance(`Local\OpenListSync_SingleInstance`) {
		openPath("http://" + *addr + "/")
		return
	}

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
		app.hardenAll()
	} else {
		_ = app.saveConf()
	}
	app.ensureDirs()
	// 启动时按各目录 .nosy 刷新过滤器，保证磁盘上的 filter 与规则一致
	for _, j := range app.conf.Jobs {
		_ = app.filterFor(j)
	}

	if *runMode {
		app.runAllCLI()
		return
	}

	// 内置定时同步：软件自己按间隔跑，不依赖 Windows 计划任务
	if os.Getenv("OPENLIST_NO_SCHED") != "1" {
		app.startScheduler()
	}
	// 自动挂载：配置了 mountAuto 则启动时先测连通、再挂网盘为盘符（异步，不阻塞启动）。
	// 等待 10s+：给 OpenList 等外部 WebDAV 服务留出启动时间
	if os.Getenv("OPENLIST_NO_MOUNT") != "1" {
		go func() {
			time.Sleep(10 * time.Second)
			app.autoMountOnStart()
		}()
	}
	// 像软件一样：启动后自动打开面板页面
	if os.Getenv("OPENLIST_NO_BROWSER") != "1" {
		go func() {
			time.Sleep(700 * time.Millisecond)
			openPath("http://" + *addr + "/")
		}()
	}

	mux := app.routes()
	log.Printf("OpenList Sync 配置面板: http://%s/", *addr)
	log.Printf("工作目录: %s", base)

	// 无控制台模式：右下角托盘图标；没有托盘环境则退回普通服务
	if useTray && os.Getenv("OPENLIST_NO_TRAY") != "1" {
		if trayRun(app, *addr, mux) {
			return
		}
	}
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// routes 汇总所有 HTTP 路由
func (a *App) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/job", a.handleJob)
	mux.HandleFunc("/api/pickdirs", a.handlePickDirs)
	mux.HandleFunc("/api/prepare", a.handlePrepare)
	mux.HandleFunc("/api/local/list", a.handleLocalList)
	mux.HandleFunc("/api/remote/list", a.handleRemoteList)
	mux.HandleFunc("/api/nosy", a.handleNosy)
	mux.HandleFunc("/api/test", a.handleTest)
	mux.HandleFunc("/api/mount", a.handleMount)
	mux.HandleFunc("/api/pull", a.handlePull)
	mux.HandleFunc("/api/action", a.handleAction)
	mux.HandleFunc("/api/running", a.handleRunning)
	mux.HandleFunc("/api/log", a.handleLog)
	mux.HandleFunc("/api/open", a.handleOpen)
	return mux
}

// handleLocalList 浏览本机文件系统（供网页内"选择本地文件夹"用，替代系统对话框）
func (a *App) handleLocalList(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		// 列盘符
		var drives []map[string]string
		for _, d := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
			root := string(d) + ":\\"
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				drives = append(drives, map[string]string{"name": string(d) + ":", "path": root})
			}
		}
		writeJSON(w, map[string]any{"ok": true, "path": "", "parent": "", "dirs": drives})
		return
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": p})
		return
	}
	var dirs []map[string]string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") || strings.HasPrefix(n, "$") {
			continue
		}
		child := filepath.Join(p, n)
		dirs = append(dirs, map[string]string{"name": n, "path": child})
	}
	parent := filepath.Dir(strings.TrimRight(p, `\/`))
	if parent == p {
		parent = ""
	}
	writeJSON(w, map[string]any{"ok": true, "path": p, "parent": parent, "dirs": dirs})
}

// handleRemoteList 浏览 OpenList 网盘目录（供"选择文件夹"用）。
// 界面用"无前缀"路径（如 /ty2/电脑专用），内部自动补 remote: 前缀调 rclone。
func (a *App) handleRemoteList(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownRcloneConf(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	remote := orDefault(a.conf.Globals.Remote, "openlist")
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	// 归一化显示路径（无前缀）：根为 ""，其余以 "/" 开头
	display := raw
	display = strings.TrimPrefix(display, remote+":")
	if display != "" && !strings.HasPrefix(display, "/") {
		display = "/" + display
	}
	display = strings.TrimRight(display, "/")
	rclonePath := remote + ":" + display
	if display == "" {
		rclonePath = remote + ":"
	}

	out, err := a.rclone("lsjson", rclonePath, "--dirs-only", "--max-depth", "1")
	type Dir struct {
		Name    string `json:"name"`
		Path    string `json:"path"`    // 显示路径（无前缀）
		ModTime string `json:"modTime"` // 已格式化
	}
	var dirs []Dir
	if err == nil {
		var items []struct {
			Name    string `json:"Name"`
			ModTime string `json:"ModTime"`
			IsDir   bool   `json:"IsDir"`
		}
		if json.Unmarshal([]byte(out), &items) == nil {
			for _, it := range items {
				if !it.IsDir {
					continue
				}
				child := display + "/" + it.Name
				if display == "" {
					child = "/" + it.Name
				}
				mt := ""
				if t, e := time.Parse(time.RFC3339, it.ModTime); e == nil {
					mt = t.Local().Format("2006-01-02 15:04:05")
				}
				dirs = append(dirs, Dir{Name: it.Name, Path: child, ModTime: mt})
			}
		}
	}
	// 面包屑：首页(根) + 各级
	type Crumb struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	crumbs := []Crumb{{Name: "首页", Path: ""}}
	if display != "" {
		acc := ""
		for _, seg := range strings.Split(strings.Trim(display, "/"), "/") {
			if seg == "" {
				continue
			}
			acc += "/" + seg
			crumbs = append(crumbs, Crumb{Name: seg, Path: acc})
		}
	}
	sort.Slice(dirs, func(i, k int) bool { return dirs[i].Name < dirs[k].Name })
	writeJSON(w, map[string]any{"ok": err == nil, "displayPath": display, "remote": remote, "dirs": dirs, "crumb": crumbs, "error": errString(err)})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runAllCLI 供计划任务调用：串行跑所有启用任务，任一失败退出码=1
func (a *App) runAllCLI() {
	rc := 0
	n := 0
	for _, j := range a.conf.Jobs {
		if !j.Enabled {
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
	}
	fmt.Printf("\n[openlist-sync] 共执行 %d 个目录，退出码 %d\n", n, rc)
	os.Exit(rc)
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

// 为某任务构造 bisync 参数
func (a *App) bisyncArgs(j Job, mode, filterPath string) []string {
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

	cmd := exec.Command(a.rcloneBin(), a.bisyncArgs(j, mode, filterPath)...)
	hideWindow(cmd)
	procOut, err := cmd.CombinedOutput()

	body := strings.TrimSpace(string(procOut))
	header := fmt.Sprintf("【%s】%s ↔ %s\n", j.Name, j.LocalDir, j.RemoteDir)
	if err != nil {
		header += fmt.Sprintf("❌ %s 失败: %v\n", j.Name, err)
	} else {
		header += fmt.Sprintf("✅ %s 完成\n", j.Name)
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

// ---------- 处理器 ----------

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "index not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	autoOn := a.conf.Globals.AutoEnabled
	var nextRun string
	next := a.nextRunUnix.Load()
	if autoOn && next > 0 {
		nextRun = time.Unix(next, 0).Format(time.RFC3339)
	}
	writeJSON(w, map[string]any{
		"os":         runtime.GOOS,
		"scheduler":  autoOn, // 内置自动同步是否开启
		"interval":   a.conf.Globals.IntervalMin,
		"nextRun":    nextRun,
		"configPath": a.confPath,
		"rclonePath": a.rcloneBin(),
		"jobCount":   len(a.conf.Jobs),
	})
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c := a.conf
		if c.Globals.Pass != "" {
			c.Globals.Pass = "••••••••"
		}
		writeJSON(w, c)
	case http.MethodPost:
		var c Config
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			httpErr(w, 400, "配置格式错误: "+err.Error())
			return
		}
		if strings.Contains(c.Globals.Pass, "•") {
			c.Globals.Pass = a.conf.Globals.Pass
		}
		// 前端不维护这些内部字段，保存时保留旧值，避免被冲空
		if c.Globals.Remote == "" {
			c.Globals.Remote = a.conf.Globals.Remote
		}
		if strings.TrimSpace(c.Globals.RemoteFolderName) == "" {
			c.Globals.RemoteFolderName = a.conf.Globals.RemoteFolderName
		}
		a.conf.Globals = c.Globals
		// 保存设置后，已自动生成的目标目录随总目录一起刷新
		for i := range a.conf.Jobs {
			a.conf.Jobs[i] = a.prepJob(a.conf.Jobs[i])
		}
		if err := a.saveConf(); err != nil {
			httpErr(w, 500, "保存失败: "+err.Error())
			return
		}
		a.ensureDirs()
		writeJSON(w, map[string]any{"ok": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

// 增/改/删单个任务
func (a *App) handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Action string `json:"action"` // add|update|delete
		Job    Job    `json:"job"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	switch req.Action {
	case "add":
		j := a.tagAuto(req.Job)
		j = a.prepJob(j)
		j.ID = newID()
		a.conf.Jobs = append(a.conf.Jobs, j)
	case "update":
		idx := -1
		for i := range a.conf.Jobs {
			if a.conf.Jobs[i].ID == req.Job.ID {
				idx = i
			}
		}
		if idx < 0 {
			httpErr(w, 404, "任务不存在")
			return
		}
		req.Job = a.tagAuto(req.Job)
		req.Job = a.prepJob(req.Job)
		a.conf.Jobs[idx] = req.Job
	case "delete":
		nj := a.conf.Jobs[:0]
		for _, j := range a.conf.Jobs {
			if j.ID != req.Job.ID {
				nj = append(nj, j)
			}
		}
		a.conf.Jobs = nj
	default:
		httpErr(w, 400, "未知操作")
		return
	}
	if err := a.saveConf(); err != nil {
		httpErr(w, 500, "保存失败: "+err.Error())
		return
	}
	a.ensureDirs()
	writeJSON(w, map[string]any{"ok": true, "jobs": a.conf.Jobs})
}

// 原生多选文件夹对话框：一次选择多个本地文件夹
func (a *App) handlePickDirs(w http.ResponseWriter, r *http.Request) {
	paths, err := pickDirectories()
	if err != nil || len(paths) == 0 {
		writeJSON(w, map[string]any{"ok": false, "canceled": true})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "paths": paths})
}

// handlePrepare 在网盘上创建所选任务的目标目录与垃圾桶目录（选完本地文件夹后自动调用）
func (a *App) handlePrepare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if _, err := a.ownRcloneConf(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	created, failed := 0, 0
	for _, id := range req.IDs {
		j := a.job(id)
		if j == nil || strings.TrimSpace(j.RemoteDir) == "" {
			continue
		}
		if _, e := a.rclone("mkdir", j.RemoteDir); e == nil {
			created++
		} else {
			failed++
		}
		if j.RemoteTrashDir != "" {
			_, _ = a.rclone("mkdir", j.RemoteTrashDir)
		}
	}
	writeJSON(w, map[string]any{"ok": failed == 0, "created": created, "failed": failed})
}

// .nosy 过滤规则：GET 读取，POST 写入到该任务本地根目录下
func (a *App) handleNosy(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("jobId")
	j := a.job(id)
	if j == nil {
		httpErr(w, 404, "任务不存在")
		return
	}
	nosyPath := filepath.Join(j.LocalDir, ".nosy")
	switch r.Method {
	case http.MethodGet:
		b, err := os.ReadFile(nosyPath)
		if err != nil {
			writeJSON(w, map[string]any{"ok": true, "content": "", "exists": false, "path": nosyPath})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "content": string(b), "exists": true, "path": nosyPath})
	case http.MethodPost:
		var req struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = os.MkdirAll(j.LocalDir, 0o755)
		if strings.TrimSpace(req.Content) == "" {
			_ = os.Remove(nosyPath)
			_ = os.Remove(filepath.Join(a.base, "jobs", j.ID+".filter"))
			writeJSON(w, map[string]any{"ok": true, "removed": true})
			return
		}
		if err := os.WriteFile(nosyPath, []byte(req.Content), 0o644); err != nil {
			httpErr(w, 500, "写入失败: "+err.Error())
			return
		}
		_ = a.filterFor(*j) // 立即按新规则重新生成过滤器，避免滞后
		writeJSON(w, map[string]any{"ok": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	var out strings.Builder
	if _, err := a.ownRcloneConf(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	out.WriteString("== rclone 版本 ==\n")
	v, _ := a.rclone("version")
	out.WriteString(firstN(v, 2) + "\n\n")

	out.WriteString("== 连接测试：列出根目录 ==\n")
	lsd, err := a.rclone("lsd", orDefault(a.conf.Globals.Remote, "openlist")+":")
	if err != nil {
		out.WriteString(lsd + "\n连接失败，请检查地址/账号/密码与 WebDAV 是否开启。\n")
		writeJSON(w, map[string]any{"ok": false, "output": out.String()})
		return
	}
	out.WriteString(lsd + "\n")

	out.WriteString("== 哈希能力 ==\n")
	feat, _ := a.rclone("backend", "features", orDefault(a.conf.Globals.Remote, "openlist")+":")
	if strings.Contains(feat, "\"Hashes\": []") {
		out.WriteString("Hashes: []  → 不暴露哈希，--checksum 不可用，请用 size-only。\n")
	}

	out.WriteString("\n== 各目录状态 ==\n")
	for _, j := range a.conf.Jobs {
		nosyState := "无 .nosy（同步全部）"
		if _, e := os.Stat(filepath.Join(j.LocalDir, ".nosy")); e == nil {
			n := len(GitignoreToRclone(readOr("", filepath.Join(j.LocalDir, ".nosy"))))
			nosyState = fmt.Sprintf("有 .nosy（%d 条规则）", n)
		}
		out.WriteString(fmt.Sprintf("· %s\n  本地 %s  存在=%v\n  远端 %s  ", j.Name, j.LocalDir, dirExists(j.LocalDir), j.RemoteDir))
		if _, e := a.rclone("lsf", j.RemoteDir, "--max-depth", "1"); e != nil {
			out.WriteString("不存在（首次同步自动创建）\n")
		} else {
			out.WriteString("OK\n")
		}
		out.WriteString("  过滤：" + nosyState + "\n")
	}
	writeJSON(w, map[string]any{"ok": true, "output": out.String()})
}

func readOr(_ string, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Action string `json:"action"`
		JobID  string `json:"jobId"` // 空=全部启用任务
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	var targets []Job
	if req.JobID != "" {
		j := a.job(req.JobID)
		if j == nil {
			httpErr(w, 404, "任务不存在")
			return
		}
		targets = []Job{*j}
	} else {
		for _, j := range a.conf.Jobs {
			if j.Enabled {
				targets = append(targets, j)
			}
		}
	}
	if len(targets) == 0 {
		httpErr(w, 400, "没有启用的目录任务")
		return
	}

	// 后台启动，立即返回，前端轮询 /api/running 与日志看进度（大目录首次同步可能十几分钟）
	a.mu.Lock()
	for _, j := range targets {
		if a.running[j.ID] {
			a.mu.Unlock()
			httpErr(w, 409, j.Name+" 正在同步中，请等这次完成")
			return
		}
	}
	for _, j := range targets {
		a.running[j.ID] = true
	}
	a.mu.Unlock()

	action := req.Action
	go func(js []Job) {
		defer func() {
			a.mu.Lock()
			for _, j := range js {
				delete(a.running, j.ID)
			}
			a.mu.Unlock()
		}()
		for _, j := range js {
			jj := j
			switch action {
			case "resync":
				a.seedAndResync(jj)
			case "dry-run":
				a.runJob(jj, "dry-run")
			default: // sync：没基线则先建基线，否则常规双向
				if !a.hasListings(jj) {
					a.seedAndResync(jj)
				} else {
					a.runJob(jj, "sync")
				}
			}
		}
	}(targets)

	names := make([]string, 0, len(targets))
	for _, j := range targets {
		names = append(names, j.Name)
	}
	writeJSON(w, map[string]any{"ok": true, "started": names, "message": "已在后台开始：" + strings.Join(names, "、") + "，进度看下方日志"})
}

// 查询哪些任务正在运行
func (a *App) handleRunning(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	running := map[string]bool{}
	for k, v := range a.running {
		running[k] = v
	}
	writeJSON(w, map[string]any{"running": running})
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
	}
}

// startScheduler 软件内置定时器：启动即跑一次（若开启），之后按当前设置的间隔循环。
// 间隔与开关每轮实时读取，改了设置立即生效；关闭时不跑但继续等待开关打开。
func (a *App) startScheduler() {
	go func() {
		time.Sleep(6 * time.Second) // 等 HTTP 就绪
		if a.conf.Globals.AutoEnabled {
			a.runAll()
		}
		for {
			mins := a.conf.Globals.IntervalMin
			if mins < 1 {
				mins = 30
			}
			a.nextRunUnix.Store(time.Now().Add(time.Duration(mins) * time.Minute).Unix())
			time.Sleep(time.Duration(mins) * time.Minute)
			if a.conf.Globals.AutoEnabled {
				a.runAll()
			}
		}
	}()
}

func (a *App) handleTask(w http.ResponseWriter, r *http.Request) {
	// 已移除 Windows 计划任务：自动同步由程序内置计时负责，故此接口不再使用
	httpErr(w, 410, "计划任务已移除，自动同步由程序内置计时负责")
}

// ---------- 网盘挂载（rclone mount，需 WinFsp） ----------

type mountStateT struct {
	mu   sync.Mutex
	disk string // 当前挂载盘符，空=未挂载
	cmd  *exec.Cmd
}

var mountState = mountStateT{}

// winFspInstalled 检测 WinFsp 驱动（rclone mount 必需）
func winFspInstalled() bool {
	for _, p := range []string{
		os.Getenv("ProgramFiles") + `\WinFsp`,
		os.Getenv("ProgramFiles(x86)") + `\WinFsp`,
	} {
		if st, e := os.Stat(p); e == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// handleMount GET=状态 POST action=mount|unmount
func (a *App) handleMount(w http.ResponseWriter, r *http.Request) {
	mountState.mu.Lock()
	defer mountState.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"mounted":   mountState.disk != "",
			"disk":      mountState.disk,
			"winfsp":    winFspInstalled(),
			"hasConf":   fileExists(filepath.Join(a.base, "rclone.conf")),
			"autoMount": a.conf.Globals.MountAuto,
			"autoDisk":  orDefault(a.conf.Globals.MountAutoDisk, "Z"),
		})
		return
	}
	var req struct {
		Action string `json:"action"` // mount | unmount
		Disk   string `json:"disk"`   // 盘符，如 Z
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Action == "unmount" {
		a.doUnmount()
		// 只卸载，不改自动挂载开关（开关由面板挂载参数控制）
		writeJSON(w, map[string]any{"ok": true, "mounted": false})
		return
	}
	// mount
	if mountState.disk != "" {
		httpErr(w, 409, "已挂载到 "+mountState.disk+":，请先卸载")
		return
	}
	if !winFspInstalled() {
		httpErr(w, 400, "未安装 WinFsp 驱动：请先到 https://winfsp.dev/ 下载安装 WinFsp，然后重试")
		return
	}
	// 挂载前先测 WebDAV 连通性：连不通就不挂
	if _, err := a.ownRcloneConf(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if _, err := a.rclone("lsd", orDefault(a.conf.Globals.Remote, "openlist")+":"); err != nil {
		httpErr(w, 400, "WebDAV 连不上，不能挂载：请先检查地址/账号/密码并测试连接")
		return
	}
	disk := strings.ToUpper(strings.TrimSpace(strings.TrimRight(req.Disk, ":")))
	if disk == "" {
		disk = "Z"
	}
	if len(disk) != 1 || disk[0] < 'A' || disk[0] > 'Z' {
		httpErr(w, 400, "盘符无效: "+disk)
		return
	}
	// 挂载整个 WebDAV 为盘符根（用户要求：整个网盘挂进来，不是单个文件夹）
	remote := orDefault(a.conf.Globals.Remote, "openlist")
	target := remote + ":"
	// 盘符占用检查
	diskPath := disk + `:\`
	if _, e := os.Stat(diskPath); e == nil {
		httpErr(w, 409, disk+": 盘符已被占用，请换一个")
		return
	}
	confPath := filepath.Join(a.base, "rclone.conf")
	g := a.conf.Globals
	// 激进预取方案（实测：起播/拖动 0.1s 内，因为 chunk 预取把整段拉进本地缓存）
	bufferMB := g.MountBufferMB
	if bufferMB <= 0 {
		bufferMB = 256
	}
	dirCache := g.MountDirCacheSec
	if dirCache <= 0 {
		dirCache = 600
	}
	if g.MountPlayMode {
		if bufferMB < 64 {
			bufferMB = 256
		}
		if dirCache < 60 {
			dirCache = 600
		}
	}
	// full 缓存 + 大分块预取 → 起播/拖动进度条命中缓存（0.1s）；no-modtime 枚举提速数倍
	args := []string{"mount", target, disk + ":",
		"--config", confPath,
		"--vfs-cache-mode", "full",
		"--vfs-read-chunk-size", "32M",
		"--vfs-read-chunk-size-limit", "512M",
		"--buffer-size", strconv.Itoa(bufferMB) + "M",
		"--vfs-read-ahead", "512M",
		"--dir-cache-time", strconv.Itoa(dirCache) + "s",
		"--vfs-cache-poll-interval", "10s",
		"--no-modtime",
		"--links",
		"--transfers", "16",
		"--multi-thread-streams", "16",
		"--timeout", "5m",
		"--contimeout", "90s",
		"--network-mode",
		"--log-file", a.rcloneLog, "--log-level", "INFO",
	}
	// 缓存自动清理：rclone 原生机制——超过保留时长的文件块在下次 poll 时自动删除
	if s := g.MountCacheKeepSec; s > 0 {
		args = append(args, "--vfs-cache-max-age", strconv.Itoa(s)+"s")
	}
	if gb := g.MountCacheMaxGB; gb > 0 {
		args = append(args, "--vfs-cache-max-size", strconv.Itoa(gb)+"G")
	}
	if strings.TrimSpace(g.MountCacheDir) != "" {
		_ = os.MkdirAll(g.MountCacheDir, 0o755)
		args = append(args, "--cache-dir", g.MountCacheDir)
	}
	cmd := exec.Command(a.rcloneBin(), args...)
	// 独立进程组 + 隐藏窗口：便于退出时投递 CTRL_BREAK 让 rclone mount 安全卸载
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		httpErr(w, 500, "挂载启动失败: "+err.Error())
		return
	}
	mountState.cmd = cmd
	mountState.disk = disk
	// 等盘符出现（最多 20s）
	ok := false
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		if _, e := os.Stat(diskPath); e == nil {
			ok = true
			break
		}
	}
	if !ok {
		a.doUnmount()
		httpErr(w, 500, "挂载超时：盘符未出现（查看 rclone.log）")
		return
	}
	// 挂载成功：记住盘符并启用自动挂载（配置文件可再改）
	a.mu.Lock()
	a.conf.Globals.MountAuto = true
	a.conf.Globals.MountAutoDisk = disk
	a.mu.Unlock()
	_ = a.saveConf()
	writeJSON(w, map[string]any{"ok": true, "mounted": true, "disk": disk, "target": target})
}

func (a *App) doUnmount() {
	if mountState.cmd != nil && mountState.cmd.Process != nil {
		// rclone mount 收到优雅终止信号时会自动卸载 FUSE 盘符（Kill 会留下死链）
		sendGracefulTerminate(mountState.cmd.Process)
	}
	mountState.cmd = nil
	mountState.disk = ""
}

// autoMountOnStart 程序启动 10 秒后按配置自动挂载：
// 先测 WebDAV 连通（OpenList 可能还没起来），通过才挂；失败不重试（用户可在面板手动挂）
func (a *App) autoMountOnStart() {
	if !a.conf.Globals.MountAuto {
		return
	}
	mountState.mu.Lock()
	defer mountState.mu.Unlock()
	if mountState.disk != "" {
		return // 已挂载
	}
	if !winFspInstalled() {
		log.Println("自动挂载跳过：未安装 WinFsp")
		return
	}
	// 连通性测试
	if _, err := a.rclone("lsd", orDefault(a.conf.Globals.Remote, "openlist")+":"); err != nil {
		log.Println("自动挂载跳过：WebDAV 连不通（服务可能未启动）")
		return
	}
	disk := strings.ToUpper(strings.TrimSpace(a.conf.Globals.MountAutoDisk))
	if disk == "" {
		disk = "Z"
	}
	diskPath := disk + `:\`
	if _, e := os.Stat(diskPath); e == nil {
		log.Println("自动挂载跳过：盘符被占用 " + disk)
		return
	}
	remote := orDefault(a.conf.Globals.Remote, "openlist")
	confPath := filepath.Join(a.base, "rclone.conf")
	g := a.conf.Globals
	bufferMB := g.MountBufferMB
	if bufferMB <= 0 {
		bufferMB = 256
	}
	dirCache := g.MountDirCacheSec
	if dirCache <= 0 {
		dirCache = 600
	}
	if g.MountPlayMode {
		if bufferMB < 64 {
			bufferMB = 256
		}
		if dirCache < 60 {
			dirCache = 300
		}
	}
	args := []string{"mount", remote + ":", disk + ":",
		"--config", confPath,
		"--vfs-cache-mode", "full",
		"--vfs-read-chunk-size", "32M",
		"--vfs-read-chunk-size-limit", "512M",
		"--buffer-size", strconv.Itoa(bufferMB) + "M",
		"--vfs-read-ahead", "512M",
		"--dir-cache-time", strconv.Itoa(dirCache) + "s",
		"--vfs-cache-poll-interval", "10s",
		"--no-modtime", "--links",
		"--transfers", "16", "--multi-thread-streams", "16",
		"--timeout", "5m", "--contimeout", "90s",
		"--network-mode",
		"--log-file", a.rcloneLog, "--log-level", "INFO",
	}
	// 缓存自动清理（rclone 原生）：max-age 按时长清、max-size 按容量上限清（清最久未访问）
	if s := g.MountCacheKeepSec; s > 0 {
		args = append(args, "--vfs-cache-max-age", strconv.Itoa(s)+"s")
	}
	if gb := g.MountCacheMaxGB; gb > 0 {
		args = append(args, "--vfs-cache-max-size", strconv.Itoa(gb)+"G")
	}
	if strings.TrimSpace(g.MountCacheDir) != "" {
		_ = os.MkdirAll(g.MountCacheDir, 0o755)
		args = append(args, "--cache-dir", g.MountCacheDir)
	}
	cmd := exec.Command(a.rcloneBin(), args...)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		log.Println("自动挂载失败: " + err.Error())
		return
	}
	mountState.cmd = cmd
	mountState.disk = disk
	log.Println("自动挂载完成: " + disk + ": -> " + remote + ":")
}

// ---------- 从网盘拉取（显式 pull：远端新增/更新 -> 本地，不删本地） ----------

// handlePull POST {jobId}：对指定（或全部启用）任务执行 rclone copy 远端->本地（只增不删）
func (a *App) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		JobID string `json:"jobId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if _, err := a.ownRcloneConf(); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	var targets []Job
	if req.JobID != "" {
		j := a.job(req.JobID)
		if j == nil {
			httpErr(w, 404, "任务不存在")
			return
		}
		targets = []Job{*j}
	} else {
		for _, j := range a.conf.Jobs {
			if j.Enabled {
				targets = append(targets, j)
			}
		}
	}
	if len(targets) == 0 {
		httpErr(w, 400, "没有启用的目录任务")
		return
	}

	a.mu.Lock()
	for _, j := range targets {
		if a.running[j.ID] {
			a.mu.Unlock()
			httpErr(w, 409, j.Name+" 正在同步中，请稍候")
			return
		}
	}
	for _, j := range targets {
		a.running[j.ID] = true
	}
	a.mu.Unlock()

	go func(js []Job) {
		defer func() {
			a.mu.Lock()
			for _, j := range js {
				delete(a.running, j.ID)
			}
			a.mu.Unlock()
		}()
		for _, j := range js {
			jj := j
			a.pullJob(jj)
		}
	}(targets)

	names := []string{}
	for _, j := range targets {
		names = append(names, j.Name)
	}
	writeJSON(w, map[string]any{"ok": true, "message": "正在从网盘拉取：" + strings.Join(names, "、")})
}

// pullJob 远端 -> 本地单向增量（copy：只新增/更新，不删本地；跳过本地已有同尺寸文件）
func (a *App) pullJob(j Job) {
	if _, err := a.ownRcloneConf(); err != nil {
		return
	}
	_ = os.MkdirAll(a.stateDir(j), 0o755)
	_ = os.MkdirAll(filepath.Dir(a.jobLog(j)), 0o755)
	logPath := a.jobLog(j)
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "\n===== [%s] %s 从网盘拉取开始 =====\n", time.Now().Format("2006-01-02 15:04:05"), j.Name)
		f.Close()
	}
	args := []string{"copy", j.RemoteDir, j.LocalDir,
		"--config", filepath.Join(a.base, "rclone.conf"),
		"--size-only",
		"--transfers", strconv.Itoa(a.conf.Globals.Transfers),
		"--checkers", strconv.Itoa(a.conf.Globals.Checkers),
		"--retries", strconv.Itoa(a.conf.Globals.Retries),
		"--low-level-retries", strconv.Itoa(a.conf.Globals.LowLevelRetries),
		"--contimeout", orDefault(a.conf.Globals.Contimeout, "30s"),
		"--timeout", orDefault(a.conf.Globals.TimeoutStr, "5m"),
		"--log-file", logPath, "--log-level", "INFO",
	}
	if fp := a.filterFor(j); fp != "" {
		args = append(args, "--filter-from", fp)
	}
	cmd := exec.Command(a.rcloneBin(), args...)
	hideWindow(cmd)
	_, _ = cmd.CombinedOutput()
	if f, e := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); e == nil {
		fmt.Fprintf(f, "===== [%s] 从网盘拉取结束 =====\n", j.Name)
		f.Close()
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			lines = n
		}
	}
	id := r.URL.Query().Get("jobId")
	var path string
	if id != "" {
		if j := a.job(id); j != nil {
			path = a.jobLog(*j)
		}
	}
	if path == "" {
		// 总览：合并所有任务日志尾部
		var b strings.Builder
		for _, j := range a.conf.Jobs {
			b.WriteString("===== " + j.Name + " (" + j.ID + ") =====\n")
			if data, err := os.ReadFile(a.jobLog(j)); err == nil {
				all := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
				if len(all) > 40 {
					all = all[len(all)-40:]
				}
				b.WriteString(strings.Join(all, "\n") + "\n")
			} else {
				b.WriteString("（暂无日志，先跑一次）\n")
			}
			b.WriteString("\n")
		}
		writeJSON(w, map[string]any{"path": "(各任务合并)", "content": b.String()})
		return
	}
	b, err := os.ReadFile(path)
	content := ""
	if err == nil {
		all := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if len(all) > lines {
			all = all[len(all)-lines:]
		}
		content = strings.Join(all, "\n")
	} else if os.IsNotExist(err) {
		content = "（暂无同步日志，先跑一次同步）"
	} else {
		content = "读取日志失败: " + err.Error()
	}
	writeJSON(w, map[string]any{"path": path, "content": content})
}

func (a *App) handleOpen(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("path")
	if target == "" {
		httpErr(w, 400, "缺少 path")
		return
	}
	openPath(target)
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 小工具 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func firstN(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
