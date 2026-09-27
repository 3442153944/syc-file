// commands/user.rs
// 职责：用户域——登录/注册/找回密码/资料维护。
use crate::api::user::{api as user_api, params::*, response::*};
use crate::app_shell::quick_paste::register_quick_paste_shortcut;
use crate::commands::support::{api_data, ensure_ok, make_client};
use crate::config::SharedSyncConfig;
use crate::logger;
use tauri::State;

/// 登录：成功后把 token 写入 SyncConfig，顺带把账号里的粘贴快传设置（快捷键/默认有效期）
/// 同步进本地配置并立即注册快捷键——换设备/重装后登录一次就直接能用，不用再手动去
/// 设置页触发一次保存。
#[tauri::command]
pub async fn login(
    username: String,
    password: String,
    config: State<'_, SharedSyncConfig>,
    app: tauri::AppHandle,
) -> Result<LoginData, String> {
    let device_id = config.read().device_id.clone();
    let client = make_client(&config.read())?;
    let resp = user_api::login(&client, LoginParams { username, password, device_id }).await?;
    let data = api_data(resp, "login")?;
    config.write().token = data.token.clone();
    apply_quick_share_user_settings(&app, &config, &data.user);
    Ok(data)
}

/// 把前端持有的 token 交还给 Rust（仅当 Rust 侧还没有 token 时）。
///
/// 为什么需要：token 刻意不写进 config.yml（避免明文凭证），应用一重启 Rust 侧就是空的，
/// verify 必然失败 —— 于是即使 token 还在有效期内，桌面端每次启动都得重新登录，
/// 而 Web 端 token 有效就直接进去了。前端 localStorage 里本来就一直存着这个 token，
/// 启动时交还给 Rust 再走 verify，就和 Web 端行为一致：有效就进，无效就去登录页。
///
/// 只在 Rust 侧为空时才写入：已登录状态下不该被前端的值覆盖（比如刚切换了账号）。
#[tauri::command]
pub fn restore_token(token: String, config: State<SharedSyncConfig>) {
    let token = token.trim();
    if token.is_empty() {
        return;
    }
    let mut cfg = config.write();
    if cfg.token.is_empty() {
        cfg.token = token.to_string();
    }
}

/// 用当前 config 里的 token 验证登录态，同步把账号里的粘贴快传设置应用到本地。
#[tauri::command]
pub async fn verify(config: State<'_, SharedSyncConfig>, app: tauri::AppHandle) -> Result<VerifyData, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::verify(&client).await?;
    let data = api_data(resp, "verify")?;
    if let Some(u) = &data.user {
        apply_quick_share_user_settings(&app, &config, u);
    }
    Ok(data)
}

/// 把账号里配置的粘贴快传快捷键/默认有效期同步进本地 SyncConfig 并立即注册快捷键。
/// login/verify 成功后都要走一遍，保证换设备/重装后不用手动去设置页触发一次才生效。
fn apply_quick_share_user_settings(
    app: &tauri::AppHandle,
    config: &State<SharedSyncConfig>,
    user: &UserInfo,
) {
    if let Some(hotkey) = &user.quick_share_hotkey {
        if let Err(e) = register_quick_paste_shortcut(app, hotkey) {
            logger::warn("app", format!("登录后注册粘贴快传快捷键失败: {}", e));
        }
    }
    let mut cfg = config.write();
    if user.quick_share_hotkey.is_some() {
        cfg.quick_share_hotkey = user.quick_share_hotkey.clone();
    }
    if user.quick_share_expire_minutes.is_some() {
        cfg.quick_share_expire_minutes = user.quick_share_expire_minutes;
    }
    cfg.save();
}

#[tauri::command]
pub async fn register(
    username: String,
    password: String,
    email: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<serde_json::Value, String> {
    let client = make_client(&config.read())?;
    let resp = user_api::register(
        &client,
        RegisterParams {
            username,
            password,
            email,
        },
    )
    .await?;
    api_data(resp, "register")
}

#[tauri::command]
pub async fn reset_password(
    username: String,
    old_password: String,
    new_password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = user_api::reset_password(
        &client,
        ResetPasswordParams {
            username,
            old_password,
            new_password,
        },
    )
    .await?;
    ensure_ok(resp)
}

/// 修改密码（已登录场景，需旧密码验证）
#[tauri::command]
pub async fn change_password(
    old_password: String,
    new_password: String,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let resp = user_api::change_password(
        &client,
        ChangePasswordParams {
            old_password,
            new_password,
        },
    )
    .await?;
    ensure_ok(resp)
}

/// 更新用户资料（multipart，可选头像文件）
#[tauri::command]
pub async fn update_profile(
    username: Option<String>,
    email: Option<String>,
    phone: Option<String>,
    avatar_path: Option<String>,
    config: State<'_, SharedSyncConfig>,
) -> Result<(), String> {
    let client = make_client(&config.read())?;
    let mut form = reqwest::multipart::Form::new();
    if let Some(u) = username {
        form = form.text("username", u);
    }
    if let Some(e) = email {
        form = form.text("email", e);
    }
    if let Some(p) = phone {
        form = form.text("phone", p);
    }
    if let Some(path) = avatar_path {
        let p = std::path::Path::new(&path);
        let name = p
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("avatar")
            .to_string();
        let bytes = std::fs::read(p).map_err(|e| format!("读取头像文件失败: {}", e))?;
        let part = reqwest::multipart::Part::bytes(bytes).file_name(name);
        form = form.part("avatar", part);
    }
    let resp = user_api::update_info(&client, form).await?;
    ensure_ok(resp)
}
