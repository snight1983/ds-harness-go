// 本文件的作用：持久凭据记录的词汇，以及它在介质上的编码。
//
// 源: packages/credentials/credentials/src/types.ts:30-59

package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
)

// 记录编解码会返回的两种错误。
var (
	// ErrMalformedRecord 表示这段字节排不成、或者读不回一条记录。
	ErrMalformedRecord = errors.New("credentials: 凭据记录格式不对")

	// ErrUnknownRecordKind 表示记录上的判别标签不是登记过的两个之一。
	ErrUnknownRecordKind = errors.New("credentials: 凭据记录的类别不认识")
)

// RecordKind 是一条记录的判别标签，也是**封闭**的两个值。
//
// 源: packages/credentials/credentials/src/types.ts:37-38,52-53
//
// 封闭是重点：接缝对两类记录能做的事不一样（api-key 的字段它读得懂，
// grant 的载荷它一个字都不许读），而分派一个封闭集合可以逐个列全。
type RecordKind string

const (
	// KindAPIKey 是接缝自己看得懂的那类：一个密钥、一组供应商环境值，或者两者都有。
	KindAPIKey RecordKind = "api-key"
	// KindGrant 是一次授权换来的产物，载荷原样保管，接缝不解释。
	KindGrant RecordKind = "grant"
)

// Record 是一条持久凭据记录，按「接缝可以拿它做什么」打了标签。
//
// 源: packages/credentials/credentials/src/types.ts:59-60（CredentialRecord）
//
// 新增: DSH 那边是 `ApiKeyRecord | GrantRecord` 这个联合类型。Go 没有和类型，
// 这里用「接口 + 一个未导出的封印方法」来代替：本包外面写不出 sealedRecord，
// 于是这个接口的实现方**只可能**是下面两个，和联合类型一样是封闭的。
//
// 不用「一个带 Kind 字段、两套载荷都塞进去的结构体」，是因为那种写法允许
// kind 是 api-key 而 grant 的载荷也填着——一个在联合类型里根本表达不出来的状态，
// 而每一个读它的人都得再判一次「这个字段这次算不算数」。
type Record interface {
	// Kind 是这条记录的判别标签。
	Kind() RecordKind

	// sealedRecord 把实现方封在本包内，见类型注释。
	sealedRecord()
}

// APIKeyRecord 是接缝自己看得懂的凭据：一个 api 密钥、一组供应商环境值，或者两者都有。
//
// 源: packages/credentials/credentials/src/types.ts:31-44（ApiKeyRecord）
//
// 两个字段**都可以为空**。两个都空的记录不是一条坏记录，它陈述的是一件确切的事实：
// 拥有方确认这条路由用它自己的环境发现来认证。这和「压根没有记录」是两回事——
// 后者表示没人确认过任何事，配置界面必须把它报成未配置。
type APIKeyRecord struct {
	// Key 是非空的密钥值，仅当这条凭据本身就是一个密钥时才有。
	Key string
	// Env 是形如 AWS_PROFILE 的供应商环境值。
	//
	// 名字应当是 POSIX 标识符，也就是 [IsRefName] 认的那套语法。
	// 接缝**不**在这里强制它：DSH 侧同样只在文档里写明，
	// 而拿到不合语法的名字的提供方，需要的是先问一句 [IsRefName]，
	// 不是在存储的时候被拒。
	Env map[string]string
}

// Kind 实现 [Record]。
func (APIKeyRecord) Kind() RecordKind { return KindAPIKey }

func (APIKeyRecord) sealedRecord() {}

// GrantRecord 是一次授权换来的产物，为它的拥有方原样保管。
//
// 源: packages/credentials/credentials/src/types.ts:46-57（GrantRecord）
//
// 接缝**从不**读取、校验、或者改写 [GrantRecord.Payload]：它是按拥有方插件的格式写的，
// 只有那个插件解释得了。唯一的约束是它经得起一次 JSON 往返。
type GrantRecord struct {
	// Payload 是拥有方自定义的 JSON 值，对接缝和其余所有插件都不透明。
	//
	// 新增: DSH 那边是 `unknown`，在 TS 里就是「随便什么，JSON.stringify 得动」。
	// Go 这边用 json.RawMessage，也就是**一段声称自己已经是 JSON 的字节**。
	//
	// 用原始字节而不是 any，是因为「不透明」在这里可以做到字面意义上的不透明：
	// 接缝不必解码就能存取，往返是逐字节精确的。解成 map[string]any 再排回去的话，
	// 大整数会被 float64 磨掉精度、键的顺序会变——而这是别人的授权令牌，
	// 磨掉一位数字的后果是它再也换不回访问权，且没有任何一次错误能解释它。
	Payload json.RawMessage
}

// Kind 实现 [Record]。
func (GrantRecord) Kind() RecordKind { return KindGrant }

func (GrantRecord) sealedRecord() {}

// MarshalJSON 把这条记录连同判别标签一起排出去。
//
// 新增: TS 的结构类型让 JSON.stringify 免费得到这件事，Go 需要自己写。
// 由接缝来写而不是让每个提供方各写一份，理由和 [Record] 是封闭集合一样：
// 判别标签的字面量一旦在 N 个提供方里各写一遍，迟早有一份写成 "apikey"，
// 而那条记录换个提供方就读不回来了。
func (r APIKeyRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(apiKeyWire{Kind: KindAPIKey, Key: r.Key, Env: r.Env})
}

// MarshalJSON 把这条记录连同判别标签一起排出去，理由同 [APIKeyRecord.MarshalJSON]。
//
// 空载荷当场报错，不排成 `null`。理由和本仓库 storagejson 里那条一样：
// 一段声称自己是 JSON 的字节声称错了，必须当场失败，而不是往介质上写一条
// 下次读回来是 null 的记录——那条记录在拥有方看来就是一次静默的授权丢失。
func (r GrantRecord) MarshalJSON() ([]byte, error) {
	if !json.Valid(r.Payload) {
		return nil, fmt.Errorf("%w：grant 的载荷不是合法 JSON", ErrMalformedRecord)
	}
	return json.Marshal(grantWire{Kind: KindGrant, Payload: r.Payload})
}

// UnmarshalRecord 把一段字节读回一条 [Record]。
//
// 新增: 这是 [APIKeyRecord.MarshalJSON] 的读取面。Go 的 encoding/json 解不进接口，
// 所以按判别标签分派这一步必须有人写；由接缝写的理由同上。
//
// 不认识的标签报 [ErrUnknownRecordKind] 而不是当成 api-key：一条由更新版本的
// 拥有方写下的记录，被旧版本读成一条空的 api-key 记录之后，
// 下一次 [Provider.ModifyRecord] 就会把它真的覆盖掉。
func UnmarshalRecord(data []byte) (Record, error) {
	var tagged struct {
		Kind RecordKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedRecord, err)
	}

	switch tagged.Kind {
	case KindAPIKey:
		var wire apiKeyWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedRecord, err)
		}
		return APIKeyRecord{Key: wire.Key, Env: wire.Env}, nil

	case KindGrant:
		var wire grantWire
		// 这一支目前走不到，也没有用例覆盖：grantWire 只有 kind 和一个
		// json.RawMessage，而上面那次解码已经证明整段是合法 JSON、kind 是个字符串——
		// RawMessage 容得下**任何**合法 JSON 值，于是这次解码没有可失败的地方。
		// 仍然把错误接住而不是丢掉：哪天 grantWire 上多一个有类型的字段，
		// 丢掉的那个错误会让一条读不进去的记录变成一条空记录，静默地过去。
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedRecord, err)
		}
		if !json.Valid(wire.Payload) {
			return nil, fmt.Errorf("%w：grant 记录缺少载荷", ErrMalformedRecord)
		}
		return GrantRecord{Payload: wire.Payload}, nil

	default:
		return nil, fmt.Errorf("%w：%q", ErrUnknownRecordKind, string(tagged.Kind))
	}
}

// apiKeyWire 和 grantWire 是两条记录在介质上的样子。
//
// 单独摆出来而不是给 [APIKeyRecord] 加 json 标签，是因为判别标签 kind 不是记录的字段——
// 它是**类型自己**，由 [Record.Kind] 给出。放进结构体的话，一条记录就多了一个
// 可以和自己的类型对不上的字段。
type apiKeyWire struct {
	Kind RecordKind        `json:"kind"`
	Key  string            `json:"key,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

type grantWire struct {
	Kind    RecordKind      `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}
