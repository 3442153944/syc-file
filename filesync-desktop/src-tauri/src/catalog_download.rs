// 手动下载的文件流处理，与窗口和对话框解耦，供原生命令和本机 HTTP 测试共用。
use crate::transfers;
use std::time::Duration;
use tokio::io::AsyncWriteExt;

pub(crate) async fn save_response(
    mut response: reqwest::Response,
    local_path: &str,
) -> Result<(), String> {
    if response.status() != reqwest::StatusCode::OK
        || !response
            .headers()
            .contains_key(reqwest::header::CONTENT_DISPOSITION)
    {
        return Err(format!(
            "下载失败（HTTP {}），请检查登录状态和文件权限",
            response.status().as_u16()
        ));
    }
    // 独占创建，不覆盖已有文件。失败时只删除本次创建的未完成文件。
    let mut file = tokio::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&local_path)
        .await
        .map_err(|e| {
            if e.kind() == std::io::ErrorKind::AlreadyExists {
                "保存位置已有同名文件，请选择其他名称".into()
            } else {
                format!("无法创建下载文件：{e}")
            }
        })?;
    transfers::download_progress(&local_path, "downloading", None, None);
    let result: Result<(), String> = async {
        let expected = response.content_length();
        let mut received = 0u64;
        loop {
            let chunk = tokio::time::timeout(Duration::from_secs(60), response.chunk())
                .await
                .map_err(|_| "下载超时，请重试".to_string())?
                .map_err(|e| e.without_url().to_string())?;
            let Some(chunk) = chunk else {
                break;
            };
            file.write_all(&chunk)
                .await
                .map_err(|e| format!("写入下载文件失败：{e}"))?;
            received += chunk.len() as u64;
        }
        if expected.is_some_and(|size| size != received) {
            return Err("下载内容不完整，请重试".into());
        }
        file.flush().await.map_err(|e| e.to_string())?;
        file.sync_all().await.map_err(|e| e.to_string())?;
        Ok(())
    }
    .await;
    drop(file);
    if let Err(error) = result {
        let error = match tokio::fs::remove_file(&local_path).await {
            Ok(()) => error,
            Err(e) => format!("{error}；未完成文件清理失败：{e}"),
        };
        transfers::download_progress(&local_path, "error", None, Some(error.clone()));
        return Err(error);
    }
    transfers::download_progress(&local_path, "done", None, None);
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::{Path, PathBuf};
    use tokio::io::AsyncReadExt;

    struct Output {
        dir: PathBuf,
    }
    impl Output {
        fn new() -> Self {
            let dir = std::env::temp_dir()
                .join(format!("filesync-download-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir(&dir).unwrap();
            Self { dir }
        }
        fn path(&self) -> String {
            self.dir.join("download.bin").to_string_lossy().into_owned()
        }
    }
    impl Drop for Output {
        fn drop(&mut self) {
            let _ = std::fs::remove_file(self.dir.join("download.bin"));
            let _ = std::fs::remove_dir(&self.dir);
        }
    }
    async fn response(raw: &'static str) -> reqwest::Response {
        let server = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = server.local_addr().unwrap();
        tokio::spawn(async move {
            let (mut socket, _) = server.accept().await.unwrap();
            let mut request = [0u8; 4096];
            socket.read(&mut request).await.unwrap();
            socket.write_all(raw.as_bytes()).await.unwrap();
            socket.shutdown().await.unwrap();
        });
        reqwest::Client::builder()
            .no_proxy()
            .build()
            .unwrap()
            .get(format!("http://{addr}/download"))
            .send()
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn saves_complete_file_and_refuses_overwrite() {
        let output = Output::new();
        let path = output.path();
        let raw = "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nContent-Disposition: attachment; filename=data.bin\r\nConnection: close\r\n\r\ndata";
        save_response(response(raw).await, &path).await.unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), b"data");
        assert!(save_response(response(raw).await, &path).await.is_err());
        assert_eq!(std::fs::read(&path).unwrap(), b"data");
    }

    #[tokio::test]
    async fn rejects_business_error_instead_of_saving_it_as_a_file() {
        let output = Output::new();
        let path = output.path();
        let raw = "HTTP/1.1 200 OK\r\nContent-Length: 12\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n{\"code\":401}";
        assert!(save_response(response(raw).await, &path).await.is_err());
        assert!(!Path::new(&path).exists());
    }

    #[tokio::test]
    async fn removes_incomplete_download_after_stream_failure() {
        let output = Output::new();
        let path = output.path();
        let raw = "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nContent-Disposition: attachment; filename=data.bin\r\nConnection: close\r\n\r\ndata";
        assert!(save_response(response(raw).await, &path).await.is_err());
        assert!(!Path::new(&path).exists());
    }
}
