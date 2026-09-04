// 本文件的作用：那条工作区边界——谁在调、他准看哪些会话、看不见的那部分血统
// 在结果里长什么样。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts
//
// 这里的每一条判断都只有一个目的：模型只准看见和调用方**同一个工作目录**的会话。
// 越界一律报同一句话（[unauthorizedTarget]），既不说那个会话存不存在，也不说
// 它属于谁——那两件事本身就是情报。

package querytool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// caller 是这次调用的发起方，从它的活会话上摘下来的三样事实。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:22-26
//
// 摘成一份快照而不是握着那个活会话：授权判断必须对着**同一份**观察做完，
// 中途会话又长出几条事件不该让一半判断用旧的、一半用新的。
type caller struct {
	id     sessionlog.SessionID
	header sessionlog.SessionHeader
	events []sessionlog.Event
}

// callerOf 从这次工具执行里找出发起方。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:52-66
func (c *Controller) callerOf(exec *tools.RunContext) (caller, error) {
	if exec == nil || exec.Agent == nil {
		return caller{}, fail(CodeMissingAgent, "session query tools require an agent-bound caller")
	}
	// 新增: DSH 的 exec.agent 就是 agent 对象。Go 这边它是一把不透明的作用域钥匙，
	// 所以走 Config.AgentOf。认不出这把钥匙和「压根没有调用方」是同一件事：
	// 两种情形下都没有一个工作区可以拿来画边界。
	target, err := c.agentOf(exec.Agent)
	if err != nil || target == nil {
		return caller{}, fail(CodeMissingAgent, "session query tools require an agent-bound caller")
	}
	sess := target.Session()
	return caller{id: sess.ID(), header: sess.Header(), events: sess.Events()}, nil
}

// targetID 挑这次调用指的是哪个会话；没给就是调用方自己。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:68-70
func targetID(sessionID string, from caller) sessionlog.SessionID {
	if sessionID == "" {
		return from.id
	}
	return sessionlog.SessionID(sessionID)
}

// authorizeTarget 判这个目标准不准被这个调用方读。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:72-88
//
// 自己读自己一律放行，连问都不问：调用方对自己那份日志本来就有全部权限，而且
// 那一问会在一个还没落地的新会话上失败。
//
// 别人的会话要引擎点头：按 id 加工作区两条一起筛，回来的记录**恰好一条**才算数。
// 零条是「不在这个工作区、或者压根没有」，多条只可能是语料坏了；两种都拒。
func (c *Controller) authorizeTarget(ctx context.Context, from caller, target sessionlog.SessionID) error {
	if target == from.id {
		return nil
	}
	if from.header.WorkspaceID == "" {
		return unauthorizedTarget()
	}
	records, err := call(ctx, c, "target authorization", func() ([]sessionquery.Record, error) {
		return c.service.FilterSessions(ctx, []sessionquery.SessionFilter{
			sessionquery.IDFilter{Values: []sessionlog.SessionID{target}},
			sessionquery.WorkspaceFilter{Values: []sessionlog.WorkspaceID{from.header.WorkspaceID}},
		})
	})
	if err != nil {
		return err
	}
	if len(records) != 1 {
		return unauthorizedTarget()
	}
	return nil
}

// recordAuthorized 判一条记录准不准给这个调用方看。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:246-260（workspaceAccess）
func recordAuthorized(record sessionquery.Record, from caller) bool {
	return headerAuthorized(record.Header, from)
}

// headerAuthorized 判一份会话头准不准给这个调用方看。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:94-97
//
// 两支分开写不是啰嗦：调用方自己那个会话即使**不属于**任何工作区也看得见自己
// （两边都是空串，相等），而一个不属于任何工作区的调用方看不见任何别人——否则
// 一个空工作区标识会和所有别的空标识相等，等于把边界整个拆掉。
func headerAuthorized(header sessionlog.SessionHeader, from caller) bool {
	if header.ID == from.id {
		return header.WorkspaceID == from.header.WorkspaceID
	}
	return from.header.WorkspaceID != "" && header.WorkspaceID == from.header.WorkspaceID
}

// assertObservedTargetAuthorized 再查一遍引擎交回来的那份头。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:99-108
//
// 授权是在调用**之前**做的，交回来的那份观察必须自证它就是当初批准的那一个：
// id 对不上说明引擎答非所问，工作区标识变了说明这个会话在这中间被挪出了工作区。
// 两种都当越界办。
func assertObservedTargetAuthorized(from caller, target sessionlog.SessionID, observed sessionlog.SessionHeader) error {
	if observed.ID != target || !headerAuthorized(observed, from) {
		return unauthorizedTarget()
	}
	return nil
}

// authorizeSessionIDs 一次筛出这批 id 里哪些准给这个调用方看。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:110-134
func (c *Controller) authorizeSessionIDs(
	ctx context.Context,
	from caller,
	ids []sessionlog.SessionID,
) (map[sessionlog.SessionID]struct{}, error) {
	authorized := map[sessionlog.SessionID]struct{}{}
	var other []sessionlog.SessionID
	seen := map[sessionlog.SessionID]struct{}{}
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if id == from.id {
			// 自己一律放行，不占引擎那一趟。
			authorized[from.id] = struct{}{}
			continue
		}
		other = append(other, id)
	}
	if from.header.WorkspaceID == "" || len(other) == 0 {
		return authorized, nil
	}
	records, err := call(ctx, c, "session-id authorization", func() ([]sessionquery.Record, error) {
		return c.service.FilterSessions(ctx, []sessionquery.SessionFilter{
			sessionquery.IDFilter{Values: other},
			sessionquery.WorkspaceFilter{Values: []sessionlog.WorkspaceID{from.header.WorkspaceID}},
		})
	})
	if err != nil {
		return nil, err
	}
	requested := make(map[sessionlog.SessionID]struct{}, len(other))
	for _, id := range other {
		requested[id] = struct{}{}
	}
	for _, record := range records {
		if _, ok := requested[record.Header.ID]; !ok {
			// 没问过的 id 出现在结果里说明引擎答非所问；不认它。
			continue
		}
		if recordAuthorized(record, from) {
			authorized[record.Header.ID] = struct{}{}
		}
	}
	return authorized, nil
}

// titleView 是一个会话在结果里显示成什么名字。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:28-31
type titleView struct {
	// text 是那个名字；读不到时是 "untitled"。
	text string
	// unavailableCode 是读不到的原因；空串表示读到了。
	unavailableCode Code
}

// untitledText 是没有标题、或者标题读不到时那个占位名。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:148
const untitledText = "untitled"

// readTitles 一次读出这批会话此刻的显示名。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:136-152
//
// 交出来的表对每一个请求过的 id 都有一项：一行列表宁可显示 "untitled"，也不能
// 因为标题读不到就整行消失——那会让模型以为那个会话不存在。
func (c *Controller) readTitles(
	ctx context.Context,
	from caller,
	ids []sessionlog.SessionID,
) (map[sessionlog.SessionID]titleView, error) {
	observations, err := call(ctx, c, "title observation",
		func() ([]sessionquery.ProjectionResult[sessionquery.TitleObservation], error) {
			return c.service.ReadTitleSnapshots(ctx, ids)
		})
	if err != nil {
		return nil, err
	}
	result := make(map[sessionlog.SessionID]titleView, len(observations))
	for _, observation := range observations {
		if observation.Err != nil {
			view, err := c.unavailableTitle(observation.Err)
			if err != nil {
				return nil, err
			}
			result[observation.SessionID] = view
			continue
		}
		// 标题也要过边界：一次批量观察同样可能捞回一个不该看见的会话。
		if err := assertObservedTargetAuthorized(from, observation.SessionID, observation.Value.Session); err != nil {
			return nil, err
		}
		text := untitledText
		if observation.Value.Titled {
			text = observation.Value.Title.Title
		}
		result[observation.SessionID] = titleView{text: text}
	}
	return result, nil
}

// readTitle 读一个会话此刻的显示名。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:154-161
func (c *Controller) readTitle(ctx context.Context, from caller, id sessionlog.SessionID) (titleView, error) {
	titles, err := c.readTitles(ctx, from, []sessionlog.SessionID{id})
	if err != nil {
		return titleView{}, err
	}
	return titles[id], nil
}

// unavailableTitle 把一条被隔离的标题失败翻成一个占位名。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:163-170
//
// 一条例外：清洗之后仍然是「越界」的，**抛出去**而不是画成占位名。别的失败
// （日志坏了、后端挂了）只是这一行读不出名字，越界却说明这次调用整个不该发生，
// 把它降级成一行 "untitled" 等于确认了那个会话存在。
func (c *Controller) unavailableTitle(err error) (titleView, error) {
	sanitized := c.sanitize("title observation item", err)
	code := codeOf(sanitized)
	if code == CodeUnauthorized {
		return titleView{}, sanitized
	}
	return titleView{text: untitledText, unavailableCode: code}, nil
}

// titleText 把一个显示名排成结果里那一行。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:232-236
func titleText(view titleView) string {
	if view.unavailableCode == "" {
		return view.text
	}
	return fmt.Sprintf("%s (title unavailable: %s)", view.text, view.unavailableCode)
}

// authorizedDescendant 是血统树上一个准给调用方看的节点。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:33-36
type authorizedDescendant struct {
	// record 是这个会话。
	record sessionquery.Record
	// descendants 是它的孩子；其中的 nil 表示那一支越界了。
	descendants []*authorizedDescendant
}

// authorizeDescendants 把一棵血统子树按工作区边界裁一遍。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:172-201
//
// 越界的那一支换成 nil 而不是**删掉**：删掉会让树看起来是完整的，模型据此以为
// 这个会话没有更多孩子。留一个占位，界面上画成「[outside workspace subtree]」，
// 说的是「这里有东西，你看不到」。
//
// 越界的节点整支剪掉，不再往下走：它的孩子即使碰巧在同一个工作目录下也不该露出来——那会
// 把那个不可见的父节点的存在反推出来。
//
// 新增: DSH 用一条显式的待办链把递归摊平（那边的血统可以很深，怕爆栈）。Go 的
// goroutine 栈会自己长，所以这里就是递归，读起来直接得多。
func authorizeDescendants(nodes []sessionquery.LineageNode, from caller) []*authorizedDescendant {
	result := make([]*authorizedDescendant, 0, len(nodes))
	for _, node := range nodes {
		if !recordAuthorized(node.Session, from) {
			result = append(result, nil)
			continue
		}
		result = append(result, &authorizedDescendant{
			record:      node.Session,
			descendants: authorizeDescendants(node.Descendants, from),
		})
	}
	return result
}

// descendantVisit 是遍历裁过的子树时看见的一个节点。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:38-42
type descendantVisit struct {
	// node 是那个节点；nil 表示这一支越界了。
	node *authorizedDescendant
	// depth 是它在子树里的层数，根那一层是 0。
	depth int
}

// visitDescendants 按前序把裁过的子树摊成一串。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:203-220
//
// 前序、且**包含** nil 占位：呈现那一层要靠 depth 缩进，靠占位画那句
// 「[outside workspace subtree]」。
func visitDescendants(nodes []*authorizedDescendant) []descendantVisit {
	var visits []descendantVisit
	var walk func(nodes []*authorizedDescendant, depth int)
	walk = func(nodes []*authorizedDescendant, depth int) {
		for _, node := range nodes {
			visits = append(visits, descendantVisit{node: node, depth: depth})
			if node == nil {
				continue
			}
			walk(node.descendants, depth+1)
		}
	}
	walk(nodes, 0)
	return visits
}

// descendantIDs 取裁过的子树里所有看得见的会话 id。
//
// 源: packages/session-query/tool-session-query/src/workspace-access.ts:222-230
func descendantIDs(nodes []*authorizedDescendant) []sessionlog.SessionID {
	var ids []sessionlog.SessionID
	for _, visit := range visitDescendants(nodes) {
		if visit.node == nil {
			continue
		}
		ids = append(ids, visit.node.record.Header.ID)
	}
	return ids
}
