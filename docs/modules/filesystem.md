# 文件系统

## 定位

`fs` 是模型执行世界的文件系统接缝，统一路径解析、观察版本、条件写入、编辑意图和策略通知；`adapter/objectstore` 用通用对象存储 Backend 实现这套接缝。

## 架构

```text
工具 / 指令加载器
       |
       v
fs.FileSystem + fs.Policy
       |
       v
adapter/objectstore.Store
       |
       v
storage.Backend
```

`Target` 是解析后的稳定目标，`TargetKey` 用于后端寻址，`Version` 表示观察到的内容版本。读取返回 `Present` 或 `Absent` 观察；写入必须携带 `CreateIfAbsent` 或 `ReplaceIfVersion`，避免盲目覆盖并发修改。

## 核心能力

| 能力 | 接口 |
|---|---|
| 路径解析 | `Resolve`、`Contains` |
| 元数据与读取 | `Stat`、`Lstat`、`ReadText`、`ReadBytes`、`StreamText` |
| 目录 | `ListDir` |
| 条件写入 | `WriteText`、`WriteIntent`、`WriteOutcome` |
| 条件编辑 | `EditText`、`EditIntent`、`EditOutcome` |
| 策略 | `Policy.DecideWriteIntent`、`DecideEditIntent`、`NotifyObserved` |
| 可选：操作系统里的名字 | `OSPathFileSystem.ProcessPath`、`FileURL` |

## 为什么进程路径和 file: URI 单独成一道可选接缝

`FileSystem` 上的十个方法是**每个后端都做得到**的。`ProcessPath` 和 `FileURL` 不是：它们要求目标在操作系统的文件命名空间里也有一个名字，而本仓库唯一的生产后端 `adapter/objectstore.Store` 架在对象存储上——一个对象没有进程能打开的路径，也没有 `file:` URI。

上游 DSH 把十二个方法一起写在抽象类上，因为它那边每一个后端都架在真的文件系统上。这里不是。强制实现只剩三种写法，三种都是坏的：交回对象键或 `s3://` 串是一次静默的说谎，调用方会把它交给一次 `open()` 然后在很远的地方失败；交回空串是同一个谎，只是失败得更晚；panic 则把「这个后端没有这项能力」这件**静态**的事实推迟到运行期才说，而且是用一个能带走整个进程的方式说——这个包是嵌在长期运行的服务里跑的。

所以语义交给类型系统：做得到的后端实现 `fs.OSPathFileSystem`，做不到的不实现，调用方类型断言，断言不过就是「这条路在这个部署上走不通」，一个 error 而不是一次崩溃。同样的手法见 `feature/persistence.SeekableBackend`。

本仓库目前没有调用方：会用到它的消费方（起子进程、拼命令行）整支在裁决里是范围外。这道接缝留着是为了宿主自己挂本地后端时有个说得清的位置。

## 一致性与安全

- 路径先规范化为 Target，再交给后端；显示路径不能直接当存储键。
- `Contains` 按规范化路径判断父子关系，拒绝前缀字符串式越界。
- 条件写入把调用方观察过的版本带到提交点，版本变化时返回冲突。
- Policy 的决策器按注册顺序组成，观察通知在实际读取后触发。

## 生命周期与并发

`objectstore.Store` 不拥有传入的 `storage.Backend`。并发一致性依赖 Backend 的条件写语义；`VerifyCreateIfAbsent` 可在启动时确认后端确实支持原子“不存在才创建”。

## 失败语义

文件错误使用稳定 `ErrorCode`，区分不存在、冲突、非法路径、不支持和后端失败。条件写冲突不会自动重试或覆盖，调用方必须重新读取版本。

## 能力边界

- 不是 `os.File` 的完整替代，不支持任意文件描述符操作、符号链接创建和 mmap。
- `objectstore` 是文件语义适配，不是本地磁盘镜像。
- 沙箱、租户根目录和授权规则由宿主配置与 Policy 决定。
- 预设目录属于宿主部署配置，故意不走这套执行世界文件系统。

## 相关源码

- `fs/fs.go`
- `fs/types.go`
- `fs/policy.go`
- `adapter/objectstore/`
