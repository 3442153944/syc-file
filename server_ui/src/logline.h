#pragma once

#include <QDateTime>
#include <QString>
#include <QStringList>

// LogLine 是日志页解析后的一行（解析失败时 raw 保留原文）。
struct LogLine {
    QString raw;
    QString time;
    QString level;
    QString caller;
    QString message;
    bool parsed = false; // 是否成功解析成 zap JSON（纯文本行只显示原文）
};

// logutil 收拢「日志文件读取 / 解析 / 展示格式」这些纯函数，页面只负责摆放控件。
namespace logutil {

QString formatBytes(quint64 bytes);
QString formatUptime(qint64 ns);
QString formatTime(const QDateTime &dt);
QString formatLogTime(const QDateTime &dt);
QString levelColor(const QString &level);
int levelRank(const QString &level);
QString escapeHtml(const QString &text);
LogLine parseLine(const QString &raw);
QStringList readTailLines(const QString &path, int maxLines);
QStringList listLogFiles(const QString &dir);

} // namespace logutil