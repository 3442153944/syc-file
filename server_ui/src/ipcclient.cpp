#include "ipcclient.h"

#include <QLocalSocket>
#include <QTimer>

#include <memory>

namespace {

// parseHttpResponse 解析「状态行 + 头 + 空行 + body」；连接在响应后关闭，body 读到 EOF 即可。
bool parseHttpResponse(const QByteArray &raw, int *status, QByteArray *body, QString *error)
{
    const int headerEnd = raw.indexOf("\r\n\r\n");
    if (headerEnd < 0) {
        *error = QStringLiteral("IPC 响应不完整（缺少响应头结束标记）");
        return false;
    }
    const QByteArray head = raw.left(headerEnd);
    QList<QByteArray> lines = head.split('\n');
    if (lines.isEmpty()) {
        *error = QStringLiteral("IPC 响应为空");
        return false;
    }
    // 状态行：HTTP/1.0 200 OK
    const QList<QByteArray> parts = lines.first().trimmed().split(' ');
    if (parts.size() < 2) {
        *error = QStringLiteral("IPC 响应状态行格式不对");
        return false;
    }
    bool ok = false;
    const int code = parts.at(1).toInt(&ok);
    if (!ok || code <= 0) {
        *error = QStringLiteral("IPC 响应状态码解析失败");
        return false;
    }
    *status = code;

    int contentLength = -1;
    for (int i = 1; i < lines.size(); ++i) {
        const QByteArray line = lines.at(i).trimmed();
        const int colon = line.indexOf(':');
        if (colon <= 0)
            continue;
        const QByteArray name = line.left(colon).trimmed().toLower();
        const QByteArray value = line.mid(colon + 1).trimmed();
        if (name == "content-length") {
            bool lengthOk = false;
            contentLength = value.toInt(&lengthOk);
            if (!lengthOk)
                contentLength = -1;
        } else if (name == "transfer-encoding" && value.contains("chunked")) {
            // 正常情况下 HTTP/1.0 请求不会触发 chunked；真遇到就明确报错，避免把分块字节当 JSON。
            *error = QStringLiteral("IPC 响应使用了分块编码，协议不匹配");
            return false;
        }
    }
    QByteArray payload = raw.mid(headerEnd + 4);
    if (contentLength >= 0 && payload.size() > contentLength)
        payload.truncate(contentLength);
    *body = payload;
    return true;
}

struct RequestState {
    QByteArray data;
    bool connected = false;
    bool finished = false;
};

} // namespace

IpcHttpClient::IpcHttpClient(QObject *parent)
    : QObject(parent)
{
}

void IpcHttpClient::setEndpoint(const QString &endpoint, const QString &token)
{
    endpoint_ = endpoint;
    token_ = token;
}

QByteArray IpcHttpClient::buildRequest(const QByteArray &method, const QString &path, const QByteArray &body,
                                       const QByteArray &contentType) const
{
    QByteArray request;
    request += method;
    request += ' ';
    request += path.toUtf8();
    request += " HTTP/1.0\r\n";
    request += "Host: syc-file-local\r\n";
    request += "X-Syc-Local-Token: ";
    request += token_.toUtf8();
    request += "\r\nConnection: close\r\n";
    if (method == "POST") {
        request += "Content-Type: ";
        request += contentType.isEmpty() ? QByteArray("text/plain; charset=utf-8") : contentType;
        request += "\r\nContent-Length: ";
        request += QByteArray::number(body.size());
        request += "\r\n";
    }
    request += "\r\n";
    request += body;
    return request;
}

void IpcHttpClient::get(const QString &path, Handler handler, int timeoutMs)
{
    send(buildRequest("GET", path, QByteArray(), QByteArray()), std::move(handler), timeoutMs);
}

void IpcHttpClient::post(const QString &path, const QByteArray &body, const QByteArray &contentType,
                         Handler handler, int timeoutMs)
{
    send(buildRequest("POST", path, body, contentType), std::move(handler), timeoutMs);
}

void IpcHttpClient::send(const QByteArray &request, Handler handler, int timeoutMs)
{
    auto *socket = new QLocalSocket(this);
    auto *timer = new QTimer(socket);
    timer->setSingleShot(true);
    auto state = std::make_shared<RequestState>();

    auto finish = [socket, timer, handler = std::move(handler), state](bool ok, int status,
                                                                      const QByteArray &body,
                                                                      const QString &error) {
        if (state->finished)
            return;
        state->finished = true;
        timer->stop();
        socket->abort();
        socket->deleteLater();
        if (handler)
            handler(ok, status, body, error);
    };

    QObject::connect(socket, &QLocalSocket::connected, socket, [socket, request, state]() {
        state->connected = true;
        socket->write(request);
        socket->flush();
    });
    QObject::connect(socket, &QLocalSocket::readyRead, socket, [socket, state]() {
        state->data += socket->readAll();
    });
    QObject::connect(socket, &QLocalSocket::disconnected, socket, [state, finish]() {
        int status = 0;
        QByteArray body;
        QString error;
        if (!parseHttpResponse(state->data, &status, &body, &error)) {
            finish(false, 0, QByteArray(), error);
            return;
        }
        finish(true, status, body, QString());
    });
    QObject::connect(socket, &QLocalSocket::errorOccurred, socket,
                     [socket, state, finish](QLocalSocket::LocalSocketError) {
                         // 连接阶段失败时不会再发 disconnected，这里收尾。
                         if (!state->connected)
                             finish(false, 0, QByteArray(),
                                    QStringLiteral("连接本地 IPC 失败：%1").arg(socket->errorString()));
                     });
    QObject::connect(timer, &QTimer::timeout, socket, [finish]() {
        finish(false, 0, QByteArray(), QStringLiteral("本地 IPC 请求超时"));
    });

    if (endpoint_.isEmpty()) {
        finish(false, 0, QByteArray(), QStringLiteral("后端没有提供 IPC 端点（runtime.json 缺失）"));
        return;
    }

    timer->start(timeoutMs);
    socket->connectToServer(endpoint_);
}

bool IpcHttpClient::probe(const QString &endpoint, int timeoutMs)
{
    if (endpoint.isEmpty())
        return false;
    QLocalSocket socket;
    socket.connectToServer(endpoint);
    if (!socket.waitForConnected(timeoutMs))
        return false;
    socket.disconnectFromServer();
    return true;
}