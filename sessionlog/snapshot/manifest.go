// 本文件的作用：读一份场景旁边那张 `snapshot.yml`——它写的是这份夹具**归谁管**，
// 以及那些光看日志本身补不回来的事实。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts

package snapshot

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/snight1983/ds-harness-go/attachment"
)

// Recording 说这份会话是录出来的还是手写的。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:10
type Recording string

const (
	// RecordingLive 表示重跑一遍场景就能重新生成这份日志。
	RecordingLive Recording = "live"
	// RecordingAuthored 表示它是手写的，没有哪次真实运行能生成它。
	RecordingAuthored Recording = "authored"
)

// HeaderManifest 说这份夹具在请求头这件事上认领了什么。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:13-28
//
// 系统提示词和工具 schema 在每份夹具里都一模一样，[Options] 缺省把它们换成记号
// （理由见那里）。代价是那两段原文得有人留着，这一段就是「谁留着」的声明：同一个
// Class 底下只有 Pin 的那一份是权威副本。
type HeaderManifest struct {
	// Class 是请求头的类名，只有逐字节相同的请求头才共用一个类名。
	Class string
	// Pin 表示本场景拥有这个类的记号化请求头序列。
	Pin bool
	// SystemPromptSource 指出系统提示词原文留在哪个场景。
	SystemPromptSource string
	// ToolSchemasSource 指出工具 schema 原文留在哪个场景。
	ToolSchemasSource string
	// ChildSystemPrompts 是那些自带一份不同系统提示词的子会话序号。
	ChildSystemPrompts []int
	// ChildToolSchemas 是那些自带一套不同工具 schema 的子会话序号。
	ChildToolSchemas []int
	// Changes 是首次请求头之后**合法**的换头次数；缺省不填表示一次都不该换。
	Changes *int
}

// ReplayManifest 说这份夹具旁边另有一份脚本覆盖。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:31-34
//
// 模型是怎么答的，绝大多数时候从日志里那串分块就能倒推回来；错误、中断、以及
// 压根没发出去的请求倒推不回来，那时候场景旁边会放一份 `replay.override.json`，
// 由 [github.com/snight1983/ds-harness-go/feature/replay] 读。
type ReplayManifest struct {
	// Override 恒为真——这个字段存在本身就是全部信息。
	Override bool
}

// InputAttachment 是一件进不了会话日志的二进制输入。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:53-60
type InputAttachment struct {
	// ID 是会话消息里留下的那个内容寻址标识。
	ID attachment.ID
	// MediaType 是控制方声明的媒体类型。
	MediaType attachment.MediaType
	// Data 是完整的 base64 负载。
	Data string
}

// InputManifest 是那些**没能进入会话**、因而日志里找不到的控制方输入。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:63-68
//
// 一次被准入挡下来的输入不会留下任何用户事件，可是「它被挡下来了」恰恰是这个场景
// 要验的东西，所以原样记在这里。
type InputManifest struct {
	// Task 是一次性任务的原文。
	Task string
	// Attachments 是那些按内容寻址的二进制输入。
	Attachments []InputAttachment
}

// SessionReference 说这份夹具借用别处的 `session.jsonl`。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:71-74
type SessionReference struct {
	// Source 是从本场景目录出发、指向那份日志的相对路径，一律用正斜杠。
	Source string
}

// Manifest 是一份场景夹具的归属声明。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:77-104
//
// 新增: DSH 那份清单还有六个字段，本仓库一个都没有，出现即报错：
//   - `profile`（headless/sdk/acp/web）和 `composition`：那是 `dsh` 这个命令行的
//     启动档位和装配 id，Go 这边没有命令行，装配点是 harness 那个包。
//   - `platform`（posix/pwsh）和 `permission`：那是进程回退那一路要挑的宿主 shell
//     和权限预设，本仓库不起子进程。
//   - `environment`：往被测进程里塞环境变量，同上。
//   - `workspace`：本机工作区的准备与终态，服务端没有硬盘（见
//     [github.com/snight1983/ds-harness-go/internal/devtools/oscheck]）。
type Manifest struct {
	// Version 恒为 1。
	Version int
	// Scenario 是场景目录名，重复写一遍是为了挪动和复制时 diff 看得出来。
	Scenario string
	// Recording 说这份会话是录出来的还是手写的。
	Recording Recording
	// Header 是请求头的类名与权威副本归属。
	Header *HeaderManifest
	// Replay 只在场景旁边另有一份脚本覆盖时出现。
	Replay *ReplayManifest
	// Input 只在有输入没能进入会话时出现。
	Input *InputManifest
	// Session 只在本目录**不**拥有 `session.jsonl` 时出现。
	Session *SessionReference
}

var manifestKeys = []string{
	"version", "scenario", "recording", "header", "replay", "input", "session",
}

// ParseManifest 读一份 `snapshot.yml`，不认未知字段。
//
// 源: packages/test-support/session-snapshot/src/manifest.ts:153-354
//
// path 只用来出错时报位置。不认未知字段是有意的：一份清单写错了字段名，沉默地
// 忽略它等于让一条本该生效的归属声明凭空消失，而那件事要到某天两份夹具同时改红
// 才会被发现。
//
// 新增: DSH 用 js-yaml 的 JSON_SCHEMA 挡掉 YAML 那些 JS 专用标签。Go 这边
// [yaml.Unmarshal] 本来就不会照着标签造对象，改成逐字段验类型——一个 `!!timestamp`
// 解出来不是字符串，当场就在类型这一关被挡下。
func ParseManifest(source []byte, path string) (Manifest, error) {
	var parsed any
	if err := yaml.Unmarshal(source, &parsed); err != nil {
		return Manifest{}, fmt.Errorf("snapshot: %s：不是合法 YAML：%w", path, err)
	}
	manifest, err := readManifest(parsed)
	if err != nil {
		return Manifest{}, fmt.Errorf("snapshot: %s：%w", path, err)
	}
	return manifest, nil
}

func readManifest(parsed any) (Manifest, error) {
	root, err := asMapping(parsed, "清单")
	if err != nil {
		return Manifest{}, err
	}
	if err := rejectUnknownKeys(root, manifestKeys, "清单"); err != nil {
		return Manifest{}, err
	}
	if version, ok := asInt(root["version"]); !ok || version != 1 {
		return Manifest{}, errors.New("清单的 version 必须是 1")
	}

	manifest := Manifest{Version: 1}
	if value, present := root["scenario"]; present {
		if manifest.Scenario, err = kebabName(value, "清单的 scenario"); err != nil {
			return Manifest{}, err
		}
	}
	if value, present := root["recording"]; present {
		text, _ := value.(string)
		recording := Recording(text)
		if recording != RecordingLive && recording != RecordingAuthored {
			return Manifest{}, errors.New("清单的 recording 只能是 live 或者 authored")
		}
		manifest.Recording = recording
	}
	if value, present := root["header"]; present {
		if manifest.Header, err = readHeader(value); err != nil {
			return Manifest{}, err
		}
	}
	if value, present := root["replay"]; present {
		if manifest.Replay, err = readReplay(value); err != nil {
			return Manifest{}, err
		}
	}
	if value, present := root["input"]; present {
		if manifest.Input, err = readInput(value); err != nil {
			return Manifest{}, err
		}
	}
	if value, present := root["session"]; present {
		if manifest.Session, err = readSession(value); err != nil {
			return Manifest{}, err
		}
	}
	return manifest, nil
}

func readHeader(value any) (*HeaderManifest, error) {
	fields, err := asMapping(value, "清单的 header")
	if err != nil {
		return nil, err
	}
	allowed := []string{
		"class", "pin", "systemPromptSource", "toolSchemasSource",
		"childSystemPrompts", "childToolSchemas", "changes",
	}
	if err := rejectUnknownKeys(fields, allowed, "清单的 header"); err != nil {
		return nil, err
	}

	header := &HeaderManifest{}
	if header.Class, err = kebabName(fields["class"], "清单的 header.class"); err != nil {
		return nil, err
	}
	if value, present := fields["pin"]; present {
		if value != true {
			return nil, errors.New("清单的 header.pin 写出来就必须是 true")
		}
		header.Pin = true
	}
	if value, present := fields["systemPromptSource"]; present {
		if header.SystemPromptSource, err = sourcePath(value, "清单的 header.systemPromptSource"); err != nil {
			return nil, err
		}
	}
	if value, present := fields["toolSchemasSource"]; present {
		if header.ToolSchemasSource, err = sourcePath(value, "清单的 header.toolSchemasSource"); err != nil {
			return nil, err
		}
	}
	if value, present := fields["childSystemPrompts"]; present {
		if header.ChildSystemPrompts, err = positiveIndexes(value, "清单的 header.childSystemPrompts"); err != nil {
			return nil, err
		}
	}
	if value, present := fields["childToolSchemas"]; present {
		if header.ChildToolSchemas, err = positiveIndexes(value, "清单的 header.childToolSchemas"); err != nil {
			return nil, err
		}
	}
	if value, present := fields["changes"]; present {
		changes, ok := asInt(value)
		if !ok || changes < 0 {
			return nil, errors.New("清单的 header.changes 必须是一个非负整数")
		}
		header.Changes = &changes
	}
	return header, nil
}

func readReplay(value any) (*ReplayManifest, error) {
	fields, err := asMapping(value, "清单的 replay")
	if err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(fields, []string{"override"}, "清单的 replay"); err != nil {
		return nil, err
	}
	if fields["override"] != true {
		return nil, errors.New("清单的 replay.override 必须是 true")
	}
	return &ReplayManifest{Override: true}, nil
}

func readInput(value any) (*InputManifest, error) {
	fields, err := asMapping(value, "清单的 input")
	if err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(fields, []string{"task", "attachments"}, "清单的 input"); err != nil {
		return nil, err
	}

	input := &InputManifest{}
	if value, present := fields["task"]; present {
		task, ok := value.(string)
		if !ok || strings.TrimSpace(task) == "" {
			return nil, errors.New("清单的 input.task 写出来就不能是空串")
		}
		input.Task = task
	}
	if value, present := fields["attachments"]; present {
		if input.Attachments, err = readAttachments(value); err != nil {
			return nil, err
		}
	}
	if input.Task == "" && len(input.Attachments) == 0 {
		return nil, errors.New("清单的 input 至少要写 task 或者 attachments 其中一样")
	}
	return input, nil
}

func readAttachments(value any) ([]InputAttachment, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("清单的 input.attachments 必须是一个非空数组")
	}
	out := make([]InputAttachment, 0, len(items))
	seen := make(map[attachment.ID]bool, len(items))
	for index, item := range items {
		label := fmt.Sprintf("清单的 input.attachments[%d]", index)
		fields, err := asMapping(item, label)
		if err != nil {
			return nil, err
		}
		if err := rejectUnknownKeys(fields, []string{"id", "mediaType", "data"}, label); err != nil {
			return nil, err
		}
		id, ok := fields["id"].(string)
		if !ok || !strings.HasPrefix(id, "sha256:") {
			return nil, fmt.Errorf("%s.id 必须以 sha256: 开头", label)
		}
		mediaType, ok := fields["mediaType"].(string)
		if !ok || !strings.Contains(mediaType, "/") {
			return nil, fmt.Errorf("%s.mediaType 必须是一个媒体类型", label)
		}
		data, ok := fields["data"].(string)
		if !ok || data == "" {
			return nil, fmt.Errorf("%s.data 必须是非空的 base64", label)
		}
		if seen[attachment.ID(id)] {
			return nil, fmt.Errorf("%s.id 和前面某一件重了", label)
		}
		seen[attachment.ID(id)] = true
		out = append(out, InputAttachment{
			ID: attachment.ID(id), MediaType: attachment.MediaType(mediaType), Data: data,
		})
	}
	return out, nil
}

func readSession(value any) (*SessionReference, error) {
	fields, err := asMapping(value, "清单的 session")
	if err != nil {
		return nil, err
	}
	if err := rejectUnknownKeys(fields, []string{"source"}, "清单的 session"); err != nil {
		return nil, err
	}
	source, ok := fields["source"].(string)
	if !ok || strings.TrimSpace(source) == "" {
		return nil, errors.New("清单的 session.source 不能是空串")
	}
	// 反斜杠和盘符都不认：这条路径要在任何一台机器上读出同一份夹具。
	if strings.HasPrefix(source, "/") || strings.ContainsAny(source, "\\\x00") ||
		(len(source) > 1 && source[1] == ':') {
		return nil, errors.New("清单的 session.source 必须是一条用正斜杠写的相对路径")
	}
	return &SessionReference{Source: source}, nil
}

// asMapping 认一个 YAML 映射。
func asMapping(value any, label string) (map[string]any, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是一个映射", label)
	}
	return fields, nil
}

// rejectUnknownKeys 挡下所有没在册的字段，名字按字典序报，于是同一份清单每次报的一样。
func rejectUnknownKeys(fields map[string]any, allowed []string, label string) error {
	var unknown []string
	for key := range fields {
		if !slices.Contains(allowed, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("%s 有不认识的字段：%s", label, strings.Join(unknown, "、"))
}

// kebabName 认一个小写连字符名字。
func kebabName(value any, label string) (string, error) {
	text, ok := value.(string)
	if !ok || !isKebab(text) {
		return "", fmt.Errorf("%s 必须是一个小写连字符名字", label)
	}
	return text, nil
}

// sourcePath 认一个名字，或者一条由这种名字拼起来的语料库相对路径。
func sourcePath(value any, label string) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s 必须是一个小写连字符名字或者语料库相对路径", label)
	}
	for _, segment := range strings.Split(text, "/") {
		if !isKebab(segment) {
			return "", fmt.Errorf("%s 必须是一个小写连字符名字或者语料库相对路径", label)
		}
	}
	return text, nil
}

// positiveIndexes 认一串互不相同的正整数序号。
func positiveIndexes(value any, label string) ([]int, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是一串互不相同的正整数", label)
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		number, ok := asInt(item)
		if !ok || number < 1 || slices.Contains(out, number) {
			return nil, fmt.Errorf("%s 必须是一串互不相同的正整数", label)
		}
		out = append(out, number)
	}
	return out, nil
}

// isKebab 判断一个名字是不是小写连字符写法。
//
// 新增: DSH 那边是 /^[a-z0-9]+(?:-[a-z0-9]+)*$/。这里手写是因为本包已经有四个
// 正则，再加一个纯形状判断不值当。
func isKebab(text string) bool {
	if text == "" {
		return false
	}
	for _, segment := range strings.Split(text, "-") {
		if segment == "" {
			return false
		}
		for _, char := range segment {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

// asInt 读一个当前平台能完整表示的 YAML 整数；yaml.v3 会按大小解成三种整数类型。
func asInt(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		converted := int(number)
		return converted, int64(converted) == number
	case uint64:
		converted := int(number)
		return converted, converted >= 0 && uint64(converted) == number
	default:
		return 0, false
	}
}
