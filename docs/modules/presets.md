# Agent 预设与 Persona

## 定位

`preset/agentpresets` 发现能力组合，把静态链接的 Go 组装器挂到常驻作用域，并让 Agent 通过父作用域加入预设；`preset/persona` 提供替换系统人设的组合项。

## 架构与预设结构

```text
<root>/<preset-id>/
  agent.cordis.yml   # 组合清单
  preset.yml         # 可选展示元数据
```

发现过程不缓存目录清单，`Roster.List` 和 `Resolve` 每次重扫根目录。坏元数据只影响展示；坏组合仍在列表中，但 Mount 会明确失败，避免被占用的 ID 从界面消失。

## 存储接缝

`agentpresets` 自己不碰任何一种介质：它只对 `Config.FileSystem`（即 `fs.FileSystem`）说话。接缝上的路径是斜杠分隔、对本包不透明的字符串，翻译成介质自己的形状是实现方的事。版本身份走 `fs.Info.Version` 这个不可解释的令牌——对象存储用 ETag，本地介质用 stat 身份；答不出身份时装载不换代。

这里原先另有一道 `Config.Store` 窄接缝（`Stat`/`List`/`ReadFile`/`WriteFile`/`MakeDir`/`Remove`/`RemoveTree`），和 `fs.FileSystem` 方法一一对应地重复，以及一个落在本地目录上的实现 `preset/presetstore/localdir`。两者都已删除：一棵内容树的读写在本仓库只剩 `fs.FileSystem` 一条缝，换介质是装配时挑一个后端，业务代码一行不动。`fs.FileSystem` 为此补上了这条缝原有而它缺的四个原语（`WriteBytes`/`MakeDir`/`Remove`/`RemoveTree`）；唯一没有跟过来的是 `WriteFile` 上那个 `executable` 位，理由见 `fs/fs.go` 上 `WriteBytes` 的文档。

## 静态组装

Go 不支持运行时 import npm 包，因此组合行通过 `ComposerSet` 查找编译期注册的 `Composer`。一份预设只装到一个常驻作用域；多个 Agent 通过 `scope.BindParent` 共享该装载。任一 Composer 安装失败时整份 Mount 逆序回滚。

`Roster.Recompose` 可以把 Agent 重新绑定到另一预设。实际选择写入 Session 事件，`ResolveSessionPreset` 以最后一次选择为准，而不是永远读取创建头。

## Persona

`persona.Install` 在目标作用域注册人设提示词，覆盖外层部署默认值。它不能覆盖同一层已有的同名槽位，避免两份人设无序并存。是否包含运行时上下文由显式配置决定。

## 生命周期与并发

- Roster 用单飞方式避免同一预设被并发重复装载。
- 文件戳变化触发装载换代；旧代在不再被引用后释放。
- `Roster.Close` 释放全部常驻 Mount，Agent 局部父绑定随 Agent 作用域释放。

## 失败语义

- 未知 Composer、无效 YAML、重复 ID、不可写根和安装失败返回可区分错误。

## 能力边界

- 预设只能调用宿主编译进来的 Composer，不能下载或动态执行任意代码。
- 用户创作只允许从已有组合整目录复制到配置的可写根。
- 预设根是宿主配置：装配方把根路径填进 `Config.Roots`，本包只在这些根底下读写。走的是同一个 `fs.FileSystem` 接口，但接的是哪一个后端由装配决定。
- Persona 只影响系统提示词，不授予工具或外部权限。

## 相关源码

- `preset/agentpresets/`
- `preset/persona/`
- `fs/`
- `scope/`
- `harness/systemprompt/`
