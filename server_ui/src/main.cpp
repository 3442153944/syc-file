#include "backendclient.h"
#include "appinfo.h"
#include "icons.h"
#include "mainwindow.h"
#include "theme.h"

#include <QCryptographicHash>
#include <QApplication>
#include <QCommandLineParser>
#include <QDir>
#include <QFileInfo>
#include <QIcon>
#include <QLocalServer>
#include <QLocalSocket>

#ifdef Q_OS_WIN
#include <windows.h>
#endif

namespace {

// instanceNameFor 与后端 desktoplaunch 的单实例约定一致：
// 同一份 config.yaml 只允许一个控制台窗口。
// 先把路径规范化（统一成 '/'、Windows 下转小写），否则 Go 传来的 `\` 路径和用户手敲的 `/` 路径
// 会算出两个名字，单实例失效、后端重启会多开窗口。
QString instanceNameFor(const QString &configPath)
{
    QString canonical = QDir::cleanPath(QFileInfo(configPath).absoluteFilePath());
#ifdef Q_OS_WIN
    canonical.replace(QLatin1Char('\\'), QLatin1Char('/'));
    canonical = canonical.toLower();
#endif
    const QByteArray hash = QCryptographicHash::hash(canonical.toUtf8(), QCryptographicHash::Sha1).toHex();
    return QStringLiteral("syc-file-gui-") + QString::fromLatin1(hash.left(16));
}

// claimSingleInstance 返回 true = 本进程是唯一实例；false = 已有实例（调用方直接退出）。
// Windows 用命名互斥体做权威判定：QLocalServer 的命名管道允许多实例，
// 单靠 listen() 失败在实际环境里不可靠（实测会起出第二个窗口）。
bool claimSingleInstance(const QString &name)
{
#ifdef Q_OS_WIN
    const QString mutexName = QStringLiteral("Local\\") + name;
    HANDLE mutex = CreateMutexW(nullptr, FALSE, reinterpret_cast<LPCWSTR>(mutexName.utf16()));
    const bool alreadyRunning = mutex != nullptr && GetLastError() == ERROR_ALREADY_EXISTS;
    // 句柄故意不关闭：进程存活期间一直持有，退出时由系统释放。
    return !alreadyRunning;
#else
    Q_UNUSED(name);
    return true; // Unix 由 QLocalServer::listen 的地址占用判定（见 main）
#endif
}

// notifyExistingInstance 让已运行的控制台把窗口显示出来（手动双击/开始菜单时用）。
void notifyExistingInstance(const QString &name)
{
    QLocalSocket socket;
    socket.connectToServer(name);
    if (!socket.waitForConnected(400))
        return;
    socket.write("show\n");
    socket.flush();
    socket.waitForBytesWritten(300);
}

} // namespace

int main(int argc, char *argv[])
{
    QApplication app(argc, argv);
    theme::apply(app);

    app.setApplicationName(appinfo::displayName());
    app.setApplicationDisplayName(appinfo::displayName());
    app.setOrganizationName(appinfo::vendorName());
    app.setWindowIcon(QIcon(icons::appIcon(256)));
    app.setQuitOnLastWindowClosed(false); // 关窗只隐藏到托盘，由托盘菜单决定退出

    QCommandLineParser parser;
    parser.setApplicationDescription(
        QStringLiteral("私有云服务器设置：查看后端状态、编辑配置、过滤日志、启停后端，常驻系统托盘。"));
    parser.addHelpOption();
    QCommandLineOption configOption(QStringLiteral("config"),
                                    QStringLiteral("指定 config.yaml 路径（默认：程序目录/config/config.yaml）"),
                                    QStringLiteral("path"));
    QCommandLineOption autoStartOption(
        QStringLiteral("auto-start"),
        QStringLiteral("由后端自动拉起：已有实例时静默退出，不弹窗（后端 desktoplaunch 使用）"));
    parser.addOptions({configOption, autoStartOption});
    parser.process(app);

    const bool autoStart = parser.isSet(autoStartOption);
    QString configPath = parser.value(configOption);
    if (configPath.isEmpty())
        configPath = QDir(QCoreApplication::applicationDirPath())
                         .absoluteFilePath(QStringLiteral("config/config.yaml"));
    configPath = QFileInfo(configPath).absoluteFilePath();

    // ---- 单实例：同一份配置只保留一个控制台 ----
    // 后端启动时也会拉起控制台；如果用户已经开着，后起的实例把请求转给已有窗口（手动启动时）
    // 或直接退出（--auto-start 时，避免后端每次重启都把窗口弹出来）。
    const QString instanceName = instanceNameFor(configPath);

#ifdef Q_OS_WIN
    if (!claimSingleInstance(instanceName)) {
        if (!autoStart)
            notifyExistingInstance(instanceName);
        return 0;
    }
    QLocalServer instanceServer;
    if (!instanceServer.listen(instanceName))
        qWarning("single-instance notify pipe failed: %s", qPrintable(instanceServer.errorString()));
#else
    QLocalServer instanceServer;
    if (!instanceServer.listen(instanceName)) {
        if (!autoStart)
            notifyExistingInstance(instanceName);
        return 0;
    }
#endif

    BackendClient client(configPath);
    MainWindow window(&client);

    QObject::connect(&instanceServer, &QLocalServer::newConnection, &window, [&instanceServer, &window]() {
        while (QLocalSocket *socket = instanceServer.nextPendingConnection()) {
            QObject::connect(socket, &QLocalSocket::readyRead, socket, [socket, &window]() {
                const QByteArray command = socket->readAll();
                if (command.contains("show"))
                    window.showWindow();
                socket->disconnectFromServer();
            });
            QObject::connect(socket, &QLocalSocket::disconnected, socket, &QLocalSocket::deleteLater);
        }
    });

    // 控制台单独启动不代表后端启动：这里只创建/显示窗口并开始轮询，不会去拉起后端。
    if (!client.startMinimized())
        window.show();
    window.startPolling();

    return app.exec();
}