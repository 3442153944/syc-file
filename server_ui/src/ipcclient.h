#pragma once

#include <QByteArray>
#include <QObject>
#include <QString>

#include <functional>

// IpcHttpClient 在「本机 IPC」上发 HTTP 请求：
//   - Windows：命名管道（QLocalSocket 连接 syc-file-localapi-xxxx）；
//   - Linux/macOS：Unix domain socket 路径。
//
// 不经过任何 IP 协议栈，代理的 TUN/透明接管无法劫持；也不占端口、不暴露给外部网络。
// 后端 Go 的 http.Server 直接跑在这条字节流上：请求用 HTTP/1.0 + Connection: close，
// 服务端会关闭连接且不使用 chunked，客户端读到 EOF 即完整响应。
class IpcHttpClient : public QObject {
    Q_OBJECT

public:
    // Handler(ok, status, body, error)：ok=false 时 status 可能为 0（连接/超时错误）。
    using Handler = std::function<void(bool ok, int status, const QByteArray &body, const QString &error)>;

    explicit IpcHttpClient(QObject *parent = nullptr);

    void setEndpoint(const QString &endpoint, const QString &token);
    QString endpoint() const { return endpoint_; }
    bool hasEndpoint() const { return !endpoint_.isEmpty(); }

    void get(const QString &path, Handler handler, int timeoutMs = 4000);
    void post(const QString &path, const QByteArray &body, const QByteArray &contentType, Handler handler,
              int timeoutMs = 4000);

    // probe 同步探测端点是否可连接（只用于「后端是否还在跑」这类短判断，最多阻塞 timeoutMs）。
    static bool probe(const QString &endpoint, int timeoutMs = 250);

private:
    void send(const QByteArray &request, Handler handler, int timeoutMs);
    QByteArray buildRequest(const QByteArray &method, const QString &path, const QByteArray &body,
                            const QByteArray &contentType) const;

    QString endpoint_;
    QString token_;
};