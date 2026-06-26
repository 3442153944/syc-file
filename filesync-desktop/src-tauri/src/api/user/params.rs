// api/user/params.rs
// 职责：用户模块所有请求参数结构体。
// 字段驼峰，serde rename_all snake_case 对齐后端 json tag。
use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize)]
pub struct LoginParams {
    pub username: String,
    pub password: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RegisterParams {
    pub username: String,
    pub password: String,
    pub email: Option<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ResetPasswordParams {
    pub username: String,
    pub new_password: String,
    pub old_password: String,
}

/// update-info 为 multipart，此结构仅供参考字段定义，实际调用需构造 Form
#[derive(Debug, Serialize, Deserialize)]
pub struct UpdateUserInfoParams {
    pub username: Option<String>,
    pub email: Option<String>,
    pub phone: Option<String>,
}
