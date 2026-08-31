// 本文件的作用：每一次提供方请求都要带上的那份产品身份，以及它渲染成的
// User-Agent 头。
//
// 源: packages/llm/llm/src/attribution.ts:1-68
//
// 这份身份是**非秘密**的：只有产品名、版本、仓库地址三样公开事实。密钥、
// 本地路径、会话标识、提示词文本、任何按用户或者按请求变化的东西，一样都不许
// 进来——它们会跟着每一条请求发给提供方。
//
// 集中在这里而不是每个适配器各写各的，是为了让它们不会各自漂移：提供方那边看到
// 的产品名只有一个。

package llm

import "fmt"

// AppIdentity 是发给 LLM 提供方的那份静态公开身份。
//
// 源: packages/llm/llm/src/attribution.ts:25-32
type AppIdentity struct {
	// Product 是 User-Agent 里的产品标记，小写、用连字符分词。
	Product string
	// Version 是产品版本。
	Version string
	// URL 是产品仓库主页，渲染成 User-Agent 的注释部分。
	URL string
}

// AppIdentityVersion 是本装置报出去的版本号。
//
// 源: packages/llm/llm/src/attribution.ts:16
//
// 新增: DSH 在运行期从自己那份 package.json 里读 version，为的是「发出去的
// User-Agent 不会和发布出去的版本对不上」。Go 没有随二进制一起装的清单文件，
// 所以这里是一个常量，跟着 DSH 那份 packages/llm/llm/package.json 的 version 走。
const AppIdentityVersion = "0.1.1-rc.2"

// DefaultAppIdentity 交出本装置自己那份身份：每个适配器默认发的就是它。
//
// 源: packages/llm/llm/src/attribution.ts:40-44
//
// 需要贴牌的部署把自己那份 [AppIdentity] 传给 [AttributionHeaders]；不传就落回
// 这一份。没有任何办法把归属信息整个关掉。
//
// 新增: DSH 是一个导出的 const 对象（外加 deepFreeze 那一路的约定），这里是一个
// 函数。Go 的包级变量是可写的，导出一个 var 就等于让任何一个包都能把所有人发出去
// 的产品身份改掉；交出一份复制品则谁都改不动别人手里那份。这和本包 Message 那边
// 「值语义 + Clone」是同一个理由。
func DefaultAppIdentity() AppIdentity {
	return AppIdentity{
		Product: "deepseek-harness",
		Version: AppIdentityVersion,
		URL:     "https://github.com/deepseek-ai/deepseek-harness",
	}
}

// UserAgent 把一份身份渲染成标准的 User-Agent 值：product/version (+url)。
//
// 源: packages/llm/llm/src/attribution.ts:53-55
//
// 括号里那个 +url 注释是 RFC 9110 §10.1.5 的 product + comment 写法，
// 也是自报家门的惯例形式。
//
// 新增: DSH 那边参数带默认值，省略即取 APP_IDENTITY。Go 没有默认参数，
// 想要那份默认身份就显式写 [DefaultAppIdentity]()。
func UserAgent(identity AppIdentity) string {
	return fmt.Sprintf("%s/%s (+%s)", identity.Product, identity.Version, identity.URL)
}

// AttributionHeaders 造出适配器每一次提供方请求都必须带上的那几个归属头。
//
// 源: packages/llm/llm/src/attribution.ts:64-67
//
// 头名字是小写的——HTTP 的字段名在线上不分大小写。当下只有 user-agent 一个。
func AttributionHeaders(identity AppIdentity) map[string]string {
	return map[string]string{"user-agent": UserAgent(identity)}
}
