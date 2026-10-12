package update

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"syc-file/config"
	"syc-file/internal/model"
)

// 桌面发布采用稳定 x.y.z，与安卓现有整数版本约定兼容。
func desktopVersionCode(version string) (int64, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, errors.New("版本名应为 x.y.z（如 0.1.1）")
	}
	var v [3]int64
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return 0, errors.New("版本名格式无效")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, errors.New("仅支持稳定版本 x.y.z")
			}
		}
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n < 0 || (i > 0 && n > 99) || n > 900000000000000 {
			return 0, errors.New("版本号超出范围（次版本和修订号应小于100）")
		}
		v[i] = n
	}
	return v[0]*10000 + v[1]*100 + v[2], nil
}

func validateTarget(platform, arch, bundle, version, signature, name string) (int64, error) {
	if platform == "android" {
		if arch != "" || bundle != "" {
			return 0, errors.New("Android 无需选择桌面架构或包类型")
		}
		return 0, nil // 保持安卓历史版本号/版本名行为。
	}
	if platform != "windows" && platform != "linux" {
		return 0, errors.New("不支持的平台")
	}
	if arch != "x86_64" && arch != "aarch64" && arch != "i686" {
		return 0, errors.New("请选择安装包的目标架构")
	}
	ext := map[string]string{"nsis": ".exe", "deb": ".deb", "appimage": ".appimage"}[bundle]
	if ext == "" || (platform == "windows" && bundle != "nsis") || (platform == "linux" && bundle == "nsis") {
		return 0, errors.New("平台与安装包类型不匹配")
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		return 0, errors.New("安装包扩展名与包类型不匹配")
	}
	if len(signature) > 16384 {
		return 0, errors.New("signature too large")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(decoded) < 64 {
		return 0, errors.New("请上传构建产物旁的 .sig 签名文件")
	}
	return desktopVersionCode(version)
}

func releaseQuery(db *gorm.DB, platform, arch, bundle string) *gorm.DB {
	q := db.Where("platform = ?", platform)
	if platform != "android" {
		q = q.Where("arch = ? AND bundle_type = ?", arch, bundle)
	}
	return q
}

// Tauri updater 使用原始 JSON / 204，不能包业务信封。沿用登录鉴权与设备绑定。
func (h *APIHandler) DesktopManifest(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	platform, arch, bundle := c.Query("platform"), c.Query("arch"), c.Query("bundle_type")
	if platform != "windows" && platform != "linux" {
		c.JSON(400, gin.H{"error": "不支持的平台"})
		return
	}
	current, err := desktopVersionCode(c.Query("version"))
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var rel model.AppRelease
	err = releaseQuery(h.db, platform, arch, bundle).Where("enabled = ? AND version_code > ?", true, current).Order("version_code desc").First(&rel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": "查询更新失败"})
		return
	}
	// 客户端始终把下载 URL 绑定到它当前配置的入口（含反代前缀/端口）。
	c.JSON(200, gin.H{
		"version": rel.VersionName, "notes": rel.Notes,
		"signature": rel.Signature, "pub_date": rel.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		"release_id": rel.ID,
		"mandatory":  rel.Mandatory || (rel.MinVersionCode > 0 && current < rel.MinVersionCode),
		"url":        fmt.Sprintf("https://%s/v1/update/package/%d", c.Request.Host, rel.ID),
	})
}

func (h *APIHandler) DownloadPackage(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	var rel model.AppRelease
	if err := h.db.Where("id = ? AND enabled = ?", c.Param("id"), true).First(&rel).Error; err != nil {
		c.Status(404)
		return
	}
	real, err := filepath.EvalSymlinks(rel.FilePath)
	if err != nil || !config.Conf.IsPathAllowed(real) {
		c.Status(404)
		return
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		c.Status(404)
		return
	}
	c.FileAttachment(real, rel.FileName)
}
