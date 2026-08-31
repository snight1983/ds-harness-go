// 本文件的作用：验那些从包外够不着的内部件——切分、随机、以及两条只有构造出
// 反常环境才走得到的兜底分支。
//
// 这个文件在包内（其余测试都在 llmmockserver_test 里）。判据是「从包外能不能
// 到达」：能到达的一律走外部测试，那样验的是别人真正用得着的那一面。

package mockserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestSplitTextCountsCodePointsNotBytes 验切分按码点走。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:306-311
//
// JS 的字符串迭代按码点走，照抄成 Go 的字节切片会把一个多字节字符劈成两半，
// 线路上就出现半个字符——而这台服务器是用来验别人怎么处理流式文本的，自己
// 先发出坏字符等于把题目改掉。
func TestSplitTextCountsCodePointsNotBytes(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name string
		text string
		size int
		want []string
	}{
		{"空文本切出空表", "", 4, []string{}},
		{"整除", "abcdefgh", 4, []string{"abcd", "efgh"}},
		{"末片不满", "abcdefg", 4, []string{"abcd", "efg"}},
		{"一片装得下", "abc", 8, []string{"abc"}},
		{"每片一个码点", "abc", 1, []string{"a", "b", "c"}},
		// 每个汉字在 UTF-8 里占三个字节，按字节切会切出坏字符。
		{"多字节字符不被劈开", "中文测试内容", 2, []string{"中文", "测试", "内容"}},
		// 星号外的码点在 UTF-8 里占四个字节，在 JS 里则占两个 UTF-16 码元。
		{"星号外字符算一个码点", "𝄞𝄞𝄞", 2, []string{"𝄞𝄞", "𝄞"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := splitText(testCase.text, testCase.size)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("切分不对\n得到 %q\n想要 %q", got, testCase.want)
			}
		})
	}
}

// TestSeededRandomIsReproducibleAndInRange 验同一颗种子给出同一串数。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:299-304
//
// 「重放上次那一跑」是随机模式存在的全部理由，而它整个建立在这个函数是纯的、
// 只由种子决定这一点上。上界也要验：[chooseRandomBehavior] 的循环走不到底这个
// 结论依赖「交出的数严格小于 1」。
func TestSeededRandomIsReproducibleAndInRange(t *testing.T) {
	t.Parallel()
	first := seededRandom(42)
	second := seededRandom(42)
	for index := range 64 {
		left, right := first(), second()
		if left != right {
			t.Fatalf("第 %d 个数就对不上：%v 和 %v", index, left, right)
		}
		if left < 0 || left >= 1 {
			t.Fatalf("第 %d 个数落在 [0,1) 外：%v", index, left)
		}
	}

	// 不同的种子给出不同的串，否则「种子」这个词没有意义。
	other := seededRandom(43)
	if seededRandom(42)() == other() {
		t.Error("两颗不同的种子给出了同一个数")
	}
}

// TestChooseRandomBehaviorWalksTheWeights 验按权重挑的那一步落点正确。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:602-615
//
// 名册是排过序的（见 [resolveRandomWeights]），所以这里可以按下标算出每个区间，
// 用一个定死的 draw 精确打在区间边界上。边界正是权重实现最容易差一位的地方。
func TestChooseRandomBehaviorWalksTheWeights(t *testing.T) {
	t.Parallel()
	weights := []weightedBehavior{
		{behavior: BehaviorSuccess, weight: 1},
		{behavior: BehaviorEmpty, weight: 2},
		{behavior: BehaviorStall, weight: 1},
	}
	// 总权重 4，三段分别是 [0,1)、[1,3)、[3,4)。
	for _, testCase := range []struct {
		draw float64
		want Behavior
	}{
		{0.0, BehaviorSuccess},  // 第一段的左端
		{0.24, BehaviorSuccess}, // 第一段内
		{0.25, BehaviorEmpty},   // 第二段的左端，正好在边界上
		{0.74, BehaviorEmpty},   // 第二段内
		{0.75, BehaviorStall},   // 第三段的左端
		{0.999, BehaviorStall},  // 上界附近
	} {
		got := chooseRandomBehavior(weights, func() float64 { return testCase.draw })
		if got != testCase.want {
			t.Errorf("draw=%v 该挑 %q，挑了 %q", testCase.draw, testCase.want, got)
		}
	}

	// 只有一项时循环一步都不走，直接落到最后一项上。
	single := []weightedBehavior{{behavior: BehaviorSuccess, weight: 3}}
	if got := chooseRandomBehavior(single, func() float64 { return 0.99 }); got != BehaviorSuccess {
		t.Errorf("只有一项时该挑它，挑了 %q", got)
	}
}

// failingReader 读一下就报错，用来构造「请求体读到一半断了」。
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("读不动了") }

// TestReadJSONBodyDistinguishesEmptyBrokenAndNull 验请求体的三种下场。
//
// 「空」和「一个 JSON null」必须分开，这是 [RequestRecord.HasBody] 存在的理由；
// 「读断了」得报出来而不是当成空的——把一次传输故障静静地当成一个空请求体，
// 正是这台服务器要帮别人抓的那类毛病。
func TestReadJSONBodyDistinguishesEmptyBrokenAndNull(t *testing.T) {
	t.Parallel()

	body, hasBody, err := readJSONBody(strings.NewReader(""))
	if err != nil || hasBody || body != nil {
		t.Errorf("空请求体该是 (nil, false, nil)，得到 (%v, %v, %v)", body, hasBody, err)
	}

	body, hasBody, err = readJSONBody(strings.NewReader("null"))
	if err != nil || !hasBody || body != nil {
		t.Errorf("JSON null 该是 (nil, true, nil)，得到 (%v, %v, %v)", body, hasBody, err)
	}

	if _, _, err = readJSONBody(strings.NewReader("{oops")); err == nil {
		t.Error("坏 JSON 该报错")
	}

	// 新增: DSH 那边 Node 把请求体攒好了才调处理器，读一半断掉根本到不了这里。
	// Go 的 [net/http] 交的是一个还没读的流，这条路是真实可达的。
	if _, _, err = readJSONBody(failingReader{}); err == nil {
		t.Error("读断了该报错，不该当成一个空请求体")
	}
}

// TestRunRefusesABehaviorWithoutAScript 验那条 default 分支真的会拦住漏网的行为。
//
// 新增: TypeScript 用 assertNever 让「switch 少了一个 case」变成编译错误，Go 没有
// 等价物。少了这条 default，一个新加进名册却忘了写演法的行为会让处理器什么都不写
// 就返回——客户端收到一个空的 200，而剧本已经被消费掉了。这正是本服务器要帮别人
// 抓的「安静地演成别的样子」，自己身上一条都不能有。
//
// 这条从包外够不着：[Behaviors] 上的每一种都有演法（那由外部的
// TestEveryScriptableBehaviorIsPlayable 钉住），只有在包内才造得出一个名册外的值。
func TestRunRefusesABehaviorWithoutAScript(t *testing.T) {
	t.Parallel()
	server, err := Start(Options{Sequence: []Behavior{BehaviorSuccess}})
	if err != nil {
		t.Fatalf("起服务器失败：%v", err)
	}
	defer func() { _ = server.Close() }()

	record := &RequestRecord{Attempt: 1, Behavior: "还没写演法的行为"}
	current := &exchange{
		server:  server,
		record:  record,
		request: httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}")),
		writer:  httptest.NewRecorder(),
	}
	err = current.run()
	if err == nil {
		t.Fatal("名册外的行为该被 default 分支拦住")
	}
	if !strings.Contains(err.Error(), "没有为行为") {
		t.Errorf("理由该说清是哪种行为没演法，得到 %q", err.Error())
	}
}

// TestPauseReportsAClientThatAlreadyLeft 验零延时那一步仍然是一次存活检查。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:374-380
//
// 零延时不是「什么都不做」：DSH 在那里仍然要问一句客户端还在不在。少了这一问，
// 一个已经走掉的客户端会被记成正常收尾，而「客户端先走了」和「本端演完了」是
// 这台服务器要区分的两件事。
//
// 这条从包外够不着：要让客户端恰好在零延时那一瞬之前走掉，从外面只能靠抢时间。
func TestPauseReportsAClientThatAlreadyLeft(t *testing.T) {
	t.Parallel()
	server, err := Start(Options{Sequence: []Behavior{BehaviorSuccess}})
	if err != nil {
		t.Fatalf("起服务器失败：%v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 客户端已经走了。

	record := &RequestRecord{Attempt: 1, Behavior: BehaviorSuccess}
	current := &exchange{
		server:  server,
		record:  record,
		request: httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx),
		writer:  httptest.NewRecorder(),
	}

	if current.pause(0) {
		t.Error("客户端已经走了，零延时那一步该返回 false")
	}
	if record.Outcome != OutcomeClientClosed {
		t.Errorf("结局该记成 %q，记成了 %q", OutcomeClientClosed, record.Outcome)
	}
}

// TestStallReleasesWhenTheClientWalksAway 验挂死的流在客户端走掉时放行。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:434-437
//
// stall 是唯一一种会把处理器阻塞住的演法，所以它的两条出路都得验：客户端自己
// 取消（这一条），以及服务器被关掉（外部的 TestServerCloseWakesAStalledHandler）。
// 少了前一条，一个取消了请求的客户端会在服务器上留下一个永远不退的协程。
//
// 这条从包外够不着得稳：从外面取消客户端之后，两条出路会同时就绪，Go 的 select
// 此时随机挑一个——想稳稳走到这一条，只能在包内把另一条摘掉。
func TestStallReleasesWhenTheClientWalksAway(t *testing.T) {
	t.Parallel()
	server, err := Start(Options{Sequence: []Behavior{BehaviorStall}})
	if err != nil {
		t.Fatalf("起服务器失败：%v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 客户端已经走了，服务器还开着。

	current := &exchange{
		server:  server,
		record:  &RequestRecord{Attempt: 1, Behavior: BehaviorStall},
		request: httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx),
		writer:  httptest.NewRecorder(),
	}

	// 客户端走掉是正常出路，不该报错——连接已经没了，没有谁还需要被掐。
	if err := current.stall(); err != nil {
		t.Errorf("客户端走掉该是正常出路，得到 %v", err)
	}
}

// TestEventMarkersSealTheInterface 验两种事件各自带着那个封口用的方法。
//
// [Event] 靠一个非导出方法把实现者关在包内，于是消费方的 type switch 是穷尽的，
// 不需要一个「不认识的事件」兜底分支。这条断言钉的就是那个前提：名册上确实只有
// 这两种，而且它们都真的实现了封口。
func TestEventMarkersSealTheInterface(t *testing.T) {
	t.Parallel()
	var events = []Event{RequestEvent{}, ResultEvent{}}
	for _, event := range events {
		switch typed := event.(type) {
		case RequestEvent:
			typed.isEvent()
		case ResultEvent:
			typed.isEvent()
		default:
			t.Errorf("多出来一种事件：%T", event)
		}
	}
}
