// 本文件的作用：验命令行解析——收下什么、拒绝什么、以及拒绝的理由说没说清。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:1-128

package mockserver_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"ds-harness-go/llm/mockserver"
)

// TestCLIHelpNeedsNoSequence 验 --help 不受必填项牵连。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:8-11
func TestCLIHelpNeedsNoSequence(t *testing.T) {
	t.Parallel()
	if _, err := mockserver.ParseCLIArgs([]string{"--help"}); !errors.Is(err, mockserver.ErrCLIHelp) {
		t.Fatalf("--help 该要用法说明，得到 %v", err)
	}
	if !strings.Contains(mockserver.CLIUsage, "--sequence") {
		t.Error("用法说明里没提 --sequence，那它就没说清必填的是什么")
	}
}

// TestCLIHelpWinsOverBadArguments 验参数写错时求助仍然有效。
//
// 新增: 这条在 DSH 那边是隐含的——它先扫一遍 argv 找 --help 再切词。Go 这边
// [flag] 遇到不认识的选项会当场停下，不特意先扫一遍的话，一个写错了参数的人
// 得到的是「不认识的选项」而不是他真正需要的那份说明。
func TestCLIHelpWinsOverBadArguments(t *testing.T) {
	t.Parallel()
	for _, argv := range [][]string{
		{"--wat", "--help"},
		{"--help", "--wat"},
		{"-h"},
	} {
		if _, err := mockserver.ParseCLIArgs(argv); !errors.Is(err, mockserver.ErrCLIHelp) {
			t.Errorf("%v 该要用法说明，得到 %v", argv, err)
		}
	}
}

// TestCLIParsesEveryOption 把每个选项都配上，验它们各自落到了该落的字段上。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:13-55
func TestCLIParsesEveryOption(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{
		"--sequence", "connection_refused,partial_disconnect,success",
		"--host", "localhost",
		"--port", "9010",
		"--api-key", "mock-key",
		"--listen-delay-ms", "100",
		"--repeat-last",
		"--success-text", "done",
		"--partial-text", "half",
		"--reasoning-text", "think",
		"--chunk-size", "2",
		"--chunk-delay-ms", "3",
		"--disconnect-delay-ms", "4",
		"--retry-after-ms", "5000",
		"--request-id", "request-1",
		"--tool-name", "lookup",
		"--tool-arguments", `{"id":1}`,
	})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	want := mockserver.CLIConfig{
		StartsUnavailable: true,
		ListenDelay:       100 * time.Millisecond,
		Server: mockserver.Options{
			// connection_refused 被摘掉了：它是进程的事，服务器演不了它。
			Sequence:           []mockserver.Behavior{mockserver.BehaviorPartialDisconnect, mockserver.BehaviorSuccess},
			Host:               "localhost",
			Port:               9010,
			APIKey:             "mock-key",
			RepeatLast:         true,
			SuccessText:        "done",
			PartialText:        "half",
			ReasoningText:      "think",
			ChunkSize:          2,
			ChunkDelay:         3 * time.Millisecond,
			HasChunkDelay:      true,
			DisconnectDelay:    4 * time.Millisecond,
			HasDisconnectDelay: true,
			RetryAfter:         5 * time.Second,
			RequestID:          "request-1",
			ToolName:           "lookup",
			ToolArguments:      `{"id":1}`,
		},
	}
	if !reflect.DeepEqual(config, want) {
		t.Errorf("解析结果不对\n得到 %+v\n想要 %+v", config, want)
	}
}

// TestCLIDefaults 验一行最短的参数落出来的那份配置。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:57-78
func TestCLIDefaults(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{"--sequence", "success"})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if config.StartsUnavailable || config.ListenDelay != 0 {
		t.Errorf("没点 connection_refused 就不该有不可用期，得到 %v／%s", config.StartsUnavailable, config.ListenDelay)
	}
	if config.Server.Port != mockserver.DefaultCLIPort {
		t.Errorf("默认端口该是 %d，得到 %d", mockserver.DefaultCLIPort, config.Server.Port)
	}
	// 新增: 主机名在解析期就定死。connection_refused 打头时，进程要在服务器起来
	// 之前播报一个能连的地址，那时没人能问服务器要主机名——这条断言钉的就是
	// 「播报的地址和后来真正绑上的地址出自同一个值」。
	if config.Server.Host != "127.0.0.1" {
		t.Errorf("默认主机名该在解析期定死成回环地址，得到 %q", config.Server.Host)
	}
	if config.Server.RepeatLast || config.Server.HasRandomSeed || config.Server.RandomWeights != nil {
		t.Error("没配的开关不该自己打开")
	}
}

// TestCLIDefaultUnavailableInterval 验 connection_refused 打头时不可用期有默认值。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:72-78
func TestCLIDefaultUnavailableInterval(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{
		"--sequence", "connection_refused,success", "--port", "8001",
	})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !config.StartsUnavailable || config.ListenDelay != mockserver.DefaultListenDelay {
		t.Errorf("该有一段默认长度的不可用期，得到 %v／%s", config.StartsUnavailable, config.ListenDelay)
	}
}

// TestCLIZeroUnavailableIntervalIsNotNoUnavailableInterval 验显式的 0 不被当成没配。
//
// 新增: DSH 没有这条。Go 这边 ListenDelay 是个普通的 [time.Duration]，0 既可能是
// 「没配」也可能是「配了 0」，而这两件事对进程的行为不同：后者仍然要播报那条
// unavailable 记录。这正是 [mockserver.CLIConfig.StartsUnavailable] 单独存在
// 的理由，值得一条断言钉住。
func TestCLIZeroUnavailableIntervalIsNotNoUnavailableInterval(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{
		"--sequence", "connection_refused,success", "--port", "8001", "--listen-delay-ms", "0",
	})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !config.StartsUnavailable {
		t.Error("长度为零的不可用期仍然是一段不可用期")
	}
	if config.ListenDelay != 0 {
		t.Errorf("不可用期该是 0，得到 %s", config.ListenDelay)
	}
}

// TestCLIZeroDelaysAreTakenLiterally 验两项延时的显式 0 不被默认值吃掉。
//
// 新增: 这两个 0 是人在命令行上真会写的东西，意思是「不停顿」「立刻断开」，
// 和默认的 25ms／10ms 是两种不同的演法。认不出来就等于安静地演成别的样子，
// 而那正是本服务器要帮别人抓的毛病，自己身上不能有。
func TestCLIZeroDelaysAreTakenLiterally(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{
		"--sequence", "success", "--chunk-delay-ms", "0", "--disconnect-delay-ms", "0",
	})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !config.Server.HasChunkDelay || config.Server.ChunkDelay != 0 {
		t.Errorf("--chunk-delay-ms 0 该被认下来，得到 %v／%s", config.Server.HasChunkDelay, config.Server.ChunkDelay)
	}
	if !config.Server.HasDisconnectDelay || config.Server.DisconnectDelay != 0 {
		t.Errorf("--disconnect-delay-ms 0 该被认下来，得到 %v／%s", config.Server.HasDisconnectDelay, config.Server.DisconnectDelay)
	}
}

// TestCLIParsesWeightedRandomProfile 验可重放的加权随机配置。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:80-100
func TestCLIParsesWeightedRandomProfile(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{
		"--sequence", "random",
		"--repeat-last",
		"--seed", "42",
		"--random-weights", "success=8,partial_disconnect=2",
	})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !config.Server.HasRandomSeed || config.Server.RandomSeed != 42 {
		t.Errorf("种子该是 42，得到 %v／%d", config.Server.HasRandomSeed, config.Server.RandomSeed)
	}
	want := map[mockserver.Behavior]float64{
		mockserver.BehaviorSuccess:           8,
		mockserver.BehaviorPartialDisconnect: 2,
	}
	if !reflect.DeepEqual(config.Server.RandomWeights, want) {
		t.Errorf("权重表不对\n得到 %v\n想要 %v", config.Server.RandomWeights, want)
	}
}

// TestCLIZeroSeedIsTakenLiterally 验 0 是一个正当的种子。
//
// 新增: 「重放上次那一跑」是随机模式存在的全部理由。把 0 当成没配、转而自己
// 生成一个种子，会让恰好抽到 0 的那些跑永远重放不出来。
func TestCLIZeroSeedIsTakenLiterally(t *testing.T) {
	t.Parallel()
	config, err := mockserver.ParseCLIArgs([]string{"--sequence", "random", "--seed", "0"})
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !config.Server.HasRandomSeed || config.Server.RandomSeed != 0 {
		t.Errorf("--seed 0 该被认下来，得到 %v／%d", config.Server.HasRandomSeed, config.Server.RandomSeed)
	}
}

// TestCLIRejectsInvalidArgv 是那张拒绝表。
//
// 源: packages/test-support/llm-mock-server/tests/cli.spec.ts:102-127
//
// 每一行都连着一个「理由片段」：只验「报错了」不够，报错的理由得能让人知道
// 该改哪个字。片段按 Go 这边自己的措辞写——错误信息是本包负责的东西，不是
// 从 DSH 搬过来的字符串。
func TestCLIRejectsInvalidArgv(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		argv   []string
		reason string
	}{
		{"没给剧本", nil, "--sequence 是必填的"},
		// 切词层的失败带的是 [flag] 自己的措辞，本包只在前面加上包名。
		{"不认识的选项", []string{"--wat"}, "参数解析失败"},
		{"不认识的选项带值", []string{"--wat", "x"}, "参数解析失败"},
		{"选项缺值", []string{"--port"}, "参数解析失败"},
		{"多出来的位置参数", []string{"--sequence", "success", "stray"}, "多出来一个参数"},
		{"端口不是数", []string{"--port", "NaN", "--sequence", "success"}, "--port 必须是"},
		{"端口越界", []string{"--port", "65536", "--sequence", "success"}, "--port 必须是"},
		{"剧本有空条目", []string{"--sequence", "success,"}, "非空的"},
		{"拒连写在了后面", []string{"--sequence", "success,connection_refused"}, "只能写在 --sequence 的第一位"},
		{"拒连后面没东西", []string{"--sequence", "connection_refused"}, "后面还得跟一条请求行为"},
		{"不认识的行为", []string{"--sequence", "unknown"}, "不认识的行为"},
		{"拒连要显式端口", []string{"--sequence", "connection_refused,success", "--port", "0"}, "非零的 --port"},
		{"不可用期没有拒连", []string{"--sequence", "success", "--listen-delay-ms", "5"}, "要求 --sequence 以 connection_refused 打头"},
		{"不可用期是负数", []string{"--sequence", "connection_refused,success", "--listen-delay-ms=-1"}, "--listen-delay-ms 必须是"},
		{"不可用期不是整数", []string{"--sequence", "connection_refused,success", "--listen-delay-ms", "1.5"}, "--listen-delay-ms 必须是"},
		{"不可用期越界", []string{"--sequence", "connection_refused,success", "--listen-delay-ms", "2147483648"}, "--listen-delay-ms 必须是"},
		// 0 秒的重试建议等于没有建议，所以这一项的下限和另外两项不同。
		{"重试建议为零", []string{"--sequence", "success", "--retry-after-ms", "0"}, "--retry-after-ms 必须是"},
		{"分片大小为零", []string{"--sequence", "success", "--chunk-size", "0"}, "--chunk-size 必须是"},
		{"种子不是数", []string{"--sequence", "random", "--seed", "nope"}, "--seed 必须是"},
		{"种子越界", []string{"--sequence", "random", "--seed", "4294967296"}, "--seed 必须是"},
		{"种子没有随机", []string{"--sequence", "success", "--seed", "1"}, "要求 --sequence 里有 random"},
		{"权重没有随机", []string{"--sequence", "success", "--random-weights", "success=1"}, "要求 --sequence 里有 random"},
		{"权重没有等号", []string{"--sequence", "random", "--random-weights", "success"}, "behavior=weight"},
		{"权重挂在抽象行为上", []string{"--sequence", "random", "--random-weights", "random=1"}, "只能挂在具体行为上"},
		{"权重给了两遍", []string{"--sequence", "random", "--random-weights", "success=1,success=2"}, "给了两遍"},
		{"权重不是数", []string{"--sequence", "random", "--random-weights", "success=nope"}, "有限的数"},
		{"权重是无穷", []string{"--sequence", "random", "--random-weights", "success=Inf"}, "有限的数"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := mockserver.ParseCLIArgs(testCase.argv)
			if err == nil {
				t.Fatalf("%v 该被拒", testCase.argv)
			}
			if errors.Is(err, mockserver.ErrCLIHelp) {
				t.Fatalf("%v 被当成了求助", testCase.argv)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("理由里该有 %q，得到 %q", testCase.reason, err.Error())
			}
		})
	}
}

// TestCLIExplicitZeroIsNotAbsent 验那几条「显式的零」不被当成没配。
//
// 新增: 这是 [flag.FlagSet.Visit] 存在的理由，DSH 那边由 parseArgs 的稀疏
// values 天然提供。少了它，下面这两行都会安静地跑起来——前者落到默认的 8000
// 端口上（于是 connection_refused 演的根本不是「那个端口上没人」），后者则让
// 一个跟剧本对不上的选项蒙混过关。两种都是「配的和演的不是一回事」。
func TestCLIExplicitZeroIsNotAbsent(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name   string
		argv   []string
		reason string
	}{
		{
			"拒连配上零端口",
			[]string{"--sequence", "connection_refused,success", "--port", "0"},
			"非零的 --port",
		},
		{
			"零长度不可用期没有拒连",
			[]string{"--sequence", "success", "--listen-delay-ms", "0"},
			"要求 --sequence 以 connection_refused 打头",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := mockserver.ParseCLIArgs(testCase.argv)
			if err == nil {
				t.Fatalf("%v 该被拒，显式的零不是没配", testCase.argv)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Errorf("理由里该有 %q，得到 %q", testCase.reason, err.Error())
			}
		})
	}
}
