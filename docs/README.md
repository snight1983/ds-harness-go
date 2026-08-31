# ds-harness-go 文档

这里记录运行时的架构、模块职责、设计约束和能力边界。文档以源码当前行为为准，不把规划中的能力写成已经实现。

## 模块文档

- [core/agent](core/agent.md)：活 Agent 的公共契约、注册表、收件箱投影和生命周期扩展点。

## 项目级文档

- [总体设计](DESIGN.md)：运行时边界、持久化和装配设计。
- [移植裁决](portmap/rulings.md)：DeepSeek Harness 包级能力的移植或排除决定。
- [符号级裁决](portmap/decisions.md)：逐符号实现依据。

## 阅读方式

`docs/` 可以直接作为 GitHub Pages 的发布目录。发布后，页面左侧显示模块目录，右侧显示所选文档；Markdown 文件在 GitHub 仓库内也可以直接阅读。
