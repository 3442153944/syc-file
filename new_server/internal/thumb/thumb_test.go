package thumb

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/pkg/logger"
)

func TestNormalizeWidth(t *testing.T) {
	cases := map[int]int{-1: 256, 0: 256, 1: 128, 128: 128, 129: 256, 256: 256, 257: 512, 512: 512, 4000: 512}
	for in, want := range cases {
		if got := normalizeWidth(in); got != want {
			t.Errorf("normalizeWidth(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSupported(t *testing.T) {
	for _, n := range []string{"a.jpg", "A.JPG", "b.jpeg", "c.png", "d.gif", "e.webp", "f.bmp", "g.tiff",
		"v.mp4", "v.MKV", "v.mov", "s.mp3", "s.flac", "s.m4a"} {
		if !Supported(n) {
			t.Errorf("%s 应当支持", n)
		}
	}
	// .ts 多半是 TypeScript 源文件；.wav 没有封面机制
	for _, n := range []string{"a.txt", "c.svg", "noext", "d.png.bak", "code.ts", "a.wav", "a.go"} {
		if Supported(n) {
			t.Errorf("%s 不应支持", n)
		}
	}
}

// 源文件改了（修改时间变）缓存键必须变，否则会一直返回旧缩略图。
func TestCacheKeyFollowsSourceChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi1, _ := os.Stat(p)
	k1 := cacheKey(p, fi1)
	if k1 != cacheKey(p, fi1) {
		t.Fatal("同一文件同一状态的键必须稳定")
	}
	later := fi1.ModTime().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if k1 == cacheKey(p, fi2) {
		t.Fatal("修改时间变了，键必须变")
	}
}

// memIndex 内存版到期索引，只用于测试。
type memIndex struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func newMemIndex() *memIndex { return &memIndex{m: map[string]time.Time{}} }

func (x *memIndex) Touch(_ context.Context, member string, at time.Time) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.m[member] = at
	return nil
}

func (x *memIndex) Due(_ context.Context, now time.Time, limit int) ([]string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []string
	for k, at := range x.m {
		if !at.After(now) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (x *memIndex) Remove(_ context.Context, members ...string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, m := range members {
		delete(x.m, m)
	}
	return nil
}

func (x *memIndex) expiry(member string) (time.Time, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	at, ok := x.m[member]
	return at, ok
}

func (x *memIndex) size() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.m)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("没有 ffmpeg，跳过")
	}
	fp, _ := exec.LookPath("ffprobe") // 没有也行：只是视频/音频相关测试会跳过
	logger.Logger = zap.NewNop()
	return &Service{
		ffprobe:      fp,
		dir:          t.TempDir(),
		tmpDir:       t.TempDir(),
		tmpTTL:       time.Hour,
		idx:          newMemIndex(),
		reg:          newMemRegistry(),
		ffmpeg:       ff,
		defaultWidth: 256,
		sem:          make(chan struct{}, 4),
		queue:        make(chan string, 16),
		tmpQueue:     make(chan string, 16),
		inflight:     make(map[string]*call),
	}
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// 600x400 的 PNG：中间一块红色，四周完全透明。
func writeAlphaPNG(t *testing.T, p string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 600, 400))
	for y := 100; y < 300; y++ {
		for x := 150; x < 450; x++ {
			img.Set(x, y, color.NRGBA{R: 220, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateScalesAndFlattensTransparency(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "alpha.png")
	writeAlphaPNG(t, src)

	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("产出不是合法 JPEG: %v", err)
	}
	b := img.Bounds()
	if b.Dx() > 256 || b.Dy() > 256 {
		t.Errorf("长边超过 256: %v", b)
	}
	// 透明区域必须是白底而不是黑底
	r, g, bl, _ := img.At(3, 3).RGBA()
	if r>>8 < 240 || g>>8 < 240 || bl>>8 < 240 {
		t.Errorf("透明区域应为白色，实际 (%d,%d,%d)", r>>8, g>>8, bl>>8)
	}
	// 不放大：小图保持原尺寸
	small := filepath.Join(t.TempDir(), "small.png")
	sm := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	var buf bytes.Buffer
	_ = png.Encode(&buf, sm)
	_ = os.WriteFile(small, buf.Bytes(), 0o644)
	out2, err := s.Get(context.Background(), small, 256)
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := os.Open(out2)
	defer f2.Close()
	if cfg, _ := jpeg.DecodeConfig(f2); cfg.Width != 40 || cfg.Height != 30 {
		t.Errorf("小图不应被放大: %dx%d", cfg.Width, cfg.Height)
	}
}

// 同一张图并发请求：全部成功、拿到同一个缓存文件，缓存目录里只有一个产物（没有残留临时文件）。
func TestConcurrentGetGeneratesOnce(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)

	var wg sync.WaitGroup
	outs := make([]string, 20)
	errs := make([]error, 20)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i], errs[i] = s.Get(context.Background(), src, 256)
		}(i)
	}
	wg.Wait()
	for i := range outs {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if outs[i] != outs[0] {
			t.Fatalf("应得到同一个缓存文件: %s vs %s", outs[i], outs[0])
		}
	}
	if n := countFiles(s.tmpDir); n != 1 {
		t.Errorf("临时目录应只有 1 个文件，实际 %d", n)
	}
}

// ffmpeg 处理不了的文件：第一次报错，之后直接判为不支持，不再重复起 ffmpeg。
func TestBrokenImageIsNotRetried(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "broken.png")
	if err := os.WriteFile(src, []byte("this is not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), src, 256); err == nil {
		t.Fatal("损坏的图片应当失败")
	}
	if _, err := s.Get(context.Background(), src, 256); err != ErrUnsupported {
		t.Fatalf("第二次应直接返回 ErrUnsupported，实际 %v", err)
	}
}

func TestGetRejectsNonImageAndMissing(t *testing.T) {
	s := newTestService(t)
	txt := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(txt, []byte("hi"), 0o644)
	if _, err := s.Get(context.Background(), txt, 256); err != ErrUnsupported {
		t.Errorf("非图片应返回 ErrUnsupported，实际 %v", err)
	}
	if _, err := s.Get(context.Background(), filepath.Join(t.TempDir(), "nope.png"), 256); !os.IsNotExist(err) {
		t.Errorf("不存在的文件应返回不存在错误，实际 %v", err)
	}
	var nilSvc *Service
	if _, err := nilSvc.Get(context.Background(), txt, 256); err != ErrUnsupported {
		t.Errorf("nil 服务应安全返回 ErrUnsupported，实际 %v", err)
	}
}

// ── 临时层 ────────────────────────────────────────────────────

// 磁盘上原本就有的文件：缩略图进临时目录、到期时间记进索引，持久目录不受影响。
func TestGetGoesToTempTierAndIsIndexed(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)

	before := time.Now()
	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, s.tmpDir) {
		t.Fatalf("应落在临时目录 %s，实际 %s", s.tmpDir, out)
	}
	if n := countFiles(s.dir); n != 0 {
		t.Errorf("持久目录不该有文件，实际 %d", n)
	}
	idx := s.idx.(*memIndex)
	member, _ := filepath.Rel(s.tmpDir, out)
	at, ok := idx.expiry(filepath.ToSlash(member))
	if !ok {
		t.Fatalf("临时缩略图应记入到期索引，索引内容: %v", idx.m)
	}
	if at.Before(before.Add(s.tmpTTL-time.Second)) || at.After(time.Now().Add(s.tmpTTL+time.Second)) {
		t.Errorf("到期时间应约为现在+TTL，实际 %v", at)
	}
}

// 上传/同步的文件走持久层；之后再请求直接命中持久层，不会再往临时层生成一份。
func TestPersistentTierTakesPrecedence(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)

	perm, err := s.get(context.Background(), src, 256, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(perm, s.dir) {
		t.Fatalf("persist=true 应落在持久目录，实际 %s", perm)
	}
	got, err := s.Get(context.Background(), src, 256)
	if err != nil || got != perm {
		t.Fatalf("Get 应命中持久层 %s，实际 %s (%v)", perm, got, err)
	}
	if countFiles(s.tmpDir) != 0 || s.idx.(*memIndex).size() != 0 {
		t.Error("命中持久层时不该产生临时文件或索引")
	}
}

// 被再次访问的临时缩略图到期时间顺延，常看的不会过期。
func TestTempHitExtendsExpiry(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)
	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := filepath.Rel(s.tmpDir, out)
	member = filepath.ToSlash(member)
	idx := s.idx.(*memIndex)

	// 把到期时间改成「快到了」，再访问一次应被拉回约 TTL 之后
	_ = idx.Touch(context.Background(), member, time.Now().Add(time.Minute))
	if _, err := s.Get(context.Background(), src, 256); err != nil {
		t.Fatal(err)
	}
	at, _ := idx.expiry(member)
	if at.Before(time.Now().Add(s.tmpTTL - time.Minute)) {
		t.Errorf("命中后到期时间应顺延到约 TTL 之后，实际 %v", at)
	}
}

// 到期的被销毁（文件 + 索引），没到期的原样保留。
func TestPurgeExpired(t *testing.T) {
	s := newTestService(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	writeAlphaPNG(t, a)
	writeAlphaPNG(t, b)
	outA, err := s.Get(context.Background(), a, 256)
	if err != nil {
		t.Fatal(err)
	}
	outB, err := s.Get(context.Background(), b, 256)
	if err != nil {
		t.Fatal(err)
	}
	idx := s.idx.(*memIndex)
	memberA, _ := filepath.Rel(s.tmpDir, outA)
	_ = idx.Touch(context.Background(), filepath.ToSlash(memberA), time.Now().Add(-time.Minute)) // a 已到期

	if n := s.purgeExpired(context.Background(), time.Now()); n != 1 {
		t.Errorf("应销毁 1 个，实际 %d", n)
	}
	if _, err := os.Stat(outA); !os.IsNotExist(err) {
		t.Error("到期的缩略图文件应已被删除")
	}
	if _, err := os.Stat(outB); err != nil {
		t.Error("未到期的缩略图不该被删除")
	}
	if _, ok := idx.expiry(filepath.ToSlash(memberA)); ok {
		t.Error("到期条目应已从索引移除")
	}
	if idx.size() != 1 {
		t.Errorf("索引里应只剩未到期的 1 条，实际 %d", idx.size())
	}
}

// 索引内容来自 Redis，不能信任：指向临时目录之外的成员不得被删除。
func TestPurgeNeverDeletesOutsideTempDir(t *testing.T) {
	s := newTestService(t)
	outside := filepath.Join(filepath.Dir(s.tmpDir), "precious.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	idx := s.idx.(*memIndex)
	_ = idx.Touch(context.Background(), "../"+filepath.Base(outside), time.Now().Add(-time.Hour))
	_ = idx.Touch(context.Background(), "../../etc/passwd", time.Now().Add(-time.Hour))

	s.purgeExpired(context.Background(), time.Now())
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("临时目录之外的文件被误删了")
	}
	if idx.size() != 0 {
		t.Errorf("非法成员也应从索引里清掉，剩 %d", idx.size())
	}
}

// 兜底清理：索引管不到的残留（Redis 丢了、进程中途退出）按修改时间清。
func TestSweepOrphans(t *testing.T) {
	s := newTestService(t)
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(s.tmpDir, "ab", name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-age)
		_ = os.Chtimes(p, old, old)
		return p
	}
	stale := mk("stale_256.jpg", 3*s.tmpTTL)         // 超过 2 倍 TTL 没人碰
	fresh := mk("fresh_256.jpg", s.tmpTTL/2)         // 还新
	halfWritten := mk("x_256.tmp123.jpg", time.Hour) // 生成到一半遗留的临时文件
	freshTmp := mk("y_256.tmp456.jpg", time.Minute)  // 正在生成的，不能动

	if n := s.sweepOrphans(time.Now()); n != 2 {
		t.Errorf("应清理 2 个，实际 %d", n)
	}
	for _, p := range []string{stale, halfWritten} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s 应已被清理", filepath.Base(p))
		}
	}
	for _, p := range []string{fresh, freshTmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s 不该被清理", filepath.Base(p))
		}
	}
}

// 浏览触发：EnqueueTemp 只收图片、同一张不重复排队；worker 起来后生成到临时层。
func TestEnqueueTempDedupesAndGenerates(t *testing.T) {
	s := newTestService(t)
	Global = s
	defer func() { Global = nil }()
	dir := t.TempDir()
	img, txt := filepath.Join(dir, "a.png"), filepath.Join(dir, "a.txt")
	writeAlphaPNG(t, img)
	_ = os.WriteFile(txt, []byte("hi"), 0o644)

	EnqueueTemp(img, txt, img)
	EnqueueTemp(img)
	if got := len(s.tmpQueue); got != 1 {
		t.Fatalf("应只排 1 个（非图片不收、重复不排），实际 %d", got)
	}

	go s.worker()
	deadline := time.Now().Add(10 * time.Second)
	for countFiles(s.tmpDir) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker 没有在限定时间内生成临时缩略图")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 处理完后应能再次入队（pending 标记要清掉，否则以后这张图永远排不进来）
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, pending := s.pendingTmp.Load(img); !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("处理完成后 pending 标记没有清除")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 持久层队列优先：两个队列都有活时先做持久层的。
func TestNextPrefersPersistentQueue(t *testing.T) {
	s := newTestService(t)
	s.tmpQueue <- "tmp1.png"
	s.queue <- "perm1.png"
	s.tmpQueue <- "tmp2.png"
	s.queue <- "perm2.png"

	var got []string
	for i := 0; i < 4; i++ {
		p, persist := s.next()
		got = append(got, p)
		if persist != strings.HasPrefix(p, "perm") {
			t.Errorf("%s 的 persist 标记不对: %v", p, persist)
		}
	}
	if got[0] != "perm1.png" || got[1] != "perm2.png" {
		t.Errorf("应先取完持久层队列，实际顺序 %v", got)
	}
}

// ── 视频 / 音频封面 ───────────────────────────────────────────

// runFFmpeg 用 ffmpeg 合成测试素材；做不出来（缺编码器等）就跳过，不算失败。
func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("没有 ffmpeg")
	}
	if out, err := exec.Command(ff, append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Skipf("无法合成测试素材（%v）: %s", err, out)
	}
}

func needProbe(t *testing.T, s *Service) {
	t.Helper()
	if s.ffprobe == "" {
		t.Skip("没有 ffprobe")
	}
}

// 纯红色封面图
func writeRedCover(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "cover.png")
	runFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=200x200:d=1", "-frames:v", "1", p)
	return p
}

func decodeJPEG(t *testing.T, p string) image.Image {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("不是合法 JPEG: %v", err)
	}
	return img
}

func isRedish(img image.Image) bool {
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	return r>>8 > 170 && g>>8 < 90 && bl>>8 < 90
}

func TestFrameTime(t *testing.T) {
	cases := []struct{ dur, want float64 }{
		{0, 1}, {-5, 1}, {1.5, 0}, {5, 1}, {30, 3}, {200, 10}, {7200, 10},
	}
	for _, c := range cases {
		if got := frameTime(c.dur); got != c.want {
			t.Errorf("frameTime(%v) = %v, want %v", c.dur, got, c.want)
		}
	}
}

// 没有封面的视频：取画面里的一帧，尺寸按缩略图规格缩小。
func TestVideoWithoutCoverUsesAFrame(t *testing.T) {
	s := newTestService(t)
	needProbe(t, s)
	dir := t.TempDir()
	src := filepath.Join(dir, "plain.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=3:size=640x480:rate=10", "-pix_fmt", "yuv420p", src)

	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, out)
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 192 {
		t.Errorf("640x480 缩到长边 256 应为 256x192，实际 %v", b)
	}
	if isRedish(img) {
		t.Error("不该是封面图的颜色，应取的是视频画面")
	}
}

// 带内嵌封面的视频：用封面，而不是视频画面。
func TestVideoPrefersEmbeddedCover(t *testing.T) {
	s := newTestService(t)
	needProbe(t, s)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=3:size=640x480:rate=10", "-pix_fmt", "yuv420p", plain)
	cover := writeRedCover(t, dir)
	withCover := filepath.Join(dir, "withcover.mp4")
	runFFmpeg(t, "-i", plain, "-i", cover, "-map", "0", "-map", "1", "-c", "copy", "-disposition:v:1", "attached_pic", withCover)

	out, err := s.Get(context.Background(), withCover, 256)
	if err != nil {
		t.Fatal(err)
	}
	if img := decodeJPEG(t, out); !isRedish(img) {
		t.Error("带封面的视频应使用封面图（红色），实际不是")
	}
}

// 极短的视频（不足 2 秒）也能取到画面，不会因为 seek 过头而失败。
func TestVeryShortVideo(t *testing.T) {
	s := newTestService(t)
	needProbe(t, s)
	src := filepath.Join(t.TempDir(), "short.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=duration=0.5:size=320x240:rate=10", "-pix_fmt", "yuv420p", src)
	out, err := s.Get(context.Background(), src, 256)
	if err != nil {
		t.Fatal(err)
	}
	decodeJPEG(t, out)
}

// 音频：没有封面 → 不支持（且不会反复重试）；有封面 → 用封面。
func TestAudioCover(t *testing.T) {
	s := newTestService(t)
	needProbe(t, s)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.flac")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=1", plain)

	if _, err := s.Get(context.Background(), plain, 256); err != ErrUnsupported {
		t.Fatalf("没有封面的音频应返回 ErrUnsupported，实际 %v", err)
	}
	if _, err := s.Get(context.Background(), plain, 256); err != ErrUnsupported {
		t.Fatalf("第二次也应是 ErrUnsupported（失败已被记住），实际 %v", err)
	}
	if n := countFiles(s.tmpDir) + countFiles(s.dir); n != 0 {
		t.Errorf("没有封面时不该产生任何文件，实际 %d", n)
	}

	cover := writeRedCover(t, dir)
	withCover := filepath.Join(dir, "withcover.flac")
	runFFmpeg(t, "-i", plain, "-i", cover, "-map", "0", "-map", "1", "-c", "copy", "-disposition:v:0", "attached_pic", withCover)
	out, err := s.Get(context.Background(), withCover, 256)
	if err != nil {
		t.Fatal(err)
	}
	if img := decodeJPEG(t, out); !isRedish(img) {
		t.Error("带封面的音频应使用封面图（红色）")
	}
}

// 服务器上没有 ffprobe：视频/音频没有缩略图，但图片完全不受影响。
func TestNoFFprobeOnlyAffectsMedia(t *testing.T) {
	s := newTestService(t)
	s.ffprobe = ""
	dir := t.TempDir()
	vid := filepath.Join(dir, "v.mp4")
	_ = os.WriteFile(vid, []byte("not really a video"), 0o644)
	if _, err := s.Get(context.Background(), vid, 256); err != ErrUnsupported {
		t.Errorf("没有 ffprobe 时视频应返回 ErrUnsupported，实际 %v", err)
	}
	img := filepath.Join(dir, "a.png")
	writeAlphaPNG(t, img)
	if _, err := s.Get(context.Background(), img, 256); err != nil {
		t.Errorf("图片不应受影响: %v", err)
	}
}

// ── 持久层来源登记与清理 ──────────────────────────────────────

// memRegistry 内存版来源登记，只用于测试；Scan 按键排序后分批返回，以便覆盖多批的路径。
type memRegistry struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemRegistry() *memRegistry { return &memRegistry{m: map[string]string{}} }

func (r *memRegistry) Put(_ context.Context, member, src string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[member] = src
	return nil
}

func (r *memRegistry) Scan(_ context.Context, cursor uint64, count int) (map[string]string, uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.m))
	for k := range r.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	start := int(cursor)
	if start >= len(keys) {
		return map[string]string{}, 0, nil
	}
	end := start + count
	next := uint64(end)
	if end >= len(keys) {
		end, next = len(keys), 0
	}
	out := map[string]string{}
	for _, k := range keys[start:end] {
		out[k] = r.m[k]
	}
	return out, next, nil
}

func (r *memRegistry) Delete(_ context.Context, members ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range members {
		delete(r.m, m)
	}
	return nil
}

func (r *memRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}

// 用一个有内容的目录充当「健康的存储根」，让 storageHealthy 通过；测试结束自动恢复。
func withHealthyStorage(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "marker"), []byte("x"), 0o644)
	old := config.Conf.File.AllowedPaths
	config.Conf.File.AllowedPaths = []string{root}
	t.Cleanup(func() { config.Conf.File.AllowedPaths = old })
}

func TestWidthOfMember(t *testing.T) {
	cases := map[string]int{"ab/abcdef_256.jpg": 256, "ab/abcdef_128.jpg": 128, "ab/abcdef.jpg": 0, "x_notnum.jpg": 0, "": 0}
	for in, want := range cases {
		if got := widthOfMember(in); got != want {
			t.Errorf("widthOfMember(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPersistentGenerationIsRegistered(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)
	if _, err := s.get(context.Background(), src, 256, true); err != nil {
		t.Fatal(err)
	}
	reg := s.reg.(*memRegistry)
	if reg.size() != 1 {
		t.Fatalf("持久层生成后应登记 1 条，实际 %d", reg.size())
	}
	for _, v := range reg.m {
		if v != src {
			t.Errorf("登记的来源应是 %s，实际 %s", src, v)
		}
	}
	// 临时层不登记
	other := filepath.Join(t.TempDir(), "b.png")
	writeAlphaPNG(t, other)
	if _, err := s.Get(context.Background(), other, 256); err != nil {
		t.Fatal(err)
	}
	if reg.size() != 1 {
		t.Errorf("临时层不该登记到持久层登记表，实际 %d", reg.size())
	}
}

// 源文件被改动后会生成新版缩略图，旧版成了孤儿：清理只删旧版，保留新版和没动过的。
func TestCleanPermRemovesStaleVersionKeepsCurrent(t *testing.T) {
	withHealthyStorage(t)
	s := newTestService(t)
	dir := t.TempDir()
	changed, untouched := filepath.Join(dir, "changed.png"), filepath.Join(dir, "untouched.png")
	writeAlphaPNG(t, changed)
	writeAlphaPNG(t, untouched)
	oldThumb, _ := s.get(context.Background(), changed, 256, true)
	keep, _ := s.get(context.Background(), untouched, 256, true)

	later := time.Now().Add(time.Hour) // 模拟文件被改动：修改时间变了，缓存键随之变化
	_ = os.Chtimes(changed, later, later)
	newThumb, err := s.get(context.Background(), changed, 256, true)
	if err != nil {
		t.Fatal(err)
	}
	if newThumb == oldThumb {
		t.Fatal("源文件改动后应生成新版缩略图")
	}

	if n := s.cleanPerm(context.Background()); n != 1 {
		t.Errorf("应只清理 1 个旧版，实际 %d", n)
	}
	if fileOK(oldThumb) {
		t.Error("旧版缩略图应已删除")
	}
	if !fileOK(newThumb) || !fileOK(keep) {
		t.Error("新版与未改动的缩略图不该被删")
	}
	if got := s.reg.(*memRegistry).size(); got != 2 {
		t.Errorf("登记表应剩 2 条（新版 + 未改动），实际 %d", got)
	}
}

func TestCleanPermRemovesThumbOfDeletedSource(t *testing.T) {
	withHealthyStorage(t)
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "gone.png")
	writeAlphaPNG(t, src)
	th, _ := s.get(context.Background(), src, 256, true)
	_ = os.Remove(src)

	if n := s.cleanPerm(context.Background()); n != 1 {
		t.Errorf("源文件已删应清理 1 个，实际 %d", n)
	}
	if fileOK(th) || s.reg.(*memRegistry).size() != 0 {
		t.Error("缩略图和登记都应已清除")
	}
}

// 存储盘没挂上时所有源文件都「不存在」，那不是被删了：整轮跳过，缓存原样保留。
func TestCleanPermSkippedWhenStorageUnavailable(t *testing.T) {
	s := newTestService(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writeAlphaPNG(t, src)
	th, _ := s.get(context.Background(), src, 256, true)
	_ = os.Remove(src)

	old := config.Conf.File.AllowedPaths
	config.Conf.File.AllowedPaths = []string{t.TempDir()} // 存在但是空的：模拟没挂载上的盘
	defer func() { config.Conf.File.AllowedPaths = old }()

	if n := s.cleanPerm(context.Background()); n != 0 {
		t.Errorf("存储不可用时不该清理任何东西，实际 %d", n)
	}
	if !fileOK(th) {
		t.Error("缩略图不该被删")
	}
}

// 登记内容来自 Redis，不能信任：指向持久目录之外的条目只清登记，绝不删外面的文件。
func TestCleanPermNeverDeletesOutsidePermDir(t *testing.T) {
	withHealthyStorage(t)
	s := newTestService(t)
	outside := filepath.Join(filepath.Dir(s.dir), "precious.txt")
	_ = os.WriteFile(outside, []byte("keep me"), 0o644)
	defer os.Remove(outside)
	reg := s.reg.(*memRegistry)
	_ = reg.Put(context.Background(), "../"+filepath.Base(outside), filepath.Join(t.TempDir(), "missing_source.png"))

	s.cleanPerm(context.Background())
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("持久目录之外的文件被误删了")
	}
	if reg.size() != 0 {
		t.Errorf("非法条目应从登记表清掉，剩 %d", reg.size())
	}
}

// 多批扫描（登记表比一批大）也要清干净，不能只处理第一批。
func TestCleanPermHandlesMultipleBatches(t *testing.T) {
	withHealthyStorage(t)
	s := newTestService(t)
	reg := s.reg.(*memRegistry)
	gone := filepath.Join(t.TempDir(), "gone.png")
	for i := 0; i < 1203; i++ {
		_ = reg.Put(context.Background(), fmt.Sprintf("ab/k%04d_256.jpg", i), gone)
	}
	s.cleanPerm(context.Background())
	if reg.size() != 0 {
		t.Errorf("1203 条失效登记应全部清掉，剩 %d", reg.size())
	}
}

// 登记功能上线前就生成的缩略图：启动回填时补登记，之后才纳入定时清理。
func TestBackfillRegistersPreexistingThumbs(t *testing.T) {
	s := newTestService(t)
	reg := s.reg
	s.reg = nil // 模拟「登记功能上线之前」
	root := t.TempDir()
	src := filepath.Join(root, "a.png")
	writeAlphaPNG(t, src)
	if _, err := s.get(context.Background(), src, 256, true); err != nil {
		t.Fatal(err)
	}
	s.reg = reg

	s.Backfill(root)
	if reg.(*memRegistry).size() != 1 {
		t.Errorf("回填应为已有缩略图补登记，实际 %d", reg.(*memRegistry).size())
	}
	if len(s.queue) != 0 {
		t.Errorf("已有缩略图不该重新排队，实际 %d", len(s.queue))
	}
}
