#include "logline.h"

#include <QDirIterator>
#include <QFile>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonObject>

#include <algorithm>

namespace logutil {

QString formatBytes(quint64 bytes)
{
    if (bytes == 0)
        return QStringLiteral("—");
    const double mb = static_cast<double>(bytes) / (1024.0 * 1024.0);
    if (mb >= 1024.0)
        return QStringLiteral("%1 GiB").arg(QString::number(mb / 1024.0, 'f', 1));
    return QStringLiteral("%1 MiB").arg(QString::number(mb, 'f', 1));
}

QString formatUptime(qint64 ns)
{
    if (ns <= 0)
        return QStringLiteral("—");
    const qint64 seconds = ns / 1000000000LL;
    const qint64 days = seconds / 86400;
    const qint64 hours = (seconds % 86400) / 3600;
    const qint64 minutes = (seconds % 3600) / 60;
    const qint64 secs = seconds % 60;
    if (days > 0)
        return QStringLiteral("%1 天 %2 小时 %3 分").arg(days).arg(hours).arg(minutes);
    if (hours > 0)
        return QStringLiteral("%1 小时 %2 分 %3 秒").arg(hours).arg(minutes).arg(secs);
    if (minutes > 0)
        return QStringLiteral("%1 分 %2 秒").arg(minutes).arg(secs);
    return QStringLiteral("%1 秒").arg(secs);
}

QString formatTime(const QDateTime &dt)
{
    if (!dt.isValid())
        return QStringLiteral("—");
    return dt.toLocalTime().toString(QStringLiteral("yyyy-MM-dd HH:mm:ss"));
}

QString formatLogTime(const QDateTime &dt)
{
    if (!dt.isValid())
        return QStringLiteral("—");
    return dt.toLocalTime().toString(QStringLiteral("MM-dd HH:mm:ss.zzz"));
}

QString levelColor(const QString &level)
{
    if (level.compare(QStringLiteral("error"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#c0392b");
    if (level.compare(QStringLiteral("warn"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#b7791f");
    if (level.compare(QStringLiteral("debug"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#6b7280");
    return QStringLiteral("#1f6feb");
}

int levelRank(const QString &level)
{
    if (level.compare(QStringLiteral("debug"), Qt::CaseInsensitive) == 0)
        return 1;
    if (level.compare(QStringLiteral("info"), Qt::CaseInsensitive) == 0)
        return 2;
    if (level.compare(QStringLiteral("warn"), Qt::CaseInsensitive) == 0)
        return 3;
    if (level.compare(QStringLiteral("error"), Qt::CaseInsensitive) == 0)
        return 4;
    return 2;
}

QString escapeHtml(const QString &text)
{
    QString out = text;
    out.replace(QLatin1Char('&'), QStringLiteral("&amp;"));
    out.replace(QLatin1Char('<'), QStringLiteral("&lt;"));
    out.replace(QLatin1Char('>'), QStringLiteral("&gt;"));
    return out;
}

// parseLine 解析 zap JSON 日志行；非 JSON（panic 多行等）时保留原文做消息。
LogLine parseLine(const QString &raw)
{
    LogLine line;
    line.raw = raw;
    const QJsonDocument doc = QJsonDocument::fromJson(raw.toUtf8());
    if (!doc.isObject()) {
        line.message = raw;
        return line;
    }
    const QJsonObject o = doc.object();
    line.parsed = true;
    // ts 可能是 ISO8601 字符串（zap 的 ISO8601TimeEncoder，如 2026-11-15T18:53:34.499+0800），
    // 也可能是秒级浮点数（zap 默认 epoch 编码）。两种都认。
    const QJsonValue tsValue = o.value(QStringLiteral("ts"));
    QDateTime stamp;
    if (tsValue.isString()) {
        QString raw = tsValue.toString();
        stamp = QDateTime::fromString(raw, Qt::ISODateWithMs);
        if (!stamp.isValid()) {
            // 偏移量可能是 +0800（没有冒号），Qt 的 ISO 解析要求 +08:00。
            const int size = raw.size();
            if (size >= 5) {
                const QChar sign = raw.at(size - 5);
                if ((sign == QLatin1Char('+') || sign == QLatin1Char('-')) && raw.at(size - 4).isDigit()
                    && raw.at(size - 3).isDigit() && raw.at(size - 2).isDigit()
                    && raw.at(size - 1).isDigit()) {
                    raw.insert(size - 2, QLatin1Char(':'));
                }
            }
            stamp = QDateTime::fromString(raw, Qt::ISODateWithMs);
            if (!stamp.isValid())
                stamp = QDateTime::fromString(raw, Qt::ISODate);
        }
    } else if (tsValue.isDouble()) {
        const double seconds = tsValue.toDouble();
        if (seconds > 0)
            stamp = QDateTime::fromMSecsSinceEpoch(static_cast<qint64>(seconds * 1000.0));
    }
    if (stamp.isValid())
        line.time = formatLogTime(stamp);
    line.level = o.value(QStringLiteral("level")).toString();
    line.caller = o.value(QStringLiteral("caller")).toString();
    line.message = o.value(QStringLiteral("msg")).toString();
    if (line.message.isEmpty())
        line.message = raw;
    return line;
}

// readTailLines 从文件尾部读最多 maxLines 行：先读尾部一段，不够再往前扩（最多 8MB）。
QStringList readTailLines(const QString &path, int maxLines)
{
    QFile f(path);
    if (!f.open(QIODevice::ReadOnly | QIODevice::Text))
        return {};
    const qint64 size = f.size();
    qint64 chunk = 256 * 1024;
    QStringList lines;
    while (true) {
        const qint64 start = qMax<qint64>(0, size - chunk);
        if (!f.seek(start))
            return lines;
        const QByteArray data = f.read(chunk);
        QString text = QString::fromUtf8(data);
        if (start > 0) {
            const int nl = text.indexOf(QLatin1Char('\n'));
            if (nl >= 0)
                text = text.mid(nl + 1);
        }
        lines = text.split(QLatin1Char('\n'), Qt::SkipEmptyParts);
        if (lines.size() >= maxLines || start == 0 || chunk >= 8 * 1024 * 1024)
            break;
        chunk *= 4;
    }
    while (lines.size() > maxLines)
        lines.removeFirst();
    return lines;
}

// listLogFiles 列出日志目录下的 *.log（含 supervisor 等子目录），按修改时间倒序。
QStringList listLogFiles(const QString &dir)
{
    QStringList files;
    if (dir.isEmpty())
        return files;
    QDirIterator it(dir, QStringList{QStringLiteral("*.log")}, QDir::Files,
                    QDirIterator::Subdirectories);
    while (it.hasNext())
        files << it.next();
    std::sort(files.begin(), files.end(), [](const QString &a, const QString &b) {
        return QFileInfo(a).lastModified() > QFileInfo(b).lastModified();
    });
    return files;
}

} // namespace logutil