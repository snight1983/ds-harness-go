// 本文件的作用：一份录下来的日志压出来的快照要满足什么——时钟归零、请求头膨胀
// 换成记号、分块按内容重打包、信封不进快照，而且压两次得到同一段字节。

package snapshot_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/snapshot"
)

func TestNormalizeZeroesEveryWallClock(t *testing.T) {
	t.Parallel()

	log := buildLog(t,
		`{"version":1,"id":"s-1","createdAt":1758000000000}`,
		event(t, sessionlog.EventTurnStart, 0, 1758000000001, `{"turn":1}`),
		event(t, "goal/change", 1, 1758000000002,
			`{"kind":"goal/change","createdAt":1758000000002,"updatedAt":1758000000009}`),
	)

	got := normalizeOne(t, log, snapshot.Options{})

	if strings.Contains(got, "1758000000") {
		t.Fatalf("还留着墙上时钟：\n%s", got)
	}
	header := decode(t, firstLine(got))
	if header["createdAt"] != json.Number("0") {
		t.Errorf("会话头的 createdAt 是 %v", header["createdAt"])
	}
	if header["id"] != "{{session:1}}" {
		t.Errorf("会话身份没换成记号：%v", header["id"])
	}
}

func TestNormalizeDropsTheEventEnvelope(t *testing.T) {
	t.Parallel()

	log := buildLog(t,
		`{"version":1,"id":"s-1","createdAt":7}`,
		event(t, sessionlog.EventTurnStart, 0, 11, `{"turn":1}`),
	)

	got := normalizeOne(t, log, snapshot.Options{})

	// 信封留在快照里等于把一次落盘的批次边界也提交进仓库。
	body := decode(t, lines(got)[1])
	for _, key := range []string{"seq", "time", "seq0", "time0"} {
		if _, present := body[key]; present {
			t.Errorf("正文那一行还带着 %s：%v", key, body)
		}
	}
	if body["type"] != string(sessionlog.EventTurnStart) {
		t.Errorf("类型丢了：%v", body)
	}
}

func TestNormalizeRepacksChunksRegardlessOfFlushBoundaries(t *testing.T) {
	t.Parallel()

	// 同一串分块，一份录成三条散事件，另一份录成一行打包的——那是两次落盘批次
	// 边界不同的结果，两份该压出同一段字节。
	loose := buildLog(t, `{"version":1,"id":"s-1","createdAt":7}`,
		textChunk(t, 0, 100, "甲"), textChunk(t, 1, 105, "乙"), textChunk(t, 2, 108, "丙"))
	packed := buildLog(t, `{"version":1,"id":"s-1","createdAt":7}`,
		`{"type":"text-chunks","seq0":0,"time0":100,`+
			`"data":{"turn":1,"step":1,"index":0,"dt":[5,3],"texts":["甲","乙","丙"]}}`)

	fromLoose := normalizeOne(t, loose, snapshot.Options{})
	fromPacked := normalizeOne(t, packed, snapshot.Options{})

	if fromLoose != fromPacked {
		t.Fatalf("同一串分块压出了两段字节：\n散的 %s\n打包的 %s", fromLoose, fromPacked)
	}
	if count := len(lines(fromLoose)); count != 2 {
		t.Fatalf("三条分块该压回一行，实际正文 %d 行：\n%s", count-1, fromLoose)
	}
}

func TestNormalizeTokenizesRequestHeaderBulk(t *testing.T) {
	t.Parallel()

	log := buildLog(t, `{"version":1,"id":"s-1","createdAt":7}`,
		requestHeader(t, "你是一个助手，下面是长长的一段规矩……", "read"))

	cases := map[string]struct {
		options    snapshot.Options
		wantSystem any
		wantTools  bool
	}{
		"两段都换":  {snapshot.Options{}, "{{system}}", false},
		"留下提示词": {snapshot.Options{KeepSystem: true}, "你是一个助手，下面是长长的一段规矩……", false},
		"留下工具":  {snapshot.Options{KeepTools: true}, "{{system}}", true},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := normalizeOne(t, log, testCase.options)
			header := requestHeaderOf(t, got)

			if header["system"] != testCase.wantSystem {
				t.Errorf("system 是 %v，要的是 %v", header["system"], testCase.wantSystem)
			}
			// 换的是值不是键：一份夹具仍旧看得出这次请求带没带工具。
			if _, present := header["tools"]; !present {
				t.Fatalf("tools 这个键该留着：%v", header)
			}
			if _, verbatim := header["tools"].([]any); verbatim != testCase.wantTools {
				t.Errorf("tools 是 %v，KeepTools=%v", header["tools"], testCase.wantTools)
			}
		})
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	t.Parallel()

	log := buildLog(t,
		`{"version":1,"id":"11111111-2222-4333-8444-555555555555","createdAt":7}`,
		event(t, sessionlog.EventTurnStart, 0, 11, `{"turn":1}`),
		textChunk(t, 1, 12, "甲"), textChunk(t, 2, 13, "乙"), textChunk(t, 3, 14, "丙"),
		requestHeader(t, "规矩", "read"),
	)

	once := normalizeOne(t, log, snapshot.Options{})
	twice := normalizeOne(t, []byte(once), snapshot.Options{})

	// 压一份已经压过的日志必须原地不动：refresh 写回去的就是它自己。
	if once != twice {
		t.Fatalf("压第二遍变了：\n第一遍 %s\n第二遍 %s", once, twice)
	}
}

func TestNormalizeRefusesALogWithoutAHeader(t *testing.T) {
	t.Parallel()

	if _, err := snapshot.Normalize([][]byte{[]byte("\n  \n")}, snapshot.Options{}); err == nil {
		t.Fatal("空日志该被拒")
	}
	if _, err := snapshot.Normalize([][]byte{[]byte("不是 JSON\n")}, snapshot.Options{}); err == nil {
		t.Fatal("坏行该被拒")
	}
}

// normalizeOne 压一份日志，交回它的正文。
func normalizeOne(t *testing.T, log []byte, options snapshot.Options) string {
	t.Helper()

	out, err := snapshot.Normalize([][]byte{log}, options)
	if err != nil {
		t.Fatalf("压不出来：%v", err)
	}
	return string(out[0])
}

func buildLog(t *testing.T, header string, records ...string) []byte {
	t.Helper()

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteByte('\n')
	for _, record := range records {
		out.WriteString(record)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func event(t *testing.T, kind sessionlog.EventType, seq int, time int64, data string) string {
	t.Helper()

	line, err := json.Marshal(sessionlog.Event{
		Type: kind, Seq: seq, Time: time, Data: json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("事件排不出去：%v", err)
	}
	return string(line)
}

func textChunk(t *testing.T, seq int, time int64, text string) string {
	t.Helper()

	data, err := json.Marshal(sessionlog.AssistantChunkData{
		Turn: 1, Step: 1, Chunk: llm.TextDeltaChunk{Index: 0, Text: text},
	})
	if err != nil {
		t.Fatalf("分块负载排不出去：%v", err)
	}
	return event(t, sessionlog.EventAssistantChunk, seq, time, string(data))
}

func requestHeader(t *testing.T, system string, tool string) string {
	t.Helper()

	data, err := json.Marshal(sessionlog.RequestHeaderData{
		Header: sessionlog.EpochHeader{
			System: system,
			Tools:  []llm.ToolSchema{{Name: tool, Description: "读一个东西"}},
		},
		Reason: sessionlog.HeaderInitial,
	})
	if err != nil {
		t.Fatalf("请求头负载排不出去：%v", err)
	}
	return event(t, sessionlog.EventRequestHeader, 0, 9, string(data))
}

// requestHeaderOf 从一份压好的日志里挑出那条请求头快照的 header 字段。
func requestHeaderOf(t *testing.T, log string) map[string]any {
	t.Helper()

	for _, line := range lines(log)[1:] {
		record := decode(t, line)
		if record["type"] != string(sessionlog.EventRequestHeader) {
			continue
		}
		data, ok := record["data"].(map[string]any)
		if !ok {
			t.Fatalf("请求头事件没有负载：%v", record)
		}
		header, ok := data["header"].(map[string]any)
		if !ok {
			t.Fatalf("请求头负载里没有 header：%v", data)
		}
		return header
	}
	t.Fatalf("这份日志里没有请求头事件：\n%s", log)
	return nil
}

func decode(t *testing.T, line string) map[string]any {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("这一行读不回来：%v\n%s", err, line)
	}
	return record
}

func lines(log string) []string {
	return strings.Split(strings.TrimSuffix(log, "\n"), "\n")
}

func firstLine(log string) string {
	return lines(log)[0]
}
