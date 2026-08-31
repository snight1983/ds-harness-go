// 本文件的作用：把那套呈现词汇排出去的形状钉住——每张卡片带哪个判别标签，
// 以及哪几个字段「没给」和「给了零值」是两回事。
//
// # 这套词汇的错法
//
//   - **把判别标签做成结构体字段**。字段是可写的，一个能被改掉的标签等于没有：
//     界面按标签选渲染分支，标签一错就渲染成另一种卡片。这里标签是接口上的方法，
//     只在排 JSON 时补回去。
//   - **把「没有退出码」和「退出码是 0」拧成一件事**。0 是正常退出，nil 是这次执行
//     压根没等到退出码（还在跑、或者被信号打断）。行号和 oldText 同理：
//     oldText 为 nil 是「新建文件」，空串是「本来就是个空文件」。
package tools_test

import (
	"encoding/json"
	"testing"

	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// intPtr 造一个整数指针，给行号和退出码用。
func intPtr(value int) *int { return &value }

// stringPtr 造一个字符串指针，给 FileDiff.OldText 用。
func stringPtr(value string) *string { return &value }

// encodeView 把一张卡片排成 JSON 文本。
func encodeView(t *testing.T, view any) string {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("这张卡片应该排得出去：%v", err)
	}
	return string(encoded)
}

// fieldsOf 把一张卡片排出来再解成键值表，好逐键断言。
func fieldsOf(t *testing.T, view any) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal([]byte(encodeView(t, view)), &fields); err != nil {
		t.Fatalf("排出来的东西应该是个 JSON 对象：%v", err)
	}
	return fields
}

func TestCallViewsCarryTheirCard(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		view tools.CallView
		card string
	}{
		"通用":  {tools.GenericCallView{Title: "跑一下"}, "generic"},
		"终端":  {tools.TerminalCallView{Title: "ls"}, "terminal"},
		"改文件": {tools.DiffCallView{Title: "改一处", Diffs: []tools.FileDiff{}}, "diff"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if testCase.view.Card() != testCase.card {
				t.Fatalf("Card() 不对：%q", testCase.view.Card())
			}
			// 标签必须出现在排出去的那一份里——界面读的是 JSON，不是 Go 的方法。
			if fieldsOf(t, testCase.view)["card"] != testCase.card {
				t.Fatalf("排出去的 card 不对：%s", encodeView(t, testCase.view))
			}
		})
	}
}

func TestResultViewsCarryTheirCard(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		view  tools.ResultView
		card  string
		extra map[string]string
	}{
		"通用": {view: tools.GenericResultView{Title: "好了"}, card: "generic"},
		"终端": {view: tools.TerminalResultView{Output: "ok"}, card: "terminal"},
		"改文件": {
			view: tools.DiffResultView{Diffs: []tools.FileDiff{}},
			card: "diff",
		},
		"读文件": {view: tools.ReadResultView{Path: "a.go", Lines: []tools.ReadFileLine{}}, card: "read"},
		// 搜索和网页这两族多带一层形状标签：同一张卡片下面还分几种排布，
		// 界面先按 card 选卡片、再按 shape/kind 选排布。
		"搜索按行命中": {
			view:  tools.SearchMatchesResultView{Files: []tools.SearchFileMatches{}},
			card:  "search",
			extra: map[string]string{"shape": "matches"},
		},
		"搜索只有路径": {
			view:  tools.SearchPathsResultView{Paths: []string{}},
			card:  "search",
			extra: map[string]string{"shape": "paths"},
		},
		"联网搜索": {
			view:  tools.WebSearchResultView{Sources: []tools.WebSource{}},
			card:  "web",
			extra: map[string]string{"kind": "search"},
		},
		"抓取网页": {
			view:  tools.WebFetchResultView{URL: "https://example.com"},
			card:  "web",
			extra: map[string]string{"kind": "fetch"},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if testCase.view.Card() != testCase.card {
				t.Fatalf("Card() 不对：%q", testCase.view.Card())
			}
			fields := fieldsOf(t, testCase.view)
			if fields["card"] != testCase.card {
				t.Fatalf("排出去的 card 不对：%s", encodeView(t, testCase.view))
			}
			for key, wanted := range testCase.extra {
				if fields[key] != wanted {
					t.Fatalf("第二层标签 %s 不对：%s", key, encodeView(t, testCase.view))
				}
			}
		})
	}
}

func TestCallViewsKeepTheirPayload(t *testing.T) {
	t.Parallel()
	line := 12
	view := tools.GenericCallView{
		Title:     "读一段",
		Kind:      tools.CallRead,
		RawInput:  raw(`{"path":"a.go"}`),
		Content:   llm.Content{llm.TextBlock{Text: "正在读"}},
		Locations: []tools.FileLocation{{Path: "a.go", Line: &line}, {Path: "b.go"}},
	}
	encoded := encodeView(t, view)
	wanted := `{"card":"generic","content":[{"type":"text","text":"正在读"}],` +
		`"kind":"read","locations":[{"path":"a.go","line":12},{"path":"b.go"}],` +
		`"rawInput":{"path":"a.go"},"title":"读一段"}`
	if encoded != wanted {
		t.Fatalf("排出来的形状不对：\n实际 %s\n期望 %s", encoded, wanted)
	}

	terminal := encodeView(t, tools.TerminalCallView{Title: "ls", Description: "看看有什么", Cwd: "/tmp"})
	if terminal != `{"card":"terminal","cwd":"/tmp","description":"看看有什么","title":"ls"}` {
		t.Fatalf("终端卡片形状不对：%s", terminal)
	}

	diff := encodeView(t, tools.DiffCallView{
		Title: "改两处",
		Diffs: []tools.FileDiff{
			{Path: "new.go", OldText: nil, NewText: "package main"},
			{Path: "empty.go", OldText: stringPtr(""), NewText: "x"},
		},
		Locations: []tools.FileLocation{{Path: "new.go"}},
	})
	// oldText 为 null 是「这个文件是新建的」，空串是「本来就是个空文件」——
	// 两者都必须出现在排出去的那一份里，omitempty 会把它们拧成一件事。
	if diff != `{"card":"diff","diffs":[{"path":"new.go","oldText":null,"newText":"package main"},`+
		`{"path":"empty.go","oldText":"","newText":"x"}],"locations":[{"path":"new.go"}],"title":"改两处"}` {
		t.Fatalf("改文件卡片形状不对：%s", diff)
	}
}

func TestResultViewsKeepTheirPayload(t *testing.T) {
	t.Parallel()
	terminal := fieldsOf(t, tools.TerminalResultView{
		Title:    "跑完了",
		Output:   "hello",
		ExitCode: intPtr(0),
		Signal:   "SIGTERM",
	})
	// 退出码 0 是「正常退出」，它必须排得出去；omitempty 会把它和「没有退出码」
	// 拧成一件事，界面就没法把「成功」和「还没结束」分开。
	if terminal["exitCode"] != float64(0) {
		t.Fatalf("退出码 0 应该排得出去：%v", terminal)
	}
	if _, present := fieldsOf(t, tools.TerminalResultView{Output: "x"})["exitCode"]; present {
		t.Fatalf("没有退出码时不该排出这个键")
	}

	read := encodeView(t, tools.ReadResultView{
		Title:      "a.go",
		Path:       "a.go",
		Offset:     10,
		Lines:      []tools.ReadFileLine{{Number: 10, Text: "package main"}},
		TotalLines: 200,
		Lang:       "go",
		Content:    llm.Content{llm.TextBlock{Text: "package main"}},
	})
	if read != `{"card":"read","content":[{"type":"text","text":"package main"}],"lang":"go",`+
		`"lines":[{"number":10,"text":"package main"}],"offset":10,"path":"a.go",`+
		`"title":"a.go","totalLines":200}` {
		t.Fatalf("读文件结果形状不对：%s", read)
	}

	matches := encodeView(t, tools.SearchMatchesResultView{
		Title: "找 foo",
		Files: []tools.SearchFileMatches{
			{Path: "a.go", Matches: []tools.SearchLineMatch{{LineNumber: 3, Line: "foo()"}}},
		},
		Truncated: true,
		Total:     99,
	})
	if matches != `{"card":"search","files":[{"path":"a.go","matches":[{"lineNumber":3,"line":"foo()"}]}],`+
		`"shape":"matches","title":"找 foo","total":99,"truncated":true}` {
		t.Fatalf("搜索命中结果形状不对：%s", matches)
	}

	paths := encodeView(t, tools.SearchPathsResultView{Paths: []string{"a.go", "b.go"}, Total: 2})
	if paths != `{"card":"search","paths":["a.go","b.go"],"shape":"paths","total":2,"truncated":false}` {
		t.Fatalf("搜索路径结果形状不对：%s", paths)
	}

	web := encodeView(t, tools.WebSearchResultView{
		Sources: []tools.WebSource{
			{URL: "https://example.com", Title: "示例", Snippet: "一段", PublishedAt: "去年"},
		},
		Answer: "答案",
	})
	// PublishedAt 原样保管：本包不解释它的格式，"去年" 也照发。
	if web != `{"answer":"答案","card":"web","kind":"search","sources":[{"url":"https://example.com",`+
		`"title":"示例","snippet":"一段","publishedAt":"去年"}],"truncated":false}` {
		t.Fatalf("联网搜索结果形状不对：%s", web)
	}

	fetch := encodeView(t, tools.WebFetchResultView{URL: "https://example.com", StatusCode: 404, Truncated: true})
	if fetch != `{"card":"web","kind":"fetch","statusCode":404,"truncated":true,"url":"https://example.com"}` {
		t.Fatalf("抓取网页结果形状不对：%s", fetch)
	}

	generic := encodeView(t, tools.GenericResultView{Content: llm.Content{llm.TextBlock{Text: "好了"}}})
	if generic != `{"card":"generic","content":[{"type":"text","text":"好了"}]}` {
		t.Fatalf("通用结果形状不对：%s", generic)
	}

	diff := encodeView(t, tools.DiffResultView{Diffs: []tools.FileDiff{{Path: "a.go", OldText: stringPtr("old"), NewText: "new"}}})
	if diff != `{"card":"diff","diffs":[{"path":"a.go","oldText":"old","newText":"new"}]}` {
		t.Fatalf("改文件结果形状不对：%s", diff)
	}
}

func TestViewsAreASealedUnion(t *testing.T) {
	t.Parallel()
	// 这两族都是封闭联合：变体只能在本包里加。这个测试真正在断言的是**编译得过**——
	// 下面每一个都实现了那个带未导出方法的接口，包外写不出第九种结果卡片。
	callViews := []tools.CallView{
		tools.GenericCallView{}, tools.TerminalCallView{}, tools.DiffCallView{},
	}
	resultViews := []tools.ResultView{
		tools.GenericResultView{}, tools.TerminalResultView{}, tools.DiffResultView{},
		tools.SearchMatchesResultView{}, tools.SearchPathsResultView{},
		tools.ReadResultView{}, tools.WebSearchResultView{}, tools.WebFetchResultView{},
	}
	if len(callViews) != 3 || len(resultViews) != 8 {
		t.Fatalf("变体数量对不上：%d / %d", len(callViews), len(resultViews))
	}
	for _, view := range callViews {
		if view.Card() == "" {
			t.Fatalf("每张调用卡片都得有标签")
		}
	}
	for _, view := range resultViews {
		if view.Card() == "" {
			t.Fatalf("每张结果卡片都得有标签")
		}
	}
}

func TestCallKindsAreTheDocumentedSet(t *testing.T) {
	t.Parallel()
	kinds := []tools.CallKind{
		tools.CallRead, tools.CallEdit, tools.CallDelete, tools.CallMove,
		tools.CallSearch, tools.CallExecute, tools.CallFetch, tools.CallOther,
	}
	wanted := []string{"read", "edit", "delete", "move", "search", "execute", "fetch", "other"}
	for index, kind := range kinds {
		if string(kind) != wanted[index] {
			t.Fatalf("第 %d 个类别不对：%q", index, kind)
		}
	}
}
