package file

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"net/http"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

func DeleteDownloadHistory(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 获取登录信息
		claims, ok := c.Get("UserInfo")
		if !ok || claims == nil {
			c.JSON(http.StatusOK, gin.H{"code": 401, "message": "请先登录", "data": nil})
			return
		}

		// 2. 解析请求体
		var body struct {
			Ids []int `json:"ids"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "请求参数错误", "data": nil})
			return
		}
		if len(body.Ids) == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 400, "message": "ids不能为空", "data": nil})
			return
		}

		userClaims := claims.(*token.Claims)
		userID := userClaims.UserID

		// 3. 删除（加 user_id 条件防止越权删除别人的记录）
		result := db.Delete(&model.DownloadHistory{}, "user_id = ? AND id IN ?", userID, body.Ids)
		if result.Error != nil {
			logger.Logger.Error("删除下载记录失败", zap.Error(result.Error))
			c.JSON(http.StatusOK, gin.H{"code": 500, "message": "删除失败", "data": nil})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"message": "删除成功",
			"data":    gin.H{"deleted": result.RowsAffected},
		})
	}
}
