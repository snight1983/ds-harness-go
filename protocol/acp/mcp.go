// 本文件的作用：把一条 ACP 会话自带的那份 MCP 服务器声明，验完之后翻成本仓库的
// MCP 客户端配置，挂进那个还没公布的 agent 作用域。
//
// 源: packages/acp/acp/src/mcp.ts:1-143

package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	wire "github.com/coder/acp-go-sdk"
	"golang.org/x/text/unicode/norm"

	"github.com/snight1983/ds-harness-go/protocol/mcp"
	"github.com/snight1983/ds-harness-go/scope"
)

// validServerName 是可以原样用作命名空间的那种服务器名。
//
// 源: packages/acp/acp/src/mcp.ts:10
//
// 它和 [github.com/snight1983/ds-harness-go/protocol/mcp.Config] 那边卡的形状是同一条，两边
// 各留一份：这一份决定「要不要改名」，那一份是配置自己的不变量。
var validServerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// MCPConfigError 是一次「对面声明错了、改一下就能对」的 MCP 失败。
//
// 源: packages/acp/acp/src/mcp.ts:12-18（AcpMcpConfigError）
//
// Message 面向协议对面，原样保留英文，理由同 [ContentError]。
type MCPConfigError struct {
	// Message 是那句能安全送出去的说明。
	Message string
}

// Error 实现 error。
func (e *MCPConfigError) Error() string { return e.Message }

// mcpConfigErrorf 造一个 MCP 声明失败。
func mcpConfigErrorf(format string, args ...any) *MCPConfigError {
	return &MCPConfigError{Message: fmt.Sprintf(format, args...)}
}

// MCPHost 是这座桥用得到的那一块 MCP 服务：只要「照这份配置连一台服务器上去」。
//
// 源: packages/acp/acp/src/mcp.ts:32（agentCtx.plugin(McpClient, config)）
//
// 新增: DSH 是往 agent 上下文里 `plugin()` 一个 cordis 插件，拆除由那个上下文管。
// Go 里对应的是 [github.com/snight1983/ds-harness-go/protocol/mcp.Host.Connect] 收一个宿主作用域，
// 拆除同样由那个作用域管。这里照本包 [Peer]、[ModelResolver] 的成例收窄成一个方法，
// 交进来的 [github.com/snight1983/ds-harness-go/protocol/mcp.Host] 自然满足它。
type MCPHost interface {
	// Connect 照 config 连一台 MCP 服务器，寿命挂在 owner 上。
	Connect(ctx context.Context, owner *scope.Scope, config mcp.Config) (*mcp.Connection, error)
}

// MountMCPServers 把一条会话的整份 MCP 声明验完、挂进 owner。
//
// 源: packages/acp/acp/src/mcp.ts:20-33（mountAcpMcpServers）
//
// owner 应当是那个**还没公布**的 agent 作用域，也就是 [agent.Setup] 收到的那一个：
// 这样第一次提示词装配之前工具就已经在位。整份声明先全部验完再开始连，所以一条写坏
// 的声明不会留下几台连了一半的服务器；而连到一半失败时不必自己回滚——setup 报错会让
// 整个事务回滚并处置这个作用域（见 [agent.Setup] 的约定）。
//
// 新增: DSH 的签名还收一个 sessionCwd，那是给 stdio 那一支当工作目录用的。stdio 不
// 移（见 [resolveMCPConfigs]），所以这个参数在这里不存在。
func MountMCPServers(
	ctx context.Context,
	host MCPHost,
	owner *scope.Scope,
	servers []wire.McpServer,
) error {
	if len(servers) == 0 {
		return nil
	}
	if host == nil {
		return mcpConfigErrorf("mcpServers is not supported: no MCP host is mounted")
	}
	configs, err := resolveMCPConfigs(servers)
	if err != nil {
		return err
	}
	for _, config := range configs {
		if _, err := host.Connect(ctx, owner, config); err != nil {
			return fmt.Errorf("acp: 挂 MCP 服务器 %s 失败：%w", config.ServerName, err)
		}
	}
	return nil
}

// resolveMCPConfigs 把那些线上声明翻成配置，认不出的传输一律拒。
//
// 源: packages/acp/acp/src/mcp.ts:35-74（resolveMcpConfigs）
//
// 新增: **范围裁剪——只收 HTTP 那一支。** DSH 收 stdio 和 http 两种，另外两种
// （sse、acp）它也拒。这里多拒一种 stdio，理由不是这座桥挑的：本仓库的
// [github.com/snight1983/ds-harness-go/protocol/mcp] 整个只有 Streamable HTTP 一条传输，因为
// stdio 那一支要靠 `@deepseek-ai/dsh-subprocess`，而那个包整体不移（裁决记在
// docs/portmap/decisions.md）。所以这里连它自己都没有可翻的目标配置。
//
// 跟着 stdio 一起不移的还有两样 DSH 有而这里没有的东西，都是它专属的：那个
// sessionCwd 参数（stdio 子进程的工作目录），以及 entriesToRecord 的 environment
// 那一支（子进程环境变量的校验）。为一条连不出去的传输留一套校验没有意义。
//
// 握手时这座桥只声明 `mcpCapabilities.http`，所以一个守规矩的客户端根本不会送
// stdio 过来；这里的拒绝是给不守规矩的那种留的。
func resolveMCPConfigs(servers []wire.McpServer) ([]mcp.Config, error) {
	names := map[string]struct{}{}
	configs := make([]mcp.Config, 0, len(servers))
	for index := range servers {
		server := &servers[index]

		name, err := serverDeclaredName(index, server)
		if err != nil {
			return nil, err
		}
		serverName, err := normalizeServerName(name)
		if err != nil {
			return nil, err
		}
		if _, duplicate := names[serverName]; duplicate {
			return nil, mcpConfigErrorf("mcpServers contains duplicate normalized name: %s", serverName)
		}
		names[serverName] = struct{}{}

		if server.Http == nil {
			return nil, mcpConfigErrorf("mcpServers[%d] transport %s is not supported",
				index, unsupportedTransport(server))
		}
		if err := assertHTTPURL(server.Http.Url, fmt.Sprintf("mcpServers[%d].url", index)); err != nil {
			return nil, err
		}
		headers, err := headersToMap(server.Http.Headers, fmt.Sprintf("mcpServers[%d].headers", index))
		if err != nil {
			return nil, err
		}
		configs = append(configs, mcp.Config{
			ServerName:         serverName,
			URL:                server.Http.Url,
			Headers:            headers,
			FailOnStartupError: true,
		})
	}
	return configs, nil
}

// serverDeclaredName 掏出这个联合值那一支自己带的名字。
//
// 一个哪一支都没填的联合值是本构建认不出的传输标签，那时连名字都没处可取。
func serverDeclaredName(index int, server *wire.McpServer) (string, error) {
	switch {
	case server.Http != nil:
		return server.Http.Name, nil
	case server.Sse != nil:
		return server.Sse.Name, nil
	case server.Stdio != nil:
		return server.Stdio.Name, nil
	case server.Acp != nil:
		return server.Acp.Name, nil
	default:
		return "", mcpConfigErrorf("mcpServers[%d] declares an unsupported transport", index)
	}
}

// unsupportedTransport 说出这一条用的是哪种传输，好让那句拒绝话说得具体。
func unsupportedTransport(server *wire.McpServer) string {
	switch {
	case server.Sse != nil:
		return "sse"
	case server.Stdio != nil:
		return "stdio"
	case server.Acp != nil:
		return "acp"
	default:
		return "unknown"
	}
}

// headerNameChars 是 RFC 7230 的 token 字符集。
//
// 新增: DSH 用 node:http 的 validateHeaderName。Go 标准库里对应的那个
// （golang.org/x/net/http/httpguts）是内部包，所以这里照同一份 RFC 自己写一遍。
var headerNameChars = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

// headersToMap 把线上那串有序的名值对翻成一张表，重名不放过。
//
// 源: packages/acp/acp/src/mcp.ts:76-108（entriesToRecord 的 header 那一支）
//
// 新增: DSH 那边要用 `Object.create(null)`，因为 `__proto__` 是一个合法的头名字，
// 而普通对象上给它赋值会打到原型的 setter 上去。Go 的 map 没有这个问题。
func headersToMap(entries []wire.HttpHeader, field string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !headerNameChars.MatchString(entry.Name) || !validHeaderValue(entry.Value) {
			return nil, mcpConfigErrorf("%s contains an invalid header entry", field)
		}
		// 头名字大小写不敏感，所以重名要按小写判。
		identity := strings.ToLower(entry.Name)
		if _, duplicate := seen[identity]; duplicate {
			return nil, mcpConfigErrorf("%s contains duplicate name: %s", field, entry.Name)
		}
		seen[identity] = struct{}{}
		headers[entry.Name] = entry.Value
	}
	return headers, nil
}

// validHeaderValue 判一个头的值里有没有送不出去的字节。
//
// 新增: 和 node:http 的 validateHeaderValue 卡的是同一个集合——制表符、可打印
// ASCII，以及 obs-text 那一段；CR、LF 和别的控制字符都不行（放过它们就是一个
// 响应拆分漏洞）。Go 的 rune 在这里要按码点判：值是从 JSON 解出来的，可能带非
// ASCII，而 obs-text 只到 0xff。
func validHeaderValue(value string) bool {
	for _, char := range value {
		if char == '\t' {
			continue
		}
		if char < 0x20 || char == 0x7f || char > 0xff {
			return false
		}
	}
	return true
}

// nonNamespaceChars 是命名空间里留不下来的那些字符。
var nonNamespaceChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// normalizeServerName 从对面那个给人看的名字里，造一个稳定的本地命名空间。
//
// 源: packages/acp/acp/src/mcp.ts:110-122
//
// 已经合规的原样留着；否则取一段能读的词根，后面缀上原名 sha256 的前 8 位十六进制
// ——词根是给人看的，那 8 位才是保证两个不同原名不会撞进同一个命名空间的那一半。
func normalizeServerName(name string) (string, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsFunc(name, isNameControl) {
		return "", mcpConfigErrorf("mcpServers contains an invalid server name")
	}
	if validServerName.MatchString(name) {
		return name, nil
	}
	slug := nonNamespaceChars.ReplaceAllString(norm.NFKD.String(name), "_")
	slug = strings.Trim(slug, "_")
	if len(slug) > 20 {
		slug = slug[:20]
	}
	if slug == "" {
		slug = "server"
	}
	digest := sha256.Sum256([]byte(name))
	normalized := slug + "_" + hex.EncodeToString(digest[:])[:8]
	if len(normalized) > 32 {
		normalized = normalized[:32]
	}
	return normalized, nil
}

// isNameControl 判一个码点是不是名字里不许出现的控制字符。
func isNameControl(char rune) bool { return char <= 0x1f || char == 0x7f }

// assertHTTPURL 要求这是一个绝对的 HTTP(S) 地址。
//
// 源: packages/acp/acp/src/mcp.ts:124-132
func assertHTTPURL(value, field string) error {
	parsed, err := url.Parse(value)
	// Go 的 url.Parse 比 JS 的 URL 宽得多：它接受相对地址，也接受没有主机的地址。
	// 这里把那两样都补上，好让这条判据和 DSH 的 `new URL()` 是同一条。
	if err != nil || !parsed.IsAbs() || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return mcpConfigErrorf("%s must be an absolute HTTP(S) URL", field)
	}
	return nil
}
