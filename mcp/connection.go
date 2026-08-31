// 本文件的作用：一台 MCP 服务器那条被监督的连接——一代一代地连、把工具同步进来、
// 断了按退避策略重连、彻底放弃时把工具撤干净。
//
// 源: packages/mcp/mcp-client/src/connection.ts:115-351
// 源: packages/mcp/mcp-client/src/transport.ts:31-49

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
)

// generationCloseTimeout 是等一代连接自己收敛的上限。
//
// 源: packages/mcp/mcp-client/src/connection.ts:50
const generationCloseTimeout = 5 * time.Second

// clientName 和 clientVersion 是本装置在 MCP 握手里报的身份。
//
// 源: packages/mcp/mcp-client/src/connection.ts:238-239
const (
	clientName    = "dsh-mcp-client"
	clientVersion = "0.0.1"
)

// supervisor 是一台服务器那条连接的监督者。
//
// 新增: DSH 那份实现里有一整套 `isCurrent(generation)` 守卫、一条把每次 syncTools
// 串起来的 `syncChain`、以及一个 `attemptSettled` 标志。那些东西全是在 JS 的单线程
// 事件循环里，把「几个异步回调各自乱序回来」重新排成一条线用的。Go 这边监督者**就是
// 一个 goroutine**：连、同步、服务、退避全在它一个人手里顺着往下走，`tools/list_changed`
// 通知从 SDK 的回调 goroutine 送进一个 channel，由这个 goroutine 在 select 里收。
// 于是「两次同步交错换代」这件事在结构上就发生不了，那三样守卫一件都不需要。
type supervisor struct {
	// label 是所有日志行的前缀。
	//
	// 源: packages/mcp/mcp-client/src/connection.ts:124
	label string
	// config 是这台服务器那份验过的配置。
	config Config
	// policy 是解算完的重连策略。
	policy ReconnectPolicy
	// registry 是要往里注册工具的那张表。
	registry *tools.Runtime
	// owner 是这些注册挂在哪个作用域下。
	owner *scope.Scope
	// logger 记连接生命周期上的每一件事。
	logger *slog.Logger
	// options 是平常那一代同步用的桥选项。
	options bridgeOptions
	// startupOptions 是**首次**同步用的桥选项；只有 FailOnStartupError 时才和
	// options 不同——那时注册冲突要一路抛到启动等待那条路上去。
	//
	// 源: packages/mcp/mcp-client/src/connection.ts:131-135
	startupOptions bridgeOptions

	// listChanged 收 SDK 那边报来的「工具清单变了」。
	//
	// 容量 1 且送不进去就丢：连着来的几条通知，效果和一条完全相同——反正下一次
	// 同步会把整份清单重新取一遍。
	listChanged chan struct{}
	// ready 交出首次尝试的结果，恰好送一次。
	ready chan error
	// done 在监督者退出、并且把工具撤干净之后关闭。
	done chan struct{}
	// cancel 让监督者停下来。
	cancel context.CancelFunc
}

// startSupervisor 起一个监督者，并且立刻交回它——首次尝试的结果从 ready 里拿。
//
// 源: packages/mcp/mcp-client/src/connection.ts:123-311
func startSupervisor(
	config Config,
	policy ReconnectPolicy,
	registry *tools.Runtime,
	owner *scope.Scope,
	admit ImageAdmission,
	logger *slog.Logger,
) *supervisor {
	label := fmt.Sprintf("mcp-client(%s)", config.ServerName)
	options := bridgeOptions{
		serverName:      config.ServerName,
		toolCallTimeout: config.ToolCallTimeout,
		admit:           admit,
		logger:          logger,
	}
	startupOptions := options
	startupOptions.strictRegistration = config.FailOnStartupError

	// 监督者的生命跟自己这个 ctx 走，不跟发起 Connect 的那次调用走：那次调用的 ctx
	// 在 Connect 返回之后可能立刻就完了，而这条连接是要一直活着的。
	ctx, cancel := context.WithCancel(context.WithoutCancel(context.Background()))
	s := &supervisor{
		label:          label,
		config:         config,
		policy:         policy,
		registry:       registry,
		owner:          owner,
		logger:         logger,
		options:        options,
		startupOptions: startupOptions,
		listChanged:    make(chan struct{}, 1),
		ready:          make(chan error, 1),
		done:           make(chan struct{}),
		cancel:         cancel,
	}
	go s.run(ctx)
	return s
}

// stop 让监督者停下来，等它把工具撤干净。
//
// 源: packages/mcp/mcp-client/src/connection.ts:327-349
func (s *supervisor) stop() {
	s.cancel()
	<-s.done
}

// run 是监督者的主循环：一代一代地连下去，直到被叫停或者彻底放弃。
//
// 源: packages/mcp/mcp-client/src/connection.ts:237-311
func (s *supervisor) run(ctx context.Context) {
	// 撤工具放在最后，而且用一个不带取消的 ctx：走到这里 ctx 已经完了，
	// 拿它去撤注册会被作用域那一层当成「这次清理被取消了」。
	var disposers toolDisposers
	defer func() {
		if err := disposers.disposeAll(context.WithoutCancel(ctx)); err != nil {
			s.logger.Error("mcp: 撤销这一代工具时出错", "serverName", s.config.ServerName, "error", err)
		}
		close(s.done)
	}()

	startup := true
	failedAttempts := 0
	for {
		session, err := s.connectGeneration(ctx, startup, &disposers)
		if startup {
			s.ready <- err
			startup = false
		}

		var connectedAt time.Time
		if err == nil {
			connectedAt = time.Now()
			if failedAttempts > 0 {
				s.logger.Info(fmt.Sprintf("%s: reconnected and re-synced tools (attempt %d/%d)",
					s.label, failedAttempts, s.policy.MaxAttempts))
			}
			s.serve(ctx, session, &disposers)
		}
		if ctx.Err() != nil {
			return
		}

		delay, keepGoing := s.planReconnect(ctx, connectedAt, &failedAttempts, &disposers)
		if !keepGoing {
			// 不再重连了，但这些工具的归属没变：它们要活到调用方真的把这条连接关掉为止。
			// 「放弃」那条路已经在 planReconnect 里把它们撤过了，这里只是别提前退出。
			<-ctx.Done()
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

// connectGeneration 跑一次连接尝试：新客户端、新传输、连上、然后同步一次工具。
//
// 源: packages/mcp/mcp-client/src/connection.ts:237-305
//
// 同步失败时这一代当场关掉——一条连着但没有工具的连接对模型毫无意义，
// 而且它还占着下一次尝试要用的那条传输。
func (s *supervisor) connectGeneration(
	ctx context.Context, startup bool, disposers *toolDisposers,
) (*sdk.ClientSession, error) {
	client := sdk.NewClient(
		&sdk.Implementation{Name: clientName, Version: clientVersion},
		&sdk.ClientOptions{
			// 在 Connect **之前**就装上：首次同步期间来的一条清单变更，
			// 应该排在它后面被处理掉，而不是被丢掉。
			ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
				select {
				case s.listChanged <- struct{}{}:
				default:
				}
			},
			Logger: s.logger,
		},
	)
	session, err := client.Connect(ctx, s.transport(), nil)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn(fmt.Sprintf("%s: connection attempt failed: %v", s.label, err))
		}
		return nil, err
	}

	options := s.options
	if startup {
		options = s.startupOptions
	}
	next, err := syncTools(ctx, session, s.registry, s.owner, options, *disposers)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn(fmt.Sprintf("%s: connection attempt failed: %v", s.label, err))
		}
		s.closeGeneration(session, false)
		return nil, err
	}
	*disposers = next
	return session, nil
}

// serve 守着一代已经连上的连接，直到它断了或者监督者被叫停。
//
// 源: packages/mcp/mcp-client/src/connection.ts:256-269（清单变更）、247-255（断线）
func (s *supervisor) serve(ctx context.Context, session *sdk.ClientSession, disposers *toolDisposers) {
	closed := make(chan struct{})
	go func() {
		// Wait 交回的错误就是这条连接是怎么断的；断线本身由下面那条 select 认，
		// 原因写在传输自己的日志里，这里不重复报一遍。
		_ = session.Wait()
		close(closed)
	}()

	for {
		select {
		case <-s.listChanged:
			s.logger.Info(fmt.Sprintf("%s: tool list changed, re-syncing", s.label))
			next, err := syncTools(ctx, session, s.registry, s.owner, s.options, *disposers)
			if err != nil {
				// 取的那一步失败：上一代还注册着，disposers 也还拿着它——
				// 继续拿最后一份好的清单服务下去。
				if ctx.Err() == nil {
					s.logger.Error(fmt.Sprintf("%s: tool re-sync failed: %v", s.label, err))
				}
				continue
			}
			*disposers = next
		case <-closed:
			return
		case <-ctx.Done():
			s.closeGeneration(session, true)
			return
		}
	}
}

// closeGeneration 关掉一代连接，并且等它真的收敛。
//
// 源: packages/mcp/mcp-client/src/connection.ts:181-190（waitForClose）、285-291、336-341
//
// 等收敛而不是关完就走，是因为一条关不掉的传输意味着对面那一端还在；再叠一条
// 新连接上去，两条会同时对着同一台服务器说话。DSH 写这条时针对的是 stdio 那种
// 会留下子进程的传输，本包只有 HTTP，但「关不掉的传输不该被叠加」这条理由照样成立。
func (s *supervisor) closeGeneration(session *sdk.ClientSession, duringDisposal bool) {
	closed := make(chan struct{})
	go func() {
		_ = session.Wait()
		close(closed)
	}()
	_ = session.Close()

	timer := time.NewTimer(generationCloseTimeout)
	defer timer.Stop()
	select {
	case <-closed:
	case <-timer.C:
		if duringDisposal {
			s.logger.Error(fmt.Sprintf(
				"%s: generation did not close within %s during disposal — server shutdown may be incomplete",
				s.label, generationCloseTimeout))
			return
		}
		s.logger.Error(fmt.Sprintf(
			"%s: failed generation did not close within %s — reconnect stopped to avoid overlapping server processes;"+
				" reload the plugin or restart the Host to retry", s.label, generationCloseTimeout))
	}
}

// planReconnect 决定要不要再连一次、以及等多久。
//
// 源: packages/mcp/mcp-client/src/connection.ts:192-225
//
// 第二个返回值为 false 表示这条连接到此为止：要么策略本来就不许重连，要么这次
// 断线事件的失败预算已经用光。
func (s *supervisor) planReconnect(
	ctx context.Context, connectedAt time.Time, failedAttempts *int, disposers *toolDisposers,
) (time.Duration, bool) {
	lostEstablished := !connectedAt.IsZero()
	if !s.policy.Enabled {
		message := "connection failed and reconnect is disabled — no tools were registered;" +
			" reload the plugin or restart the Host to connect"
		if lostEstablished {
			message = "connection lost and reconnect is disabled —" +
				" registered tools will fail until an HMR reload or Host restart"
		}
		s.logger.Error(fmt.Sprintf("%s: %s", s.label, message))
		return 0, false
	}
	// 一条撑过了稳定窗口（= MaxDelay，也就是退避的最大间隔）的连接，说明上一次
	// 断线事件已经过去了：这次从头开始算预算。
	if lostEstablished && time.Since(connectedAt) >= s.policy.MaxDelay {
		*failedAttempts = 0
	}
	*failedAttempts++
	if *failedAttempts > s.policy.MaxAttempts {
		if err := disposers.disposeAll(context.WithoutCancel(ctx)); err != nil {
			s.logger.Error("mcp: 放弃重连时撤销工具出错", "serverName", s.config.ServerName, "error", err)
		}
		*disposers = nil
		s.logger.Error(fmt.Sprintf(
			"%s: giving up after %d consecutive failed reconnect attempts — tools unregistered;"+
				" reload the plugin or restart the Host to reconnect", s.label, s.policy.MaxAttempts))
		return 0, false
	}
	delay := backoff(s.policy, *failedAttempts)
	action := "connection failed; retrying"
	if lostEstablished {
		action = "connection lost; reconnecting"
	}
	// 新增: DSH 那句话里印的是毫秒数。Go 的 [time.Duration] 自己就印成 `500ms`，
	// 那比一个裸数字好读，而这一行是日志不是契约，所以照 Go 的写法来。
	s.logger.Warn(fmt.Sprintf("%s: %s in %s (attempt %d/%d)",
		s.label, action, delay, *failedAttempts, s.policy.MaxAttempts))
	return delay, true
}

// backoff 算第 attempt 次重连前要等多久：翻倍到上限为止。
//
// 源: packages/mcp/mcp-client/src/connection.ts:216
//
// 新增: DSH 写的是 `Math.min(maxDelayMs, initialDelayMs * 2 ** (attempt - 1))`，
// JS 的数字溢出成 Infinity，min 照样把它夹回上限。Go 的移位溢出是**回绕**，
// 移够 64 位之后得到 0，那会变成「不等就重连」。所以这里显式判溢出。
func backoff(policy ReconnectPolicy, attempt int) time.Duration {
	shift := attempt - 1
	if shift >= 63 {
		return policy.MaxDelay
	}
	delay := policy.InitialDelay << uint(shift)
	if delay <= 0 || delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

// transport 造一条新的 Streamable HTTP 传输。
//
// 源: packages/mcp/mcp-client/src/transport.ts:44-48
//
// 新增: stdio 那一支不移，理由见包文档。
func (s *supervisor) transport() sdk.Transport {
	transport := &sdk.StreamableClientTransport{Endpoint: s.config.URL}
	if len(s.config.Headers) > 0 {
		// 新增: DSH 通过 `requestInit.headers` 传自定义请求头。Go SDK 的
		// StreamableClientTransport 没有这个字段，所以包一层 RoundTripper——
		// 那是 Go 里给一个自己不拥有的 HTTP 客户端加请求头的常规做法。
		transport.HTTPClient = &http.Client{Transport: &headerRoundTripper{
			base:    http.DefaultTransport,
			headers: s.config.Headers,
		}}
	}
	return transport
}

// headerRoundTripper 给每一条出去的请求挂上一组固定的请求头。
type headerRoundTripper struct {
	// base 是真正发请求的那一层。
	base http.RoundTripper
	// headers 是要挂上去的请求头。
	headers map[string]string
}

// RoundTrip 实现 [net/http.RoundTripper]。
//
// 按 RoundTripper 的契约，实现方不许改传进来的那个请求，所以这里先浅拷贝一份、
// 再给拷贝出来的那份换一张自己的头表。
func (r *headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	for name, value := range r.headers {
		cloned.Header.Set(name, value)
	}
	return r.base.RoundTrip(cloned)
}
