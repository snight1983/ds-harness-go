// 本文件的作用：这个包在不变量注册表里的那个名字。
//
// 源: packages/interaction/tool-ask-user/src/invariant.ts:10

package askuser

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/interaction/tool-ask-user/src/invariant.ts:10
//
// 这个包**没有**要注册的不变量，DSH 自己在注释里写明了原因：这件面向模型的适配器
// 没有自己的生命周期流，执行关系归它调用的那道能力接缝管
// （[ds-harness-go/interaction/userquestions]）。所以这里只留下这个名字——一个包
// 要在注册表里占住自己的所有权，得先有名字——而没有 RegisterInvariants。
const PackageName = "@deepseek-ai/dsh-tool-ask-user"
