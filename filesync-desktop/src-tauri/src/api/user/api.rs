// api/user/api.rs
// 职责：用户相关 API 调用封装。
// 每个函数只做：组装参数 + 调用 ApiClient，不包含业务逻辑。
use super::{params::*, response::*};
use crate::api::{client::{ApiClient, ApiResponse}, routes};

/// 用户登录
pub async fn login(client: &ApiClient, params: LoginParams) -> Result<ApiResponse<LoginData>, String> {
    client.post(routes::USER_LOGIN, &params).await
}

/// 校验 Token 有效性
pub async fn verify(client: &ApiClient) -> Result<ApiResponse<VerifyData>, String> {
    client.post_empty(routes::USER_VERIFY).await
}

/// 用户注册
pub async fn register(client: &ApiClient, params: RegisterParams) -> Result<ApiResponse<serde_json::Value>, String> {
    // 注：后端 register 返回 {message, user} 无 code，暂用 Value 接收
    client.post(routes::USER_REGISTER, &params).await
}

/// 重置密码
pub async fn reset_password(client: &ApiClient, params: ResetPasswordParams) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post(routes::USER_RESET_PASSWORD, &params).await
}

/// 更新用户信息（multipart，调用方需传入已构造好的 Form）
pub async fn update_info(client: &ApiClient, form: reqwest::multipart::Form) -> Result<ApiResponse<serde_json::Value>, String> {
    client.post_multipart(routes::USER_UPDATE_INFO, form).await
}
