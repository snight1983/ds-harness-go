// 本文件的作用：把一行命令行参数解析成一份配置，不起进程也不起监听器。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:1-213

package mockserver

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// BehaviorConnectionRefused 是只有独立进程演得出来的那一种：**监听器还没起来**。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:15-16（CONNECTION_REFUSED_BEHAVIOR）
//
// 它不在 [behaviorOrder] 里，因为一个已经接下请求的处理器演不出「端口上没人」。
// 它只能写在 --sequence 的第一位，效果是进程先等一段时间再去绑端口，这段时间里
// 客户端拿到的是 TCP 层的拒绝而不是任何 HTTP 响应。
const BehaviorConnectionRefused Behavior = "connection_refused"

// CLI 各项没配时用的值。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:34,153
const (
	// DefaultCLIPort 是命令行不给 --port 时绑的端口。
	//
	// 和 [Options] 的零值端口不同：库里的 0 意思是「让操作系统挑」，而命令行是给
	// 人和脚本用的，得有个能提前写进配置文件的定值。
	DefaultCLIPort = 8000
	// DefaultListenDelay 是 connection_refused 打头时、绑端口之前的等待时长。
	DefaultListenDelay = 750 * time.Millisecond
)

// maxTimerDelayMillis 是 [MaxTimerDelay] 换算成毫秒的整数，命令行按毫秒收数。
const maxTimerDelayMillis = int64(MaxTimerDelay / time.Millisecond)

// ErrCLIHelp 表示这行参数要的是用法说明，不是一次运行。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:30
//
// 新增: DSH 返回的是 { kind: 'help' } | { kind: 'run', config } 这种可辨识联合。
// Go 换成 (值, error) 加一个哨兵——这不是随手选的，[flag.ErrHelp] 就是标准库对
// 同一件事的答案，照着它写，调用方的 errors.Is 判断和处理别的命令行时是同一套。
var ErrCLIHelp = errors.New("mockserver: 要的是用法说明")

// CLIUsage 是 --help 和参数出错时打印的用法说明。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:36-65（MOCK_LLM_CLI_USAGE）
//
// 正文保持英文：这是被复刻的能力的一部分，选项名本来就是英文，说明跟着选项名走
// 才对得上；而且这份文本会被脚本 grep。
const CLIUsage = `Usage: mockserver [options]

Required:
  --sequence <a,b,...>       Ordered behaviors; connection_refused is allowed first

Listener:
  --host <host>              Default 127.0.0.1
  --port <port>              Default 8000; required and nonzero for connection_refused
  --api-key <token>          Validate exact Bearer token when present
  --listen-delay-ms <ms>     Unavailable interval (default 750 with connection_refused)
  --repeat-last              Repeat the final request behavior after exhaustion
  --seed <uint32>            Reproduce random selections
  --random-weights <a=n,...> Relative weights for concrete behaviors

Response:
  --success-text <text>
  --partial-text <text>
  --reasoning-text <text>
  --chunk-size <count>
  --chunk-delay-ms <ms>
  --disconnect-delay-ms <ms>
  --retry-after-ms <ms>
  --request-id <id>
  --tool-name <name>
  --tool-arguments <json>

Other:
  --help
`

// CLIConfig 是一行命令行解析出来的东西：服务器配置，加上进程级的那两件事。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:18-26（MockLlmCliConfig）
type CLIConfig struct {
	// Server 是去掉 connection_refused 之后的服务器配置。
	Server Options
	// ListenDelay 是绑端口之前的等待时长；没点 connection_refused 时是 0。
	ListenDelay time.Duration
	// StartsUnavailable 记着剧本是不是真的要了一段「端口上没人」的时间。
	//
	// 它和 ListenDelay 不是一回事：--listen-delay-ms 0 配上 connection_refused 是
	// 一段长度为零的不可用期，进程仍然要为它多发一条遥测记录。
	StartsUnavailable bool
}

// cliStringOptions 是全部收字符串的选项名，与 [CLIUsage] 一一对应。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:119-138
//
// 数字类的选项也收字符串：数值转换、边界和跨选项的约束全都自己做。这一点和 DSH
// 一样——那边的注释写得明白，parseArgs 只负责切词。让 [flag] 直接解析成 int 会把
// 错误信息的措辞交给标准库，而这些措辞是本包自己要负责的。
var cliStringOptions = []string{
	"sequence",
	"host",
	"port",
	"api-key",
	"listen-delay-ms",
	"seed",
	"random-weights",
	"success-text",
	"partial-text",
	"reasoning-text",
	"chunk-size",
	"chunk-delay-ms",
	"disconnect-delay-ms",
	"retry-after-ms",
	"request-id",
	"tool-name",
	"tool-arguments",
}

// ParseCLIArgs 解析可执行文件名之后的那些参数。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:147-213
//
// 要的是用法说明时返回 [ErrCLIHelp]；其余 error 都是这行参数不成立。
//
// 新增: Node 的 parseArgs 在 strict 模式下自己会拒绝多余的位置参数，[flag] 不会——
// 它遇到第一个非选项就停下，把剩下的全塞进 Args。所以解析完必须自己查一遍 NArg，
// 不然 `--sequence success stray --host x` 会安静地跑起来，而且 --host 根本没生效。
func ParseCLIArgs(argv []string) (CLIConfig, error) {
	// --help 在切词之前就认，和 DSH 一样：参数写错了的时候，人要的正是用法说明，
	// 先报一个「不认识的选项」再让他去猜怎么问是帮倒忙。
	for _, argument := range argv {
		if argument == "--help" {
			return CLIConfig{}, ErrCLIHelp
		}
	}

	set := flag.NewFlagSet("mockserver", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.Usage = func() {}
	for _, name := range cliStringOptions {
		set.String(name, "", "")
	}
	repeatLast := set.Bool("repeat-last", false, "")

	if err := set.Parse(argv); err != nil {
		// [flag] 把 -h／-help 当成求助，即便没有登记过这两个名字。
		if errors.Is(err, flag.ErrHelp) {
			return CLIConfig{}, ErrCLIHelp
		}
		return CLIConfig{}, fmt.Errorf("mockserver: 参数解析失败：%w", err)
	}
	if set.NArg() > 0 {
		return CLIConfig{}, fmt.Errorf("mockserver: 多出来一个参数 %q", set.Arg(0))
	}

	// 新增: [flag.FlagSet.Visit] 只走真正出现过的选项，这是 Go 这边「这个选项配了
	// 没有」的等价物——parseArgs 的 values 里没配的键就是不存在。
	//
	// 少了它，「没配」就只能拿零值来认，而下面那几条约束恰恰要在零值上分叉：
	// `--port 0` 配上 connection_refused 必须被拒（零值会让它看起来像没配，于是
	// 落到默认的 8000 上安静地跑起来）；`--listen-delay-ms 0` 没有 connection_refused
	// 同样必须被拒；`--seed 0` 和两项延时的 0 都得按字面算（见 [Options]）。
	// 这几处的共同点是**显式的零和没配要的行为不一样**。
	given := map[string]string{}
	set.Visit(func(option *flag.Flag) { given[option.Name] = option.Value.String() })

	return buildCLIConfig(given, *repeatLast)
}

// buildCLIConfig 把切好词的选项化成一份配置，并查那三条跨选项的约束。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:152-212
func buildCLIConfig(given map[string]string, repeatLast bool) (CLIConfig, error) {
	rawSequence, hasSequence := given["sequence"]
	if !hasSequence {
		return CLIConfig{}, fmt.Errorf("mockserver: --sequence 是必填的")
	}
	sequence, startsUnavailable, err := parseCLISequence(rawSequence)
	if err != nil {
		return CLIConfig{}, err
	}

	options := Options{
		Sequence:      sequence,
		Port:          DefaultCLIPort,
		RepeatLast:    repeatLast,
		Host:          given["host"],
		APIKey:        given["api-key"],
		SuccessText:   given["success-text"],
		PartialText:   given["partial-text"],
		ReasoningText: given["reasoning-text"],
		RequestID:     given["request-id"],
		ToolName:      given["tool-name"],
		ToolArguments: given["tool-arguments"],
	}

	if raw, ok := given["port"]; ok {
		port, err := parseIntegerArg("--port", raw, 0, 65535)
		if err != nil {
			return CLIConfig{}, err
		}
		options.Port = int(port)
	}
	if raw, ok := given["chunk-size"]; ok {
		size, err := parseIntegerArg("--chunk-size", raw, 1, math.MaxInt32)
		if err != nil {
			return CLIConfig{}, err
		}
		options.ChunkSize = int(size)
	}
	if raw, ok := given["seed"]; ok {
		seed, err := parseIntegerArg("--seed", raw, 0, math.MaxUint32)
		if err != nil {
			return CLIConfig{}, err
		}
		options.RandomSeed = uint32(seed)
		options.HasRandomSeed = true
	}
	if raw, ok := given["random-weights"]; ok {
		weights, err := parseCLIRandomWeights(raw)
		if err != nil {
			return CLIConfig{}, err
		}
		options.RandomWeights = weights
	}
	// 三项延时的下限不一样：前两项的 0 是一种正当的演法（不停顿、立刻断开），
	// 而 0 秒的重试建议等于没有建议，所以它从 1 起。configured 收下「这项配过」，
	// 好让显式的 0 不被 [Options] 的「零值即默认」吃掉。
	for _, entry := range []struct {
		option     string
		low        int64
		target     *time.Duration
		configured *bool
	}{
		{"chunk-delay-ms", 0, &options.ChunkDelay, &options.HasChunkDelay},
		{"disconnect-delay-ms", 0, &options.DisconnectDelay, &options.HasDisconnectDelay},
		{"retry-after-ms", 1, &options.RetryAfter, nil},
	} {
		raw, ok := given[entry.option]
		if !ok {
			continue
		}
		value, err := parseDurationArg("--"+entry.option, raw, entry.low)
		if err != nil {
			return CLIConfig{}, err
		}
		*entry.target = value
		if entry.configured != nil {
			*entry.configured = true
		}
	}

	listenDelay := DefaultListenDelay
	rawListenDelay, hasListenDelay := given["listen-delay-ms"]
	if hasListenDelay {
		listenDelay, err = parseDurationArg("--listen-delay-ms", rawListenDelay, 0)
		if err != nil {
			return CLIConfig{}, err
		}
	}

	// 三条跨选项的约束。它们查的都是「这个选项在这份剧本下讲不讲得通」，而不是
	// 值本身合不合法，所以只能等剧本解析完了再查。
	if startsUnavailable && options.Port == 0 {
		return CLIConfig{}, fmt.Errorf("mockserver: connection_refused 要求显式给一个非零的 --port")
	}
	if !startsUnavailable && hasListenDelay {
		return CLIConfig{}, fmt.Errorf("mockserver: --listen-delay-ms 要求 --sequence 以 connection_refused 打头")
	}
	if !hasRandom(sequence) && (options.HasRandomSeed || options.RandomWeights != nil) {
		return CLIConfig{}, fmt.Errorf("mockserver: --seed 和 --random-weights 要求 --sequence 里有 random")
	}

	if !startsUnavailable {
		listenDelay = 0
	}
	// 新增: 把主机名在这里定死，而不是像 DSH 那样让进程那一侧再写一遍
	// `serverOptions.host ?? '127.0.0.1'`。connection_refused 打头时，进程要在
	// 服务器起来**之前**就播报一个能连的地址，那时没人能问服务器要主机名——两处
	// 各写一份默认值迟早会对不上，而对不上的表现是播报的地址连不通。
	if options.Host == "" {
		options.Host = defaultHost
	}
	return CLIConfig{Server: options, ListenDelay: listenDelay, StartsUnavailable: startsUnavailable}, nil
}

// hasRandom 判一份剧本里有没有 random。
func hasRandom(sequence []Behavior) bool {
	for _, behavior := range sequence {
		if behavior == BehaviorRandom {
			return true
		}
	}
	return false
}

// parseCLISequence 把 --sequence 的值切成一份剧本，并认出打头的 connection_refused。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:81-98
//
// 交出去的剧本里**不含** connection_refused：它是进程的事，服务器演不了它。
func parseCLISequence(raw string) (sequence []Behavior, startsUnavailable bool, err error) {
	entries := strings.Split(raw, ",")
	for index, entry := range entries {
		entries[index] = strings.TrimSpace(entry)
		if entries[index] == "" {
			return nil, false, fmt.Errorf("mockserver: --sequence 要的是一串非空的、逗号分隔的行为名")
		}
	}
	for _, entry := range entries[1:] {
		if Behavior(entry) == BehaviorConnectionRefused {
			return nil, false, fmt.Errorf("mockserver: connection_refused 只能写在 --sequence 的第一位")
		}
	}

	startsUnavailable = Behavior(entries[0]) == BehaviorConnectionRefused
	if startsUnavailable {
		entries = entries[1:]
	}
	if len(entries) == 0 {
		return nil, false, fmt.Errorf("mockserver: connection_refused 后面还得跟一条请求行为")
	}

	sequence = make([]Behavior, 0, len(entries))
	for _, entry := range entries {
		behavior := Behavior(entry)
		if !IsBehavior(behavior) {
			return nil, false, fmt.Errorf("mockserver: 不认识的行为 %q", entry)
		}
		sequence = append(sequence, behavior)
	}
	return sequence, startsUnavailable, nil
}

// parseCLIRandomWeights 把 --random-weights 的值切成一张权重表。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:100-116
func parseCLIRandomWeights(raw string) (map[Behavior]float64, error) {
	weights := map[Behavior]float64{}
	for _, entry := range strings.Split(raw, ",") {
		name, rawWeight, hasSeparator := strings.Cut(entry, "=")
		if !hasSeparator || name == "" || rawWeight == "" || strings.Contains(rawWeight, "=") {
			return nil, fmt.Errorf("mockserver: --random-weights 要的是逗号分隔的 behavior=weight")
		}
		behavior := Behavior(name)
		if !IsConcreteBehavior(behavior) {
			return nil, fmt.Errorf("mockserver: 随机权重只能挂在具体行为上，收到 %q", name)
		}
		if _, duplicate := weights[behavior]; duplicate {
			return nil, fmt.Errorf("mockserver: %q 的随机权重给了两遍", name)
		}
		weight, err := parseFloatArg("--random-weights", rawWeight)
		if err != nil {
			return nil, err
		}
		weights[behavior] = weight
	}
	return weights, nil
}

// parseFloatArg 把一个选项的值解析成一个有限的数。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:67-71
//
// 无穷和 NaN 要单独挡：[strconv.ParseFloat] 认识 "Inf" 和 "NaN" 这两个写法，而一个
// 无穷大的权重会让按权重挑行为这件事失去意义。
func parseFloatArg(option, value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
		return 0, fmt.Errorf("mockserver: %s 必须是一个有限的数，收到 %q", option, value)
	}
	return parsed, nil
}

// parseIntegerArg 把一个选项的值解析成范围内的整数。
//
// 源: packages/test-support/llm-mock-server/src/cli.ts:73-79
//
// 新增: DSH 分两步——先 Number() 转成浮点，再查它是不是整数。JS 没有整数类型，
// 那一步是必须的。Go 直接 [strconv.ParseInt]，"1.5" 在这里根本转不出来。
func parseIntegerArg(option, value string, low, high int64) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < low || parsed > high {
		return 0, fmt.Errorf("mockserver: %s 必须是 %d 到 %d 之间的整数，收到 %q", option, low, high, value)
	}
	return parsed, nil
}

// parseDurationArg 把一个以毫秒计的选项解析成 [time.Duration]。
//
// 命令行收的是毫秒而不是 Go 的 "25ms" 写法：这些选项名本身带着 -ms 后缀，是被
// 复刻的那套命令行的一部分，脚本按名字和单位一起对。
func parseDurationArg(option, value string, low int64) (time.Duration, error) {
	milliseconds, err := parseIntegerArg(option, value, low, maxTimerDelayMillis)
	if err != nil {
		return 0, err
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}
