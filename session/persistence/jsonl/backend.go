// 本文件的作用：那个真的往盘上写字节的后端——它怎么配、一份存档怎么找到、
// 怎么读回来、怎么落地、怎么追加、怎么把一次崩溃修复落下去。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:117-987

package jsonl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// BackendName 是这个后端在编排层的诊断和收尾错误里露出来的名字。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:142
const BackendName = "session-persistence-jsonl"

// artifactFilename 是一份存档的逻辑文件名，和物理编码后缀无关。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:289-291
const artifactFilename = logBaseName + ".jsonl"

// DefaultPackChunks 是那个「把连着的增量分块压成一行」的开关的缺省值。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:39
const DefaultPackChunks = true

// DefaultCompression 是缺省的物理编码。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:40
//
// 新增: DSH 的缺省是 zstd。本包只写明文，所以缺省是明文——一个缺省就报错的
// 配置不是缺省。那一档为什么没移过来，逐项记在 docs/portmap/decisions.md。
const DefaultCompression = CompressionNone

// Config 是这个后端的部署配置。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:61-85
type Config struct {
	// Root 是所有会话文件的根目录，**必填**。
	//
	// 没有缺省值：拿 [os.Getwd] 当缺省会让会话文件跟着本进程的工作目录到处散
	// （一次 bash 调用、一个子进程就能改掉它）。已经存在的根必须是一个读得动
	// 的目录；不存在的根在第一次落地时建出来。
	Root string

	// PackChunks 决定连着的助手增量分块要不要压成一行存储记录；nil 表示用
	// [DefaultPackChunks]。
	//
	// 无损，一份真实会话上量出来小六成。关掉是一条事件一行，为的是好查。
	// **读那一侧和它无关**：一份日志的排布不取决于写它的那一刻这里是什么。
	//
	// 新增: 指针，因为「没给」和「明确给了 false」不是同一件事，而缺省是 true。
	PackChunks *bool

	// Compression 是物理编码；空串表示用 [DefaultCompression]。
	Compression Compression

	// PreparedSessionCacheSize 转给编排器，见
	// [persistence.CoordinatorOptions.PreparedSessionCacheSize]。
	PreparedSessionCacheSize int

	// WriteBatchMaxDelay 转给编排器，见
	// [persistence.CoordinatorOptions.WriteBatchMaxDelay]。
	WriteBatchMaxDelay time.Duration

	// Logger 收那些只在本进程里有意义的诊断；nil 就用 [slog.Default]。
	Logger *slog.Logger
}

// tornMarker 是这个后端交给编排器、又原样收回来的那张断尾凭据。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:87-91
//
// 编排器不看它里面有什么（[persistence.StoredPrefix.TornMarker] 是 any），
// 它唯一的用途是被递回 [Backend.CommitRepair]。
type tornMarker struct {
	// truncateTo 是坏尾巴从哪个字节开始。
	truncateTo int64
	// recoveredEvents 是从那截坏尾巴里捞回来、要在截断之后重新写下去的事件。
	//
	// 明文编码下它永远是空的：一条没有换行的记录就是没写完，捞不出东西来。
	// 这个字段是给那些按块封帧的物理编码留的——一个写坏的块里完全可能装着
	// 好几条完整的记录。
	recoveredEvents []session.Event
}

// Backend 是把会话日志写成盘上 JSONL 文件的持久化后端。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:117-123
//
// 它满足 [persistence.Backend] 和 [persistence.LocatingBackend]。要一个能直接用的
// [persistence.Store]，用 [New]。
//
// 它**不**满足 [persistence.SeekableBackend]：JSONL 是顺序介质，按 seq 寻址得先
// 把前面全解一遍，那正是编排层那条退路做的事，在这里再写一遍不会更便宜。
type Backend struct {
	root        string
	packChunks  bool
	compression Compression
	logger      *slog.Logger

	// rootEncodingOnce 保证「这个根是不是属于另一种物理编码」只查一次。
	rootEncodingOnce sync.Once
	rootEncodingErr  error
}

// 这两行钉住这个后端真的填满了那两道缝。
var (
	_ persistence.Backend         = (*Backend)(nil)
	_ persistence.LocatingBackend = (*Backend)(nil)
)

// NewBackend 造一个 JSONL 后端。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:150-166
//
// 根路径当场解成绝对路径：晚一点再解，本进程工作目录的一次改动就能把同一个
// 后端劈成两个根。
func NewBackend(config Config) (*Backend, error) {
	if config.Root == "" {
		return nil, errors.New("session/persistence/jsonl: 要一个存会话日志的根目录")
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("session/persistence/jsonl: 根目录 %q 解不成绝对路径：%w", config.Root, err)
	}
	compression := config.Compression
	if compression == "" {
		compression = DefaultCompression
	}
	if compression != CompressionNone {
		return nil, fmt.Errorf(
			"session/persistence/jsonl: 物理编码 %q 本包还没有搬过来（见 docs/portmap/decisions.md），只能用 %q",
			string(compression), string(CompressionNone))
	}
	packChunks := DefaultPackChunks
	if config.PackChunks != nil {
		packChunks = *config.PackChunks
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	backend := &Backend{root: root, packChunks: packChunks, compression: compression, logger: logger}
	if err := backend.assertUsableRoot(); err != nil {
		return nil, err
	}
	return backend, nil
}

// Name 是这个后端的名字。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:137-142
func (b *Backend) Name() string { return BackendName }

// Root 是这个后端存会话日志的那个根目录，已经解成绝对路径。
func (b *Backend) Root() string { return b.root }

// Locate 给出一个会话那份存档的绝对路径，不碰文件系统。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:173-176
//
// 第二个返回值为假只可能是因为这份头上的身份编不出一段路径来——那时候
// 「它在哪」这个问题本身就没有答案。
func (b *Backend) Locate(meta session.SessionHeader) (persistence.Location, bool) {
	path, err := logPath(b.root, meta.Cwd, meta.ID, b.compression)
	if err != nil {
		return persistence.Location{}, false
	}
	return persistence.Location{Kind: "jsonl", Path: path}, true
}

// LoadStored 按身份读出一份物理前缀，扫遍这个根下面所有的工程目录。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:218-226
func (b *Backend) LoadStored(
	ctx context.Context,
	id session.SessionID,
) (persistence.StoredPrefix, error) {
	path, err := b.locateExisting(ctx, id)
	if err != nil {
		return persistence.StoredPrefix{}, err
	}
	return b.readPrefix(ctx, path, id)
}

// ReadStoredRevision 只读出一个会话当前的变更令牌，不读它的事件字节。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:228-247
func (b *Backend) ReadStoredRevision(
	ctx context.Context,
	id session.SessionID,
) (persistence.Revision, error) {
	path, err := b.locateExisting(ctx, id)
	if err != nil {
		return "", err
	}
	identity, err := statIdentity(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", persistence.ErrSessionNotFound
	}
	if err != nil {
		return "", err
	}
	return identity.revision(), nil
}

// ReadRaw 逐字节原样读出一个会话那份存档的文本。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:249-292
//
// 交出来的是后端当初写下的那些字节，不是从解出来的事件重新拼的——压过的分块行、
// 键的顺序、换行方式因此逐字保住。要的就是这个保真度：它是给人看、给人比对的。
func (b *Backend) ReadRaw(ctx context.Context, id session.SessionID) (persistence.RawArtifact, error) {
	path, err := b.locateExisting(ctx, id)
	if err != nil {
		return persistence.RawArtifact{}, err
	}
	buffer, _, err := readStableFile(ctx, path)
	if err != nil {
		return persistence.RawArtifact{}, err
	}
	content := string(buffer)
	firstLine, _, _ := strings.Cut(content, "\n")
	meta, err := parseHeaderMeta([]byte(firstLine))
	if err != nil || meta.ID != id {
		return persistence.RawArtifact{}, fmt.Errorf(
			"session/persistence/jsonl: 会话日志 %q 坏了：第一行不是这个会话的头", path)
	}
	return persistence.RawArtifact{Meta: meta, Filename: artifactFilename, Content: content}, nil
}

// AppendBatch 把一批 seq 连续的事件持久化下去，materialized 为假时先把这个会话落地。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:431-439
func (b *Backend) AppendBatch(
	ctx context.Context,
	meta session.SessionHeader,
	events []session.Event,
	materialized bool,
) error {
	if err := b.ensureRootEncoding(ctx); err != nil {
		return err
	}
	if materialized {
		return b.appendLines(meta, events)
	}
	return b.materialize(meta, events)
}

// 这里没有 MaterializeHeader。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:441-444
//
// 上游那个 ensureMaterialized 是「让一条建了但还没落过事件的空会话自己在耐久
// 列举里露面」的唯一一条路。本仓库那道缝整条不在：[persistence.Backend] 上没有
// 这个方法，[persistence.Coordinator] 上也没有对应的操作，逐项理由记在
// docs/portmap/decisions.md 的 acp/acp 那一节。这里只在后端上长出一个没人调的
// 方法不会补上那道缝，只会多一处半成品——[Backend.materialize] 已经接得住
// 「一批事件为空」这种落地，等那道缝真的开出来时，接上去是一行。

// CommitRepair 把一次崩溃修复落盘：截掉坏尾巴，再把捞回来的事件和合成的收尾写下去。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:446-460
//
// 两步各自 fsync，**不要求原子**——这道缝本来就不要求（见
// [persistence.Backend.CommitRepair]）。截断之后崩掉，下一次装载看到的是一份更短
// 但依然自洽的日志，那次修复会原样重来：补出来的收尾是确定的。
func (b *Backend) CommitRepair(
	ctx context.Context,
	meta session.SessionHeader,
	torn any,
	closers []session.Event,
) error {
	path, err := logPath(b.root, meta.Cwd, meta.ID, b.compression)
	if err != nil {
		return err
	}
	marker, err := castTornMarker(torn)
	if err != nil {
		return err
	}
	repaired := closers
	if marker != nil {
		if err := truncateDurably(path, marker.truncateTo); err != nil {
			return err
		}
		repaired = make([]session.Event, 0, len(marker.recoveredEvents)+len(closers))
		repaired = append(repaired, marker.recoveredEvents...)
		repaired = append(repaired, closers...)
	}
	if len(repaired) > 0 {
		if err := b.appendLines(meta, repaired); err != nil {
			return err
		}
	}
	if marker != nil {
		b.logger.WarnContext(ctx, "会话从一截写坏的尾巴上恢复过来，那些没写完的字节被丢掉了",
			slog.String("backend", BackendName), slog.String("session", string(meta.ID)))
	}
	return nil
}

// castTornMarker 把编排器递回来的那张凭据认回来。
//
// nil 表示这次修复没有要截的尾巴。认不出来是**编排器**把别的后端的凭据递错了地方，
// 那时候截断一个由别人算出来的偏移量是最坏的选择。
func castTornMarker(torn any) (*tornMarker, error) {
	if torn == nil {
		return nil, nil
	}
	marker, ok := torn.(*tornMarker)
	if !ok {
		return nil, fmt.Errorf("session/persistence/jsonl: 收到一张不是本后端发出的断尾凭据（%T）", torn)
	}
	return marker, nil
}

// List 列出所有已落地会话的元数据，只读每份存档的头。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:462-465
func (b *Backend) List(ctx context.Context) ([]session.SessionHeader, error) {
	artifacts, err := b.listArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	headers := make([]session.SessionHeader, 0, len(artifacts))
	for _, artifact := range artifacts {
		headers = append(headers, artifact.header)
	}
	return headers, nil
}

// ListSnapshots 列出已落地的会话，各带一个便宜的变更令牌。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:467-486
//
// 列举和取令牌之间那份存档可能刚好被删掉；那不是错误，它只是不在了。
func (b *Backend) ListSnapshots(ctx context.Context) ([]persistence.Snapshot, error) {
	artifacts, err := b.listArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]persistence.Snapshot, 0, len(artifacts))
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, err := statIdentity(artifact.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, persistence.Snapshot{
			Header:   artifact.header,
			Revision: identity.revision(),
		})
	}
	return snapshots, nil
}

// ---- 读一份存档 ----

// readPrefix 读出一份物理前缀，并把断尾状态折成那张不透明的凭据。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:316-355
func (b *Backend) readPrefix(
	ctx context.Context,
	path string,
	expected session.SessionID,
) (persistence.StoredPrefix, error) {
	buffer, revision, err := readStableFile(ctx, path)
	if err != nil {
		return persistence.StoredPrefix{}, err
	}
	scan, err := scanLog(buffer)
	if err != nil {
		return persistence.StoredPrefix{}, b.locateParseRefusal(path, err)
	}
	if err := b.assertStoredIdentity(path, scan.meta, expected); err != nil {
		return persistence.StoredPrefix{}, err
	}
	prefix := persistence.StoredPrefix{Meta: scan.meta, Events: scan.events, Revision: revision}
	if scan.committedBytes < int64(len(buffer)) {
		prefix.TornMarker = &tornMarker{truncateTo: scan.committedBytes}
	}
	return prefix, nil
}

// locateParseRefusal 给一条在解析期发出的格式拒绝补上它拒的是哪份存档。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:342-350
//
// 这种拒绝发生在拿到 [session.SessionHeader] **之前**，所以编排层那条靠
// Locate 补位置的路走不通；这里把这次读真正拒掉的那份存档挂上去。
func (b *Backend) locateParseRefusal(path string, err error) error {
	var refusal *persistence.FormatUnsupportedError
	if !errors.As(err, &refusal) || refusal.Location != nil {
		return err
	}
	return refusal.WithLocation(persistence.Location{Kind: "jsonl", Path: path})
}

// ---- 落地、追加 ----

// materialize 原子地写下头那一行加第一批事件：写暂存、fsync、发布。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:529-542
//
// 落地和第一批必须是同一次提交（见 [persistence.Backend.AppendBatch]），
// 所以它们编在一段字节里、经由同一次发布落地。
func (b *Backend) materialize(meta session.SessionHeader, events []session.Event) error {
	finalPath, err := logPath(b.root, meta.Cwd, meta.ID, b.compression)
	if err != nil {
		return err
	}
	if err := b.rejectOppositeArtifact(meta.Cwd, meta.ID); err != nil {
		return err
	}
	content, err := b.encodeMaterialization(meta, events)
	if err != nil {
		return err
	}
	if err := ensureDurableDirectory(filepath.Dir(finalPath)); err != nil {
		return err
	}
	// 绝不往一份已经存在的日志上发布：落地是这个后端认定为新的那个会话的第一次
	// 写。这里有文件就说明盘上另有一个会话和它撞了号——那必须喊出来。
	// 编排层建会话那一步已经挡过一道，所以这里挡的是那之后的 TOCTOU。
	present, err := exists(finalPath)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf(
			"session/persistence/jsonl: 不落地 %q：盘上已经有一份它的日志了（该装载或者续跑它）",
			string(meta.ID))
	}
	staging, err := writeSyncedTempFile(finalPath, content)
	if err != nil {
		return err
	}
	if err := publishNewFile(staging, finalPath); err != nil {
		_ = os.Remove(staging)
		return err
	}
	return nil
}

// encodeMaterialization 编出头和第一批事件。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:634-645
func (b *Backend) encodeMaterialization(
	meta session.SessionHeader,
	events []session.Event,
) ([]byte, error) {
	header, err := encodeHeaderLine(meta)
	if err != nil {
		return nil, err
	}
	content := append(header, '\n')
	if len(events) == 0 {
		return content, nil
	}
	body, err := eventLines(events, b.packChunks)
	if err != nil {
		return nil, err
	}
	content = append(content, body...)
	return append(content, '\n'), nil
}

// appendLines 把一批事件编好、追加、fsync。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:647-651
// 源: packages/session/session-persistence-jsonl/src/index.ts:670-673
func (b *Backend) appendLines(meta session.SessionHeader, events []session.Event) error {
	body, err := eventLines(events, b.packChunks)
	if err != nil {
		return err
	}
	path, err := logPath(b.root, meta.Cwd, meta.ID, b.compression)
	if err != nil {
		return err
	}
	return appendDurably(path, append(body, '\n'))
}

// ---- 找一份存档 ----

// storedArtifact 是列举时看见的一份存档：它的头，和它在哪。
type storedArtifact struct {
	header session.SessionHeader
	path   string
}

// listArtifacts 列出这个根下面所有成形、且身份不重的存档。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:488-525
func (b *Backend) listArtifacts(ctx context.Context) ([]storedArtifact, error) {
	if err := b.ensureRootEncoding(ctx); err != nil {
		return nil, err
	}
	projects, err := b.listProjectDirs()
	if err != nil {
		return nil, err
	}
	var artifacts []storedArtifact
	seen := map[session.SessionID]struct{}{}
	for _, project := range projects {
		dirs, err := b.listSessionDirs(project)
		if err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			artifact, ok, err := b.readArtifactHeader(dir)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, duplicate := seen[artifact.header.ID]; duplicate {
				return nil, fmt.Errorf(
					"session/persistence/jsonl: 会话标识 %q 在多个工程目录里各有一份日志",
					string(artifact.header.ID))
			}
			seen[artifact.header.ID] = struct{}{}
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts, nil
}

// readArtifactHeader 只读一个会话目录里那份存档的头。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:497-520
//
// 第二个返回值为假表示这个目录里没有一份本后端读得了的存档：文件不在、
// 空的或者只写了半行、或者那一行根本不是一份会话头。这三种都是正常控制流。
func (b *Backend) readArtifactHeader(dir string) (storedArtifact, bool, error) {
	opposite := filepath.Join(dir, logBaseName+logSuffix(b.oppositeCompression()))
	present, err := exists(opposite)
	if err != nil {
		return storedArtifact{}, false, err
	}
	if present {
		return storedArtifact{}, false, b.encodingMismatch(opposite)
	}
	path := filepath.Join(dir, logBaseName+logSuffix(b.compression))
	present, err = exists(path)
	if err != nil || !present {
		return storedArtifact{}, false, err
	}
	line, complete, err := readFirstLine(path)
	if err != nil || !complete {
		return storedArtifact{}, false, err
	}
	meta, err := parseHeaderMeta(line)
	if errors.Is(err, errHeaderMalformed) {
		return storedArtifact{}, false, nil
	}
	if err != nil {
		return storedArtifact{}, false, b.locateParseRefusal(path, err)
	}
	if err := b.assertStoredIdentity(path, meta, ""); err != nil {
		return storedArtifact{}, false, err
	}
	return storedArtifact{header: meta, path: path}, true, nil
}

// locateExisting 找出一个身份此刻那份唯一的存档在哪。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:792-814
//
// 找不到时返回 [persistence.ErrSessionNotFound]——那是正常控制流，
// 建会话前的撞号探测走的就是它。
func (b *Backend) locateExisting(ctx context.Context, id session.SessionID) (string, error) {
	if err := b.ensureRootEncoding(ctx); err != nil {
		return "", err
	}
	segment, err := encodeSegment(string(id))
	if err != nil {
		return "", err
	}
	projects, err := b.listProjectDirs()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := b.rejectLegacyFlatArtifact(project, segment); err != nil {
			return "", err
		}
		dir := filepath.Join(project, segment)
		opposite := filepath.Join(dir, logBaseName+logSuffix(b.oppositeCompression()))
		present, err := exists(opposite)
		if err != nil {
			return "", err
		}
		if present {
			return "", b.encodingMismatch(opposite)
		}
		path := filepath.Join(dir, logBaseName+logSuffix(b.compression))
		present, err = exists(path)
		if err != nil {
			return "", err
		}
		if present {
			matches = append(matches, path)
		}
	}
	switch len(matches) {
	case 0:
		return "", persistence.ErrSessionNotFound
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf(
			"session/persistence/jsonl: 会话标识 %q 在多个工程目录里各有一份日志", string(id))
	}
}

// listProjectDirs 列出这个根下面那些给人看的工程目录。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:869-881
//
// 只有「根不存在」表示一个会话都没有；别的 I/O 故障一律上抛。
func (b *Backend) listProjectDirs() ([]string, error) {
	entries, err := os.ReadDir(b.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(b.root, entry.Name()))
		}
	}
	return dirs, nil
}

// listSessionDirs 列出一个工程目录下那些会话自己的目录，顺手拒掉早先那种平铺排布。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:883-892
func (b *Backend) listSessionDirs(project string) ([]string, error) {
	entries, err := os.ReadDir(project)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zstd") {
				return nil, b.legacyLayout(filepath.Join(project, name))
			}
			continue
		}
		dirs = append(dirs, filepath.Join(project, name))
	}
	return dirs, nil
}

// ---- 那几道守门 ----

// assertUsableRoot 要求一个已经存在的根必须是读得动的目录。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:816-824
//
// 新增: 「是不是目录」要单独 [os.Stat] 一次，不能只看 [os.ReadDir] 报没报错。
// 根被一个普通文件占着时，POSIX 上 ReadDir 报 ENOTDIR，Windows 上报的却是
// ERROR_PATH_NOT_FOUND——那个错映到 [fs.ErrNotExist]，会被下面那句「不存在是
// 正常的」放过去，于是同一份配置在两个平台上一个拦得住、一个拦不住。
func (b *Backend) assertUsableRoot() error {
	info, err := os.Stat(b.root)
	if errors.Is(err, fs.ErrNotExist) {
		// 根还不存在是正常的：它在第一次落地时建出来。
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("session/persistence/jsonl: 根 %q 已经存在，但它不是一个目录", b.root)
	}
	if _, err := os.ReadDir(b.root); err != nil {
		return err
	}
	return nil
}

// ensureRootEncoding 拒掉一个已经属于另一种物理编码的根，只查一次。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:894-907
func (b *Backend) ensureRootEncoding(ctx context.Context) error {
	b.rootEncodingOnce.Do(func() { b.rootEncodingErr = b.checkRootEncoding(ctx) })
	return b.rootEncodingErr
}

// checkRootEncoding 走一遍这个根，看有没有另一种编码的存档。
func (b *Backend) checkRootEncoding(ctx context.Context) error {
	projects, err := b.listProjectDirs()
	if err != nil {
		return err
	}
	for _, project := range projects {
		dirs, err := b.listSessionDirs(project)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			if err := ctx.Err(); err != nil {
				return err
			}
			incompatible := filepath.Join(dir, logBaseName+logSuffix(b.oppositeCompression()))
			present, err := exists(incompatible)
			if err != nil {
				return err
			}
			if present {
				return b.encodingMismatch(incompatible)
			}
		}
	}
	return nil
}

// assertStoredIdentity 拒掉一份指不到它自己那个位置上去的头。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:826-847
//
// 新增: 上游在两条路不同时还会比 realpath，为的是在大小写不敏感的文件系统上
// 认下同一个文件的两种拼法。这里不比：本包所有的路径都是自己从
// [Backend.root] 和头里的字段拼出来的，两条不同的拼法只可能来自一份对不上的
// 头，而那正是这道判据要拦的。
func (b *Backend) assertStoredIdentity(
	path string,
	meta session.SessionHeader,
	expected session.SessionID,
) error {
	if expected != "" && meta.ID != expected {
		return fmt.Errorf(
			"session/persistence/jsonl: 会话日志 %q 坏了：要的是 %q，头里写的是 %q",
			path, string(expected), string(meta.ID))
	}
	expectedPath, err := logPath(b.root, meta.Cwd, meta.ID, b.compression)
	if err != nil {
		return fmt.Errorf(
			"session/persistence/jsonl: 会话日志 %q 坏了：头里那个身份编不出一段存储路径：%w", path, err)
	}
	if path != expectedPath {
		return fmt.Errorf(
			"session/persistence/jsonl: 会话日志 %q 坏了：头里的身份 %q 和工作目录指的是 %q",
			path, string(meta.ID), expectedPath)
	}
	return nil
}

// rejectLegacyFlatArtifact 拒掉一份还摆在早先那种平铺排布里的存档。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:909-922
func (b *Backend) rejectLegacyFlatArtifact(project, segment string) error {
	for _, compression := range []Compression{CompressionZstd, CompressionNone} {
		path := filepath.Join(project, segment+logSuffix(compression))
		present, err := exists(path)
		if err != nil {
			return err
		}
		if present {
			return b.legacyLayout(path)
		}
	}
	return nil
}

// rejectOppositeArtifact 拒掉在另一种物理编码下已经存在的同一个会话。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:924-927
func (b *Backend) rejectOppositeArtifact(cwd string, id session.SessionID) error {
	path, err := logPath(b.root, cwd, id, b.oppositeCompression())
	if err != nil {
		return err
	}
	present, err := exists(path)
	if err != nil {
		return err
	}
	if present {
		return b.encodingMismatch(path)
	}
	return nil
}

// oppositeCompression 是另一种物理编码。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:929-931
func (b *Backend) oppositeCompression() Compression {
	if b.compression == CompressionZstd {
		return CompressionNone
	}
	return CompressionZstd
}

// encodingMismatch 是「这份存档属于另一种物理编码」那句拒绝。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:933-939
func (b *Backend) encodingMismatch(path string) error {
	return fmt.Errorf(
		"session/persistence/jsonl: 存档 %q 用的是 %s，而这个后端配的是 %q；"+
			"换一个根，或者把编码配成对得上的那一种",
		path, logSuffix(b.oppositeCompression()), string(b.compression))
}

// legacyLayout 是「这份存档还摆在早先那种平铺排布里」那句拒绝。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:941-946
func (b *Backend) legacyLayout(path string) error {
	return fmt.Errorf(
		"session/persistence/jsonl: 存档 %q 用的是不再支持的平铺排布；"+
			"换一个根，或者先把它挪进「工程目录／会话目录」再装载",
		path)
}
