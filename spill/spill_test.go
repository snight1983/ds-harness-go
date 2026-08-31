// 本文件的作用：这条接缝对实现方要求的是什么形状——一个方法收下全文、
// 换回句柄、字节数和取回说明，请求里的字段一个不落地送到实现方手上。
//
// 源: packages/spill/spill/tests/service.spec.ts
//
// DSH 那份测试三条里有两条（重复注册要报错、销毁后服务槽要空掉）测的是 cordis
// 容器自己的行为，本仓库没有那个容器，也就没有对应的断言，裁决记在 portmap.tsv。

package spill

import (
	"context"
	"errors"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// stubStore 是一个把请求原样记下来的最小实现，用来观察这条接缝的过手。
type stubStore struct {
	last SaveText
	err  error
}

func (s *stubStore) SaveText(_ context.Context, input SaveText) (Ref, error) {
	s.last = input
	if s.err != nil {
		return Ref{}, s.err
	}
	return Ref{
		Locator:       Locator("/stub/" + input.SuggestedName),
		Bytes:         len(input.Content),
		RetrievalHint: "Use the stub reader.",
	}, nil
}

// request 排一次典型的外置请求。
func request(content string) SaveText {
	return SaveText{
		Owner:         Owner{SessionID: session.SessionID("s1")},
		Source:        Source{ToolName: "web_fetch", CallID: llm.CallID("c1"), Label: "result"},
		SuggestedName: "web_fetch.txt",
		Content:       content,
	}
}

func TestStoreHandsTheWholeRequestToTheBackendAndReturnsARef(t *testing.T) {
	t.Parallel()

	stub := &stubStore{}
	var store Store = stub

	ref, err := store.SaveText(context.Background(), request("hello"))
	if err != nil {
		t.Fatalf("存不了：%v", err)
	}
	if ref.Locator != "/stub/web_fetch.txt" || ref.Bytes != 5 || ref.RetrievalHint != "Use the stub reader." {
		t.Fatalf("交回来的引用不对：%+v", ref)
	}

	// 请求里的每一格都得原样到实现方手上：后端要靠 Source 拼出一个能读懂的名字，
	// 靠 Owner 决定存到哪个会话名下，少一格它就只能瞎猜。
	if stub.last.Content != "hello" || stub.last.SuggestedName != "web_fetch.txt" {
		t.Fatalf("正文或建议名没过手：%+v", stub.last)
	}
	if stub.last.Owner.SessionID != "s1" {
		t.Fatalf("归属会话没过手：%+v", stub.last.Owner)
	}
	if stub.last.Source != (Source{ToolName: "web_fetch", CallID: "c1", Label: "result"}) {
		t.Fatalf("产出它的工具与调用没过手：%+v", stub.last.Source)
	}
}

func TestStoreReportsAStorageFailureInsteadOfDegradingItself(t *testing.T) {
	t.Parallel()

	// 后端不许自己造一个假句柄糊弄过去：模型会照着句柄去取一份不存在的东西。
	// 怎么退让是调用方的事——外置策略收到这条错误之后原样保留内联结果。
	boom := errors.New("盘满了")
	var store Store = &stubStore{err: boom}

	ref, err := store.SaveText(context.Background(), request("hello"))
	if !errors.Is(err, boom) {
		t.Fatalf("存储故障没报上来：%v", err)
	}
	if ref != (Ref{}) {
		t.Fatalf("存不成的时候不该交回一个像模像样的引用：%+v", ref)
	}
}
