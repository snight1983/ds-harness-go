// 本文件的作用：把每一次「发给模型的」和「模型返回的」原样各存成一个文件。
//
// # 存什么
//
//   - `<序号>-request.json` —— 发出去的那一整份请求体，一个字节不改。
//   - `<序号>-response.txt` —— 服务端返回的每一条分块，一行一条，原文。
//
// # 为什么要原文
//
// 有一条链路上偶尔会出现这样一步：模型这一轮既没给正文、也没给工具调用，只有一段
// 思考；而那步思考的**文字里**写着一个工具调用的名字和参数。那一步于是什么都没做。
//
// 从适配器交出去的东西上看不出这是谁造成的——模型把调用写成了字、服务端把它塞进了
// 思考字段、还是适配器归错了类，三种可能留下的痕迹一样。只有两边都留着原文才分得清：
// 请求里那件工具在不在清单里、返回里那段字出现在哪个字段、`finish_reason` 是什么。
//
// # 它不改任何行为
//
// 只往文件里写。写不进去就当作没这回事——一条诊断记录绝不该把一次请求弄失败。
//
// # 默认是关的
//
// 开关是 [AdapterOptions.RawLogDir]，空串表示不记，**这是默认**。它一次请求写两个
// 文件、约 1.7 MB，几小时就是几十 MB，所以它只该在查一件事的时候打开。
package openaicompat

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

var rawSeq atomic.Int64

// rawSession 是这一次请求的落点。一次请求一个，形影不离。
type rawSession struct {
	dir string
	seq int64
}

// rawOpen 按配置里那个目录开一份记录。
//
// 目录为空交回 nil，后面每一个方法在 nil 上都当空操作——所以调用点不用自己分支。
// 建不出目录也交回 nil：记不下来是小事，为它把一次请求弄失败是大事。
func rawOpen(dir string) *rawSession {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	// **不清目录。**
	//
	// 先前这里会先把目录清一遍，本意是「这份记录只属于这一次」。代价是把上一次
	// 的证据也删了——而那正是要找的那一次。文件名带序号，本来就不会互相覆盖。
	return &rawSession{dir: dir, seq: rawSeq.Add(1)}
}

func (s *rawSession) path(suffix string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%03d-%s", s.seq, suffix))
}

// request 存下这一次发出去的请求体。
func (s *rawSession) request(body []byte) {
	if s == nil {
		return
	}
	_ = os.WriteFile(s.path("request.json"), body, 0o644)
}

// chunk 追加上一条返回的分块原文。
func (s *rawSession) chunk(raw string) {
	if s == nil {
		return
	}
	file, err := os.OpenFile(s.path("response.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.WriteString(raw + "\n")
}

// note 追加上一行给读的人看的标注（比如收尾时的裁决）。
func (s *rawSession) note(text string) {
	s.chunk("# " + text)
}
