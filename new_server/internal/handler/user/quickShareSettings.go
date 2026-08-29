package user

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

// hotkeyHexPattern 快捷键存的是前端录制后编码出来的十六进制数字（如 "0x108"），不是
// "Ctrl+Shift+V" 这种平台相关的文本——桌面端三端都从同一份数字解出实际按键，Linux 上
// 也一样能用。这里只做格式校验，不关心具体数值（数值语义由桌面端 hotkey_codec.rs 解释）。
var hotkeyHexPattern = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)

// HandlerFuncSaveQuickShareSettings 保存粘贴快传的账号级设置：全局唤起快捷键 + 默认分享有效期。
// 两个字段都是可选的部分更新（同 updateUserInfo.go 的写法），传空字符串/0 就跳过不改那一项。
func HandlerFuncSaveQuickShareSettings(db *gorm.DB, _ *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req struct {
			Hotkey        string `json:"hotkey"`
			ExpireMinutes int    `json:"expire_minutes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数解析失败", "data": nil})
			return
		}
		if req.Hotkey != "" && !hotkeyHexPattern.MatchString(req.Hotkey) {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "快捷键格式不合法", "data": nil})
			return
		}

		updates := map[string]interface{}{}
		if req.Hotkey != "" {
			updates["quick_share_hotkey"] = req.Hotkey
		}
		if req.ExpireMinutes > 0 {
			updates["quick_share_expire_minutes"] = req.ExpireMinutes
		}
		if len(updates) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "没有需要更新的字段", "data": nil})
			return
		}

		if err := db.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			logger.Logger.Error("保存粘贴快传设置失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "保存失败", "data": nil})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "保存成功", "data": nil})
	}
}
