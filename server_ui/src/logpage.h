#pragma once

#include "logline.h"

#include <QVector>
#include <QWidget>

class BackendClient;
struct BackendStatus;
class QButtonGroup;
class QCheckBox;
class QComboBox;
class QHBoxLayout;
class QLabel;
class QLineEdit;
class QPlainTextEdit;
class QPushButton;
class QTimer;

// LogPage 负责日志页：选择日志文件、级别/关键词过滤、尾部读取、实时跟随。
// 日志文件由后端按配置写入，本页只读文件（不经 localapi），所以后端离线也能看历史日志。
class LogPage : public QWidget {
    Q_OBJECT

public:
    explicit LogPage(BackendClient *client, QWidget *parent = nullptr);

    // setStatus 只用来更新「日志目录在哪」：在线时用 /status 的 log_dir，离线时按配置推算。
    void setStatus(const BackendStatus &status);

public slots:
    void reloadFiles();
    void reloadContent();
    void openLogDir();

private:
    void followTick();
    void appendLines(const QVector<LogLine> &lines);
    QString renderLine(const LogLine &line) const;
    bool matchLevel(const LogLine &line) const;
    bool matchKeyword(const LogLine &line) const;
    QString logDir() const;
    QString currentFile() const;
    void updateCountLabel();
    QPushButton *addLevelPill(QHBoxLayout *layout, const QString &text, int threshold);

    BackendClient *client_ = nullptr;
    QString logDirOverride_;

    QComboBox *fileCombo_ = nullptr;
    QComboBox *linesCombo_ = nullptr;
    QLineEdit *keywordEdit_ = nullptr;
    QCheckBox *followCheck_ = nullptr;
    QPlainTextEdit *view_ = nullptr;
    QLabel *countLabel_ = nullptr;
    QButtonGroup *levelGroup_ = nullptr;
    QTimer *followTimer_ = nullptr;

    qint64 followOffset_ = 0;
    QByteArray pendingBytes_;
    int levelThreshold_ = 0;
    int displayedLines_ = 0;
};