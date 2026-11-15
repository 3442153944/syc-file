// server_ui_snapshot 是开发自检工具：把控制台窗口离屏渲染成 PNG，方便在没有人工点击的情况下
// 检查布局/配色/中文渲染。它不会出现在安装目录里，也不参与托盘常驻。
//
// 用法（Windows）：
//   server_ui_snapshot.exe --config <config.yaml> --page 0 --out shot.png [--width 1200 --height 780]
//
// 窗口用 Qt::WA_DontShowOnScreen + render() 生成，不会真的弹到屏幕上；等 2.2 秒
// （一次 /status 轮询 + 配置表单拉取）后再截图。
#include "backendclient.h"
#include "appinfo.h"
#include "configlocator.h"
#include "mainwindow.h"
#include "theme.h"

#include <QApplication>
#include <QCommandLineParser>
#include <QDir>
#include <QFileInfo>
#include <QTimer>

int main(int argc, char *argv[])
{
    QApplication app(argc, argv);
    theme::apply(app);
    app.setApplicationName(appinfo::displayName());
    app.setQuitOnLastWindowClosed(false);

    QCommandLineParser parser;
    parser.setApplicationDescription(QStringLiteral("离屏渲染「私有云服务器设置」页面为 PNG"));
    parser.addHelpOption();
    QCommandLineOption configOption(QStringLiteral("config"), QStringLiteral("config.yaml 路径"),
                                    QStringLiteral("path"));
    QCommandLineOption outOption(QStringLiteral("out"), QStringLiteral("输出 PNG 路径"),
                                 QStringLiteral("path"));
    QCommandLineOption pageOption(QStringLiteral("page"), QStringLiteral("页面：0 概览 / 1 配置 / 2 日志"),
                                  QStringLiteral("index"), QStringLiteral("0"));
    QCommandLineOption widthOption(QStringLiteral("width"), QStringLiteral("窗口宽度"), QStringLiteral("px"),
                                   QStringLiteral("1200"));
    QCommandLineOption heightOption(QStringLiteral("height"), QStringLiteral("窗口高度"), QStringLiteral("px"),
                                    QStringLiteral("780"));
    parser.addOptions({configOption, outOption, pageOption, widthOption, heightOption});
    parser.process(app);

    const QString configPath = configlocator::resolve(parser.value(configOption));

    QString outPath = parser.value(outOption);
    if (outPath.isEmpty())
        outPath = QDir(QCoreApplication::applicationDirPath()).absoluteFilePath(QStringLiteral("snapshot.png"));

    BackendClient client(configPath);
    MainWindow window(&client);
    window.setAttribute(Qt::WA_DontShowOnScreen, true);
    window.resize(parser.value(widthOption).toInt(), parser.value(heightOption).toInt());
    window.showPage(parser.value(pageOption).toInt());
    window.show();
    window.startPolling();

    QTimer::singleShot(2200, &window, [&window, outPath]() {
        // WA_DontShowOnScreen 下 grab() 不保证有内容，直接 render 到 pixmap 更可靠。
        QPixmap image(window.size());
        image.fill(Qt::white);
        window.render(&image);
        const bool ok = !image.isNull() && image.save(outPath);
        qInfo("snapshot %s: %s (%dx%d)", ok ? "saved" : "FAILED", qPrintable(outPath), image.width(),
              image.height());
        QCoreApplication::quit();
    });

    return app.exec();
}