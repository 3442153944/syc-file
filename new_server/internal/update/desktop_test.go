package update

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/pkg/token"
	"testing"
)

func fixture(t *testing.T) (*gorm.DB, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err = db.AutoMigrate(&model.AppRelease{}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("UserInfo", &token.Claims{UserID: 1, Roles: []string{"admin"}}); c.Next() })
	h := NewAPIHandler(db)
	r.GET("/check", h.Check)
	r.GET("/desktop", h.DesktopManifest)
	r.POST("/publish", h.Publish)
	return db, r
}
func TestStableVersionAndTargetValidation(t *testing.T) {
	sig := base64.StdEncoding.EncodeToString(make([]byte, 128))
	if code, e := validateTarget("windows", "x86_64", "nsis", "1.2.3", sig, "setup.exe"); e != nil || code != 10203 {
		t.Fatal(code, e)
	}
	for _, target := range []struct{ p, a, b, v, s, n string }{
		{"windows", "x86_64", "deb", "1.2.3", sig, "x.deb"},
		{"linux", "x86_64", "nsis", "1.2.3", sig, "x.exe"},
		{"windows", "x86_64", "nsis", "1.2.3", "", "x.exe"},
		{"windows", "x86_64", "nsis", "1.100.0", sig, "x.exe"},
		{"windows", "x86_64", "nsis", "01.2.3", sig, "x.exe"},
		{"windows", "x86_64", "nsis", "1.2.3-beta", sig, "x.exe"},
	} {
		if _, e := validateTarget(target.p, target.a, target.b, target.v, target.s, target.n); e == nil {
			t.Fatalf("accepted invalid %+v", target)
		}
	}
	if _, e := validateTarget("android", "", "", "custom-version", "", "app.apk"); e != nil {
		t.Fatal("android compatibility", e)
	}
}
func TestManifestSelectsExactTargetAndReturns204ForCurrentVersion(t *testing.T) {
	db, r := fixture(t)
	for _, rel := range []model.AppRelease{
		{Platform: "windows", Arch: "x86_64", BundleType: "nsis", VersionCode: 101, VersionName: "0.1.1", Enabled: true, Signature: "signed", Mandatory: true},
		{Platform: "windows", Arch: "aarch64", BundleType: "nsis", VersionCode: 99999, VersionName: "9.99.99", Enabled: true},
		{Platform: "linux", Arch: "x86_64", BundleType: "deb", VersionCode: 99999, VersionName: "9.99.99", Enabled: true},
		{Platform: "windows", Arch: "x86_64", BundleType: "nsis", VersionCode: 102, VersionName: "0.1.2", Enabled: false},
	} {
		if e := db.Create(&rel).Error; e != nil {
			t.Fatal(e)
		}
	}
	url := "/desktop?platform=windows&arch=x86_64&bundle_type=nsis&version=0.1.0"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "\"version\":\"0.1.1\"") || !strings.Contains(w.Body.String(), "\"mandatory\":true") {
		t.Fatal(w.Code, w.Body)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if _, ok := body["data"]; ok {
		t.Fatal("manifest must not use business envelope")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", strings.Replace(url, "version=0.1.0", "version=0.1.1", 1), nil))
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestAndroidCheckStillIgnoresDesktopArchitectures(t *testing.T) {
	db, r := fixture(t)
	db.Create(&model.AppRelease{Platform: "android", VersionCode: 10301, VersionName: "1.3.1", Enabled: true})
	db.Create(&model.AppRelease{Platform: "windows", Arch: "x86_64", BundleType: "nsis", VersionCode: 99999, VersionName: "9.99.99", Enabled: true})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/check?platform=android&version_code=10300", nil))
	if !bytes.Contains(w.Body.Bytes(), []byte("\"version_name\":\"1.3.1\"")) {
		t.Fatal(fmt.Sprint(w.Body))
	}
}

func TestSameVersionDifferentTargetsDoNotOverwrite(t *testing.T) {
	db, r := fixture(t)
	old := config.Conf.File.AllowedPaths
	dir := t.TempDir()
	config.Conf.File.AllowedPaths = []string{dir}
	t.Cleanup(func() { config.Conf.File.AllowedPaths = old })
	sig := base64.StdEncoding.EncodeToString(make([]byte, 128))
	for _, arch := range []string{"x86_64", "aarch64", "x86_64"} {
		payload := map[string]interface{}{"platform": "windows", "arch": arch, "bundle_type": "nsis", "signature": sig, "version_code": 101, "version_name": "0.1.1", "file_path": filepath.Join(dir, "setup.exe"), "file_name": "setup.exe", "enabled": false}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/publish", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), "\"code\":200") {
			t.Fatal(w.Body)
		}
	}
	var count int64
	db.Model(&model.AppRelease{}).Count(&count)
	if count != 2 {
		t.Fatalf("expected 2 target rows, got %d", count)
	}
}
