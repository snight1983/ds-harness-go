// Command llmmockserver 把 mockserver 包起成一个独立进程，播报地址、转发遥测、接住信号。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:1-50
//
// 这个进程存在的理由是**跨进程**：被测的那一侧常常自己就是一个进程（一个 CLI、
// 一个服务），没法把模拟服务器当库嵌进去。它把地址和随机种子按 JSONL 播报到标准
// 输出，编排脚本读第一行就知道往哪儿连、以及这一跑的种子是多少。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/snight1983/ds-harness-go/llm/mockserver"
)

func main() {
	// 新增: DSH 分别挂 SIGINT 和 SIGTERM 两个处理器，为的是退出码不同（130／143）。
	// Go 的 [signal.NotifyContext] 只告诉你「被信号打断了」，说不出是哪一个，所以
	// 这里用原始的通知通道，把信号本身留到 [exitCode] 那里换算。
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, signals))
}

// unavailableRecord 是绑端口之前播报的那一行：这段时间里连过来会被拒绝。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:21-25
type unavailableRecord struct {
	Type          string `json:"type"`
	BaseURL       string `json:"baseURL"`
	ListenDelayMS int64  `json:"listenDelayMs"`
}

// readyRecord 是端口绑好之后播报的那一行。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:32-36
//
// RandomSeed 一定要播报出去：一次随机长跑挂掉之后，重放它靠的就是这个数。
type readyRecord struct {
	Type       string `json:"type"`
	BaseURL    string `json:"baseURL"`
	RandomSeed uint32 `json:"randomSeed"`
}

// run 跑完一整趟进程，交出退出码。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:12-49
//
// signals、stdout、stderr 都是参数而不是直接取全局的，为的是这一整段能在测试里
// 原样跑一遍——DSH 对应的这一段是标着 v8 ignore 的、验不到的胶水。
func run(argv []string, stdout, stderr io.Writer, signals <-chan os.Signal) int {
	config, err := mockserver.ParseCLIArgs(argv)
	if errors.Is(err, mockserver.ErrCLIHelp) {
		_, _ = io.WriteString(stdout, mockserver.CLIUsage)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n\n%s", err, mockserver.CLIUsage)
		return 1
	}

	// 新增: [json.Encoder] 不能并发用，而遥测是从各个处理器协程上来的。DSH 跑在
	// 单线程事件循环上，那边天然不会有半行插进另外半行里；这里得自己锁。
	var writing sync.Mutex
	encoder := json.NewEncoder(stdout)
	emit := func(value any) {
		writing.Lock()
		defer writing.Unlock()
		// 标准输出写不动只可能是下游管道关了，那时进程本来就该停，没有别的补救。
		_ = encoder.Encode(value)
	}

	if config.StartsUnavailable {
		address := net.JoinHostPort(config.Server.Host, strconv.Itoa(config.Server.Port))
		emit(unavailableRecord{
			Type:          "unavailable",
			BaseURL:       "http://" + address + "/v1",
			ListenDelayMS: config.ListenDelay.Milliseconds(),
		})
		// 新增: DSH 在这段等待里不管信号——Node 还没挂处理器，默认行为直接把进程
		// 杀了。Go 这边 [signal.Notify] 一调用，默认行为就没了，不自己接住的话
		// 一个还在「不可用期」里的进程会对 Ctrl-C 毫无反应。
		timer := time.NewTimer(config.ListenDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case received := <-signals:
			return exitCode(received)
		}
	}

	options := config.Server
	options.OnEvent = func(event mockserver.Event) { emit(event) }
	server, err := mockserver.Start(options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n\n%s", err, mockserver.CLIUsage)
		return 1
	}

	emit(readyRecord{
		Type:       "ready",
		BaseURL:    server.BaseURL() + "/v1",
		RandomSeed: server.RandomSeed(),
	})

	received := <-signals
	// 关不干净不改退出码：退出码要回答的是「谁让我停的」，那件事已经确定了。
	_ = server.Close()
	return exitCode(received)
}

// exitCode 按 shell 的老规矩把信号换算成退出码：128 加信号编号。
//
// 源: packages/test-support/llm-mock-server/src/bin.ts:43-44
//
// SIGINT 是 2 得 130，SIGTERM 是 15 得 143，和 DSH 写死的两个数一样，但这里是算
// 出来的——多接一种信号时不用再记一个数字。
func exitCode(received os.Signal) int {
	if number, isSystem := received.(syscall.Signal); isSystem {
		return 128 + int(number)
	}
	return 1
}
