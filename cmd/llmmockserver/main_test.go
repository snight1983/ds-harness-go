// 本文件的作用：验这个进程本身——播报的那几行 JSONL、参数出错时的下场、
// 以及信号换算出来的退出码。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:12-49
//
// 新增: DSH 那一整段是标着 v8 ignore 的、验不到的胶水——它直接取全局的
// process.argv／process.stdout，还自己往 process 上挂信号处理器，测试进不去。
// Go 这边把这三样都做成了 [run] 的参数，于是整段可以在测试里原样跑一遍。

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// recordWait 是等一行播报的上限。到了这个点还没等到，等的那一行就是真的不会来了，
// 与其挂到整个包超时（那时只剩一堆读不出所以然的协程栈），不如当场把话说清。
const recordWait = 30 * time.Second

// runResult 是一次后台跑完之后能问到的东西。
type runResult struct {
	code   int
	stderr *bytes.Buffer
	lines  <-chan string
	closer func()
}

// startRun 在后台跑一趟，把标准输出接到一根管子上，再由一条协程不停地把它切成行。
//
// 用管子而不是缓冲区，是因为这些用例要等的是「播报到哪一行了」，而不是
// 「跑完之后一共播报了什么」——进程在收到信号之前根本不会结束。
//
// 那条协程不能省。[io.Pipe] 是同步的：没人读的时候一次写入就把写的那一方钉在原地。
// 而播报正是从 HTTP 处理器协程上来的，[TestRunForwardsTelemetryAsJSONL] 又恰好在
// 请求还没返回的时候停在 http.Post 里——没人读，处理器发不出 request 那一行，
// 于是响应头永远不写，Post 永远不返回，两边互相等着。改成有人一直在读之后，
// 「读到第几行」这件事由带缓冲的通道来记，而不是由谁先动谁后动来记。
func startRun(t *testing.T, argv []string, signals <-chan os.Signal) *runResult {
	t.Helper()
	reader, writer := io.Pipe()
	lines := make(chan string, 64)
	result := &runResult{stderr: &bytes.Buffer{}, lines: lines}

	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		// 关掉通道就是「播报到此为止」，让等下一行的人立刻知道没有下一行了。
		close(lines)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		result.code = run(argv, writer, result.stderr, signals)
		_ = writer.Close()
	}()
	result.closer = func() { <-done }
	t.Cleanup(func() { _ = reader.Close() })
	return result
}

// nextRecord 等下一行 JSONL 并解开它。
func nextRecord(t *testing.T, result *runResult) map[string]any {
	t.Helper()
	select {
	case line, ok := <-result.lines:
		if !ok {
			t.Fatal("播报已经结束，没等到下一行")
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("这一行不是 JSON：%q", line)
		}
		return record
	case <-time.After(recordWait):
		t.Fatalf("等了 %s 也没等到下一行播报", recordWait)
		return nil
	}
}

// freePort 借一个端口再还回去，好让 connection_refused 有个显式端口可绑。
//
// connection_refused 演的是「那个端口上没人」，所以它要求显式的非零端口——
// 让操作系统挑的话，播报出去的地址在绑上之前根本不存在。
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("借端口失败：%v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// TestRunPrintsUsageForHelp 验求助走标准输出、退出码为零。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:14-17
//
// 用法说明是被人读的，不是错误，所以它去标准输出而不是标准错误；退出码 0 让
// `mockserver --help` 在脚本的 set -e 下不会把整个脚本带倒。
func TestRunPrintsUsageForHelp(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr, nil); code != 0 {
		t.Errorf("求助的退出码该是 0，得到 %d", code)
	}
	if !strings.Contains(stdout.String(), "--sequence") {
		t.Errorf("用法说明该进标准输出，得到 %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("求助不该往标准错误写东西，得到 %q", stderr.String())
	}
}

// TestRunRejectsBadArguments 验参数出错时理由和用法一起进标准错误。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:18-20
//
// 两样都要给：只给理由，人得再敲一次 --help 才知道该怎么写；只给用法，人不知道
// 自己错在哪一个字上。标准输出保持干净，那样一个读 JSONL 的脚本不会把错误信息
// 当成一条播报去解析。
func TestRunRejectsBadArguments(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		argv   []string
		reason string
	}{
		{"没给剧本", nil, "--sequence 是必填的"},
		{"不认识的选项", []string{"--wat"}, "参数解析失败"},
		{"不认识的行为", []string{"--sequence", "nope"}, "不认识的行为"},
		{"拒连要显式端口", []string{"--sequence", "connection_refused,success", "--port", "0"}, "非零的 --port"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := run(testCase.argv, &stdout, &stderr, nil); code != 1 {
				t.Errorf("参数出错的退出码该是 1，得到 %d", code)
			}
			if !strings.Contains(stderr.String(), testCase.reason) {
				t.Errorf("理由里该有 %q，得到 %q", testCase.reason, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Error("报错时该把用法一起给出来")
			}
			if stdout.Len() != 0 {
				t.Errorf("报错不该弄脏标准输出，得到 %q", stdout.String())
			}
		})
	}
}

// TestRunRejectsAnUnusableListener 验绑不上端口时的下场。
//
// 这条走的是 [mockserver.Start] 失败那一路：参数本身讲得通，是监听器起不来。
// 它和参数出错共用同一个出口（退出码 1 + 用法说明），因为对调用方来说两者是
// 同一件事——这台服务器没起来，别往下走了。
func TestRunRejectsAnUnusableListener(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占端口失败：%v", err)
	}
	defer func() { _ = occupied.Close() }()
	port := occupied.Addr().(*net.TCPAddr).Port

	var stdout, stderr bytes.Buffer
	code := run([]string{"--sequence", "success", "--port", strconv.Itoa(port)}, &stdout, &stderr, nil)
	if code != 1 {
		t.Errorf("绑不上端口的退出码该是 1，得到 %d", code)
	}
	if !strings.Contains(stderr.String(), "监听") {
		t.Errorf("理由该说清是监听失败，得到 %q", stderr.String())
	}
}

// TestRunAnnouncesReadyThenExitsOnSignal 验最常走的那一趟：播报 ready，收信号退出。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:32-44
//
// ready 那一行是整个进程存在的理由：编排脚本读第一行就知道往哪儿连、以及这一跑
// 的种子是多少。randomSeed 必须在里面——一次随机长跑挂掉之后，重放它靠的就是它。
func TestRunAnnouncesReadyThenExitsOnSignal(t *testing.T) {
	t.Parallel()
	signals := make(chan os.Signal, 1)
	result := startRun(t, []string{"--sequence", "random", "--port", "0"}, signals)

	ready := nextRecord(t, result)
	if ready["type"] != "ready" {
		t.Fatalf("第一行该是 ready，得到 %v", ready)
	}
	baseURL, _ := ready["baseURL"].(string)
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") || !strings.HasSuffix(baseURL, "/v1") {
		t.Errorf("播报的地址不对：%q", baseURL)
	}
	// 端口给的是 0（让操作系统挑），播报出去的必须是真正绑上的那个，否则这一行
	// 的全部用处——告诉别人往哪儿连——就没了。
	if strings.Contains(baseURL, ":0/") {
		t.Errorf("播报的该是真正绑上的端口，得到 %q", baseURL)
	}
	if _, hasSeed := ready["randomSeed"].(float64); !hasSeed {
		t.Errorf("ready 里必须带种子，否则这一跑重放不出来：%v", ready)
	}

	signals <- syscall.SIGINT
	result.closer()
	if result.code != 130 {
		t.Errorf("SIGINT 该换算成 130，得到 %d", result.code)
	}
}

// TestRunForwardsTelemetryAsJSONL 验遥测真的被转成了 JSONL 播报出去。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:38
//
// 这是这个进程存在的另一半理由：被测的那一侧是个独立进程，没法把模拟服务器当库
// 嵌进去问它 [mockserver.Server.Requests]，只能靠这几行 JSONL 隔着管子看。
// 键名保持小驼峰，因为读这些行的是已经存在的脚本。
func TestRunForwardsTelemetryAsJSONL(t *testing.T) {
	t.Parallel()
	signals := make(chan os.Signal, 1)
	result := startRun(t, []string{"--sequence", "success", "--port", "0"}, signals)

	ready := nextRecord(t, result)
	if ready["type"] != "ready" {
		t.Fatalf("第一行该是 ready，得到 %v", ready)
	}
	baseURL, _ := ready["baseURL"].(string)

	response, err := http.Post(baseURL+"/chat/completions", "application/json",
		strings.NewReader(`{"model":"mock"}`))
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()

	// 一次请求播报两条：接下来要演什么，以及演完之后是什么结局。
	request := nextRecord(t, result)
	if request["type"] != "request" || request["behavior"] != "success" || request["attempt"] != float64(1) {
		t.Errorf("request 那一行不对：%v", request)
	}
	outcome := nextRecord(t, result)
	if outcome["type"] != "result" || outcome["outcome"] != "completed" {
		t.Errorf("result 那一行不对：%v", outcome)
	}
	// chunksSent 是这两行里唯一能反映「本端到底交出去多少」的数，脚本靠它判断
	// 一次半截的流断在了第几片上。
	if outcome["chunksSent"] != float64(5) {
		t.Errorf("该播报发了 5 条事件，得到 %v", outcome["chunksSent"])
	}

	signals <- syscall.SIGINT
	result.closer()
	if result.code != 130 {
		t.Errorf("SIGINT 该换算成 130，得到 %d", result.code)
	}
}

// TestRunAnnouncesUnavailableBeforeBinding 验 connection_refused 打头的那两行。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:21-31
//
// 两行分别标出不可用期的两端，脚本靠它们知道「现在连过去应该被拒」和「现在
// 可以连了」。unavailable 里播报的地址必须和后来 ready 的完全一致——脚本要在
// 同一个地址上先验拒绝再验成功，两个地址对不上的话这条用例根本立不住。
func TestRunAnnouncesUnavailableBeforeBinding(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	signals := make(chan os.Signal, 1)
	result := startRun(t, []string{
		"--sequence", "connection_refused,success",
		"--port", strconv.Itoa(port),
		"--listen-delay-ms", "50",
	}, signals)

	unavailable := nextRecord(t, result)
	if unavailable["type"] != "unavailable" {
		t.Fatalf("第一行该是 unavailable，得到 %v", unavailable)
	}
	if unavailable["listenDelayMs"] != float64(50) {
		t.Errorf("不可用期该是 50 毫秒，得到 %v", unavailable["listenDelayMs"])
	}

	ready := nextRecord(t, result)
	if ready["type"] != "ready" {
		t.Fatalf("第二行该是 ready，得到 %v", ready)
	}
	if unavailable["baseURL"] != ready["baseURL"] {
		t.Errorf("两行播报的地址对不上：%v 和 %v", unavailable["baseURL"], ready["baseURL"])
	}

	signals <- syscall.SIGTERM
	result.closer()
	if result.code != 143 {
		t.Errorf("SIGTERM 该换算成 143，得到 %d", result.code)
	}
}

// TestRunAnswersSignalsDuringTheUnavailableWindow 验不可用期里也能被叫停。
//
// 新增: DSH 在这段等待里不管信号——Node 还没挂处理器，默认行为直接把进程杀了。
// Go 这边 [signal.Notify] 一调用，默认行为就没了，不自己接住的话一个还在不可用期
// 里的进程会对 Ctrl-C 毫无反应，而这段等待可以长达几十分钟。
func TestRunAnswersSignalsDuringTheUnavailableWindow(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	signals := make(chan os.Signal, 1)
	// 不可用期定得足够长，好让这条用例是在等待中间打断它，而不是等它自己走完。
	result := startRun(t, []string{
		"--sequence", "connection_refused,success",
		"--port", strconv.Itoa(port),
		"--listen-delay-ms", "600000",
	}, signals)

	if record := nextRecord(t, result); record["type"] != "unavailable" {
		t.Fatalf("第一行该是 unavailable，得到 %v", record)
	}

	started := time.Now()
	signals <- syscall.SIGINT
	result.closer()

	if result.code != 130 {
		t.Errorf("不可用期里的 SIGINT 也该换算成 130，得到 %d", result.code)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Errorf("等了 %s 才停，说明信号没被接住", elapsed)
	}
	// 被叫停的进程不该再播报 ready——那一行的意思是「可以连了」，而它根本没绑上。
	// closer 回来时 run 已经返回、管子已经关上，所以这里要么立刻收到通道关闭，
	// 要么收到一行本不该存在的播报，不会悬着。
	if line, ok := <-result.lines; ok {
		t.Errorf("停下之后不该再有播报，多出来一行 %q", line)
	}
}

// TestExitCodeFollowsTheShellConvention 验信号到退出码的换算。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:43-44
//
// 128 加信号编号是 shell 的老规矩。DSH 写死了 130 和 143 两个数，这里是算出来的，
// 所以多接一种信号时不用再记一个数字——这条断言钉的是那个算法而不是那两个数。
func TestExitCodeFollowsTheShellConvention(t *testing.T) {
	t.Parallel()
	if got := exitCode(syscall.SIGINT); got != 130 {
		t.Errorf("SIGINT 该是 130，得到 %d", got)
	}
	if got := exitCode(syscall.SIGTERM); got != 143 {
		t.Errorf("SIGTERM 该是 143，得到 %d", got)
	}
	// 不是系统信号的话算不出编号，退回一个笼统的失败码。
	if got := exitCode(fakeSignal{}); got != 1 {
		t.Errorf("算不出编号的信号该是 1，得到 %d", got)
	}
}

// fakeSignal 是一个不是 [syscall.Signal] 的 [os.Signal]，用来走那条兜底。
type fakeSignal struct{}

func (fakeSignal) String() string { return "假信号" }
func (fakeSignal) Signal()        {}
