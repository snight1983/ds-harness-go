// 本文件验运行期上下文投影：什么时候提出一条新快照、什么时候闭嘴、从既有日志
// 恢复状态时认哪一条，以及一次表面替换把留存的那条盖掉之后它怎么说话。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:1-76

package agentloop

import (
	"context"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// newProjection 在一个新建的活会话上装一份投影，返回它和那个会话。
func newProjection(t *testing.T, seed []sessionlog.Event) (*RuntimeContextProjection, *session.Store, *session.Session) {
	t.Helper()
	store := newStore(t)
	owner := rootScope(t)
	live := liveSession(t, store, owner, "s", session.CreateOptions{Seed: seed})
	projection, err := NewRuntimeContextProjection(context.Background(), owner, store, live)
	if err != nil {
		t.Fatalf("造投影失败：%v", err)
	}
	return projection, store, live
}

// TestRuntimeContextSourceIsTheDSHStringVerbatim 钉住那个署名字符串一个字符都没改。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:12
//
// 它进了会话日志，而且是本投影**认领自己那些消息**的唯一判据。改掉它，一份既有
// 日志里的快照全部变成别人的消息：投影以为一条都没投过，于是重投一遍，而旧的那些
// 还留在表面上——模型同时看见两份互相矛盾的运行期上下文。
func TestRuntimeContextSourceIsTheDSHStringVerbatim(t *testing.T) {
	t.Parallel()

	if RuntimeContextSource != "@deepseek-ai/dsh-system-prompt" {
		t.Errorf("署名变了：%q", RuntimeContextSource)
	}
}

// TestProjectionNeedsAStoreAndALiveSession 钉住少了任一件都造不出投影。
//
// 少了存储就订阅不到权威事件，投影从此只认自己提出过什么、不认日志里真的落了
// 什么；少了活会话则连初始状态都恢复不出来。两种情况下投出来的快照都是错的，
// 而错在这里没有任何征兆——所以在入口拒掉。
func TestProjectionNeedsAStoreAndALiveSession(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	owner := rootScope(t)
	live := liveSession(t, store, owner, "s", session.CreateOptions{})

	if _, err := NewRuntimeContextProjection(context.Background(), owner, nil, live); err == nil {
		t.Error("没有存储该造不出来")
	}
	if _, err := NewRuntimeContextProjection(context.Background(), owner, store, nil); err == nil {
		t.Error("没有活会话该造不出来")
	}
}

// TestProjectionSaysNothingWhenThereNeverWasAnySnapshot 钉住从没投过又没有上下文时不说话。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:60-62
//
// 这一条不是省流量：那句作废陈述是说给「见过旧快照的模型」听的。一个从没见过
// 快照的会话收到它，等于凭空被告知有一份并不存在的上下文作废了。
func TestProjectionSaysNothingWhenThereNeverWasAnySnapshot(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, nil)
	if _, ok := projection.Project("", nil); ok {
		t.Error("从没投过、现在也没有，不该提出任何消息")
	}
}

// TestProjectionProposesTheFirstSnapshot 钉住第一份非空上下文投得出来，
// 而且带着那些具名贡献。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:66-74
//
// 那些贡献进的是消息的 snapshot 形态，重放的一方靠它知道这份快照是谁拼出来的。
func TestProjectionProposesTheFirstSnapshot(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, nil)
	sections := []llm.ContextSnapshotSection{{Name: "cwd", Text: "/work"}}
	message, ok := projection.Project("Current runtime context: /work", sections)
	if !ok {
		t.Fatal("第一份上下文该投得出来")
	}
	if got := textOf(t, message); got != "Current runtime context: /work" {
		t.Errorf("正文不对：%q", got)
	}
	plugin, isPlugin := message.Source.(llm.PluginSource)
	if !isPlugin || plugin.Plugin != RuntimeContextSource {
		t.Fatalf("来源该是本投影署的名：%#v", message.Source)
	}
	snapshot, isSnapshot := plugin.Context.(llm.SnapshotContext)
	if !isSnapshot {
		t.Fatalf("该带 snapshot 形态：%#v", plugin.Context)
	}
	if len(snapshot.Sections) != 1 || snapshot.Sections[0].Name != "cwd" {
		t.Errorf("那些具名贡献没带上：%#v", snapshot.Sections)
	}
}

// TestProjectionOmitsTheSnapshotFormWhenThereAreNoSections 钉住没有贡献时不带 snapshot 形态。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:68-72
//
// 那句作废陈述没有任何贡献可归属。带一个空的 sections 上去，等于告诉重放方
// 「这份快照由零个来源拼成」，那是一句关于装配的假话。
func TestProjectionOmitsTheSnapshotFormWhenThereAreNoSections(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, nil)
	message, ok := projection.Project("only text", nil)
	if !ok {
		t.Fatal("该投得出来")
	}
	plugin, isPlugin := message.Source.(llm.PluginSource)
	if !isPlugin {
		t.Fatalf("来源类型不对：%#v", message.Source)
	}
	if plugin.Context != nil {
		t.Errorf("不该带任何上下文形态：%#v", plugin.Context)
	}
}

// TestProjectionStaysSilentWhileTheSnapshotIsUnchanged 钉住同一份上下文只投一次。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:65
//
// 每一步都重投一遍的话，一段长对话的表面会被同一份上下文铺满，而且每一条都是
// 提供方提示词缓存的一次新前缀——既涨钱又挤掉真正的对话。
func TestProjectionStaysSilentWhileTheSnapshotIsUnchanged(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	message, ok := projection.Project("same", nil)
	if !ok {
		t.Fatal("第一次该投得出来")
	}
	appendEvent(t, live, sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      mustData(t, sessionlog.UserMessageData{Message: message}),
		SurfaceOp: sessionlog.AppendOp{},
	})

	if _, again := projection.Project("same", nil); again {
		t.Error("同一份上下文不该再投一次")
	}
	if _, changed := projection.Project("different", nil); !changed {
		t.Error("换了内容该再投一次")
	}
}

// TestProjectionAnnouncesThatEarlierSnapshotsNoLongerApply 钉住上下文清空时发的是一句正面陈述。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:13、63-64
//
// 「不发」在这里是错的：早先那份快照还留在表面上，模型看得见。什么都不说等于
// 让它继续按一份已经作废的上下文行事。
func TestProjectionAnnouncesThatEarlierSnapshotsNoLongerApply(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	message, ok := projection.Project("first", nil)
	if !ok {
		t.Fatal("第一次该投得出来")
	}
	appendEvent(t, live, sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      mustData(t, sessionlog.UserMessageData{Message: message}),
		SurfaceOp: sessionlog.AppendOp{},
	})

	cleared, ok := projection.Project("", nil)
	if !ok {
		t.Fatal("清空上下文该投一句作废陈述")
	}
	text := textOf(t, cleared)
	if !strings.Contains(text, "no longer apply") {
		t.Errorf("那句话得说明旧快照作废了：%q", text)
	}
	if text != runtimeContextCleared {
		t.Errorf("措辞变了：%q", text)
	}
}

// TestProjectionFollowsTheAuthoritativeLogNotItsOwnProposals 钉住状态跟的是**落进日志的**事件，
// 不是它自己提出过什么。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:44-50、59
//
// 「提出」和「提交」是两件事：循环那一步的前置钩子可能根本没把这条候选消息追加
// 进去（比如那一步被取消了）。投影要是把提出当成投过，下一步就会以为表面上已经
// 有这份上下文，于是闭嘴——而它其实一条都没进去。
func TestProjectionFollowsTheAuthoritativeLogNotItsOwnProposals(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, nil)
	if _, ok := projection.Project("ctx", nil); !ok {
		t.Fatal("第一次该投得出来")
	}
	if _, again := projection.Project("ctx", nil); !again {
		t.Error("这条候选没进日志，下一次该照投不误")
	}
}

// TestProjectionIgnoresSnapshotsFromOtherSessions 钉住事件订阅按会话过滤。
//
// 一份存储上挂着许多会话，观察者收的是所有会话的事件。不过滤的话，隔壁会话投出
// 的一份快照会让**这个**会话的投影以为自己已经投过——于是这份对话的模型永远
// 看不到属于它的运行期上下文。
func TestProjectionIgnoresSnapshotsFromOtherSessions(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	owner := rootScope(t)
	mine := liveSession(t, store, owner, "mine", session.CreateOptions{})
	theirs := liveSession(t, store, owner, "theirs", session.CreateOptions{})

	projection, err := NewRuntimeContextProjection(context.Background(), owner, store, mine)
	if err != nil {
		t.Fatalf("造投影失败：%v", err)
	}
	appendEvent(t, theirs, runtimeContextEvent(t, "ctx"))

	if _, ok := projection.Project("ctx", nil); !ok {
		t.Error("隔壁会话的快照不该让这份投影闭嘴")
	}
}

// TestProjectionIgnoresMessagesItDoesNotOwn 钉住只认自己署名的那些消息。
//
// 一条真人发的用户消息碰巧和当前上下文同文，不能让投影以为快照已经在表面上。
func TestProjectionIgnoresMessagesItDoesNotOwn(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	appendEvent(t, live, foreignUserEvent(t, "ctx"))

	if _, ok := projection.Project("ctx", nil); !ok {
		t.Error("别人的消息不该被当成本投影投过的快照")
	}
}

// TestProjectionRestoresTheRetainedSnapshotFromAnExistingLog 钉住续跑起来的会话认得出
// 自己上次投的那条。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:31-42
//
// 恢复不出来的后果是：一个刚续跑的会话在第一步就重投一份和表面上一模一样的快照。
func TestProjectionRestoresTheRetainedSnapshotFromAnExistingLog(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, seedOf(
		foreignUserEvent(t, "hello"),
		runtimeContextEvent(t, "ctx"),
	))

	if _, ok := projection.Project("ctx", nil); ok {
		t.Error("表面上已经有这份快照了，不该重投")
	}
	if _, ok := projection.Project("other", nil); !ok {
		t.Error("换了内容该投得出来")
	}
}

// TestProjectionRestoreStopsAtTheLastSnapshot 钉住从后往前扫、认最后那一条。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:33-41
//
// 认成第一条的话，一个投过好几轮的会话续跑之后会拿一份很旧的快照当现状，
// 于是「没变」的判断整个反过来：真的没变时它重投，真的变了时它闭嘴。
func TestProjectionRestoreStopsAtTheLastSnapshot(t *testing.T) {
	t.Parallel()

	projection, _, _ := newProjection(t, seedOf(
		runtimeContextEvent(t, "old"),
		foreignUserEvent(t, "hello"),
		runtimeContextEvent(t, "new"),
	))

	if _, ok := projection.Project("new", nil); ok {
		t.Error("最后那条快照才是现状，不该重投")
	}
	if _, ok := projection.Project("old", nil); !ok {
		t.Error("旧那条不是现状，同文也该重投")
	}
}

// TestProjectionRestoreRemembersASnapshotThatIsNoLongerOnTheSurface 钉住扫到一条
// **已经被盖掉**的自家消息时，只记下「曾经投过」。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:36-40
//
// 这是那一位 everSeen 单独存在的理由：留存的那条没了，但曾经有过。所以现在
// 清空上下文必须发那句作废陈述——模型在被压缩掉的那段历史里见过快照。
func TestProjectionRestoreRemembersASnapshotThatIsNoLongerOnTheSurface(t *testing.T) {
	t.Parallel()

	shadowed := runtimeContextEvent(t, "ctx")
	replacement := foreignUserEvent(t, "compacted")
	replacement.SurfaceOp = sessionlog.ReplaceOp{Start: 0, End: 0}
	replacement.SourceEventSeqs = []int{0}

	projection, _, _ := newProjection(t, seedOf(shadowed, replacement))

	message, ok := projection.Project("", nil)
	if !ok {
		t.Fatal("曾经投过就该发那句作废陈述")
	}
	if got := textOf(t, message); got != runtimeContextCleared {
		t.Errorf("发的不是作废陈述：%q", got)
	}
	if _, again := projection.Project("ctx", nil); !again {
		t.Error("那条快照已经不在表面上了，同文也该重投")
	}
}

// TestProjectionForgetsARetainedSnapshotThatAReplacementCovers 钉住活着的时候一次表面
// 替换盖掉留存的那条，投影跟着放手。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:51-56
//
// 压缩就是这么发生的：那条快照还在日志里，但模型再也看不到它。仍然把它当成
// 「表面上有」的话，压缩之后模型就永远失去运行期上下文了——投影一直以为没变。
func TestProjectionForgetsARetainedSnapshotThatAReplacementCovers(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	snapshot := appendEvent(t, live, runtimeContextEvent(t, "ctx"))
	if _, ok := projection.Project("ctx", nil); ok {
		t.Fatal("刚投过，不该重投")
	}

	replacement := foreignUserEvent(t, "compacted")
	replacement.SurfaceOp = sessionlog.ReplaceOp{Start: snapshot.Seq, End: snapshot.Seq}
	replacement.SourceEventSeqs = []int{snapshot.Seq}
	appendEvent(t, live, replacement)

	if _, ok := projection.Project("ctx", nil); !ok {
		t.Error("留存的那条被盖掉了，同文也该重投")
	}
}

// TestProjectionKeepsARetainedSnapshotAnUnrelatedReplacementDidNotCover 钉住一次
// 不涉及留存那条的替换，不动投影状态。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:53-56
//
// 只要一见替换就放手的话，任何一次压缩都会让下一步白投一份和表面上一模一样的
// 快照——而那份快照仍然好端端地在表面上。
func TestProjectionKeepsARetainedSnapshotAnUnrelatedReplacementDidNotCover(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	stale := appendEvent(t, live, foreignUserEvent(t, "hello"))
	appendEvent(t, live, runtimeContextEvent(t, "ctx"))
	if _, ok := projection.Project("ctx", nil); ok {
		t.Fatal("刚投过，不该重投")
	}

	replacement := foreignUserEvent(t, "summary")
	replacement.SurfaceOp = sessionlog.ReplaceOp{Start: stale.Seq, End: stale.Seq}
	replacement.SourceEventSeqs = []int{stale.Seq}
	appendEvent(t, live, replacement)

	if _, ok := projection.Project("ctx", nil); ok {
		t.Error("这次替换没碰留存的那条，不该重投")
	}
}

// TestProjectionRefusesToReadTextOutOfAMultiBlockSnapshot 钉住一条不是「恰好一块文本」
// 的自家消息，永远不被当成和某份上下文相同。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:19-22、45-47
//
// 这是 hasText 那一位存在的理由。取不出正文的时候唯一安全的判断是「和现状不同」
// ——投多一条只是浪费，而误判成相同会让一份该刷新的上下文永远停在旧值上。
func TestProjectionRefusesToReadTextOutOfAMultiBlockSnapshot(t *testing.T) {
	t.Parallel()

	projection, _, live := newProjection(t, nil)
	appendEvent(t, live, userMessageEvent(t,
		llm.Content{llm.TextBlock{Text: "a"}, llm.TextBlock{Text: "b"}},
		llm.PluginSource{Plugin: RuntimeContextSource}))

	if _, ok := projection.Project("ab", nil); !ok {
		t.Error("取不出正文的快照不该让投影闭嘴")
	}
}
