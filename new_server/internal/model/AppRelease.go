package model

import "time"

// AppRelease 应用发布版本（当前仅 Android）。
// APK 字节走既有分片上传落到允许盘目录，本表只登记版本语义元数据；
// 客户端 check 命中后用 file_path/file_name 复用 /v1/file/download 下载，并按 file_hash(blake3) 校验后安装。
type AppRelease struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Platform    string `gorm:"size:20;not null;index:idx_platform_ver,priority:1" json:"platform"` // android
	VersionCode int64  `gorm:"not null;index:idx_platform_ver,priority:2" json:"version_code"`
	VersionName string `gorm:"size:50;not null" json:"version_name"`
	Notes       string `gorm:"type:text" json:"notes"` // 更新说明
	// APK 在服务器上的绝对路径（须落在允许盘内）+ 文件名
	FilePath string `gorm:"size:1000;not null" json:"file_path"`
	FileName string `gorm:"size:255;not null" json:"file_name"`
	FileSize int64  `gorm:"not null;default:0" json:"file_size"`
	FileHash string `gorm:"size:128" json:"file_hash"` // blake3 hex，客户端安装前校验
	// 强制更新：为 true 时客户端弹不可关闭的更新框
	Mandatory bool `gorm:"not null;default:false" json:"mandatory"`
	// 低于该版本号必须更新（0 表示不启用），与 Mandatory 取或
	MinVersionCode int64 `gorm:"not null;default:0" json:"min_version_code"`
	// 是否上架：下架的版本 check 不返回
	Enabled    bool      `gorm:"not null;default:true;index" json:"enabled"`
	UploaderID uint      `gorm:"index" json:"uploader_id"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (AppRelease) TableName() string { return "app_release" }
