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

func HandlerFuncDownloadHistory(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}
		userClaims := claims.(*token.Claims)
		userID := userClaims.UserID

		var req struct {
			PageNum  int `json:"pageNum"`
			PageSize int `json:"pageSize"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "参数错误", "data": nil})
			return
		}

		var total int64
		if err := db.Model(&model.DownloadHistory{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
			logger.Logger.Error("查询下载记录总数失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询下载记录失败", "data": nil})
			return
		}

		query := db.Where("user_id = ?", userID).Order("created_at desc")
		if req.PageNum > 0 && req.PageSize > 0 {
			query = query.Limit(req.PageSize).Offset((req.PageNum - 1) * req.PageSize)
		}

		var list []model.DownloadHistory
		if err := query.Find(&list).Error; err != nil {
			logger.Logger.Error("查询下载记录失败", zap.Error(err))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "查询下载记录失败", "data": nil})
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
