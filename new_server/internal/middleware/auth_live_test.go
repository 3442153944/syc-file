package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/logger"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

func TestOldTokenIsRejectedImmediatelyAfterAccountDeletion(t *testing.T) {
	logger.Logger = zap.NewNop()
	gin.SetMode(gin.TestMode)
	t.Setenv("SYC_JWT_SECRET", "test-only-account-deletion-signing-key")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err := token.InitSecret(db, false); err != nil {
		t.Fatal(err)
	}
	signed, err := token.GenerateToken(7, "test", "", []string{"admin"}, "device", model.LevelAdmin, 7)
	if err != nil {
		t.Fatal(err)
	}
	oldLookup, oldWhitelist := userLookup, config.Conf.Whitelist
	t.Cleanup(func() { userLookup = oldLookup; config.Conf.Whitelist = oldWhitelist })
	config.Conf.Whitelist = nil
	status, exists := int8(1), true
	SetUserLookup(func(int64) (string, int8, int8, bool) { return "user", model.LevelUser, status, exists })
	r := gin.New()
	r.Use(Auth())
	r.GET("/private", func(c *gin.Context) { response.OK(c, LevelOf(c)) })
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/private", nil)
		req.Header.Set("Token", signed)
		req.Header.Set("Device-Id", "device")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := call(); !bytes.Contains(w.Body.Bytes(), []byte(`"data":1`)) {
		t.Fatal("未使用最新权限", w.Body.String())
	}
	for _, v := range []int8{0, model.UserDeleting} {
		status = v
		if w := call(); !bytes.Contains(w.Body.Bytes(), []byte(`"code":401`)) {
			t.Fatalf("失效账号仍可使用旧令牌: %s", w.Body.String())
		}
	}
	exists = false
	if w := call(); !bytes.Contains(w.Body.Bytes(), []byte(`"code":401`)) {
		t.Fatal("已删除账号仍可使用旧令牌", w.Body.String())
	}
}
