// api/client.rs
// 职责：HTTP 客户端封装，提供 get/post/delete/multipart 方法。
// 对标 Android 的 Request.kt 单例：token 由调用方传入，不持有状态。
// 所有方法自动拼 /v1 前缀；路由常量来自 api::routes。

use reqwest::{multipart, Client, Response};
use serde::{de::DeserializeOwned, Serialize};
use std::collections::HashMap;

/// 统一响应信封，对应后端 {code, message, data}
#[derive(Debug, serde::Deserialize)]
pub struct ApiResponse<T> {
    pub code: i32,
    pub message: String,
    pub data: Option<T>,
}

impl<T> ApiResponse<T> {
    pub fn is_ok(&self) -> bool {
        self.code == 200
    }
}

/// HTTP 客户端，无状态，token 由调用方传入
pub struct ApiClient {
    client: Client,
    base_url: String,
    token: String,
}

impl ApiClient {
    pub fn new(server_url: &str, token: &str) -> Self {
        ApiClient {
            client: Client::new(),
            base_url: format!("{}/v1", server_url.trim_end_matches('/')),
            token: token.to_string(),
        }
    }

    fn url(&self, path: &str) -> String {
        format!("{}{}", self.base_url, path)
    }

    /// GET 请求，params 为可选 query string map
    pub async fn get<T: DeserializeOwned>(
        &self,
        path: &str,
        params: Option<&HashMap<&str, String>>,
    ) -> Result<ApiResponse<T>, String> {
        let mut req = self.client.get(self.url(path)).header("Token", &self.token);
        if let Some(p) = params {
            req = req.query(p);
        }
        send_and_parse(req.send().await).await
    }

    /// POST JSON 请求
    pub async fn post<T: DeserializeOwned, B: Serialize>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .post(self.url(path))
            .header("Token", &self.token)
            .json(body);
        send_and_parse(req.send().await).await
    }

    /// POST 无 body
    pub async fn post_empty<T: DeserializeOwned>(
        &self,
        path: &str,
    ) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .post(self.url(path))
            .header("Token", &self.token);
        send_and_parse(req.send().await).await
    }

    /// PUT JSON 请求
    pub async fn put<T: DeserializeOwned, B: Serialize>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .put(self.url(path))
            .header("Token", &self.token)
            .json(body);
        send_and_parse(req.send().await).await
    }

    /// DELETE 请求
    pub async fn delete<T: DeserializeOwned>(
        &self,
        path: &str,
    ) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .delete(self.url(path))
            .header("Token", &self.token);
        send_and_parse(req.send().await).await
    }

    /// POST multipart 请求（文件上传专用）
    pub async fn post_multipart<T: DeserializeOwned>(
        &self,
        path: &str,
        form: multipart::Form,
    ) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .post(self.url(path))
            .header("Token", &self.token)
            .multipart(form);
        send_and_parse(req.send().await).await
    }

    /// 构建带 token 的完整 GET URL（用于下载、WS 等 token 需放 query string 的场景）
    pub fn build_url_with_token(&self, path: &str, mut params: HashMap<&str, String>) -> String {
        params.insert("token", self.token.clone());
        let query: String = params
            .iter()
            .map(|(k, v)| format!("{}={}", k, urlenc(v)))
            .collect::<Vec<_>>()
            .join("&");
        format!("{}{}?{}", self.base_url, path, query)
    }
}

async fn send_and_parse<T: DeserializeOwned>(
    result: Result<Response, reqwest::Error>,
) -> Result<ApiResponse<T>, String> {
    match result {
        Ok(resp) => {
            let status = resp.status();
            let text = resp.text().await.map_err(|e| e.to_string())?;
            if !status.is_success() && status.as_u16() != 200 {
                return Err(format!("HTTP {}: {}", status, text));
            }
            serde_json::from_str::<ApiResponse<T>>(&text).map_err(|e| {
                format!("JSON 解析失败: {} (body: {})", e, &text[..text.len().min(200)])
            })
        }
        Err(e) => Err(e.to_string()),
    }
}

fn urlenc(s: &str) -> String {
    s.chars()
        .flat_map(|c| {
            if c.is_alphanumeric() || matches!(c, '-' | '_' | '.' | '~') {
                vec![c]
            } else {
                format!("%{:02X}", c as u32).chars().collect()
            }
        })
        .collect()
}
