// Package github 是 GitHub 那一侧的**入口适配器**：把一次原始的 HTTP 投递
// 认成一次 [webhook.VerifiedDelivery]，或者当场拒掉。
//
// 源: packages/webhook/webhook-github/src/index.ts
// 源: packages/webhook/webhook-github/src/handler.ts
//
// [webhook.VerifiedDelivery] 名字里的 Verified 是一句**前置条件**——运行时自己
// 不认身份，它假定交上来的那次投递已经认过了。本包就是替 github 这一族兑现那句话
// 的地方：签名对不上的字节，走不到任何一条规则跟前。
//
// # 不收 http.Request
//
// 新增: DSH 那边这个包是一个绑在 `ctx.webServer` 上的 cordis 插件，`apply` 往
// 注入进来的 HTTP 服务器上注册一条精确路由，处理函数直接收
// `IncomingMessage`／`ServerResponse`。本仓库不规定宿主用哪个 HTTP 框架，
// 所以这里的入口收的是**已经读出来的字节加一份请求头**（[Ingress.Accept]），
// 交回一次投递或者一个错。接 HTTP、回 202、选状态码，都是装配方的事。
//
// 换来的是这个包可以在任何一种入口上复用：一条 HTTP 路由、一个消息队列的消费者、
// 一次回放的重投，验签这件事在三种场合是同一件事。
//
// # 拒绝的理由是可分辨的
//
// 装配方要按拒绝的理由挑 HTTP 状态码，所以本包的每一种拒绝都是一个可以用
// [errors.Is] 判定的哨兵值，而不是一句文案：
//
//   - [ErrMissingHeader]、[ErrAmbiguousHeader]、[ErrMalformedPayload] → 400
//   - [ErrBadSignature] → 401
//   - [ErrSecretUnavailable] → 503
//
// 新增: DSH 把状态码直接写进它自己的 `WebhookHttpError`。本包不绑 HTTP，
// 那张对照表因此挪到了装配方手上——上面这三行是它的内容。
//
// # 一个头出现两次就是拒绝，不是取第一个
//
// 三个身份头各自必须**恰好出现一次**。出现两次时取第一个是一条真实的绕过路径：
// 中间的每一跳（代理、网关、框架）挑的那一个不一定是同一个，于是「验的那一份」
// 和「用的那一份」可以不是同一份。所以这里报 [ErrAmbiguousHeader]。
//
// 这也是入口签名收 map[string][]string 而不是 map[string]string 的理由：
// 后者在进来之前就已经把重复头折掉了，这道关根本没有机会成立。
//
// # 事件体一个字节都不重排
//
// 认过之后，本包把事件名和请求体拼成 `{"name":…,"payload":…}` 交出去，
// payload 那一段是**原字节**。它不解一遍再排回去，因为那会磨掉大整数的精度、
// 也会重排键序，而规则完全可能拿它再算一次签名或者按字节比对。
//
// # 不做什么
//
//   - **不接 HTTP，也不回响应。**没有路由、没有状态码、没有 202，
//     见上面「不收 http.Request」。
//   - **不设请求体上限。**DSH 的 `maxBodyBytes` 是**边读边数**的，字节还没进内存
//     就能掐断；本包收到的是一个已经读完的切片，那时候再嫌它大已经晚了。
//     这道关必须由读那条流的人来把，放在这里只是摆样子。
//   - **不看 Content-Type，也不看请求方法。**签名过了就是持有密钥的一方发的，
//     它把 Content-Type 写成什么不改变这个事实。那两道关属于 HTTP 层，
//     本包不在 HTTP 层。
//   - **不去重。**同一个 [webhook.DeliveryID] 送两次就认两次。去重和运行时那边
//     一样是有意不做的，见 [webhook.DeliveryID]。
//   - **不解释事件。**payload 里有什么字段、这次 push 动了哪些文件，
//     由每一条规则自己看。本包只保证那段字节是持有密钥的一方发的。
//   - **不缓存密钥。**每一次都走 [credentials.Provider.Resolve] 重新解析，
//     这样轮换过的密钥下一次投递就生效，理由见那个包。
package github
