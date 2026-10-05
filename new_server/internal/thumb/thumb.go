// Package thumb 图片缩略图：调 ffmpeg 生成小尺寸 JPEG，落盘缓存。
//
// 用途：客户端在外网/流量下，列表、最近下载等「点进详情之前」的场景只拉几 KB 的缩略图，
// 而不是几 MB 的原图。缩略图分两层，生成时机和生命周期不同：
//
//	持久层（Dir）：上传 / 同步进来的文件。落盘后异步生成（Enqueue），启动时回填已有的（Backfill），
//	             长期保留——这些文件是系统管理的，反复会被看到。
//	临时层（TempDir）：磁盘上原本就有的文件（既没上传也没同步），只在用户浏览目录时才用得到。
//	             浏览哪个目录就为那一页里的图片生成（EnqueueTemp），文件放临时目录，
//	             到期时间记在 Redis 的有序集合里，后台定时销毁；被再次访问会顺延，常看的不会过期。
//
// 请求缩略图时（Get）先找持久层，再找临时层，都没有就当场生成到临时层。
// 缓存按「源路径 + 修改时间 + 大小 + 宽度」寻址：源文件一改，缓存自然失效，无需主动清理。
package thumb

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"syc-file/config"
	"syc-file/pkg/logger"
	"syc-file/pkg/volroot"
)

// 允许的缩略图宽度（长边上限，像素）。白名单是为了限制缓存规模：不允许客户端任意指定尺寸。
var widths = []int{128, 256, 512}

const (
	defaultWidth   = 256
	defaultWorkers = 8
	queueSize      = 4096
	genTimeout     = 60 * time.Second

	defaultTempTTL = 12 * time.Hour
	janitorEvery   = 10 * time.Minute
	permCleanEvery = 6 * time.Hour
	// 临时层只处理不超过这个大小的原图：浏览到一张几个 GB 的 TIFF 不该占满 ffmpeg
	tempMaxSource = 200 << 20
)

// 可生成缩略图的扩展名（小写，带点）。ffmpeg 解码不了的会生成失败，并被记入失败集合不再重试。
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
	".webp": true, ".tif": true, ".tiff": true,
}

// 视频：优先用文件自带的封面图（容器里 attached_pic 的那路流），没有就在片长约 10% 处取一帧。
// 刻意不含 .ts：它更多是 TypeScript 源文件，把代码目录里的 .ts 都送去 ffprobe 只会白白失败。
var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".webm": true, ".mov": true, ".avi": true, ".flv": true,
	".m4v": true, ".wmv": true, ".3gp": true, ".mpg": true, ".mpeg": true,
}

// 音频：只取内嵌的专辑封面；没有封面的音频没有缩略图可言，直接判为不支持。
var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".ogg": true, ".opus": true, ".wma": true,
}

type mediaKind int

const (
	kindNone mediaKind = iota
	kindImage
	kindVideo
	kindAudio
)

func kindOf(name string) mediaKind {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case imageExts[ext]:
		return kindImage
	case videoExts[ext]:
		return kindVideo
	case audioExts[ext]:
		return kindAudio
	}
	return kindNone
}

// ErrUnsupported 不是可生成缩略图的文件类型，或此前已确认 ffmpeg 无法处理它。
var ErrUnsupported = errors.New("thumb: unsupported file")

// Service 缩略图服务。零值不可用，用 Init 创建。
type Service struct {
	dir          string // 持久层
	tmpDir       string // 临时层
	tmpTTL       time.Duration
	idx          Index    // 临时层到期索引；nil 时只靠文件修改时间兜底清理
	reg          Registry // 持久层来源登记；nil 时不清理持久层的孤儿
	ffmpeg       string
	ffprobe      string // 视频/音频要用它找封面流和片长；为空则视频/音频没有缩略图，图片不受影响
	defaultWidth int

	sem      chan struct{} // 并发上限：同步请求与后台 worker 共用同一个，总并发不会超
	queue    chan string   // 持久层生成队列（上传/同步/回填），优先级高
	tmpQueue chan string   // 临时层生成队列（浏览目录触发）

	mu         sync.Mutex
	inflight   map[string]*call // 同一个缩略图同时被多处请求时只生成一次
	failed     sync.Map         // 缓存键 → struct{}：生成失败过的，不重复烧 CPU
	pendingTmp sync.Map         // 已在临时队列里的源路径，避免反复翻页/刷新把同一张图排很多遍
}

type call struct {
	done chan struct{}
	err  error
}

// Global 进程级单例；未启用或未初始化时为 nil，包级函数对 nil 安全。
var Global *Service

// Init 按配置创建并启动服务。Disabled、找不到 ffmpeg、没有可用存储目录时返回 (nil, nil)，
// 这些都是「功能关闭」而不是错误：缩略图只是加速手段，不该影响主流程启动。
// rdb 用于临时层的到期索引；传 nil 则临时层不记索引，只靠文件修改时间兜底清理。
func Init(cfg config.ThumbnailConfig, rdb *redis.Client) (*Service, error) {
	if cfg.Disabled {
		logger.Logger.Info("缩略图功能已在配置中关闭")
		return nil, nil
	}
	bin := cfg.FFmpeg
	if bin == "" {
		bin = "ffmpeg"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		logger.Logger.Warn("未找到 ffmpeg，缩略图功能不可用（Docker 镜像已内置；裸机请安装 ffmpeg 或配置 thumbnail.ffmpeg）",
			zap.String("ffmpeg", bin))
		return nil, nil
	}
	probe := cfg.FFprobe
	if probe == "" {
		// ffprobe 与 ffmpeg 同包发行，先找同目录下的，再找 PATH
		probe = filepath.Join(filepath.Dir(path), "ffprobe")
		if _, err := os.Stat(probe); err != nil {
			probe = "ffprobe"
		}
	}
	if p, err := exec.LookPath(probe); err == nil {
		probe = p
	} else {
		probe = ""
		logger.Logger.Warn("未找到 ffprobe，视频/音频封面缩略图不可用（图片缩略图不受影响）")
	}
	dir, tmpDir := cfg.Dir, cfg.TempDir
	if dir == "" || tmpDir == "" {
		if len(config.Conf.File.AllowedPaths) == 0 {
			logger.Logger.Warn("没有配置 file.allowed_paths，无法确定缩略图目录，缩略图功能不可用")
			return nil, nil
		}
		st := config.Conf.File.Storage
		vol := volroot.RootOf(config.Conf.File.AllowedPaths[0])
		if dir == "" {
			dir = filepath.Join(vol, st.BasePath, "thumbs")
		}
		if tmpDir == "" {
			// 放在已有的临时目录下：运维一看就知道这里的东西是可随时清掉的
			tempName := st.TempPath
			if tempName == "" {
				tempName = "temp"
			}
			tmpDir = filepath.Join(vol, st.BasePath, tempName, "thumbs")
		}
	}
	for _, d := range []string{dir, tmpDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("创建缩略图目录失败: %w", err)
		}
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	ttl := time.Duration(cfg.TempTTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = defaultTempTTL
	}
	s := &Service{
		dir:          dir,
		tmpDir:       tmpDir,
		tmpTTL:       ttl,
		ffmpeg:       path,
		ffprobe:      probe,
		defaultWidth: normalizeWidth(cfg.DefaultWidth),
		sem:          make(chan struct{}, workers),
		queue:        make(chan string, queueSize),
		tmpQueue:     make(chan string, queueSize),
		inflight:     make(map[string]*call),
	}
	if rdb != nil {
		s.idx = NewRedisIndex(rdb)
		s.reg = NewRedisRegistry(rdb)
	}
	for i := 0; i < workers; i++ {
		go s.worker()
	}
	go s.janitorLoop()
	Global = s
	logger.Logger.Info("缩略图服务已启动", zap.String("dir", dir), zap.String("temp_dir", tmpDir),
		zap.Duration("temp_ttl", ttl), zap.Int("workers", workers),
		zap.Int("default_width", s.defaultWidth), zap.Int("cpus", runtime.NumCPU()),
		zap.Bool("redis_index", s.idx != nil))
	return s, nil
}

// Supported 按扩展名判断是否可能有缩略图（图片、视频、带封面的音频）。
// 只是扩展名层面的快速判断：视频/音频还取决于文件里有没有封面、服务器上有没有 ffprobe，最终以 Get 的结果为准。
func Supported(name string) bool {
	return kindOf(name) != kindNone
}

// normalizeWidth 把请求宽度归到白名单里最近的一档（向上取）：<=0 用默认，超过最大档按最大档。
func normalizeWidth(w int) int {
	if w <= 0 {
		return defaultWidth
	}
	for _, v := range widths {
		if w <= v {
			return v
		}
	}
	return widths[len(widths)-1]
}

// cacheKey 缓存键：源路径 + 修改时间 + 大小。宽度另算在文件名里，同一张图的各档缩略图共用目录前缀。
func cacheKey(fullPath string, fi fs.FileInfo) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", filepath.ToSlash(fullPath), fi.ModTime().UnixNano(), fi.Size())))
	return hex.EncodeToString(h[:])
}

// thumbName 缩略图相对路径（两层共用同一命名）：<键前两位>/<键>_<宽度>.jpg。
// 临时层里它同时是 Redis 到期索引的成员名（用正斜杠，与平台无关）。
func thumbName(key string, width int) string {
	return key[:2] + "/" + fmt.Sprintf("%s_%d.jpg", key, width)
}

func (s *Service) cachePath(key string, width int) string {
	return filepath.Join(s.dir, filepath.FromSlash(thumbName(key, width)))
}

func (s *Service) tempPath(key string, width int) string {
	return filepath.Join(s.tmpDir, filepath.FromSlash(thumbName(key, width)))
}

func fileOK(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

// Get 返回 fullPath 的缩略图文件路径：先持久层、再临时层，都没有就当场生成到临时层
// （同步，受 ctx 约束）。返回的文件只读，调用方不得修改或删除。
func (s *Service) Get(ctx context.Context, fullPath string, width int) (string, error) {
	return s.get(ctx, fullPath, width, false)
}

// get 的 persist=true 表示这是上传/同步的文件，生成到持久层；否则走临时层。
func (s *Service) get(ctx context.Context, fullPath string, width int, persist bool) (string, error) {
	kind := kindOf(fullPath)
	if s == nil || kind == kindNone {
		return "", ErrUnsupported
	}
	if kind != kindImage && s.ffprobe == "" {
		return "", ErrUnsupported
	}
	fi, err := os.Stat(fullPath)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", ErrUnsupported
	}
	w := normalizeWidth(width)
	key := cacheKey(fullPath, fi)

	perm := s.cachePath(key, w)
	if fileOK(perm) {
		return perm, nil
	}
	if _, bad := s.failed.Load(key); bad {
		return "", ErrUnsupported
	}
	if persist {
		if err := s.generateOnce(ctx, key, fullPath, perm, w); err != nil {
			return "", err
		}
		s.register(ctx, key, w, fullPath)
		return perm, nil
	}

	// 大小上限只针对图片：解码要把整张图读进内存。视频按索引定位取帧，大小无所谓
	if kind == kindImage && fi.Size() > tempMaxSource {
		return "", ErrUnsupported
	}
	tmp := s.tempPath(key, w)
	if !fileOK(tmp) {
		if err := s.generateOnce(ctx, key, fullPath, tmp, w); err != nil {
			return "", err
		}
	}
	s.touchTemp(ctx, key, w, tmp)
	return tmp, nil
}

// register 登记持久缩略图的来源，供定时清理判断它是否成了孤儿。登记失败不影响缩略图本身。
func (s *Service) register(ctx context.Context, key string, width int, src string) {
	if s.reg == nil {
		return
	}
	if err := s.reg.Put(ctx, thumbName(key, width), src); err != nil {
		logger.Logger.Debug("登记持久缩略图来源失败", zap.Error(err))
	}
}

// touchTemp 给临时缩略图续期：每次被用到都把到期时间顺延到「现在 + TTL」，常看的不会过期。
// 同时刷新文件修改时间——Redis 不可用或索引丢了时，清理兜底就靠它。
func (s *Service) touchTemp(ctx context.Context, key string, width int, path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	if s.idx == nil {
		return
	}
	if err := s.idx.Touch(ctx, thumbName(key, width), now.Add(s.tmpTTL)); err != nil {
		logger.Logger.Debug("记录临时缩略图到期时间失败", zap.Error(err))
	}
}

// generateOnce 同一个目标并发请求只跑一次 ffmpeg，其余等它的结果。
func (s *Service) generateOnce(ctx context.Context, key, src, out string, width int) error {
	s.mu.Lock()
	if c, ok := s.inflight[out]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	s.inflight[out] = c
	s.mu.Unlock()

	c.err = s.generate(ctx, key, src, out, width)

	s.mu.Lock()
	delete(s.inflight, out)
	s.mu.Unlock()
	close(c.done)
	return c.err
}

func (s *Service) generate(ctx context.Context, key, src, out string, width int) error {
	// 受并发上限约束；排队期间 ctx 取消就别再起 ffmpeg 了
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	// 先写临时文件再改名：读取方永远看不到写了一半的缩略图。临时名保留 .jpg 结尾，ffmpeg 靠它认格式
	tmp := strings.TrimSuffix(out, ".jpg") + fmt.Sprintf(".tmp%d.jpg", time.Now().UnixNano())
	defer os.Remove(tmp)

	attempts, err := s.plan(ctx, src, tmp, width)
	if err != nil {
		if errors.Is(err, ErrUnsupported) { // 确认没有可用的画面（如没有封面的音频）：记下来别再重试
			s.failed.Store(key, struct{}{})
		}
		return err
	}

	var lastErr error
	var lastOut []byte
	for _, args := range attempts {
		cctx, cancel := context.WithTimeout(ctx, genTimeout)
		outp, runErr := exec.CommandContext(cctx, s.ffmpeg, args...).CombinedOutput()
		cancel()
		if runErr == nil && fileOK(tmp) {
			return os.Rename(tmp, out)
		}
		if ctx.Err() != nil { // 调用方取消，不是文件的问题
			return ctx.Err()
		}
		lastErr, lastOut = runErr, outp
		if lastErr == nil {
			lastErr = errors.New("ffmpeg 没有产出文件")
		}
		_ = os.Remove(tmp)
	}
	// 所有尝试都失败：ffmpeg 自己处理不了这个文件，记下来别再重试
	s.failed.Store(key, struct{}{})
	logger.Logger.Warn("生成缩略图失败", zap.String("src", src), zap.Error(lastErr),
		zap.String("ffmpeg", strings.TrimSpace(string(lastOut))))
	return fmt.Errorf("thumb: ffmpeg: %w", lastErr)
}

// plan 决定要跑哪些 ffmpeg 命令（按顺序尝试，第一个出图的算数）。
//   - 图片：一条命令。
//   - 视频：有内嵌封面就用封面；没有就在片长约 10% 处取一帧，取不到再退回第 0 秒。
//   - 音频：只认内嵌封面，没有则 ErrUnsupported。
func (s *Service) plan(ctx context.Context, src, dst string, width int) ([][]string, error) {
	kind := kindOf(src)
	if kind == kindImage {
		return [][]string{ffmpegArgs(src, dst, width)}, nil
	}
	info, err := s.probe(ctx, src)
	if err != nil {
		return nil, err
	}
	if info.coverIdx >= 0 {
		return [][]string{streamArgs(src, dst, width, strconv.Itoa(info.coverIdx), -1)}, nil
	}
	if kind == kindAudio || !info.hasVideo {
		return nil, ErrUnsupported
	}
	at := frameTime(info.duration)
	attempts := [][]string{streamArgs(src, dst, width, "v:0", at)}
	if at > 0 {
		attempts = append(attempts, streamArgs(src, dst, width, "v:0", 0))
	}
	return attempts, nil
}

// frameTime 取帧位置（秒）：片长的 10%，限制在 [1, 10] 秒之间——开头常是黑屏/片头，也别跑太远；
// 片长不足 2 秒或未知就取第 0 秒/1 秒附近，别 seek 到文件之外。
func frameTime(duration float64) float64 {
	switch {
	case duration <= 0:
		return 1
	case duration < 2:
		return 0
	}
	t := duration * 0.1
	if t < 1 {
		t = 1
	}
	if t > 10 {
		t = 10
	}
	return t
}

type probeInfo struct {
	duration float64 // 秒；未知为 0
	coverIdx int     // 内嵌封面那路流的序号；没有为 -1
	hasVideo bool    // 是否有（非封面的）视频流
}

// probe 用 ffprobe 看文件里有哪些流、片长多少、哪一路是封面（disposition.attached_pic）。
func (s *Service) probe(ctx context.Context, src string) (probeInfo, error) {
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, s.ffprobe, "-v", "error", "-print_format", "json",
		"-show_entries", "format=duration:stream=index,codec_type:stream_disposition=attached_pic",
		"-i", src).Output()
	if err != nil {
		return probeInfo{}, fmt.Errorf("thumb: ffprobe: %w", err)
	}
	var r struct {
		Streams []struct {
			Index       int    `json:"index"`
			CodecType   string `json:"codec_type"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return probeInfo{}, fmt.Errorf("thumb: 解析 ffprobe 输出失败: %w", err)
	}
	info := probeInfo{coverIdx: -1}
	info.duration, _ = strconv.ParseFloat(r.Format.Duration, 64)
	for _, st := range r.Streams {
		if st.CodecType != "video" {
			continue
		}
		if st.Disposition.AttachedPic == 1 {
			if info.coverIdx < 0 {
				info.coverIdx = st.Index
			}
		} else {
			info.hasVideo = true
		}
	}
	return info, nil
}

// thumbFilter 缩放 + 白底合成 + 转 JPEG 色彩空间的滤镜链（不含输入/输出标签）。
// 缩到长边不超过 width（小图不放大）；透明区域合成到白底，否则透明 PNG/GIF 会变成黑底：
// 把缩好的画面复制一份、整幅涂白当底，再把原画面叠上去。
func thumbFilter(width int) string {
	return fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease,"+
		"split[a][b];[b]drawbox=c=white:t=fill[bg];[bg][a]overlay=format=auto,format=yuvj420p", width, width)
}

// streamArgs 从输入文件的指定流取一帧：stream 是 ffmpeg 流选择符（如 "3" 或 "v:0"），
// seek >= 0 时先定位到该秒数（放在 -i 之前是按索引快速定位，不用从头解码）。
// 滤镜输入显式写成 [0:流]：不写的话 ffmpeg 会把它接到「第一路视频流」上，选了封面流也会被忽略。
func streamArgs(src, dst string, width int, stream string, seek float64) []string {
	args := []string{"-v", "error", "-nostdin", "-y"}
	if seek >= 0 {
		args = append(args, "-ss", strconv.FormatFloat(seek, 'f', 3, 64))
	}
	return append(args,
		"-i", src,
		"-frames:v", "1",
		"-filter_complex", "[0:"+stream+"]"+thumbFilter(width)+"[v]",
		"-map", "[v]",
		"-c:v", "mjpeg", "-q:v", "5",
		"-f", "image2",
		dst,
	)
}

// ffmpegArgs 图片的命令行：单帧输入，滤镜输入不需要显式标签。动图只取第一帧。
func ffmpegArgs(src, dst string, width int) []string {
	return []string{
		"-v", "error", "-nostdin", "-y",
		"-i", src,
		"-frames:v", "1",
		"-filter_complex", thumbFilter(width),
		"-c:v", "mjpeg", "-q:v", "5",
		"-f", "image2",
		dst,
	}
}

// Enqueue 异步生成默认宽度的持久缩略图（上传/同步落盘的文件）。尽力而为：队列满了就丢——
// 请求到来时 Get 还会兜底生成。对 nil 服务、非图片安全，可以在任何落盘点无脑调用。
func Enqueue(fullPath string) {
	s := Global
	if s == nil || !Supported(fullPath) {
		return
	}
	select {
	case s.queue <- fullPath:
	default:
	}
}

// EnqueueTemp 异步为浏览到的图片生成临时缩略图：目录列表返回哪些文件，就传哪些进来。
// 尽力而为且低优先级：持久层队列里有活时 worker 先干那些；队列满了就丢，用户真看到时 Get 会当场生成。
// 同一张图已经在排队就不重复排。对 nil 服务、非图片安全。
func EnqueueTemp(paths ...string) {
	s := Global
	if s == nil {
		return
	}
	for _, p := range paths {
		if !Supported(p) {
			continue
		}
		if _, queued := s.pendingTmp.LoadOrStore(p, struct{}{}); queued {
			continue
		}
		select {
		case s.tmpQueue <- p:
		default:
			s.pendingTmp.Delete(p)
		}
	}
}

// next 取下一个要生成的文件，持久层队列优先：先非阻塞地看一眼持久队列，空了才同时等两个队列。
// persist 表示它来自持久层队列。
func (s *Service) next() (path string, persist bool) {
	select {
	case p := <-s.queue:
		return p, true
	default:
	}
	select {
	case p := <-s.queue:
		return p, true
	case p := <-s.tmpQueue:
		return p, false
	}
}

func (s *Service) worker() {
	for {
		p, persist := s.next()
		s.process(p, persist)
	}
}

func (s *Service) process(p string, persist bool) {
	ctx, cancel := context.WithTimeout(context.Background(), genTimeout+10*time.Second)
	defer cancel()
	_, err := s.get(ctx, p, s.defaultWidth, persist)
	if !persist {
		s.pendingTmp.Delete(p)
	}
	if err != nil && !errors.Is(err, ErrUnsupported) && !errors.Is(err, os.ErrNotExist) {
		logger.Logger.Debug("后台生成缩略图失败", zap.String("path", p), zap.Bool("persist", persist), zap.Error(err))
	}
}

// ── 临时层清理 ────────────────────────────────────────────────

func (s *Service) janitorLoop() {
	time.Sleep(30 * time.Second) // 等启动期的回填、预热先跑起来
	lastPerm := time.Time{}
	for {
		now := time.Now()
		s.cleanTemp(context.Background(), now)
		if now.Sub(lastPerm) >= permCleanEvery {
			lastPerm = now
			s.cleanPerm(context.Background())
		}
		time.Sleep(janitorEvery)
	}
}

// cleanTemp 一轮清理：先按 Redis 索引销毁到期的，再按文件修改时间扫一遍兜底。
func (s *Service) cleanTemp(ctx context.Context, now time.Time) {
	expired := s.purgeExpired(ctx, now)
	orphans := s.sweepOrphans(now)
	if expired+orphans > 0 {
		logger.Logger.Info("已清理过期的临时缩略图", zap.Int("expired", expired), zap.Int("orphans", orphans))
	}
}

// purgeExpired 销毁索引里到期的临时缩略图（删文件 + 删索引），返回删除的文件数。
func (s *Service) purgeExpired(ctx context.Context, now time.Time) int {
	if s.idx == nil {
		return 0
	}
	removed := 0
	for {
		members, err := s.idx.Due(ctx, now, 500)
		if err != nil {
			logger.Logger.Debug("读取临时缩略图到期索引失败", zap.Error(err))
			return removed
		}
		if len(members) == 0 {
			return removed
		}
		for _, m := range members {
			p := filepath.Join(s.tmpDir, filepath.FromSlash(m))
			// 索引内容来自 Redis，不能无条件信任：只删临时目录之内的东西
			if rel, err := filepath.Rel(s.tmpDir, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if os.Remove(p) == nil {
				removed++
			}
		}
		if err := s.idx.Remove(ctx, members...); err != nil {
			logger.Logger.Debug("清理临时缩略图索引失败", zap.Error(err))
			return removed
		}
	}
}

// sweepOrphans 兜底：删掉索引管不到的残留——Redis 被清空或不可用期间生成的、进程中途退出留下的临时文件。
// 判据是文件修改时间（每次被用到都会刷新）：超过 2 倍 TTL 没人碰过的，或生成到一半的 .tmp 超过 10 分钟的。
func (s *Service) sweepOrphans(now time.Time) int {
	removed := 0
	_ = filepath.WalkDir(s.tmpDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		age := now.Sub(fi.ModTime())
		if (strings.Contains(d.Name(), ".tmp") && age > 10*time.Minute) || age > 2*s.tmpTTL {
			if os.Remove(p) == nil {
				removed++
			}
		}
		return nil
	})
	return removed
}

// ── 持久层清理 ────────────────────────────────────────────────

// cleanPerm 按来源登记清理持久层孤儿：源文件被删除、或被改动（缓存键已变，旧缩略图没人再用）的，
// 删缩略图文件并撤销登记。只处理登记过的——没登记的不知道来源，宁可留着也不误删。返回删除的文件数。
//
// 先完整扫一遍挑出失效项，扫完再统一删：边扫边删会改变被扫描的集合，
// 游标式扫描在不同实现下对此的保证不一样，不该让清理的正确性依赖它。
func (s *Service) cleanPerm(ctx context.Context) int {
	if s.reg == nil {
		return 0
	}
	// 存储盘没挂上时，所有源文件都会「不存在」：那不是被删了，整轮跳过，别把缓存清空
	if !storageHealthy() {
		logger.Logger.Warn("存储目录不可用，跳过本轮持久缩略图清理")
		return 0
	}

	var stale []string
	scanned := 0
	var cursor uint64
	for {
		entries, next, err := s.reg.Scan(ctx, cursor, 500)
		if err != nil {
			logger.Logger.Debug("读取持久缩略图登记失败", zap.Error(err))
			return 0
		}
		for member, src := range entries {
			scanned++
			if s.permStale(member, src) {
				stale = append(stale, member)
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}

	removed := 0
	var done []string // 文件已处理（删掉或本来就没有）的登记项，之后一并撤销登记
	for _, member := range stale {
		p := filepath.Join(s.dir, filepath.FromSlash(member))
		// 登记内容来自 Redis，不能无条件信任：只删持久目录之内的东西，越界的只清登记
		if rel, err := filepath.Rel(s.dir, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			done = append(done, member)
			continue
		}
		if err := os.Remove(p); err == nil {
			removed++
			done = append(done, member)
		} else if os.IsNotExist(err) {
			done = append(done, member)
		}
	}
	for len(done) > 0 {
		n := 500
		if len(done) < n {
			n = len(done)
		}
		if err := s.reg.Delete(ctx, done[:n]...); err != nil {
			logger.Logger.Debug("清理持久缩略图登记失败", zap.Error(err))
			break
		}
		done = done[n:]
	}
	if removed > 0 {
		logger.Logger.Info("已清理失效的持久缩略图", zap.Int("removed", removed), zap.Int("scanned", scanned))
	}
	return removed
}

// permStale 这张持久缩略图是否已失效：源文件没了，或源文件现在的缓存键与它不同（被改动过）。
func (s *Service) permStale(member, src string) bool {
	fi, err := os.Stat(src)
	if err != nil {
		return errors.Is(err, os.ErrNotExist) // 权限/IO 错误等拿不准的情况不动
	}
	if fi.IsDir() {
		return true
	}
	w := widthOfMember(member)
	if w == 0 {
		return false // 认不出宽度的条目不动
	}
	return thumbName(cacheKey(src, fi), w) != member
}

// widthOfMember 从缩略图相对路径（<前缀>/<键>_<宽度>.jpg）里取宽度，认不出返回 0。
func widthOfMember(member string) int {
	base := strings.TrimSuffix(member[strings.LastIndex(member, "/")+1:], ".jpg")
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return 0
	}
	w, err := strconv.Atoi(base[i+1:])
	if err != nil {
		return 0
	}
	return w
}

// storageHealthy 配置的存储根目录是否都能读到且非空。
func storageHealthy() bool {
	for _, p := range config.Conf.File.AllowedPaths {
		entries, err := os.ReadDir(p)
		if err != nil || len(entries) == 0 {
			return false
		}
	}
	return true
}

// Backfill 扫描 roots 下已有的图片，把还没有缩略图的排进持久层队列。服务启动时在后台调用一次，
// 之后新文件靠 Enqueue。跳过以点开头的目录（.synctmp / .syncpending 等同步暂存区）和缩略图目录本身。
func (s *Service) Backfill(roots ...string) {
	if s == nil {
		return
	}
	start := time.Now()
	queued, scanned := 0, 0
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && (strings.HasPrefix(d.Name(), ".") || p == s.dir || p == s.tmpDir) {
					return filepath.SkipDir
				}
				return nil
			}
			if !Supported(d.Name()) {
				return nil
			}
			scanned++
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			key := cacheKey(p, fi)
			if fileOK(s.cachePath(key, s.defaultWidth)) {
				// 已有缩略图：补一次登记，让这次登记功能上线之前生成的也纳入定时清理
				s.register(context.Background(), key, s.defaultWidth, p)
				return nil
			}
			// 队列满了就阻塞等：回填是后台任务，宁可慢一点也别丢
			s.queue <- p
			queued++
			return nil
		})
	}
	logger.Logger.Info("缩略图回填扫描完成", zap.Int("scanned", scanned), zap.Int("queued", queued),
		zap.Duration("elapsed", time.Since(start)))
}
