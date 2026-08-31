// 本文件的作用：配置这一侧——一份夹具从哪里读、旁挂文件怎么盖在推出来的剧本上、
// 父子两份剧本按什么次序排队，以及那一步从环境变量补默认。

package replay

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ds-harness-go/llm"
)

func TestLoadScriptDerivesFromTheJSONLWhenNoOverrideIsPresent(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks())
	script, err := LoadScript(Config{File: file})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	if !reflect.DeepEqual(script, []Entry{ChunksEntry{Chunks: textChunks()}}) {
		t.Fatalf("剧本不对：%+v", script)
	}
}

func TestLoadScriptFailsLoudWhenTheFixtureIsMissing(t *testing.T) {
	_, err := LoadScript(Config{File: filepath.Join(fixtureDir(t), "absent.jsonl")})
	if !errors.Is(err, ErrFixtureNotFound) {
		t.Fatalf("要 ErrFixtureNotFound，实际 %v", err)
	}
}

func TestLoadScriptFailsWhenTheFixtureBodyIsMalformed(t *testing.T) {
	dir := fixtureDir(t)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"),
		sessionJSONL(headerLine(t, "s1", 0, 0), "{oops"))
	_, err := LoadScript(Config{File: file})
	if !errors.Is(err, ErrMalformedFixture) {
		t.Fatalf("要 ErrMalformedFixture，实际 %v", err)
	}
}

func TestLoadScriptPrefersTheSidecarAndFallsBackWhenItIsAbsent(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks())
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"),
		`[{"kind":"throw","chunks":[],"message":"401","code":"AUTH"}]`)

	replaced, err := LoadScript(Config{File: file, OverrideFile: sidecar})
	if err != nil {
		t.Fatalf("读旁挂失败：%v", err)
	}
	if !reflect.DeepEqual(replaced, []Entry{ThrowEntry{Chunks: []llm.StreamChunk{}, Message: "401", Code: "AUTH"}}) {
		t.Fatalf("整份替换不对：%+v", replaced)
	}

	// 配了路径但那份文件不在：回落到从 JSONL 推出来的那一份。
	derived, err := LoadScript(Config{File: file, OverrideFile: filepath.Join(dir, "nope.json")})
	if err != nil || len(derived) != 1 {
		t.Fatalf("该回落到推导，实际 %+v / %v", derived, err)
	}
}

func TestLoadScriptFailsWhenTheSidecarCannotBeRead(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks())
	// 一个目录 Stat 得到、ReadFile 读不了：走的是「读不了旁挂文件」那一支。
	sidecar := filepath.Join(dir, "override-dir")
	if err := os.Mkdir(sidecar, 0o700); err != nil {
		t.Fatalf("造目录失败：%v", err)
	}
	_, err := LoadScript(Config{File: file, OverrideFile: sidecar})
	if !errors.Is(err, ErrInvalidOverride) {
		t.Fatalf("要 ErrInvalidOverride，实际 %v", err)
	}
}

func TestLoadScriptPatchesTheNamedCallAndKeepsItsSiblings(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks(), shortChunks("two"))
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"),
		`{"patches":[{"at":0,"entry":{"kind":"throw","chunks":[],"message":"transient","code":"SERVER"}}]}`)
	script, err := LoadScript(Config{File: file, OverrideFile: sidecar})
	if err != nil {
		t.Fatalf("打补丁失败：%v", err)
	}
	if len(script) != 2 || script[0].Kind() != EntryThrow {
		t.Fatalf("补丁该只换第 0 次调用，实际 %+v", script)
	}
	if !reflect.DeepEqual(script[1], ChunksEntry{Chunks: shortChunks("two")}) {
		t.Fatalf("第 1 次调用该原样留着，实际 %+v", script[1])
	}
}

func TestLoadScriptAppendsWhenThePatchIndexEqualsTheDerivedLength(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks())
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `{"patches":[
		{"at":0,"entry":{"kind":"throw","chunks":[],"message":"429","code":"RATE_LIMIT"}},
		{"at":1,"entry":{"kind":"hang"}}
	]}`)
	script, err := LoadScript(Config{File: file, OverrideFile: sidecar})
	if err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if len(script) != 2 || script[1].Kind() != EntryHang {
		t.Fatalf("at 等于推导长度时该追加，实际 %+v", script)
	}
}

func TestLoadScriptRejectsAnOutOfRangeOrDuplicatePatchIndex(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "s1", 0, textChunks())
	cases := map[string]struct{ doc, want string }{
		"越界": {`{"patches":[{"at":2,"entry":{"kind":"hang"}}]}`, "越界"},
		"重复": {`{"patches":[{"at":0,"entry":{"kind":"hang"}},{"at":0,"entry":{"kind":"hang"}}]}`, "重复"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			sidecar := writeFile(t, filepath.Join(dir, name+".json"), testCase.doc)
			_, err := LoadScript(Config{File: file, OverrideFile: sidecar})
			if !errors.Is(err, ErrInvalidOverride) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("要一句带 %q 的 ErrInvalidOverride，实际 %v", testCase.want, err)
			}
		})
	}
}

func TestLoadScriptFailsWhenAPatchNeedsAFixtureThatIsNotThere(t *testing.T) {
	dir := fixtureDir(t)
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"),
		`{"patches":[{"at":0,"entry":{"kind":"hang"}}]}`)
	// 增补形式要先推一遍 JSONL，所以夹具不在时它一样当场失败。
	_, err := LoadScript(Config{File: filepath.Join(dir, "absent.jsonl"), OverrideFile: sidecar})
	if !errors.Is(err, ErrFixtureNotFound) {
		t.Fatalf("要 ErrFixtureNotFound，实际 %v", err)
	}
}

func TestLoadSessionScriptsReturnsOnePrimaryForASingleSessionScenario(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 100, textChunks())
	scripts, err := LoadSessionScripts(Config{File: file})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	if len(scripts) != 1 || scripts[0].RecordedID != "p" || scripts[0].CreatedAt != 100 || !scripts[0].Primary {
		t.Fatalf("主剧本不对：%+v", scripts)
	}
}

func TestLoadSessionScriptsOrdersChildrenByCreatedAtAfterThePrimary(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "parent", 100, textChunks())
	later := writeCalls(t, dir, "session.1.jsonl", "late", 200, textChunks())
	tie := writeCalls(t, dir, "session.2.jsonl", "tie", 100, textChunks())
	scripts, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{later, tie}})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	var order []string
	for _, script := range scripts {
		order = append(order, string(script.RecordedID))
	}
	if !reflect.DeepEqual(order, []string{"parent", "tie", "late"}) {
		t.Fatalf("次序不对：%v", order)
	}
}

func TestLoadSessionScriptsBreaksACreatedAtTieByRecordedID(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "parent", 100, textChunks())
	second := writeCalls(t, dir, "session.2.jsonl", "c2", 100, textChunks())
	first := writeCalls(t, dir, "session.1.jsonl", "c1", 100, textChunks())
	scripts, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{second, first}})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	if scripts[1].RecordedID != "c1" || scripts[2].RecordedID != "c2" {
		t.Fatalf("平手时该按录下来的 id 定序，实际 %+v", scripts)
	}
}

func TestLoadSessionScriptsDerivesAForkChildFromItsOwnEventsOnly(t *testing.T) {
	// 一份分叉出来的日志开头带着继承过来的父分块；从整份日志推会把父的回答
	// 当成孩子自己的调用回放一遍。
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "parent", 100, textChunks())
	childLines := []string{
		chunkLine(t, 0, 1, 1, llm.TextDeltaChunk{Index: 0, Text: "PARENT"}),
		chunkLine(t, 1, 1, 1, llm.FinishChunk{Reason: llm.StopFinish{}}),
		chunkLine(t, 2, 2, 1, llm.TextDeltaChunk{Index: 0, Text: "CHILD"}),
		chunkLine(t, 3, 2, 1, llm.FinishChunk{Reason: llm.StopFinish{}}),
	}
	child := writeFile(t, filepath.Join(dir, "session.1.jsonl"),
		sessionJSONL(headerLine(t, "child", 200, 2), childLines...))

	scripts, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{child}})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	want := []Entry{ChunksEntry{Chunks: []llm.StreamChunk{
		llm.TextDeltaChunk{Index: 0, Text: "CHILD"},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}}
	if !reflect.DeepEqual(scripts[1].Entries, want) {
		t.Fatalf("孩子的剧本该只有它自己那次调用，实际 %+v", scripts[1].Entries)
	}
}

func TestLoadSessionScriptsRejectsASeedLengthLongerThanTheLog(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "parent", 100, textChunks())
	child := writeFile(t, filepath.Join(dir, "session.1.jsonl"),
		sessionJSONL(headerLine(t, "child", 200, 9)))
	_, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{child}})
	if !errors.Is(err, ErrMalformedFixture) || !strings.Contains(err.Error(), "seedLength") {
		t.Fatalf("要一句点名 seedLength 的 ErrMalformedFixture，实际 %v", err)
	}
}

func TestLoadSessionScriptsFailsWhenAChildFixtureIsMissing(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	_, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{filepath.Join(dir, "absent.jsonl")}})
	if !errors.Is(err, ErrFixtureNotFound) {
		t.Fatalf("要 ErrFixtureNotFound，实际 %v", err)
	}
}

func TestLoadSessionScriptsRejectsAChildFixtureItCannotRead(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	child := writeFile(t, filepath.Join(dir, "session.1.jsonl"),
		sessionJSONL(headerLine(t, "c", 2, 0), "{oops"))
	_, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{child}})
	if !errors.Is(err, ErrMalformedFixture) {
		t.Fatalf("要 ErrMalformedFixture，实际 %v", err)
	}
}

func TestLoadSessionScriptsRejectsAChildHeaderItCannotRead(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	child := writeFile(t, filepath.Join(dir, "session.1.jsonl"), `{"type":"session","id":42}`+"\n")
	_, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{child}})
	if !errors.Is(err, ErrMalformedFixture) {
		t.Fatalf("要 ErrMalformedFixture，实际 %v", err)
	}
}

func TestLoadSessionScriptsUsesTheOverrideForThePrimaryAndStillDerivesChildren(t *testing.T) {
	dir := fixtureDir(t)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"), sessionJSONL(headerLine(t, "p", 1, 0)))
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `[{"kind":"hang"}]`)
	child := writeCalls(t, dir, "session.1.jsonl", "c", 2, textChunks())
	scripts, err := LoadSessionScripts(Config{File: file, OverrideFile: sidecar, ChildFiles: []string{child}})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	if len(scripts[0].Entries) != 1 || scripts[0].Entries[0].Kind() != EntryHang {
		t.Fatalf("主剧本该来自旁挂文件，实际 %+v", scripts[0].Entries)
	}
	if len(scripts[1].Entries) != 1 {
		t.Fatalf("孩子该照旧从它自己的 JSONL 推，实际 %+v", scripts[1].Entries)
	}
}

func TestLoadSessionScriptsDefaultsThePrimaryHeaderForAnOverrideOnlyFixture(t *testing.T) {
	// 一份只有旁挂文件、根本没有 JSONL 的夹具：头用零值，它照样排在最前面当主会话。
	dir := fixtureDir(t)
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `[{"kind":"hang"}]`)
	scripts, err := LoadSessionScripts(Config{File: filepath.Join(dir, "absent.jsonl"), OverrideFile: sidecar})
	if err != nil {
		t.Fatalf("读剧本失败：%v", err)
	}
	if len(scripts) != 1 || scripts[0].RecordedID != "" || scripts[0].CreatedAt != 0 || !scripts[0].Primary {
		t.Fatalf("主剧本该用零值头，实际 %+v", scripts)
	}
}

func TestLoadSessionScriptsRejectsAPrimaryFixtureItCannotRead(t *testing.T) {
	// 一个目录 Stat 得到、ReadFile 读不了；整份替换的旁挂文件让剧本那一步先过去，
	// 于是走到的是读头之前那一手。
	dir := fixtureDir(t)
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `[{"kind":"hang"}]`)
	_, err := LoadSessionScripts(Config{File: dir, OverrideFile: sidecar})
	if !errors.Is(err, ErrFixtureNotFound) {
		t.Fatalf("要 ErrFixtureNotFound，实际 %v", err)
	}
}

func TestLoadSessionScriptsRejectsAChildScriptItCannotDerive(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	child := writeFile(t, filepath.Join(dir, "session.1.jsonl"), sessionJSONL(headerLine(t, "c", 2, 0),
		chunkLine(t, 1, 1, 1, llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText})))
	_, err := LoadSessionScripts(Config{File: file, ChildFiles: []string{child}})
	if !errors.Is(err, ErrUnrecoverableScript) {
		t.Fatalf("要 ErrUnrecoverableScript，实际 %v", err)
	}
}

func TestLoadSessionScriptsRejectsAPrimaryHeaderItCannotRead(t *testing.T) {
	dir := fixtureDir(t)
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `[{"kind":"hang"}]`)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"), `{"type":"session","id":42}`+"\n")
	_, err := LoadSessionScripts(Config{File: file, OverrideFile: sidecar})
	if !errors.Is(err, ErrMalformedFixture) {
		t.Fatalf("要 ErrMalformedFixture，实际 %v", err)
	}
}

func TestResolveFromEnvFillsWhatTheConfigDidNotGive(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	child := writeCalls(t, dir, "session.1.jsonl", "c", 2, textChunks())
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), `[{"kind":"hang"}]`)
	t.Setenv(EnvFile, file)
	t.Setenv(EnvOverride, sidecar)
	t.Setenv(EnvChildFiles, child)

	resolved, err := ResolveFromEnv(Config{})
	if err != nil {
		t.Fatalf("补默认失败：%v", err)
	}
	if resolved.File != file || resolved.OverrideFile != sidecar {
		t.Fatalf("路径没补上：%+v", resolved)
	}
	if !reflect.DeepEqual(resolved.ChildFiles, []string{child}) {
		t.Fatalf("子会话清单没补上：%+v", resolved.ChildFiles)
	}
}

func TestResolveFromEnvKeepsWhatTheConfigAlreadyGave(t *testing.T) {
	t.Setenv(EnvFile, "env-file")
	t.Setenv(EnvOverride, "env-override")
	t.Setenv(EnvChildFiles, "env-child")
	resolved, err := ResolveFromEnv(Config{File: "own", OverrideFile: "own-override", ChildFiles: []string{}})
	if err != nil {
		t.Fatalf("补默认失败：%v", err)
	}
	if resolved.File != "own" || resolved.OverrideFile != "own-override" || len(resolved.ChildFiles) != 0 {
		t.Fatalf("配置里给了的字段不该被环境变量盖掉：%+v", resolved)
	}
}

func TestResolveFromEnvIgnoresAnEmptyChildFileList(t *testing.T) {
	t.Setenv(EnvFile, "own")
	t.Setenv(EnvChildFiles, "")
	resolved, err := ResolveFromEnv(Config{})
	if err != nil || resolved.ChildFiles != nil {
		t.Fatalf("空清单该当没给，实际 %+v / %v", resolved.ChildFiles, err)
	}
}

func TestResolveFromEnvRejectsAnUnusableConfig(t *testing.T) {
	t.Setenv(EnvFile, "")
	if _, err := ResolveFromEnv(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("没给夹具路径要 ErrInvalidConfig，实际 %v", err)
	}
	if _, err := ResolveFromEnv(Config{File: "own", Pace: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("负数 Pace 要 ErrInvalidConfig，实际 %v", err)
	}
	bad := Config{File: "own", Providers: []ProviderConfig{
		{ID: "m", Models: []ModelConfig{{ID: "m", InputModalities: []llm.ModelModality{"audio"}}}},
	}}
	_, err := ResolveFromEnv(bad)
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "inputModalities") {
		t.Fatalf("模态认不出来要一句点名 inputModalities 的 ErrInvalidConfig，实际 %v", err)
	}
}
