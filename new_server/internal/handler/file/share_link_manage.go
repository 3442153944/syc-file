package file

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

// HandlerFuncShareLinkList 分页查询当前登录用户创建过的分享链接（含已过期/已吊销），供"分享管理"页展示。
func HandlerFuncShareLinkList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req struct {
			PageNum  int `json:"pageNum"`
			PageSize int `json:"pageSize"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误", "data": nil})
			return
		}

		var total int64
		if err := db.Model(&model.ShareLink{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
			logger.Logger.Error("查询分享链接总数失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询分享链接失败", "data": nil})
			return
		}

		query := db.Where("user_id = ?", userID).Order("created_at desc")
		if req.PageNum > 0 && req.PageSize > 0 {
			query = query.Limit(req.PageSize).Offset((req.PageNum - 1) * req.PageSize)
		}

		var list []model.ShareLink
		if err := query.Find(&list).Error; err != nil {
			logger.Logger.Error("查询分享链接失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询分享链接失败", "data": nil})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "ok",
			"data": gin.H{
				"list":     list,
				"total":    total,
				"pageNum":  req.PageNum,
				"pageSize": req.PageSize,
			},
		})
	}
}

// HandlerFuncShareLinkRevoke 提前吊销一个仍然有效的分享链接：删临时文件、标记状态、清 Redis，
// 立即生效（下载接口的 Redis/DB 校验会马上失败），只有创建者本人能操作。
func HandlerFuncShareLinkRevoke(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userID := uint(claims.(*token.Claims).UserID)

		var req struct {
			ShareCode string `json:"share_code"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ShareCode == "" {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "缺少必要参数 share_code", "data": nil})
			return
		}

		var link model.ShareLink
		if err := db.Where("share_code = ?", req.ShareCode).First(&link).Error; err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 404, "message": "分享链接不存在", "data": nil})
			return
		}
		if link.UserID != userID {
			c.JSON(http.StatusOK, gin.H{"code": 403, "message": "无权操作该分享链接", "data": nil})
			return
		}
		if link.Status != shareStatusActive {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "该分享链接已失效", "data": nil})
			return
		}

		destroyShareLink(db, redisClient, link.ShareCode, resolveShareTempPath(link), shareStatusRevoked)

		logger.Logger.Info("分享链接已吊销", zap.Uint("user_id", userID), zap.String("share_code", link.ShareCode))
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": nil})
	}
}

// AdminDestroyShareLink 管理员强制清理任意用户的一条分享（不做归属校验）。目前主要给
// 「缓存管理」页用来手动清粘贴快传缓存，不用等自动过期——复用和普通吊销同一套销毁逻辑
// （删临时文件/内存内容 + 标记 DB + 清 Redis），对已经失效的记录是幂等的空操作。
func AdminDestroyShareLink(db *gorm.DB, redisClient *redis.Client, shareCode string) error {
	var link model.ShareLink
	if err := db.Where("share_code = ?", shareCode).First(&link).Error; err != nil {
		return err
	}
	if link.Status != shareStatusActive {
		return nil
	}
	destroyShareLink(db, redisClient, link.ShareCode, resolveShareTempPath(link), shareStatusRevoked)
	return nil
}
