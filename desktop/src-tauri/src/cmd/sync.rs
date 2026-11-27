use std::fs;
use std::path::PathBuf;
use std::process::Command;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use serde::Serialize;
use serde_json::{json, Value};
use tauri::{AppHandle, Manager};

/// 同步引擎可执行文件（与主程序同目录，或由 OPENLIST_SYNC_ENGINE 指定）
#[cfg(windows)]
const ENGINE_EXE: &str = "openlistsync.exe";
#[cfg(not(windows))]
const ENGINE_EXE: &str = "openlistsync";

/// 默认指向**外部** OpenList（用户自己跑的，如 D:\OpenListBuilder-windows-amd64）。
/// 本 App 不启动、不管理任何 OpenList 核心，只做客户端。
const DEFAULT_WEBDAV_URL: &str = "http://127.0.0.1:5244/dav";

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SyncRunResult {
    pub code: i32,
    pub output: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SyncEngineInfo {
    pub path: String,
    pub found: bool,
    pub data_dir: String,
}

fn data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let base = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let dir = base.join("sync");
    fs::create_dir_all(&dir).map_err(|e| format!("创建同步数据目录失败: {e}"))?;
    Ok(dir)
}

fn config_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(data_dir(app)?.join("config.json"))
}

fn write_json(path: &PathBuf, value: &Value) -> Result<(), String> {
    let json = serde_json::to_string_pretty(value).map_err(|e| e.to_string())?;
    fs::write(path, json).map_err(|e| format!("写入 {} 失败: {e}", path.display()))
}

fn engine_path(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(p) = std::env::var("OPENLIST_SYNC_ENGINE") {
        let p = PathBuf::from(p);
        if p.is_file() {
            return Some(p);
        }
    }
    if let Ok(dir) = app.path().resource_dir() {
        let p = dir.join(ENGINE_EXE);
        if p.is_file() {
            return Some(p);
        }
        #[cfg(target_os = "windows")]
        for p in dir.read_dir().into_iter().flatten().flatten() {
            let name = p.file_name().to_string_lossy().to_string();
            if name.starts_with("openlistsync-") && name.ends_with(".exe") && p.path().is_file() {
                return Some(p.path());
            }
        }
        #[cfg(not(target_os = "windows"))]
        for p in dir.read_dir().into_iter().flatten().flatten() {
            let name = p.file_name().to_string_lossy().to_string();
            if name.starts_with("openlistsync-") && p.path().is_file() {
                return Some(p.path());
            }
        }
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let p = dir.join(ENGINE_EXE);
            if p.is_file() {
                return Some(p);
            }
            #[cfg(target_os = "windows")]
            for p in dir.read_dir().into_iter().flatten().flatten() {
                let name = p.file_name().to_string_lossy().to_string();
                if name.starts_with("openlistsync-") && name.ends_with(".exe") && p.path().is_file() {
                    return Some(p.path());
                }
            }
            #[cfg(not(target_os = "windows"))]
            for p in dir.read_dir().into_iter().flatten().flatten() {
                let name = p.file_name().to_string_lossy().to_string();
                if name.starts_with("openlistsync-") && p.path().is_file() {
                    return Some(p.path());
                }
            }
        }
    }
    None
}

/// rclone 优先用桌面端同目录自带的那个，避免依赖 PATH
fn rclone_path(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(p) = std::env::var("OPENLIST_RCLONE") {
        let p = PathBuf::from(p);
        if p.is_file() {
            return Some(p);
        }
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let name = if cfg!(windows) { "rclone.exe" } else { "rclone" };
            let p = dir.join(name);
            if p.is_file() {
                return Some(p);
            }
        }
    }
    if let Ok(dir) = app.path().resource_dir() {
        let name = if cfg!(windows) { "rclone.exe" } else { "rclone" };
        let p = dir.join(name);
        if p.is_file() {
            return Some(p);
        }
    }
    None
}

fn default_config(app: &AppHandle) -> Value {
    let rclone = rclone_path(app)
        .map(|p| p.to_string_lossy().to_string())
        .unwrap_or_else(|| "rclone".to_string());
    json!({
        "globals": {
            "webdavUrl": DEFAULT_WEBDAV_URL,
            "user": "admin",
            "pass": "",
            "intervalMin": 30,
            "transfers": 1,
            "checkers": 2,
            "retries": 10,
            "lowLevelRetries": 20,
            "contimeout": "30s",
            "timeout": "5m",
            "verifyOnSync": false,
            "mountRCAddr": "127.0.0.1:5572",
            "maxDelete": 50,
            "compare": "size",
            "rclonePath": rclone,
            "remote": "openlist",
            "remoteBase": "",
            "remoteFolderName": "agents-sy",
            "autoEnabled": false,
            "mountAuto": false
        },
        "jobs": []
    })
}

fn merge_missing(target: &mut Value, defaults: &Value) {
    if let (Some(t), Some(d)) = (target.as_object_mut(), defaults.as_object()) {
        for (k, v) in d {
            if !t.contains_key(k) {
                t.insert(k.clone(), v.clone());
            }
        }
    }
}

/// 引擎是否就位 + 数据目录，给界面显示
#[tauri::command]
pub fn sync_engine_info(app: AppHandle) -> SyncEngineInfo {
    let dir = data_dir(&app).map(|p| p.to_string_lossy().to_string()).unwrap_or_default();
    match engine_path(&app) {
        Some(p) => SyncEngineInfo {
            path: p.to_string_lossy().to_string(),
            found: true,
            data_dir: dir,
        },
        None => SyncEngineInfo {
            path: String::new(),
            found: false,
            data_dir: dir,
        },
    }
}

/// 读同步配置（不存在时按引擎默认值创建）
#[tauri::command]
pub fn sync_load_config(app: AppHandle) -> Result<Value, String> {
    let path = config_path(&app)?;
    let defaults = default_config(&app);
    if !path.exists() {
        write_json(&path, &defaults)?;
        return Ok(defaults);
    }
    let text = fs::read_to_string(&path).map_err(|e| format!("读取 config.json 失败: {e}"))?;
    let mut value: Value =
        serde_json::from_str(&text).map_err(|e| format!("config.json 格式错误: {e}"))?;
    if let Some(globals) = value.get_mut("globals") {
        merge_missing(globals, &defaults["globals"]);
    } else {
        value["globals"] = defaults["globals"].clone();
    }
    if !value.get("jobs").map(|j| j.is_array()).unwrap_or(false) {
        value["jobs"] = json!([]);
    }
    Ok(value)
}

/// 保存同步配置（整份写回，引擎与桌面端共用这一个文件）
#[tauri::command]
pub fn sync_save_config(app: AppHandle, config: Value) -> Result<bool, String> {
    let path = config_path(&app)?;
    write_json(&path, &config)?;
    Ok(true)
}

/// 跑一次同步：job_id 为空则跑所有启用目录
#[tauri::command]
pub async fn sync_run(app: AppHandle, job_id: Option<String>) -> Result<SyncRunResult, String> {
    run_engine(app, job_id).await
}

/// 仅在网盘创建各任务目标目录与垃圾桶目录（添加目录后立即调用）
#[tauri::command]
pub async fn sync_prepare(app: AppHandle) -> Result<SyncRunResult, String> {
    let dir = data_dir(&app)?;
    let engine = engine_path(&app)
        .ok_or_else(|| "找不到同步引擎 openlistsync.exe，请确认它与主程序在同一目录".to_string())?;

    tauri::async_runtime::spawn_blocking(move || {
        let mut cmd = Command::new(&engine);
        cmd.arg("-base").arg(&dir).arg("-prepare");
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }
        let out = cmd.output().map_err(|e| format!("启动同步引擎失败: {e}"))?;
        let mut text = String::from_utf8_lossy(&out.stdout).to_string();
        let err = String::from_utf8_lossy(&out.stderr);
        if !err.trim().is_empty() {
            text.push_str("\n[stderr]\n");
            text.push_str(&err);
        }
        Ok(SyncRunResult {
            code: out.status.code().unwrap_or(-1),
            output: text,
        })
    })
    .await
    .map_err(|e| e.to_string())?
}

/// 深度校验：下载比对内容，把大小相同但内容已改的文件以本地为准推到网盘
#[tauri::command]
pub async fn sync_verify(app: AppHandle, job_id: Option<String>) -> Result<SyncRunResult, String> {
    run_engine_with(app, job_id, Some("-verify")).await
}

async fn run_engine(app: AppHandle, job_id: Option<String>) -> Result<SyncRunResult, String> {
    run_engine_with(app, job_id, None).await
}

async fn run_engine_with(
    app: AppHandle,
    job_id: Option<String>,
    extra_flag: Option<&str>,
) -> Result<SyncRunResult, String> {
    let dir = data_dir(&app)?;
    let engine = engine_path(&app)
        .ok_or_else(|| "找不到同步引擎 openlistsync.exe，请确认它与主程序在同一目录".to_string())?;
    let extra = extra_flag.map(|s| s.to_string());

    tauri::async_runtime::spawn_blocking(move || {
        let mut cmd = Command::new(&engine);
        cmd.arg("-base").arg(&dir).arg("-run");
        if let Some(flag) = &extra {
            cmd.arg(flag);
        }
        if let Some(id) = job_id.as_ref().filter(|s| !s.trim().is_empty()) {
            cmd.arg("-job").arg(id);
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }
        let out = cmd.output().map_err(|e| format!("启动同步引擎失败: {e}"))?;
        let mut text = String::from_utf8_lossy(&out.stdout).to_string();
        let err = String::from_utf8_lossy(&out.stderr);
        if !err.trim().is_empty() {
            text.push_str("\n[stderr]\n");
            text.push_str(&err);
        }
        Ok(SyncRunResult {
            code: out.status.code().unwrap_or(-1),
            output: text,
        })
    })
    .await
    .map_err(|e| e.to_string())?
}

/// 读取某个目录的同步日志尾巴
#[tauri::command]
pub fn sync_tail_log(app: AppHandle, job_id: String, lines: Option<usize>) -> Result<String, String> {
    let path = data_dir(&app)?.join("logs").join(format!("{job_id}.log"));
    if !path.exists() {
        return Ok(String::new());
    }
    let text = fs::read_to_string(&path).map_err(|e| format!("读取日志失败: {e}"))?;
    let all: Vec<&str> = text.lines().collect();
    let take = lines.unwrap_or(200).max(1);
    let start = all.len().saturating_sub(take);
    Ok(all[start..].join("\n"))
}

static AUTO_SYNC_BUSY: AtomicBool = AtomicBool::new(false);

/// 定时自动同步：每 20 秒读一次配置，到点跑一遍所有启用目录。
pub fn spawn_auto_sync(app: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let mut next_run: Option<Instant> = None;
        loop {
            tokio::time::sleep(Duration::from_secs(20)).await;

            let cfg = match sync_load_config(app.clone()) {
                Ok(v) => v,
                Err(_) => continue,
            };
            let globals = &cfg["globals"];
            if !globals["autoEnabled"].as_bool().unwrap_or(false) {
                next_run = None;
                continue;
            }
            let minutes = globals["intervalMin"].as_u64().unwrap_or(30).clamp(1, 24 * 60);

            let now = Instant::now();
            match next_run {
                // 刚开启先等一个周期，避免打开就传
                None => {
                    next_run = Some(now + Duration::from_secs(minutes * 60));
                    continue;
                }
                Some(t) if now < t => continue,
                _ => {}
            }

            if AUTO_SYNC_BUSY.swap(true, Ordering::SeqCst) {
                continue;
            }
            let result = run_engine(app.clone(), None).await;
            AUTO_SYNC_BUSY.store(false, Ordering::SeqCst);
            next_run = Some(Instant::now() + Duration::from_secs(minutes * 60));
            match result {
                Ok(r) => log::info!("auto sync finished, exit code {}", r.code),
                Err(e) => log::warn!("auto sync failed: {e}"),
            }
        }
    });
}
