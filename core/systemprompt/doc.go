// Package systemprompt 提供「模型这一步该看到什么」这条缝的注册表一侧：有次序的
// 系统提示段落、动态运行期上下文、工具 schema、以及提示词变量。
//
// 对应 DSH 的 @deepseek-ai/dsh-system-prompt（packages/core/system-prompt）。
//
// 源: packages/core/system-prompt/src/index.ts:1-5
//
// # 四张表
//
// 一次模型请求的输入由四类登记拼出来，各自有各自的表：
//
//   - [PromptSection]：系统提示词里的一段，按 Order 从小到大拼接。约定 -100 是宿主
//     身份，0 是部署方人设（[PersonaSection]），100–199 是工具指引。
//   - [PromptContext]：动态上下文，最终落成一条持久的 user 角色快照，可以整个压掉。
//   - [ToolProvider]：这一次看得见哪些工具。它是**提供方**而不是一张表，因为工具在
//     不同作用域里可见性不同。
//   - [VariableProvider]：`{{变量}}` 引用的值，渲染时严格插值。
//
// 段落和上下文一直到 [RenderPrompt] / [RenderContextSnapshot] 才插值；工具在装配
// 出来的时候就已经是最终次序了。
//
// # 层与遮蔽
//
// 登记落在**调用方作用域**那一层（[github.com/snight1983/ds-harness-go/core/scope.Layers] 的规矩）：宿主
// 和仓库级插件落全局层，挂在某个 agent 上的落那个 agent 自己那层。装配的时候把全局
// 层和视角作用域这条链合起来，**同名的近层盖住远层**，但位置不动——一个作用域换掉
// 某个段落的实现，不该顺带把它挪到提示词末尾去。
//
// 匿名的那几张表（工具提供方、运行期上下文压制、装配规则）不遮蔽，全都参与。
//
// # 完整提示词
//
// 一段 [PromptSection] 可以把 Complete 打开，意思是「我就是整份系统提示词」。装配
// 照样会跑完那条协作瀑布，好让工具、上下文、变量都解出来，然后把这一段原样放回去
// 当唯一的段落——所以一条装配规则**加不了也换不掉**那份提示词。同时生效的完整段落
// 多于一个，装配失败。
//
// # 这里没有照抄的部分
//
// 新增: cordis 的 Service / ctx.systemPrompt / 插件名 / inject 声明全部不移。本包就
// 是一个普通类型，装配方自己造一个 [Registry] 拿着。事件 `system-prompt/change`
// 换成 [Options].OnChange 这个回调，事件 `system-prompt/assemble` 那条瀑布换成
// [AssembleRule]——和 core/tools 那边一样。
//
// 新增: AbortSignal → [context.Context]。DSH 的 AssembleContext 上带一个 signal，
// Go 里 ctx 是每个提供方和每条规则的第一个参数，所以 [AssembleContext] 里只剩 Scope。
// 顺带地，DSH 那个类型是**可合并扩展**的（插件用 declare module 往上加字段），Go 没
// 有声明合并，[AssembleContext] 和 [PromptAssembly] 就是各自那几个字段。
//
// 新增: DSH 的 `text: string | ((context) => string)` 联合在 Go 里只留函数这一种
// 形状（[TextProvider]），固定文本用 [StaticText] 包一下。一个字段比「两个字段里
// 只许填一个」好懂，也就没有「两个都填了算谁的」这种问题。
//
// 新增: DSH 的 order 是 number，所以它得自己查 Number.isFinite。Go 的 int 天生有限，
// 那道检查整个不需要；DSH 全仓也没有人用过小数次序，而这套约定留出的间隔是 100。
//
// 新增: [PromptAssembly].Variables 是 `map[string]*string` 而不是
// `map[string]string`。DSH 那边是 `Record<string, string | undefined>`，插值时靠
// `Object.hasOwn` 分辨「名字不存在」和「名字在、这次没值」——两者报的错不一样，
// 所以 Go 这边也必须分得清。
//
// 新增: DSH 的 structuredClone(parameters) 换成 bytes.Clone。[llm.ToolSchema].Parameters
// 是一段 json.RawMessage，也就是一个字节切片；不拷一份，一条装配规则改到的就是
// 提供方自己留着的那份。
package systemprompt
