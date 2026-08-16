package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/ws"
	"syc-file/pkg/token"
)

// WSEventAppUpdate 发布新版本时通过 WS 广播给在线设备的消息 type。
const WSEventAppUpdate = "app_update"

type APIHandler struct {
	db *gorm.DB
}

func NewAPIHandler(db *gorm.DB) *APIHandler {
	return &APIHandler{db: db}
}

// Check 客户端检查更新。任意登录用户可调。
// query: platform(默认 android) + version_code(当前已装版本号)
// 返回 {has_update, mandatory, release}
func (h *APIHandler) Check(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	platform := c.DefaultQuery("platform", "android")
	versionCode, _ := strconv.ParseInt(c.Query("version_code"), 10, 64)

	var rel model.AppRelease
	err := h.db.Where("platform = ? AND enabled = ?", platform, true).
		Order("version_code desc").First(&rel).Error
	if err != nil {
		jsonOK(c, gin.H{"has_update": false})
		return
	}
	if rel.VersionCode <= versionCode {
		jsonOK(c, gin.H{"has_update": false, "release": rel})
		return
	}
	mandatory := rel.Mandatory || (rel.MinVersionCode > 0 && versionCode < rel.MinVersionCode)
	jsonOK(c, gin.H{"has_update": true, "mandatory": mandatory, "release": rel})
}

// Latest 返回某平台最新上架版本（不做版本比较，供管理/展示）。
func (h *APIHandler) Latest(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	platform := c.DefaultQuery("platform", "android")
	var rel model.AppRelease
	err := h.db.Where("platform = ? AND enabled = ?", platform, true).
		Order("version_code desc").First(&rel).Error
	if err != nil {
		jsonOK(c, nil)
		return
	}
	jsonOK(c, rel)
}

// ListReleases 版本列表（管理用，仅 admin）。query: platform(可选)
func (h *APIHandler) ListReleases(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	q := h.db.Model(&model.AppRelease{})
	if platform := c.Query("platform"); platform != "" {
		q = q.Where("platform = ?", platform)
	}
	var list []model.AppRelease
	if err := q.Order("version_code desc").Find(&list).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, gin.H{"list": list, "total": len(list)})
}

// Publish 发布新版本（仅 admin）。APK 须已经过分片上传落在允许盘内。
func (h *APIHandler) Publish(c *gin.Context) {
	claims, ok := requireAdmin(c)
	if !ok {
		return
	}
	var req struct {
		Platform       string `json:"platform"`
		VersionCode    int64  `json:"version_code" binding:"required"`
		VersionName    string `json:"version_name" binding:"required"`
		Notes          string `json:"notes"`
		FilePath       string `json:"file_path" binding:"required"`
		FileName       string `json:"file_name" binding:"required"`
		FileSize       int64  `json:"file_size"`
		FileHash       string `json:"file_hash"`
		Mandatory      bool   `json:"mandatory"`
		MinVersionCode int64  `json:"min_version_code"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// 带上真实原因：这里最常见的坑是 version_code 传了小数（如 1.2），
		// 而它是 int64——只回一句「参数解析失败」的话，客户端根本无从判断是哪个字段。
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	if req.Platform == "" {
		req.Platform = "android"
	}
	// APK 路径必须落在允许盘内（与 download/upload 一致的安全边界）
	if !config.Conf.IsPathAllowed(req.FilePath) {
		jsonErr(c, 400, "APK 路径不在允许的存储范围内")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// 同平台同版本号唯一：已存在则更新，避免重复
	rel := model.AppRelease{
		Platform:       req.Platform,
		VersionCode:    req.VersionCode,
		VersionName:    req.VersionName,
		Notes:          req.Notes,
		FilePath:       req.FilePath,
		FileName:       req.FileName,
		FileSize:       req.FileSize,
		FileHash:       req.FileHash,
		Mandatory:      req.Mandatory,
		MinVersionCode: req.MinVersionCode,
		Enabled:        enabled,
		UploaderID:     claims,
	}
	var existing model.AppRelease
	err := h.db.Where("platform = ? AND version_code = ?", req.Platform, req.VersionCode).First(&existing).Error
	if err == nil {
		rel.ID = existing.ID
		rel.CreatedAt = existing.CreatedAt
		if err := h.db.Save(&rel).Error; err != nil {
			jsonErr(c, 500, err.Error())
			return
		}
	} else {
		if err := h.db.Create(&rel).Error; err != nil {
			jsonErr(c, 500, err.Error())
			return
		}
	}

	// 上架的版本才推送在线设备
	if rel.Enabled {
		broadcastAppUpdate(&rel)
	}
	jsonOK(c, rel)
}

// UpdateRelease 修改版本（上架/下架、强制、说明等，仅 admin）。
func (h *APIHandler) UpdateRelease(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的版本ID")
		return
	}
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		jsonErr(c, 400, "参数解析失败: "+err.Error())
		return
	}
	// 白名单字段，防止改到主键/路径
	allowed := map[string]bool{"enabled": true, "mandatory": true, "notes": true, "min_version_code": true}
	patch := map[string]interface{}{}
	for k, v := range updates {
		if allowed[k] {
			patch[k] = v
		}
	}
	if len(patch) == 0 {
		jsonErr(c, 400, "无可更新字段")
		return
	}
	if err := h.db.Model(&model.AppRelease{}).Where("id = ?", id).Updates(patch).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}

// DeleteRelease 删除版本记录（仅 admin，不删磁盘 APK，属手动管理）。
func (h *APIHandler) DeleteRelease(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的版本ID")
		return
	}
	if err := h.db.Where("id = ?", id).Delete(&model.AppRelease{}).Error; err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, nil)
}

// broadcastAppUpdate 向所有在线设备广播 app_update；非 Android 客户端按 type 忽略即可。
func broadcastAppUpdate(rel *model.AppRelease) {
	payload, _ := json.Marshal(gin.H{
		"event":        WSEventAppUpdate,
		"platform":     rel.Platform,
		"version_code": rel.VersionCode,
		"version_name": rel.VersionName,
		"mandatory":    rel.Mandatory,
		"notes":        rel.Notes,
		"release_id":   rel.ID,
	})
	msg := &ws.Message{
		ID:      fmt.Sprintf("appupd-%d-%d", rel.ID, time.Now().UnixMilli()),
		Type:    ws.MessageType(WSEventAppUpdate),
		Target:  ws.NewTargetAll(),
		Content: payload,
	}
	ws.GetHub().Broadcast(msg)
}

func requireUser(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return 0, false
	}
	userClaims := claimsAny.(*token.Claims)
	return uint(userClaims.UserID), true
}

// requireAdmin 返回当前用户ID，非管理员返回 403。
func requireAdmin(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return 0, false
	}
	userClaims := claimsAny.(*token.Claims)
	admin := false
	for _, r := range userClaims.Roles {
		if r == "admin" {
			admin = true
			break
		}
	}
	if !admin {
		c.JSON(http.StatusOK, gin.H{"code": 403, "message": "权限不足，仅管理员可操作", "data": nil})
		return 0, false
	}
	return uint(userClaims.UserID), true
}

func jsonOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func jsonErr(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg, "data": nil})
}
