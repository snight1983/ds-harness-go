# 文件系统

## 定位

`fs` 是模型执行世界的文件系统接缝，统一路径解析、观察版本、条件写入、编辑意图和策略通知；`fs/objectstore` 用通用对象存储 Backend 实现这套接缝。

## 架构

```text
工具 / 指令加载器
       |
       v
fs.FileSystem + fs.Policy
       |
       v
fs/objectstore.Store
       |
       v
storage.Backend
```

`Target` 是解析后的稳定目标，`TargetKey` 用于后端寻址，`Version` 表示观察到的内容版本。读取返回 `Present` 或 `Absent` 观察；写入必须携带 `CreateIfAbsent` 或 `ReplaceIfVersion`，避免盲目覆盖并发修改。

## 核心能力

| 能力 | 接口 |
|---|---|
| 路径解析 | `Resolve`、`ProcessPath`、`FileURL`、`Contains` |
| 元数据与读取 | `Stat`、`Lstat`、`ReadText`、`ReadBytes`、`StreamText` |
| 目录 | `ListDir` |
| 条件写入 | `WriteText`、`WriteIntent`、`WriteOutcome` |
| 条件编辑 | `EditText`、`EditIntent`、`EditOutcome` |
| 策略 | `Policy.DecideWriteIntent`、`DecideEditIntent`、`NotifyObserved` |

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
- `fs/objectstore/`
