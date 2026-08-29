package user

import (
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func RegisterUserRouter(rg *gin.RouterGroup, db *gorm.DB, redisClient *redis.Client) {
	u := rg.Group("/user")
	u.POST("/register", HandlerFuncRegister(db, redisClient))
	u.POST("/login", HandlerFuncLogin(db, redisClient))
	u.POST("/reset-password", HandlerFuncResetPassword(db, redisClient))
	u.POST("/verify", HandlerFuncVerify(db, redisClient))
	u.POST("/quick-share-settings", HandlerFuncSaveQuickShareSettings(db, redisClient))
}
