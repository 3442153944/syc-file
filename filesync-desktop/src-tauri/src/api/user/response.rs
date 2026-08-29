// api/user/response.rs
// 职责：用户模块所有响应数据结构体。
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UserInfo {
    pub id: u64,
    pub username: String,
    pub email: Option<String>,
    pub phone: Option<String>,
    pub role: Option<String>,
    pub avatar: Option<String>,
    pub created_at: Option<String>,
    #[serde(default)]
    pub quick_share_hotkey: Option<String>,
    #[serde(default)]
    pub quick_share_expire_minutes: Option<i32>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct LoginData {
    pub token: String,
    pub user: UserInfo,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct VerifyData {
    pub token: Option<String>,
    pub user: Option<UserInfo>,
}
