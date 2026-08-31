// 本文件的作用：只读地列举一个父的直接孩子、以及一个根底下整棵子 agent 树——
// 直接从活会话表加可选的会话持久化读，不经过任何查询服务。候选来自**一份**
// 活优先的语料；每个孩子的模式／名字一律是那个已登记的 `subagent` 投影单元的值，
// 沿三级梯子取：活孩子走注册表的水位缓存，冷孩子先试一行耐久缓存（要过 seq 闸），
// 再不行就做一次持久化探视、经注册表重折。
//
// 源: packages/subagent/subagent/src/list-children.ts
//
// 投影那次折叠是**唯一**的分类权威——本文件自己一条描述符都不解。没有持久化时
// 列举只看活的：一个冷孩子本来就接不上，它的缺席是能力缺席，不是错误。
// 本文件不持有任何目录状态，也不问活化、agent 注册表、续接管理器或者提供方。

package subagent

import (
	"cmp"
	"context"
	"slices"

	"golang.org/x/sync/errgroup"

	coresession "ds-harness-go/core/session"
	"ds-harness-go/session"
	"ds-harness-go/session/persistence"
	"ds-harness-go/session/projection"
	"ds-harness-go/session/projectioncache"
)

// coldReadConcurrency 是一次列举里并发的冷探视条数。
//
// 源: packages/subagent/subagent/src/list-children.ts:32
//
// 写成常量是因为它约束的是一次只读的本地介质扫描，不是部署行为。
// 真出现一个走网络的持久化后端时，再把它提成一个验过的配置字段。
const coldReadConcurrency = 4

// ListEntryKind 说的是一行列举结果是一个认出来的孩子，还是一条诊断。
type ListEntryKind string

const (
	// EntryChild 是一个身份折得出来的孩子。
	EntryChild ListEntryKind = "child"
	// EntryDiagnostic 是一个立不成孩子的候选，附上为什么。
	EntryDiagnostic ListEntryKind = "diagnostic"
)

// ListActivity 是这一行在**存储快照**这一刻的活跃度。
type ListActivity string

const (
	// ActivityRunning 表示这条逻辑记录活在活会话表里。
	ActivityRunning ListActivity = "running"
	// ActivityInactive 表示它只存在于持久化里。
	ActivityInactive ListActivity = "inactive"
)

// DiagnosticReason 是一个候选为什么没有孩子那一行。
type DiagnosticReason string

const (
	// DiagnosticCorrupt 是「这份日志的折叠端不出身份」——描述符缺失、坏掉、
	// 或者版本不认识（**有意**不加区分），以及任何一个已登记单元被这份日志
	// 折崩了。这是确定性的数据损坏，按孩子逐个兜住。
	DiagnosticCorrupt DiagnosticReason = "corrupt"
	// DiagnosticUnsupported 本包**从不**产出，它留在这张单子里是给按它分流的
	// 消费方用的。
	//
	// 源: packages/subagent/subagent/src/list-children.ts:80-83
	DiagnosticUnsupported DiagnosticReason = "unsupported"
	// DiagnosticUnavailable 是「这个候选的持久化探视失败了」——下一次列举会重试。
	DiagnosticUnavailable DiagnosticReason = "unavailable"
)

// ListEntry 是一次 [ListChildren] 结果里的一行，按会话头的 CreatedAt 排、
// id 破平。
//
// 源: packages/subagent/subagent/src/list-children.ts:44-92
//
// 只有持久头上 Origin 是 [ds-harness-go/session.OriginSubagent] 的候选才会被解读。
// 一个端得出 `subagent` 投影值的候选给出 [EntryChild]；一个已经安定下来、
// 却折不出身份的候选给出 [EntryDiagnostic]；一个**正在跑**又还没有身份的候选
// 整行略过——它的描述符可能还没追加（创建窗口）。诊断转述的是投影折叠的结局
// 或者一次读失败，绝不是逐孩子的事件扫描，也绝不外露不给模型看的描述符内容。
//
// 新增: DSH 是 `{kind:'child', ...} | {kind:'diagnostic', ...}` 两支按 kind 判别
// 的联合。Go 没有判别联合，和 [DescriptorData]、[IdentityProjection] 是同一种
// 做法：**一个**结构体加一个 Kind 字段。哪些字段属于哪一支写在各自的注释里，
// 而这两支的字段本来就不重叠，一个读者只要先看 Kind 就不会读错。
type ListEntry struct {
	// Kind 是这一行属于哪一支。
	Kind ListEntryKind
	// ID 是这个候选的会话 id。两支都有。
	//
	// 对 [EntryChild] 来说它是那个耐久的孩子会话 id，跨活化稳定。
	ID session.SessionID

	// Mode 是这个孩子的生命周期模式。只有 [EntryChild] 有。
	Mode DescriptorMode
	// Label 是描述符上那个耐久的创建名。只有 [EntryChild] 有；
	// [ModeOneShot] 时可以是空串，[ModeContinuable] 时必然非空。
	Label string
	// Activity 是存储快照这一刻的活跃度。只有 [EntryChild] 有。
	//
	// 它**不**编码任何耐久结局：一个 [ActivityRunning] 的可续孩子照样可能
	// 以「所有权冲突」拒掉一次投递。
	Activity ListActivity
	// HasChildren 表示有没有一个直接后代的持久 Origin 是 subagent。
	// 只有 [EntryChild] 有。
	HasChildren bool

	// Reason 是这个候选为什么没有孩子那一行。只有 [EntryDiagnostic] 有。
	Reason DiagnosticReason
}

// DescendantListEntry 是一次 [ListDescendants] 结果里的一行：解读出来的那些
// 子 agent 事实，加上它在整棵会话树里的位置。
//
// 源: packages/subagent/subagent/src/list-children.ts:94-99
type DescendantListEntry struct {
	ListEntry
	// ParentID 是列举出来的那份头里记的、这个候选耐久的直接父。
	ParentID session.SessionID
	// Depth 是离请求的那个根有几条边；直接孩子是 1。
	Depth int
}

// ListingServices 是一次只读列举要用到的那几样服务。
//
// 新增: DSH 从 cordis 上 `ctx.get(...)` 逐个取这四样。Go 没有那个容器，
// 「在不在场」就是装配方手上有没有这个值，所以做成一个显式的结构体。
// 前两样为 nil 时报的仍旧是 DSH 那两个码——那是一个确定性的部署配置错误，
// 绝不是一次空的成功，所以**在任何一次读之前**就检查，哪怕候选数为零。
type ListingServices struct {
	// Projections 是投影单元表，身份分类唯一的权威。必填。
	Projections *projection.Registry
	// Sessions 是活会话表，语料里活的那一半从它来。必填。
	Sessions *coresession.Store
	// Persistence 是会话持久化；nil 表示这次列举只看活的。
	Persistence persistence.Store
	// Cache 是耐久投影缓存；nil 只意味着每个冷候选都走权威那一级，
	// 所以它不占任何码、也不做任何配置检查。
	Cache *projectioncache.Cache
}

// corpusRecord 是语料里的一条：一份头，外加它活着的那个会话（没有就是 nil）。
//
// 源: packages/subagent/subagent/src/list-children.ts:101
type corpusRecord struct {
	header session.SessionHeader
	live   *coresession.Session
}

// listing 是一次列举解算好的服务、语料和「谁是子 agent 的父」那张表。
//
// 源: packages/subagent/subagent/src/list-children.ts:103-109
type listing struct {
	services        ListingServices
	corpus          map[session.SessionID]corpusRecord
	subagentParents map[session.SessionID]struct{}
}

// positionedCandidate 是一个候选加上它在树里的位置。
//
// 源: packages/subagent/subagent/src/list-children.ts:111-115
type positionedCandidate struct {
	record   corpusRecord
	parentID session.SessionID
	depth    int
}

// coldRead 是一件排给冷读的活：要读的那份头，以及它在结果切片里的下标。
//
// 源: packages/subagent/subagent/src/list-children.ts:246
type coldRead struct {
	index  int
	header session.SessionHeader
}

// ListChildren 列举一个父那些按 Origin 分类出来的直接孩子。
//
// 源: packages/subagent/subagent/src/list-children.ts:134-148
//
// 语料是活会话表和可选持久化那份活优先的合并；每个身份都由 `subagent` 投影单元
// 端出来：活孩子走注册表的水位读切；冷孩子先试一行耐久缓存（要过 seq 闸），
// 不行就做一次有并发上限的持久化探视、再经注册表折一遍。
//
// 投影注册表或者活会话表没装，以及调用方取消，都报 [ds-harness-go/llm.Error]。
func ListChildren(
	ctx context.Context,
	services ListingServices,
	parentSessionID session.SessionID,
) ([]ListEntry, error) {
	prepared, err := prepareListing(ctx, services)
	if err != nil {
		return nil, err
	}
	var candidates []corpusRecord
	for _, record := range prepared.corpus {
		if record.header.ParentSession == parentSessionID &&
			record.header.Origin == session.OriginSubagent {
			candidates = append(candidates, record)
		}
	}
	slices.SortFunc(candidates, compareCorpusRecords)
	rows, err := prepared.resolveCandidateRows(ctx, candidates)
	if err != nil {
		return nil, err
	}
	entries := make([]ListEntry, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			entries = append(entries, *row)
		}
	}
	return entries, nil
}

// ListDescendants 按稳定的先序列举一个根底下每一个有会话的子 agent。
//
// 源: packages/subagent/subagent/src/list-children.ts:161-181
//
// 普通会话和一次性孩子照样是遍历节点，所以挂在它们底下的可续孩子仍然找得到。
// 分类走的是和 [ListChildren] 同一套投影支撑的运行期；一个 agent 都不装载、
// 也不恢复。失败条件和 [ListChildren] 一样。
func ListDescendants(
	ctx context.Context,
	services ListingServices,
	rootSessionID session.SessionID,
) ([]DescendantListEntry, error) {
	prepared, err := prepareListing(ctx, services)
	if err != nil {
		return nil, err
	}
	positioned := descendantCandidates(prepared.corpus, rootSessionID)
	candidates := make([]corpusRecord, len(positioned))
	for index, candidate := range positioned {
		candidates[index] = candidate.record
	}
	rows, err := prepared.resolveCandidateRows(ctx, candidates)
	if err != nil {
		return nil, err
	}
	var entries []DescendantListEntry
	for index, position := range positioned {
		if rows[index] == nil {
			continue
		}
		entries = append(entries, DescendantListEntry{
			ListEntry: *rows[index],
			ParentID:  position.parentID,
			Depth:     position.depth,
		})
	}
	return entries, nil
}

// prepareListing 一次性解算好那几样服务，并建出一份活优先的会话语料。
//
// 源: packages/subagent/subagent/src/list-children.ts:184-241
func prepareListing(ctx context.Context, services ListingServices) (*listing, error) {
	// 在任何一次读之前就查，哪怕候选数为零：模式／名字是这一行强契约里的东西，
	// 折叠能力缺席是确定性的部署配置错误，不是一次空的成功。
	if services.Projections == nil {
		return nil, NewError("列举子 agent 需要投影注册表", CodeControlProjectionsUnavailable, nil)
	}
	if services.Sessions == nil {
		return nil, NewError("列举子 agent 需要活会话表", CodeControlSessionStoreUnavailable, nil)
	}
	if err := listingCancelled(ctx); err != nil {
		return nil, err
	}
	corpus := map[session.SessionID]corpusRecord{}
	if services.Persistence != nil {
		headers, err := services.Persistence.List(ctx)
		if err != nil {
			// 后端见到取消之后可以用它自己那种失败回绝；取消这件事在本包必须是
			// 一个稳定的结局，所以先认取消。
			if cancelled := listingCancelled(ctx); cancelled != nil {
				return nil, cancelled
			}
			return nil, err
		}
		if err := listingCancelled(ctx); err != nil {
			return nil, err
		}
		for _, header := range headers {
			corpus[header.ID] = corpusRecord{header: header}
		}
	}
	// 活优先的合并，不做任何头的调和：一条活记录整条拿下它那个 id，
	// 就跟一份活优先的语料本来会端出来的一样。
	for _, live := range services.Sessions.List() {
		corpus[live.ID()] = corpusRecord{header: live.Header(), live: live}
	}
	parents := map[session.SessionID]struct{}{}
	for _, record := range corpus {
		if record.header.Origin == session.OriginSubagent && record.header.ParentSession != "" {
			parents[record.header.ParentSession] = struct{}{}
		}
	}
	return &listing{services: services, corpus: corpus, subagentParents: parents}, nil
}

// resolveCandidateRows 为对齐好的那些候选解算出投影支撑的行，冷读有并发上限。
// 交回的切片和 candidates 一一对应，nil 表示这一行**有意**略过（创建窗口）。
//
// 源: packages/subagent/subagent/src/list-children.ts:243-296
func (l *listing) resolveCandidateRows(
	ctx context.Context,
	candidates []corpusRecord,
) ([]*ListEntry, error) {
	rows := make([]*ListEntry, len(candidates))
	var coldReads []coldRead
	for index, candidate := range candidates {
		childID := candidate.header.ID
		if candidate.live == nil {
			coldReads = append(coldReads, coldRead{index: index, header: candidate.header})
			continue
		}
		// 注册表的水位缓存零日志读地端出活孩子那个值；一个还没有身份的活孩子
		// 正处在「立起它的那个提供方还没追加描述符」那个创建窗口里。
		//
		// 新增: DSH 这里包了一层 try/catch，因为它的折叠和 schema 会抛，
		// 于是一个坏掉的孩子在那边降级成一条 corrupt 诊断。Go 这边
		// [projection.Definition.Apply] 的签名是 (S, Event) (S, bool)，
		// **没有**失败通道，[projection.Registry.Snapshot] 也不返回错误，
		// 所以这条降级路径在 Go 里没有对应物：一份坏负载在单元自己那层
		// （本包是 [applyIdentity]）就已经折成「没有身份」了。
		identity, served := servedIdentity(l.services.Projections.Snapshot(liveView{live: candidate.live}))
		if !served {
			continue
		}
		row := childRow(childID, identity, ActivityRunning, l.hasChildren(childID))
		rows[index] = &row
	}

	// 冷候选只可能是持久化列出来的，所以这次窄检查是为了类型，不是为了可达性。
	if l.services.Persistence != nil && len(coldReads) > 0 {
		// 新增: DSH 手工起 min(4, n) 个 worker 去 shift 同一个队列。Go 里
		// [golang.org/x/sync/errgroup.Group.SetLimit] 就是这件事，而且顺带
		// 把「第一个错误取消其余」也办了——取消是这里唯一会往外冒的失败。
		// 各 goroutine 写的是 rows 里互不相同的下标，那是并发安全的。
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(min(coldReadConcurrency, len(coldReads)))
		for _, job := range coldReads {
			group.Go(func() error {
				row, err := l.resolveColdIdentity(groupCtx, job.header)
				if err != nil {
					return err
				}
				rows[job.index] = &row
				return nil
			})
		}
		if err := group.Wait(); err != nil {
			return nil, err
		}
	}
	if err := listingCancelled(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// descendantCandidates 不用递归地从整棵树里建出按 Origin 分类的候选。
//
// 源: packages/subagent/subagent/src/list-children.ts:298-332
func descendantCandidates(
	corpus map[session.SessionID]corpusRecord,
	rootSessionID session.SessionID,
) []positionedCandidate {
	children := map[session.SessionID][]corpusRecord{}
	for _, record := range corpus {
		parentID := record.header.ParentSession
		if parentID == "" {
			continue
		}
		children[parentID] = append(children[parentID], record)
	}
	// Go 的 map 遍历次序是随机的，所以这一步排序在本仓库比在 DSH 更承重：
	// 没有它，同一份语料每次给出的先序都不一样。
	for _, siblings := range children {
		slices.SortFunc(siblings, compareCorpusRecords)
	}

	var positioned, stack []positionedCandidate
	// pushLevel 把一层兄弟倒着压栈，于是弹出来的次序就是它们排好的次序。
	pushLevel := func(parentID session.SessionID, depth int) {
		siblings := children[parentID]
		for index := len(siblings) - 1; index >= 0; index-- {
			stack = append(stack, positionedCandidate{
				record:   siblings[index],
				parentID: parentID,
				depth:    depth,
			})
		}
	}
	visited := map[session.SessionID]struct{}{rootSessionID: {}}
	pushLevel(rootSessionID, 1)
	for len(stack) > 0 {
		position := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		id := position.record.header.ID
		if _, seen := visited[id]; seen {
			continue
		}
		visited[id] = struct{}{}
		if position.record.header.Origin == session.OriginSubagent {
			positioned = append(positioned, position)
		}
		pushLevel(id, position.depth+1)
	}
	return positioned
}

// compareCorpusRecords 按耐久的建会话时间排兄弟，再用 id 破平。
//
// 源: packages/subagent/subagent/src/list-children.ts:334-336
//
// 新增: DSH 破平用的是 localeCompare，Go 这里是字节序。破平只为让次序稳定，
// 两种做法都稳定，而字节序不依赖任何区域设置。
func compareCorpusRecords(a, b corpusRecord) int {
	return cmp.Or(
		cmp.Compare(a.header.CreatedAt, b.header.CreatedAt),
		cmp.Compare(a.header.ID, b.header.ID),
	)
}

// resolveColdIdentity 把一个冷候选沿梯子剩下那两级解出来：先试一行耐久缓存
// （要它端出来的身份过得了 seq 闸），不行就做一次持久化探视、经投影注册表重折。
//
// 源: packages/subagent/subagent/src/list-children.ts:348-410
//
// 一次失败的探视是一条**暂时的** unavailable 行，下次列举会重试；一次点着
// 另一段生命的探视、以及一份折不出身份的已安定日志，都是终局，所以报 corrupt。
//
// 交回的错误只可能是取消——按孩子逐个兜住是这条路的整个要点。
func (l *listing) resolveColdIdentity(
	ctx context.Context,
	header session.SessionHeader,
) (ListEntry, error) {
	childID := header.ID
	if l.services.Cache != nil {
		// 和下面那次权威重折不同，一次读崩了的缓存**不下任何判决**：缓存是派生
		// 数据，它自己坏了（任何一个单元那行存坏了）就悄悄落到重折那一级去。
		snapshot, err := l.services.Cache.CachedSnapshot(header)
		if err == nil && snapshot != nil {
			cached, served := servedIdentity(*snapshot)
			// 一个孩子**自己**那条描述符一旦追加就不可改，所以一行缓存里的身份
			// 只有在 seq 闸证明它折自那段自有后缀时才是终局：一份创建窗口里的
			// 检查点可能带的是分叉种子里被回放的**祖先**描述符（seq 低于
			// SeedLength），那种东西不许压过重折。其余情况一律落到重折：
			// 没有那个键（切在任何描述符之前），以及「没有身份」这个判决本身
			// ——它属于权威的重折，不属于一行派生数据。
			if served && cached.Seq >= header.SeedLength {
				return childRow(childID, cached, ActivityInactive, l.hasChildren(childID)), nil
			}
		}
	}
	if err := listingCancelled(ctx); err != nil {
		return ListEntry{}, err
	}
	inspected, err := l.services.Persistence.Inspect(ctx, childID)
	if err != nil {
		// 逐孩子隔离：这个孩子没了，或者它那次后端读失败了——一条诊断行，
		// 而这次列举本身照样成功。
		if cancelled := listingCancelled(ctx); cancelled != nil {
			return ListEntry{}, cancelled
		}
		return diagnosticRow(childID, DiagnosticUnavailable), nil
	}
	if err := listingCancelled(ctx); err != nil {
		return ListEntry{}, err
	}
	// 一个会话 id 点的是一个**槽位**，不是一段生命：一个在列举和这次读之间被
	// 删掉、又被另一个主人重新发布的孩子，绝不许漏进老父亲那份清单。
	if !sameLifecycle(inspected.Meta, header) {
		return diagnosticRow(childID, DiagnosticCorrupt), nil
	}
	// 和 [projectioncache.Cache.ColdSnapshot] 走的是同一份重折配方，只是这里
	// 有意不写回缓存：列举是只读的，一次列举不该改动任何耐久状态。
	restored, err := l.services.Projections.Restore(nil, inspected.Events, 0)
	if err != nil {
		return diagnosticRow(childID, DiagnosticCorrupt), nil
	}
	identity, served := servedIdentity(restored.Snapshot)
	if !served {
		return diagnosticRow(childID, DiagnosticCorrupt), nil
	}
	return childRow(childID, identity, ActivityInactive, l.hasChildren(childID)), nil
}

// hasChildren 表示有没有一个直接后代的持久 Origin 是 subagent。
func (l *listing) hasChildren(id session.SessionID) bool {
	_, found := l.subagentParents[id]
	return found
}

// servedIdentity 从一份读切里取出身份那个单元端出来的值。
//
// 源: packages/subagent/subagent/src/list-children.ts:284-286, 388-390
//
// 新增: DSH 要分「键被丢掉了（undefined）」和「单元自己的可序列化哨兵（null）」
// 两种缺席，因为它那个值跨得过 JSON 边界。Go 这边三级梯子端出来的都是
// [projection.Definition.View] 的返回值本身，[viewIdentity] 没有身份时返回
// 无类型 nil，所以一次类型断言就把两种缺席一起认了。
func servedIdentity(snapshot projection.Snapshot) (IdentityProjection, bool) {
	identity, served := snapshot.Values[IdentityProjectionKey].(IdentityProjection)
	return identity, served
}

// childRow 把一份端出来的身份落成它那一行孩子。
//
// 源: packages/subagent/subagent/src/list-children.ts:412-436
func childRow(
	id session.SessionID,
	identity IdentityProjection,
	activity ListActivity,
	hasChildren bool,
) ListEntry {
	return ListEntry{
		Kind:        EntryChild,
		ID:          id,
		Mode:        identity.Mode,
		Label:       identity.Label,
		Activity:    activity,
		HasChildren: hasChildren,
	}
}

// diagnosticRow 落一条诊断行。
func diagnosticRow(id session.SessionID, reason DiagnosticReason) ListEntry {
	return ListEntry{Kind: EntryDiagnostic, ID: id, Reason: reason}
}

// sameLifecycle 判一份探视回来的日志还属不属于当初列举出来的那段生命。
//
// 源: packages/subagent/subagent/src/list-children.ts:438-446
//
// 逐字段比而不是 `meta == expected`：Origin 和 AgentPreset **有意**不算见证。
// 它们是同一段生命里可以另有说法的展示性元数据，拿它们当见证会把一次无害的
// 差异误报成 corrupt。
func sameLifecycle(meta, expected session.SessionHeader) bool {
	return meta.Version == expected.Version &&
		meta.ID == expected.ID &&
		meta.CreatedAt == expected.CreatedAt &&
		meta.Cwd == expected.Cwd &&
		meta.ParentSession == expected.ParentSession &&
		meta.SeedLength == expected.SeedLength &&
		meta.DelegationDepth == expected.DelegationDepth
}

// listingCancelled 在下一个取消检查点把一次列举停掉；没取消时返回 nil。
//
// 源: packages/subagent/subagent/src/list-children.ts:448-452
func listingCancelled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return NewError("子 agent 列举被取消了", CodeCancelled, err)
	}
	return nil
}

// liveView 把一个活会话贴成投影注册表读得懂的视图。
//
// 新增: 活会话交出来的是 Seq（下一条事件的序号，恒等于日志长度），
// 而 [projection.SessionView] 要的名字是 NextSeq——同一个数，两个名字。
// DSH 那边没有这道缝，它的 Session 直接就是投影要的形状。贴在用得着的这一侧，
// 而不是往活会话上加一个只为这道面存在的别名方法。
type liveView struct{ live *coresession.Session }

// ID 是这个会话的标识。
func (v liveView) ID() session.SessionID { return v.live.ID() }

// Events 是这份日志当下的快照。
func (v liveView) Events() []session.Event { return v.live.Events() }

// NextSeq 是下一条事件将要用的 seq。
func (v liveView) NextSeq() int { return v.live.Seq() }
