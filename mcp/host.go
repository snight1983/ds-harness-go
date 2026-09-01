// 本文件的作用：那张「服务器名占用表」，以及一条连接从建立到关闭的对外接口。
//
// 源: packages/mcp/mcp-client/src/index.ts:45、140-181

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
)

// ImageAdmission 回答「这次工具执行能不能收图，收的话存到哪」。
//
// 源: packages/mcp/mcp-client/src/tools.ts:399-420（resolveImageAdmission）
//
// 新增: DSH 在那个函数里自己读活会话的请求头和 llm 服务，为的是证明「这次请求
// 真正路由到的那个模型收图」。本包在第 4 块，那两样都在后面的块里，够不着，
// 所以判断的**责任**搬给装配方，本包只留这个接缝。
//
// 交回一个错误表示拒绝，那句话会原样出现在模型看到的诊断文本里，所以它得是一句
// 人读得懂的话（DSH 那几句是 'no attachment store is mounted'、
// 'the current model route could not be resolved'、
// `model "<名字>" does not declare image input`）。为 nil 时图一律降级成文本。
type ImageAdmission func(ctx context.Context, exec tools.Execution) (attachment.Store, error)

// Options 是造一个 [Host] 的选项。
type Options struct {
	// Tools 是 MCP 工具要注册进去的那张表，必填。
	Tools *tools.Runtime

	// ImageAdmission 是图片准入接缝，可以为 nil；为 nil 时图一律降级成诊断文本。
	ImageAdmission ImageAdmission

	// Logger 记连接生命周期上的事，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Host 拥有一批 MCP 连接共用的那张服务器名占用表。
//
// 源: packages/mcp/mcp-client/src/index.ts:45（activeServerNames）
//
// 新增: DSH 是一个 cordis 插件，用 `WeakMap<Context, Set<string>>` 把占用表挂在
// 根 context 上，靠 fiber 的销毁收回去。Go 这边没有那个容器，所以那张表就是这个
// 类型自己的字段，[Host.Connect] 交回的 [Connection] 关掉时把名字还回来。
type Host struct {
	// registry 是工具注册表。
	registry *tools.Runtime
	// admit 是图片准入接缝。
	admit ImageAdmission
	// logger 是日志。
	logger *slog.Logger
	// mutex 护住 names；几条连接可以并发建立。
	mutex chan struct{}
	// names 是已经被占掉的服务器名。
	names map[string]struct{}
}

// NewHost 验一份选项，造出这个宿主。
func NewHost(options Options) (*Host, error) {
	if options.Tools == nil {
		return nil, fmt.Errorf("%w: 需要一张工具注册表", ErrInvalidConfig)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Host{
		registry: options.Tools,
		admit:    options.ImageAdmission,
		logger:   logger,
		mutex:    make(chan struct{}, 1),
		names:    map[string]struct{}{},
	}, nil
}

// Connection 是一台 MCP 服务器那条活着的连接。
//
// 源: packages/mcp/mcp-client/src/connection.ts:98-112（ConnectionHandle）
type Connection struct {
	// host 是发出这条连接的宿主。
	host *Host
	// serverName 是这条连接占着的那个名字。
	serverName string
	// supervisor 是那个一直在跑的监督者。
	supervisor *supervisor
	// closed 保证名字只还一次、监督者只叫停一次。
	closed sync.Once
}

// Connect 连上一台 MCP 服务器，并且在返回之前把首批工具注册好。
//
// 源: packages/mcp/mcp-client/src/index.ts:139-181
//
// 它**等**首次连接加首次工具发现结束再返回，好让调用方一拿到 [Connection]
// 就看得见那批工具。首次失败时：config.FailOnStartupError 为真就交回错误
// （这条连接已经清理干净），否则只记一条日志，监督者自己进重连循环。
//
// owner 是这些工具注册挂靠的作用域；它先于 [Connection.Close] 被释放时，
// 那些注册跟着一起没，这是作用域模型本来的意思。
func (h *Host) Connect(ctx context.Context, owner *scope.Scope, config Config) (*Connection, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w: 需要一个持有这些注册的作用域", ErrInvalidConfig)
	}
	validated, err := config.validate()
	if err != nil {
		return nil, err
	}
	// 先验重连策略：一份配错了的策略应该在**任何**副作用登记之前就让这次连接失败。
	policy, err := resolveReconnectPolicy(validated.Reconnect,
		fmt.Sprintf("mcp-client(%s): reconnect", validated.ServerName))
	if err != nil {
		return nil, err
	}
	// 再占名字：重名要在这里失败，而且先来的那个实例一根汗毛都不能动。
	if err := h.claim(validated.ServerName); err != nil {
		return nil, err
	}

	connection := &Connection{
		host:       h,
		serverName: validated.ServerName,
		supervisor: startSupervisor(validated, policy, h.registry, owner, h.admit, h.logger),
	}

	var startupErr error
	select {
	case startupErr = <-connection.supervisor.ready:
	case <-ctx.Done():
		// 调用方在首次尝试还没落地时就撤了。把这条连接整个拆掉再把取消交出去，
		// 免得留下一个没人持有、却还在重连的监督者。
		if closeErr := connection.Close(context.WithoutCancel(ctx)); closeErr != nil {
			h.logger.Error("mcp: 建立连接被取消时清理出错", "serverName", validated.ServerName, "error", closeErr)
		}
		return nil, context.Cause(ctx)
	}
	if startupErr != nil && validated.FailOnStartupError {
		if closeErr := connection.Close(ctx); closeErr != nil {
			h.logger.Error("mcp: 启动失败后清理出错", "serverName", validated.ServerName, "error", closeErr)
		}
		return nil, fmt.Errorf("mcp-client(%s): initial connection or tool synchronization failed: %w",
			validated.ServerName, startupErr)
	}
	return connection, nil
}

// claim 占下一个服务器名。
//
// 源: packages/mcp/mcp-client/src/index.ts:146-159
func (h *Host) claim(serverName string) error {
	h.mutex <- struct{}{}
	defer func() { <-h.mutex }()
	if _, taken := h.names[serverName]; taken {
		return fmt.Errorf(
			"%w: mcp-client: serverName %q is already in use by another mcp-client instance — pick a unique serverName",
			ErrInvalidConfig, serverName)
	}
	h.names[serverName] = struct{}{}
	return nil
}

// release 把一个服务器名还回去。
func (h *Host) release(serverName string) {
	h.mutex <- struct{}{}
	defer func() { <-h.mutex }()
	delete(h.names, serverName)
}

// Close 停掉重连、关掉活着的连接、等在途的同步收敛，然后把这台服务器还占着的
// 每一个工具都撤掉，最后把服务器名还回去。
//
// 源: packages/mcp/mcp-client/src/connection.ts:328-348
//
// 调用多次是安全的：后面几次什么都不做。
func (c *Connection) Close(ctx context.Context) error {
	c.closed.Do(func() {
		c.supervisor.stop()
		c.host.release(c.serverName)
	})
	if ctx.Err() != nil {
		// 监督者已经拿一个不带取消的 ctx 撤完工具了，这里只是把调用方的取消如实转出去。
		return context.Cause(ctx)
	}
	return nil
}
