// 本文件的作用：一份存档的第一行怎么读写，以及后面那些事件行怎么增量扫出
// 「已提交的那一段」和「写坏的那条尾巴」。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:30-107
// 源: packages/session/session-persistence-jsonl/src/format.ts:212-446

package jsonl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// headerLineType 是头那一行上的行标签，读的一方靠它把头和事件行分开。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:35-36
const headerLineType = "session"

// errHeaderMalformed 表示这一行不是一份成形的会话头。
//
// 新增: DSH 那边这件事分给两个返回口——parseHeaderMeta 返回 undefined，
// parseHeaderRecord 抛。Go 里合成一条哨兵：列举那一路把它当成「跳过这份存档」
// 的正常控制流，装载那一路把它翻成一句损坏。两处判据必须是同一份，
// 分成两个函数写迟早会各自漂走。
var errHeaderMalformed = errors.New("session/persistence/jsonl: 这一行不是一份会话头")

// headerLine 是一份存档的第一条 JSONL 记录：那份不可变的头，带上行标签。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:30-46
//
// 新增: 可选字段全用指针，为的是把「没给」和「给了零值」分开——上游靠
// `...x !== undefined ? {x} : {}` 做同一件事。retired 那两个字段只为**看它在不在**
// 而存在，所以是 [json.RawMessage]。
type headerLine struct {
	Type            string          `json:"type"`
	Version         *int            `json:"version"`
	ID              *string         `json:"id"`
	CreatedAt       *int64          `json:"createdAt"`
	Cwd             *string         `json:"cwd,omitempty"`
	ParentSession   *string         `json:"parentSession,omitempty"`
	SeedLength      *int            `json:"seedLength,omitempty"`
	Origin          *string         `json:"origin,omitempty"`
	DelegationDepth *int            `json:"delegationDepth"`
	AgentPreset     *string         `json:"agentPreset,omitempty"`
	SandboxMode     json.RawMessage `json:"sandboxMode,omitempty"`
	ApprovalPolicy  json.RawMessage `json:"approvalPolicy,omitempty"`
}

// encodeHeaderLine 把一份头排成它在盘上那一行（不含换行）。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:48-66
func encodeHeaderLine(meta session.SessionHeader) ([]byte, error) {
	line := headerLine{
		Type:            headerLineType,
		Version:         &meta.Version,
		CreatedAt:       &meta.CreatedAt,
		DelegationDepth: &meta.DelegationDepth,
	}
	id := string(meta.ID)
	line.ID = &id
	if meta.Cwd != "" {
		cwd := meta.Cwd
		line.Cwd = &cwd
	}
	if meta.ParentSession != "" {
		parent := string(meta.ParentSession)
		line.ParentSession = &parent
	}
	if meta.SeedLength != 0 {
		seed := meta.SeedLength
		line.SeedLength = &seed
	}
	if meta.Origin != "" {
		origin := string(meta.Origin)
		line.Origin = &origin
	}
	if meta.AgentPreset != "" {
		preset := meta.AgentPreset
		line.AgentPreset = &preset
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return nil, fmt.Errorf("session/persistence/jsonl: 会话 %q 的头排不出去：%w", string(meta.ID), err)
	}
	return encoded, nil
}

// parseHeaderMeta 只解一行头，不碰后面任何一条事件。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:429-446
//
// 列举就靠它：一份会话清单的开销跟**会话条数**走，不跟每段对话有多长走。
//
// 这一行不成形时返回 [errHeaderMalformed]；格式版本本构建不读、或者头上还挂着
// 那两个已经退役的策略字段，返回的是各自那条真错误——两者都不是「跳过它」。
func parseHeaderMeta(line []byte) (session.SessionHeader, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return session.SessionHeader{}, errHeaderMalformed
	}
	object, _ := parsed.(map[string]any)
	if err := refuseForeignFormatVersion(object); err != nil {
		return session.SessionHeader{}, err
	}
	var shape headerLine
	if err := json.Unmarshal(line, &shape); err != nil {
		return session.SessionHeader{}, errHeaderMalformed
	}
	return fromHeaderLine(shape)
}

// refuseForeignFormatVersion 在验头的形状、也在解任何一条事件**之前**，先拒掉
// 一份格式版本本构建不读的存档。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:265-280
//
// 次序是全部理由：一个未来的格式未必过得了今天这套结构检查，而那时使用者
// 该看到的是「升级运行时」，绝不是「日志损坏」。
//
// 新增: 上游判的是 `typeof version === 'number'`，JS 的 number 容得下 0.5。
// Go 这边 [session.SessionHeader.Version] 是 int，一个非整数的版本号既不是本构建
// 的版本、也指不出任何一个别的构建，所以它走的是「这一行不是一份会话头」——
// 那句话是准的，而 [persistence.FormatVersionRefusal] 只会说一句它自己也不懂的话。
func refuseForeignFormatVersion(object map[string]any) error {
	if object == nil {
		return nil
	}
	number, ok := object["version"].(json.Number)
	if !ok {
		return nil
	}
	version, err := number.Int64()
	if err != nil {
		return errHeaderMalformed
	}
	if int(version) == session.FormatVersion {
		return nil
	}
	id, _ := object["id"].(string)
	return &persistence.FormatUnsupportedError{
		ID:     session.SessionID(id),
		Reason: persistence.FormatVersionRefusal(session.SessionID(id), int(version)),
	}
}

// fromHeaderLine 把一行验过形状的头还原成 [session.SessionHeader]。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:68-110
//
// 新增: 上游的 isHeaderLine 只验必填那几项，cwd / parentSession / seedLength 是
// 直接强转下去的——一个 `cwd: 42` 会原样活到头里。Go 的字段有类型，那种值
// 在这里连解都解不出来，所以它和「形状不对」是同一件事。
func fromHeaderLine(line headerLine) (session.SessionHeader, error) {
	if line.SandboxMode != nil || line.ApprovalPolicy != nil {
		return session.SessionHeader{}, errors.New(
			"session/persistence/jsonl: 这份会话头上还挂着已经退役的策略基线字段")
	}
	switch {
	case line.Type != headerLineType,
		line.Version == nil, line.ID == nil,
		line.CreatedAt == nil, *line.CreatedAt < 0,
		line.DelegationDepth == nil, *line.DelegationDepth < 0,
		line.Origin != nil && *line.Origin != string(session.OriginSubagent):
		return session.SessionHeader{}, errHeaderMalformed
	}
	meta := session.SessionHeader{
		Version:         *line.Version,
		ID:              session.SessionID(*line.ID),
		CreatedAt:       *line.CreatedAt,
		DelegationDepth: *line.DelegationDepth,
	}
	if line.Cwd != nil {
		meta.Cwd = *line.Cwd
	}
	if line.ParentSession != nil {
		meta.ParentSession = session.SessionID(*line.ParentSession)
	}
	if line.SeedLength != nil {
		meta.SeedLength = *line.SeedLength
	}
	if line.Origin != nil {
		meta.Origin = session.Origin(*line.Origin)
	}
	if line.AgentPreset != nil {
		meta.AgentPreset = *line.AgentPreset
	}
	return meta, nil
}

// eventLines 把一批事件排成待写的那几行（不带末尾换行，写的一方自己补）。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:212-226
//
// packChunks 打开时，连着的同类增量分块压成一行存储记录（无损，一份真实会话
// 上量出来小六成）；关掉就一条事件一行，为的是好看。**读那一侧和这个开关无关**
// ——[logScanner] 一律走 [session.DecodeStorageRecord]，所以一份日志的排布
// 不取决于写它的那一刻这个开关是什么。
func eventLines(events []session.Event, packChunks bool) ([]byte, error) {
	records := make([][]byte, 0, len(events))
	if packChunks {
		packed, err := session.PackChunkRuns(events)
		if err != nil {
			return nil, fmt.Errorf("session/persistence/jsonl: 这一批事件压不成存储记录：%w", err)
		}
		for _, record := range packed {
			records = append(records, record)
		}
	} else {
		for _, event := range events {
			encoded, err := json.Marshal(event)
			if err != nil {
				return nil, fmt.Errorf(
					"session/persistence/jsonl: seq %d 那条 %s 事件排不出去：%w",
					event.Seq, event.Type, err)
			}
			records = append(records, encoded)
		}
	}
	return bytes.Join(records, []byte("\n")), nil
}

// logScan 是扫完一份存档得到的三样东西。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:259-263
type logScan struct {
	// meta 是这份存档的头。
	meta session.SessionHeader
	// events 是 seq 从零开始连续的那一段已提交事件。
	events []session.Event
	// committedBytes 是可以安全追加的那个字节位置，也就是坏尾巴从哪开始。
	committedBytes int64
}

// scanCheckpoint 是扫描进行到某一处时的三个游标。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:358-368
type scanCheckpoint struct {
	inputBytes     int64
	committedBytes int64
	eventCount     int
}

// logScanner 增量扫一份存档里那些**完整**的事件记录，头由外面单独喂进来。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:299-411
//
// 它的核心契约是「没有换行的那条末尾记录当它不存在」：一次崩溃留下的就是那个，
// 而它不是损坏。找换行和数字节都在原始字节上做，只有完整的记录才去解。
type logScanner struct {
	meta           session.SessionHeader
	events         []session.Event
	fragment       []byte
	inputBytes     int64
	committedBytes int64
	eventLine      int
	issue          error
	finished       bool
}

// newLogScanner 从恰好一条带换行的头记录造一个扫描器。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:316-324
func newLogScanner(headerRecord []byte) (*logScanner, error) {
	meta, err := parseHeaderRecord(headerRecord)
	if err != nil {
		return nil, err
	}
	return &logScanner{
		meta:           meta,
		inputBytes:     int64(len(headerRecord)),
		committedBytes: int64(len(headerRecord)),
	}, nil
}

// parseHeaderRecord 解一条独立喂进来的、完整的头记录。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:282-297
func parseHeaderRecord(record []byte) (session.SessionHeader, error) {
	if len(record) == 0 || record[len(record)-1] != '\n' ||
		bytes.IndexByte(record, '\n') != len(record)-1 {
		return session.SessionHeader{}, errors.New("session/persistence/jsonl: 这份会话日志是空的，或者没有头")
	}
	meta, err := parseHeaderMeta(record[:len(record)-1])
	if errors.Is(err, errHeaderMalformed) {
		return session.SessionHeader{}, errors.New("session/persistence/jsonl: 会话日志坏了：第一行不是一份会话头")
	}
	return meta, err
}

// write 吃下紧接着前面所有字节的一段明文，只留下最后那条不完整的记录。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:326-356
func (s *logScanner) write(chunk []byte) error {
	if s.finished {
		return errors.New("session/persistence/jsonl: 这个扫描器已经收工了，不能再往里写")
	}
	chunkStart := s.inputBytes
	s.inputBytes += int64(len(chunk))
	lineStart := 0
	for {
		offset := bytes.IndexByte(chunk[lineStart:], '\n')
		if offset < 0 {
			break
		}
		newline := lineStart + offset
		line := chunk[lineStart:newline]
		if len(s.fragment) > 0 {
			line = append(s.fragment, line...)
		}
		s.fragment = nil
		if err := s.consumeEventLine(line, chunkStart+int64(newline)+1); err != nil {
			return err
		}
		lineStart = newline + 1
	}
	if lineStart < len(chunk) {
		// 跨越两次 write 的那一截必须**拷**下来：喂进来的那段字节可能是一个
		// 解码器复用的输出缓冲，write 一返回它就可以被写花。
		s.fragment = append(s.fragment, chunk[lineStart:]...)
	}
	return nil
}

// checkpoint 抓一张当下的游标快照，为的是在追加一段从坏尾巴里捞出来的明文之前
// 记住「哪些是完整记录带来的」。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:358-368
func (s *logScanner) checkpoint() scanCheckpoint {
	return scanCheckpoint{
		inputBytes:     s.inputBytes,
		committedBytes: s.committedBytes,
		eventCount:     len(s.events),
	}
}

// finish 收工，把最后那条没有换行的记录当作断尾忽略掉。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:370-377
func (s *logScanner) finish() logScan {
	s.finished = true
	return logScan{meta: s.meta, events: s.events, committedBytes: s.committedBytes}
}

// consumeEventLine 解一条完整的事件记录，并把连续前缀往前推。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:379-410
//
// 撞上坏记录时**先记下来、不当场拒**：一份写坏了中段的日志，只要后面再没出现过
// 回合边界，那些残缺的字节就还只是一条长一点的断尾。可一旦后面解出了一条
// turn/end，这份日志就会被读成一段「中间那些事根本没发生过」的完整历史——
// 那比读不出来坏得多，所以那一刻才拒收整份。
func (s *logScanner) consumeEventLine(line []byte, endByte int64) error {
	s.eventLine++
	decoded, err := session.DecodeStorageRecord(json.RawMessage(line))
	if err != nil {
		if s.issue == nil {
			s.issue = fmt.Errorf(
				"session/persistence/jsonl: 会话日志坏了：第 %d 行那条已提交的记录解不开：%w",
				s.eventLine, err)
		}
		return nil
	}
	if s.issue != nil {
		if closesATurn(decoded) {
			return s.issue
		}
		return nil
	}

	rowStart := len(s.events)
	for _, event := range decoded {
		if event.Seq != len(s.events) {
			expected := len(s.events)
			s.events = s.events[:rowStart]
			s.issue = fmt.Errorf(
				"session/persistence/jsonl: 会话日志坏了：第 %d 行处已提交区间的 seq 断了（该是 %d，给的是 %d）",
				s.eventLine, expected, event.Seq)
			if closesATurn(decoded) {
				return s.issue
			}
			return nil
		}
		s.events = append(s.events, event)
	}
	s.committedBytes = endByte
	return nil
}

// closesATurn 判一行解出来的事件里有没有一条关掉了某个回合。
func closesATurn(events []session.Event) bool {
	for _, event := range events {
		if event.Type == session.EventTurnEnd {
			return true
		}
	}
	return false
}

// scanLog 把一整段（完好的或者断了尾的）字节扫成它那段保住的事件前缀。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:413-427
func scanLog(buffer []byte) (logScan, error) {
	headerEnd := bytes.IndexByte(buffer, '\n')
	if headerEnd < 0 {
		return logScan{}, errors.New("session/persistence/jsonl: 这份会话日志是空的，或者没有头")
	}
	scanner, err := newLogScanner(buffer[:headerEnd+1])
	if err != nil {
		return logScan{}, err
	}
	if err := scanner.write(buffer[headerEnd+1:]); err != nil {
		return logScan{}, err
	}
	return scanner.finish(), nil
}
