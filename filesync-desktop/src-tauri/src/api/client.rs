// api/client.rs
// 职责：HTTP 客户端封装，提供 get/post/delete/multipart 方法。
// 对标 Android 的 Request.kt 单例：token 由调用方传入，不持有状态。
// 所有方法自动拼 /v1 前缀；路由常量来自 api::routes。

use crate::net;
use reqwest::{multipart, Client, RequestBuilder, Response};
use serde::{de::DeserializeOwned, Serialize};
use std::collections::HashMap;
use std::time::Duration;

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

/// HTTP 客户端，无状态，token 由调用方传入。
/// reqwest::Client 内部 Arc，clone 便宜；为方便跨 tokio::spawn 持有，derive Clone。
#[derive(Clone)]
pub struct ApiClient {
    client: Client,
    base_url: String,
    token: String,
    /// 构造这个 client 时的节点世代号。切节点会让它作废：在途请求立刻返回
    /// NODE_SWITCHED，不用干等 TCP 超时（见 net.rs 顶部注释）。
    epoch: u64,
}

/// 节点切换导致请求被主动放弃时的错误文案。调用方（尤其是重试逻辑）可以据此
/// 判断「这不是服务端的错，是我们自己换路了」，直接重来一次即可。
pub const NODE_SWITCHED: &str = "节点已切换，本次请求已取消";

impl ApiClient {
    pub fn new(server_url: &str, token: &str) -> Self {
        // Client::new() 没有任何超时：本地 localhost 请求快到永远暴露不出来，但换成远程/
        // 隧道地址（如 cf tunnel）一旦连接卡住半开，await 会永久挂起——没有 Err、UI 上也就
        // 看不到任何失败提示，进度停在原地不动（表现为"静默失败"）。这里补上超时，让这种情况
        // 至少能变成一个真实的 Err 冒泡上去。
        let client = Client::builder()
            .connect_timeout(Duration::from_secs(15))
            .timeout(Duration::from_secs(60))
            .build()
            .unwrap_or_else(|_| Client::new());
        ApiClient {
            client,
            base_url: format!("{}/v1", server_url.trim_end_matches('/')),
            token: token.to_string(),
            epoch: net::epoch(),
        }
    }

    /// 统一出口：所有请求都从这里走，好让「切节点」能一刀切断在途请求，
    /// 并把传输层失败上报给灾备模块去触发探测。
    async fn exec<T: DeserializeOwned>(&self, req: RequestBuilder) -> Result<ApiResponse<T>, String> {
        tokio::select! {
            biased;
            // 先看有没有切节点：切了就没必要再等这条注定打在旧地址上的请求
            _ = net::wait_switch(self.epoch) => Err(NODE_SWITCHED.to_string()),
            sent = req.send() => {
                if let Err(e) = &sent {
                    // 只有连不上/超时/TLS 这类传输层错误才值得怀疑节点；
                    // HTTP 5xx 说明链路是通的，换节点解决不了问题
                    if e.is_connect() || e.is_timeout() || e.is_request() {
                        net::report_transport_failure(&e.to_string());
                    }
                }
                send_and_parse(sent).await
            }
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
        self.exec(req).await
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
        self.exec(req).await
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
        self.exec(req).await
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
        self.exec(req).await
    }

    /// DELETE 请求
    pub async fn delete<T: DeserializeOwned>(&self, path: &str) -> Result<ApiResponse<T>, String> {
        let req = self
            .client
            .delete(self.url(path))
            .header("Token", &self.token);
        self.exec(req).await
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
        self.exec(req).await
    }

    /// POST 裸字节 body + query string（分片上传专用：upload_id/index 走 query，
    /// 分片字节走 raw body，Content-Type: application/octet-stream）。
    /// 服务端按业务码（200/422/404）区分成功/校验失败/会话过期，这里把整段 JSON 返回给调用方解析。
    pub async fn post_raw_bytes<T: DeserializeOwned>(
        &self,
        path: &str,
        params: &[(&str, &str)],
        body: Vec<u8>,
    ) -> Result<ApiResponse<T>, String> {
        let mut req = self
            .client
            .post(self.url(path))
            .header("Token", &self.token)
            .header("Content-Type", "application/octet-stream")
            .body(body);
        for (k, v) in params {
            req = req.query(&[(*k, *v)]);
        }
        self.exec(req).await
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
                format!(
                    "JSON 解析失败: {} (body: {})",
                    e,
                    &text[..text.len().min(200)]
                )
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
