package user

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

func HandlerFuncUpdateUserInfo(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userClaims := claims.(*token.Claims)
		userID := uint(userClaims.UserID)

		var req struct {
			Username string `form:"username"`
			Email    string `form:"email"`
			Phone    string `form:"phone"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数解析失败", "data": nil})
			return
		}

		avatarRelPath, err := handleAvatarUpload(c, userID)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": err.Error(), "data": nil})
			return
		}

		updates := map[string]interface{}{}
		if req.Username != "" {
			updates["username"] = req.Username
		}
		if req.Email != "" {
			updates["email"] = req.Email
		}
		if req.Phone != "" {
			updates["phone"] = req.Phone
		}
		if avatarRelPath != "" {
			updates["avatar"] = avatarRelPath
		}
		if len(updates) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "没有需要更新的字段", "data": nil})
			return
		}

		if err := db.Model(&model.User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			logger.Logger.Error("更新用户信息失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "更新用户信息失败", "data": nil})
			return
		}

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "更新成功", "data": nil})
	}
}

func handleAvatarUpload(c *gin.Context, userID uint) (string, error) {
	file, header, err := c.Request.FormFile("avatar")
	if err != nil {
		return "", nil
	}
	defer func(f multipart.File) {
		if err := f.Close(); err != nil {
			logger.Logger.Error("关闭文件失败", zap.Error(err))
		}
	}(file)

	userCfg := config.Conf.User

	if header.Size > userCfg.MaxSize {
		return "", fmt.Errorf("头像文件超过最大限制 %dMB", userCfg.MaxSize/1024/1024)
	}

	origExt := strings.ToLower(filepath.Ext(header.Filename))
	if !isAvatarExtAllowed(origExt, userCfg.AllowedExtensions) {
		return "", fmt.Errorf("不支持的头像格式: %s", origExt)
	}

	img, err := decodeImage(file)
	if err != nil {
		return "", fmt.Errorf("图片解析失败: %s", err.Error())
	}

	wd, _ := os.Getwd()
	avatarDir := filepath.Join(wd, userCfg.AvatarPath)
	if err := os.MkdirAll(avatarDir, 0755); err != nil {
		return "", fmt.Errorf("创建头像目录失败: %s", err.Error())
	}

	filename := fmt.Sprintf("avatar_%d_%d.png", userID, time.Now().UnixMilli())
	savePath := filepath.Join(avatarDir, filename)

	out, err := os.Create(savePath)
	if err != nil {
		return "", fmt.Errorf("创建头像文件失败: %s", err.Error())
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		_ = os.Remove(savePath)
		return "", fmt.Errorf("保存头像失败: %s", err.Error())
	}

	return userCfg.AvatarPath + "/" + filename, nil
}

func decodeImage(f multipart.File) (image.Image, error) {
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func isAvatarExtAllowed(ext string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if strings.ToLower(a) == ext {
			return true
		}
	}
	return false
}
