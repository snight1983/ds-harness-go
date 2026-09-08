// Package persona 是一行「换掉某个 agent 的人设」的组合项。
//
// 对应 DSH 的 @deepseek-ai/dsh-persona（packages/preset/persona）。
//
// 源: packages/preset/persona/src/index.ts:1-14
//
// # 这一行只在作用域里成立
//
// 人设那个槽位是**提示词注册表自己**的配置：[github.com/snight1983/ds-harness-go/harness/systemprompt.NewRegistry]
// 无条件把 `deployment:persona` 登记在它持有者那一层。所以这一行装到一个有身份的
// agent 作用域上，是**遮蔽**掉部署方那份人设；装到注册表持有的那一层上，就是同一层
// 里重名——当场报错，而不是悄悄并存。
//
// 那条约束正是这个包存在的理由。一份 agent 预设够不着提示词注册表本身，没有这一行的
// 话，一份预设能换掉一个 agent 的工具，却永远换不掉它的身份。
//
// # 这里没有照录的部分
//
// 新增: DSH 那两个从注册表转发出来的 `PERSONA_SECTION` / `PERSONA_ORDER` 导出不移。
// 它们在 TS 那边是为了让预算作者不必把字面量再写一遍；Go 里 import 一次
// [github.com/snight1983/ds-harness-go/harness/systemprompt] 就拿到同一个常量，转发一层只会让同一个东西多出
// 第二个名字。
//
// 新增: cordis 的插件名 / inject 声明 / schemastery 运行期 schema 全部不移。配置就是
// [Config] 这个结构体，默认值由零值兑现——包括那个取反过的
// [Config].SuppressRuntimeContext（DSH 是 `includeRuntimeContext?: boolean` 默认 true）。
//
// 新增: DSH 的 `apply` 靠 `ctx.effect` 和 ctx 的生命周期各自撤销两处登记，[Install]
// 把它们收成一个撤销函数，撤起来是登记的逆序。
//
// # 不做什么
//
//   - **不授予工具、权限或者任何外部能力。**它只往提示词注册表登记一段人设，模型
//     能干什么一个字都没改。
//   - **不拥有那个槽位。**`deployment:persona` 由
//     [github.com/snight1983/ds-harness-go/harness/systemprompt.NewRegistry] 无条件登记，本包只是
//     往另一层再放一份去遮蔽它。
//   - **不决定装在哪一层。**是遮蔽掉部署方那份、还是同层重名当场报错，取决于装配方
//     交进来的那个 owner 作用域。
//   - **不渲染，也不插值。**`{{...}}` 的解算和整份提示词怎么拼归
//     [github.com/snight1983/ds-harness-go/harness/systemprompt]，本包只递一段正文和两个开关进去。
//   - **不转发提示词注册表的常量。**段名和次序 import 一次那个包就有，转发一层只会
//     给同一个东西起第二个名字。
package persona
