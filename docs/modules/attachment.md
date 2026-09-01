# 附件与图片

## 定位

`attachment` 定义与模型提供方无关的图片附件契约：准入、持久引用、读取、请求版本投影和错误分类。它把“用户上传的原图”和“送给某个模型的请求图”分成两层，避免提供方限制污染会话日志。

## 架构

```text
EncodedImage
    |
    | 规范 base64 + 批次限额
    v
ImageInput -> Store.ValidateImage -> Store.SaveImage -> ImageRef
                                                   |
                                                   v
                                      RequestImageProjector
                                                   |
                                                   v
                                             RequestImage
```

`ImageRef` 可以进入会话日志，只包含不透明附件 ID、媒体类型、字节数和尺寸；它不包含文件路径、签名 URL 或原始字节。`RequestImage` 是按模型像素和字节预算派生的确定性版本，通过 `VariantID` 去重和缓存。

## 核心接口

| 接口或函数 | 职责 |
|---|---|
| `Store` | 校验、保存和读取不可变图片 |
| `RequestImageProjector` | 按 `RequestPolicy` 生成请求版本 |
| `AdmitEncodedImages` | 校验规范 base64，并以批次方式保存 |
| `ValidateImageBatch` | 检查张数、总字节和媒体类型 |
| `SaveImages` | 先校验整批，再按输入顺序提交 |
| `ReadImageRequest` | 读取持久图并生成模型请求图 |

## 数据与安全规则

- `ID` 和 `VariantID` 都是不透明标识，不能解释为路径。
- `ImageLimits` 分开限制单图字节、消息总字节、图片数、像素、边长和媒体类型。
- base64 必须是标准编码的规范形；空串、换行、非法填充和 URL-safe 别名会被拒绝。
- 调用方声明的媒体类型必须与解码后的真实内容一致。
- 显示名永远只作展示，不能参与路径解析。

## 生命周期与并发

本包不持有后台协程。并发能力取决于 `Store` 实现；同一内容的 ID 和同一投影的 `VariantID` 必须稳定。批次操作在任何写入前完成可执行的校验，但 `Store` 是否提供跨对象事务由实现方决定。

## 失败语义

错误使用稳定 `Code` 分类，`IsImageAdmissionError` 可区分用户输入错误和基础设施错误。读取时引用与实际字节不一致必须失败，不能静默返回另一张图片。

## 能力边界

- 不内置图片编解码器、对象存储或数据库。
- 不决定模型是否支持图片；模型路由层负责选择策略。
- 不把附件存储等同于文件系统。
- 不保证外部 Store 的事务性、加密和保留策略。

## 相关源码

- `attachment/attachment.go`
- `attachment/admission.go`
- `attachment/types.go`
- `attachment/error.go`
