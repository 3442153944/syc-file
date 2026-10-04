package token

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// JWT 签名密钥存在数据库 app_secret 表里，而不是文件：
//   - 密钥随数据库一起备份 / 迁移 / 打进数据卷，不再依赖 gitignore 的 venv/key.yaml
//     （旧实现靠 runtime.Caller 取源码路径找文件，换机器、进镜像都会找不到）；
//   - 首次启动自动生成，镜像本身不带任何密钥——每个部署各有各的。
// 数据库账号密码这类「连库之前就要用」的凭据不在此列，它们只能放配置文件 / 环境变量。

const jwtSecretName = "jwt"

type appSecret struct {
	Name  string `gorm:"primaryKey;size:64"`
	Value string `gorm:"type:text;not null"`
}

func (appSecret) TableName() string { return "app_secret" }

var secret atomic.Value // []byte

// InitSecret 从数据库载入 JWT 密钥，没有就生成并落库。必须在 AutoMigrate 之后、处理任何请求之前调用。
// 首次生成时的取值优先级：环境变量 SYC_JWT_SECRET → 旧版 venv/key.yaml（仅 allowLegacyFile，即 dev 模式，
// 保证老部署升级后已签发的 token 不失效）→ 随机。
func InitSecret(db *gorm.DB, allowLegacyFile bool) error {
	if err := db.AutoMigrate(&appSecret{}); err != nil {
		return fmt.Errorf("创建 app_secret 表失败: %w", err)
	}
	var row appSecret
	err := db.Where("name = ?", jwtSecretName).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		val, genErr := firstSecret(allowLegacyFile)
		if genErr != nil {
			return genErr
		}
		// DoNothing：多实例同时首启时只有一个写入成功，其余读回同一份
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&appSecret{Name: jwtSecretName, Value: val}).Error; err != nil {
			return fmt.Errorf("写入 JWT 密钥失败: %w", err)
		}
		err = db.Where("name = ?", jwtSecretName).First(&row).Error
	}
	if err != nil {
		return fmt.Errorf("读取 JWT 密钥失败: %w", err)
	}
	if row.Value == "" {
		return errors.New("数据库中的 JWT 密钥为空")
	}
	secret.Store([]byte(row.Value))
	return nil
}

func firstSecret(allowLegacyFile bool) (string, error) {
	if v := strings.TrimSpace(os.Getenv("SYC_JWT_SECRET")); v != "" {
		return v, nil
	}
	if allowLegacyFile {
		if v := legacyKeyFile("venv/key.yaml"); v != "" {
			return v, nil
		}
	}
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 JWT 密钥失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// legacyKeyFile 读旧版 key.yaml 的 encryptionKey（相对工作目录），读不到返回空串。
func legacyKeyFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var k struct {
		EncryptionKey string `yaml:"encryptionKey"`
	}
	if yaml.Unmarshal(data, &k) != nil {
		return ""
	}
	return k.EncryptionKey
}

func getSecret() []byte {
	v, _ := secret.Load().([]byte)
	return v
}
