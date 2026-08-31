// 本文件的作用：这个包在不变量注册表里的那个名字。
//
// 源: packages/core/agent-default-model/src/invariant.ts:14

package agentdefaultmodel

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/core/agent-default-model/src/invariant.ts:14
//
// 这个包**没有**要注册的不变量，DSH 自己在模块注释里写明了原因：它没有自己的
// 事件关系可管——每一个可变的值在 [Service.CurrentSelection] 看得见它之前，
// 就已经被设置登记那一层（[settings.Options].Validate，也就是本包的
// [validateSettings]）审过一遍了。DSH 留了一个空的 installer，是为了让「这里
// 确实什么都不查」在一份组装出来的不变量清单里显式可见；Go 这边没有那种清单，
// 所以只留下这个名字——一个包要在注册表里占住自己的所有权，得先有名字——
// 而没有 RegisterInvariants。
const PackageName = "@deepseek-ai/dsh-agent-default-model"
