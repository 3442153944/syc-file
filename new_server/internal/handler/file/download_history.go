package file

import (
	"net/http"
	"syc-file/pkg/response"

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
			response.WriteHTTP(c, http.StatusOK, 401, "请先登录", nil)
			return
		}
		userClaims := claims.(*token.Claims)
		userID := userClaims.UserID

		var req struct {
			PageNum  int `json:"pageNum"`
			PageSize int `json:"pageSize"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "参数错误", nil)
			return
		}

		var total int64
		if err := db.Model(&model.DownloadHistory{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
			logger.Logger.Error("查询下载记录总数失败", zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "查询下载记录失败", nil)
			return
		}

		query := db.Where("user_id = ?", userID).Order("created_at desc")
		if req.PageNum > 0 && req.PageSize > 0 {
			query = query.Limit(req.PageSize).Offset((req.PageNum - 1) * req.PageSize)
		}

		var list []model.DownloadHistory
		if err := query.Find(&list).Error; err != nil {
			logger.Logger.Error("查询下载记录失败", zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "查询下载记录失败", nil)
			return
		}

		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"list":     list,
			"total":    total,
			"pageNum":  req.PageNum,
			"pageSize": req.PageSize,
		})
	}
}
