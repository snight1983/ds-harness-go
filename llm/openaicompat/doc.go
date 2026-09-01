// Package openaicompat 是一个通用的、说 OpenAI 兼容协议的 llm 适配器。
//
// 源: packages/llm/llm-pi-ai/src/index.ts
//
// 一个插件实例拥有一张按提供方路由索引的配置表，每条路由自己声明端点、模型清单、
// 凭据引用与请求头；每次请求都重新读一遍那张表，所以改了 key、端点、模型或旋钮
// 都能不重启就落到下一次请求上，而改了**路由集合**（或某条路由在登记那一刻被
// 捕获的重试策略）会就地把同一个适配器实例重新登记一遍。
//
// # 这个包为什么是重写而不是移植
//
// DSH 那个包叫 llm-pi-ai，它 12 个源文件里有 10 个直接 import 第三方 npm SDK
// `@earendil-works/pi-ai`（^0.82.1）。真正干活的东西——线上协议实现、内置提供方
// 目录、传输层——全在那个库里，那个包自己几乎只是一层绑定。那个库拿不到源码，
// Go 也没有等价物，所以逐行移植在物理上做不到。
//
// 裁决是：**Go 原生重写，只做 OpenAI 兼容一条协议。**
//
// 做的：
//   - 一个 OpenAI Chat Completions 的流式客户端（走 github.com/openai/openai-go/v3），
//     实现 [github.com/snight1983/ds-harness-go/llm.Adapter]；
//   - 手工声明的路由（baseURL + apiKey + headers）——正是 docs/DESIGN.md 说的
//     「本地模型走这条」；
//   - 配置校验、模型目录解算、重试策略、重放状态、图片卸载这几层照旧逐行对着 DSH 走。
//
// 不做的（连同它们那些配置字段一起）：
//   - pi-ai 那份 893 行的内置提供方目录，以及基于目录的模型发现短路；
//   - `openai-responses` 与 `anthropic-messages` 两条协议（于是 `api` 字段消失）；
//   - WebSocket 传输与 `websocketConnectTimeoutMs`；
//   - OAuth 登录流程（auth.ts / login.ts）；
//   - `compat` / `thinkingBudgets` / `cacheRetention` 这些 pi-ai 自己类型里的开关。
//
// 凡是因为上面这些而偏离 DSH 的地方，本包一律用 `新增:` 写明为什么偏离。
// `// 源:` 注释仍然指向 packages/llm/llm-pi-ai/src/...，因为那才是这些行为的出处。
//
// # 一份配置长什么样
//
//	providers:
//	  acme-gateway:
//	    displayName: Acme Gateway
//	    apiKeyEnv: ACME_GATEWAY_API_KEY
//	    baseURL: https://gateway.acme.example/v1
//	    models:
//	      - id: acme-large
//	        name: Acme Large
//	        contextWindow: 65536
//	        maxTokens: 4096
//	      - id: acme-think
//	        name: Acme Think
//	        contextWindow: 262144
//	        maxTokens: 32768
//	        # 键 = 能选的档位，值 = 线上拼法；只有 off 那一档允许留空值
//	        # （意思是「支持这一档，但什么都不发」）。
//	        reasoningEfforts:
//	          off: ''
//	          high: high
//	          max: ultra
package openaicompat

import "errors"

// PluginName 是这个插件在诊断里的名字。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:89（name）
//
// 新增: 不叫 llm-pi-ai——它不再用那个库，一条说 "llm-pi-ai: ..." 的诊断会把人
// 引到一个这份代码里根本不存在的依赖上去。
const PluginName = "llm-openai-compat"

// ErrInvalidConfig 是本包所有配置层拒绝的哨兵。
//
// 新增: DSH 那边一律 `throw new Error(...)`，调用方靠读文案分辨。Go 这边配置错误
// 全部包在这一个哨兵下面，让装配方能用 errors.Is 判定「这是配错了，不是跑挂了」。
// 理由和 [github.com/snight1983/ds-harness-go/llm/llmretry.ErrInvalidConfig] 上那条一样。
var ErrInvalidConfig = errors.New(PluginName + ": 配置不合法")
