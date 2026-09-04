// 本文件的作用：把溯源注释拿去和上游源码**对内容**核对，而不只是核对行号有没有越界。
//
// 新增: DSH 没有对应物——它是这次移植自己需要的账本工具。
//
// 为什么要有它：门禁那一项（[checkProvenance]）只验「文件在、行号在界内」。
// 上游从旧快照换到 v0.1.2-alpha.3 之后行号普遍漂移，漂出文件末尾的那 55 条被抓到了，
// **漂了却仍然落在文件内**的一条都抓不到。人工抽查四个包，每个包都有系统性偏移
// （user-approval 偏 -35 行，agent-instructions 偏 -2，persona 偏 -5）。
// 也就是说「门禁通过」当时证明不了移植完整性。
//
// 做法：给每条溯源注释找一个**锚点符号**，然后去上游文件里按名字重新定位那个符号，
// 算出它真实的行跨度，再和注释里写的比。锚点从三处来，按可信度排：
// 注释自带的 `（名字）`、这条注释所文档化的 Go 声明经裁决表反查出的上游名、
// 以及退化的「拿 Go 声明名本身做大小写不敏感匹配」（Go 的 PascalCase 对上游的 camelCase）。
//
// 刻意不引入 TS 解析器：逐行匹配声明形态 + 括号配平就够用，而多一个解析器依赖，
// 这份工具就从「随手能跑」变成「要先装东西」，那种工具最后都不会被跑。
// 代价是有一批判不出锚点的（[statusNoAnchor]），它们被明确记成判不出，而不是假装通过。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go/ast"
	"go/parser"
	"go/token"

	"github.com/snight1983/ds-harness-go/internal/devtools/rulingtable"
)

// reanchorStatus 是一条溯源注释和上游对质之后的结论。
type reanchorStatus string

const (
	// statusOK 引的范围和算出来的一模一样。
	statusOK reanchorStatus = "OK"
	// statusContains 引的范围不等于算出来的，但**完全包含**它。
	//
	// 不算错：文件头部那种「本文件整体译自某一段」的大范围溯源本来就该包住单个符号。
	statusContains reanchorStatus = "CONTAINS"
	// statusDrift 引的范围既不等于也不包含算出来的——这是真漂移。
	statusDrift reanchorStatus = "DRIFT"
	// statusNotFound 锚点符号在引的那个上游文件里找不到，可能已删除或搬走了。
	statusNotFound reanchorStatus = "NOT_FOUND"
	// statusMoved 锚点在引的那个文件里找不到，但在裁决表记的那个文件里找着了——上游搬了文件。
	//
	// 和 [statusNotFound] 分开，是因为这一类的证据强度完全不同：裁决表是从当前上游快照
	// 机器生成的，它记的那个文件对这个符号名就是权威。所以这里能连路径一起改，
	// 而 NOT_FOUND 只能出报告等人看。
	statusMoved reanchorStatus = "MOVED"
	// statusAmbiguous 同名声明在该文件里出现多次，定不了是哪一个。
	statusAmbiguous reanchorStatus = "AMBIGUOUS"
	// statusNoAnchor 判不出锚点。典型是文件头部那种整体溯源，它对应一整段而不是单个符号。
	statusNoAnchor reanchorStatus = "NO_ANCHOR"
)

// reanchorStatusOrder 是报告里状态的排列次序，按「要人看的程度」从高到低。
var reanchorStatusOrder = []reanchorStatus{
	statusDrift, statusMoved, statusNotFound, statusAmbiguous, statusNoAnchor, statusContains, statusOK,
}

// anchorTier 是一个锚点的来路，也就是这条结论的证据强度。
//
// 分层不是为了让报告好看，是为了让 -fix 敢下手。第一版把四种来路的锚点混成一串
// 平铺的候选名，于是「注释自己写明的符号名」和「拿 Go 声明名去上游做大小写不敏感
// 匹配碰上的同名东西」在结论里长得一模一样。后者根本不是证据：Go 侧的
// decodeNonNegativeInteger、NewResolver、errLoopNotActive 这类名字上游一个都没有，
// 而一旦它凑巧撞上某个无关的同名符号，-fix 就会把一条**正确**的行号改成错的
// ——那比放着不管更坏，等于亲手编出处。
type anchorTier int

const (
	// tierNone 没有锚点。
	tierNone anchorTier = iota
	// tierDeclName 是退化那一级：拿 Go 声明名本身去上游找。裁决表没覆盖到的声明只剩这条路。
	tierDeclName
	// tierRuling 是裁决表反查到的上游名，但那一行记的上游文件和注释引的**不是**同一个。
	tierRuling
	// tierRulingPath 是裁决表反查到的上游名，且那一行的上游文件和注释引的一致。
	tierRulingPath
	// tierExplicit 是注释自己括号里写明的符号名，最强。
	tierExplicit
)

// trustworthy 说的是这一级证据够不够硬到可以让机器直接改行号。
//
// 门槛画在「这个名字是人写下来的、或者裁决表和注释各自独立地指向同一个上游文件」：
// 两者都要求有一份**独立于这次猜测**的东西确认过锚点。剩下两级只进报告等人看。
func (t anchorTier) trustworthy() bool { return t >= tierRulingPath }

func (t anchorTier) String() string {
	switch t {
	case tierExplicit:
		return "注释锚点"
	case tierRulingPath:
		return "裁决表+路径一致"
	case tierRuling:
		return "裁决表"
	case tierDeclName:
		return "Go 声明名"
	}
	return "无"
}

// anchorCandidate 是一个待试的锚点名和它的来路。
// anchorCandidate 里的 path 是这个候选来路上的上游文件。
//
// 只有从裁决表反查出来的候选带路径（那张表是从当前上游快照机器生成的，路径是权威的）；
// 注释自带的括号锚点和 Go 声明名兜底那两级都为空。它的用处只有一个：符号在注释引的
// 那个文件里找不到时，去这个文件里再找一次，找着了就是上游搬了文件。
type anchorCandidate struct {
	name string
	tier anchorTier
	path string
}

// reanchorFinding 是一条溯源注释的对质结果。
//
// 它同时兼着 -fix 的施工单：rangeOffset/rangeLength 是注释里 `start[-end]`
// 那一段在**整个文件**里的字节坐标，按字节替换才能做到不动注释的其余文字。
type reanchorFinding struct {
	goPath string // 绝对路径
	goRel  string // 相对 Go 根，报告里用
	goLine int

	upstream   string // 引的上游相对路径
	movedPath  string // 搬去了哪个文件；只有 [statusMoved] 非空
	citedStart int
	citedEnd   int

	foundStart int // 算出来的跨度，定位不到时为 0
	foundEnd   int

	anchor string // 用来定位的上游符号名
	tier   anchorTier
	loose  bool // 定位是靠大小写不敏感匹配才命中的
	status reanchorStatus
	detail string

	rangeOffset int // `start[-end]` 在文件里的字节偏移
	rangeLength int
	pathOffset  int  // 上游路径在文件里的字节偏移；搬文件时要连它一起换
	hasAnchor   bool // 注释里已经带了 `（名字）`
}

// fixable 说的是这条漂移能不能交给 -fix 自动改。
//
// 三个条件缺一不可：锚点来路够硬、定位不是靠放宽大小写凑上的、以及引的范围没有
// **落在**算出的范围里面。最后那一条挡的是「Go 侧把上游一个大函数拆成了十几个方法，
// 每个只引其中几行」——那种注释引得比锚点窄是对的，改成整个函数的跨度反而把它引丢了。
//
// 落在里面那一档有一个例外，见 [reanchorFinding.jsdocBoundary]。
func (f reanchorFinding) fixable() bool {
	if f.loose {
		return false
	}
	// 搬文件那一档不看 tier：能判成 [statusMoved] 的前提就是候选带着裁决表记的路径，
	// 而带路径的候选只从裁决表来。
	if f.status == statusMoved {
		return true
	}
	if f.status != statusDrift || !f.tier.trustworthy() {
		return false
	}
	if !(f.citedStart >= f.foundStart && f.citedEnd <= f.foundEnd) {
		return true
	}
	return f.jsdocBoundary()
}

// jsdocBoundary 认的是「只差一个 JSDoc 抬头」这一种包含。
//
// 本仓库的溯源写法把紧邻声明上方的那个 JSDoc 块算进跨度里——805 条对得上的注释
// 就是这么写的，[tsSpanStart] 也是这么算的。所以「终点一模一样、起点只早了一两行」
// 说的不是引了函数内部的一小段，而是当初写注释时漏掉了抬头那几行。
//
// 卡死终点必须**完全相同**，是为了和真正的内部片段分开：那一类的终点几乎总是比
// 整个声明的终点早。起点差额也卡住，是因为 JSDoc 抬头再长也就那么几行。
const jsdocBoundarySlack = 3

func (f reanchorFinding) jsdocBoundary() bool {
	return f.citedEnd == f.foundEnd &&
		f.citedStart > f.foundStart &&
		f.citedStart-f.foundStart <= jsdocBoundarySlack
}

// editOffset 与 editLength 圈出 -fix 要替换掉的那一段。
//
// 一般只是 `start[-end]`；上游搬了文件时连前面那段路径一起换，所以从路径头开始。
// 路径和行号在注释里是 `路径:行号` 紧挨着的，中间那个冒号也在这一段里。
func (f reanchorFinding) editOffset() int {
	if f.movedPath != "" {
		return f.pathOffset
	}
	return f.rangeOffset
}

func (f reanchorFinding) editLength() int {
	if f.movedPath != "" {
		return f.rangeOffset + f.rangeLength - f.pathOffset
	}
	return f.rangeLength
}

// topDir 是这条发现所在的 Go 顶层目录，报告按它分组。
func (f reanchorFinding) topDir() string {
	if index := strings.IndexByte(f.goRel, '/'); index > 0 {
		return f.goRel[:index]
	}
	return "."
}

// citedRange 和 foundRange 把跨度渲染成注释里那种写法：单行不带连字符。
func (f reanchorFinding) citedRange() string { return formatRange(f.citedStart, f.citedEnd) }
func (f reanchorFinding) foundRange() string {
	if f.foundStart == 0 {
		return "—"
	}
	return formatRange(f.foundStart, f.foundEnd)
}

func formatRange(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// reSourceAnchored 是带字节坐标的溯源注释匹配。
//
// 和 [reSource] 分开写而不是复用，是因为这里要拿子匹配的**位置**去做字节替换，
// 所以路径必须用 `[^\s:]+` 精确框住而不能靠 `\S+` 的回溯——一个错一格的偏移
// 会把行号写到路径中间去，而那种损坏是编译不出错的。
//
// 括号锚点用全角括号，是本仓库既有的写法（`// 源: ….ts:315-316（allowedFor 那两行）`）。
var reSourceAnchored = regexp.MustCompile(`^//[ \t]*源:[ \t]*([^\s:]+):(\d+)(?:-(\d+))?(（[^）]*）)?`)

// reTSIdent 判断一段括号锚点是不是一个能拿去定位的标识符。
//
// 本仓库的括号里有两类东西：符号名（`（AdmitPrompt）`）和一句话
// （`（allowedFor 表里 enum/const 那两行）`）。后者拿去上游按名字找必然找不到，
// 于是会变成一堆假的 NOT_FOUND 把真的淹掉。所以只认长得像标识符的那些，
// 剩下的退回去按 Go 声明名反查。
var reTSIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)

// runReanchor 走遍 Go 代码，逐条对质溯源注释，出一份报告；fix 为真时顺手改掉 DRIFT。
func runReanchor(rulingPath, goRoot, dshRoot, outPath string, fix bool) error {
	rows, err := rulingtable.ReadRuling(rulingPath)
	if err != nil {
		return err
	}
	table := newAnchorTable(rows)

	findings, err := collectReanchorFindings(goRoot, dshRoot, table)
	if err != nil {
		return err
	}

	if err := writeReanchorReport(outPath, findings); err != nil {
		return err
	}

	counts := map[reanchorStatus]int{}
	for _, finding := range findings {
		counts[finding.status]++
	}
	fmt.Printf("重锚报告已写出：%s\n", outPath)
	fmt.Printf("溯源注释共 %d 条\n", len(findings))
	for _, status := range reanchorStatusOrder {
		fmt.Printf("  %-10s %d\n", status, counts[status])
	}

	if !fix {
		return nil
	}
	changed, files, err := applyReanchorFixes(findings)
	if err != nil {
		return err
	}
	fmt.Printf("已改写 %d 条溯源注释，涉及 %d 个文件。\n", changed, files)
	return nil
}

// ---- 锚点反查表 ----

// anchorTable 是「Go 声明名 → 上游符号名」的反查表，从裁决表的 PORTED 行建。
//
// 两个索引都指向整行而不是只存名字，因为定位时还要拿 `package`+`file` 去和注释引的
// 那个上游路径对一对：同一个 go_ref 上并了好几个上游符号（本仓库拿一个结构体接住
// 一族判别联合是既定做法）时，路径就是唯一能分开它们的东西。
type anchorTable struct {
	byGoRef       map[string][]rulingtable.Row
	byLastSegment map[string][]rulingtable.Row
}

func newAnchorTable(rows []rulingtable.Row) *anchorTable {
	table := &anchorTable{
		byGoRef:       map[string][]rulingtable.Row{},
		byLastSegment: map[string][]rulingtable.Row{},
	}
	for _, row := range rows {
		if row.Decision != rulingtable.Ported {
			continue
		}
		ref := strings.TrimSpace(row.GoRef)
		if ref == "" || strings.TrimSpace(row.Name) == "" {
			continue
		}
		table.byGoRef[ref] = append(table.byGoRef[ref], row)
		segments := strings.Split(ref, ".")
		last := segments[len(segments)-1]
		table.byLastSegment[last] = append(table.byLastSegment[last], row)
	}
	return table
}

// upstreamPath 是裁决表一行对应的上游相对路径，和溯源注释里写的口径一致。
func upstreamPath(row rulingtable.Row) string {
	return "packages/" + row.Package + "/" + row.File
}

// candidates 交回一串按可信度排好的上游符号名。
//
// 排序的依据只有两条：go_ref 全等比末段相等强（末段相等会把不同类型上的同名方法
// 混在一起），路径对得上比对不上强。最后无论如何都补一个 Go 声明名本身——
// 裁决表并没有覆盖全部声明，而 PascalCase 对 camelCase 的大小写不敏感匹配
// 在这份代码库里命中率相当高。
func (t *anchorTable) candidates(goRef, declName, citedPath string) []anchorCandidate {
	// 四挡，按可信度从高到低：go_ref 全等且路径对得上、go_ref 全等但路径对不上、
	// 末段相等且路径对得上、末段相等但路径对不上。末段相等会把不同类型上的同名方法
	// 混在一起，所以整体弱于 go_ref 全等；而路径对得上意味着裁决表和注释各自独立地
	// 指向了同一个上游文件，那是这里唯一能拿到的旁证。
	var buckets [4][]anchorCandidate
	bucketTiers := [4]anchorTier{tierRulingPath, tierRuling, tierRulingPath, tierRuling}
	// path 是「敢拿它去搬文件」的许可，两道闸：
	//
	// 一、只填给 go_ref 全等那两挡。末段相等太滥——`Config` 的末段在全仓命中上百行，
	// 拿它去搬文件就是给注释编一个无关的出处，而那种错误从 diff 上看是路径和行号
	// 一起变了，比不改更难发现。
	//
	// 二、同一个 go_ref 上并了好几个上游符号时（本仓库拿一个 Go 声明接住上游一族函数
	// 是既定做法），还要求候选名就是这个 Go 声明名。实测 `acp.Bridge.onSessionEvent`
	// 同时并着 `toolCallUpdate` 和 `toolResultUpdate`，而 onSessionEvent 的注释白纸黑字
	// 写着「工具……一条都不发」——没有这道闸就会把它搬到 updates.ts 上去。
	collect := func(rows []rulingtable.Row, hit, miss int, exactRef bool) {
		shared := len(rows) > 1
		for _, row := range rows {
			path := upstreamPath(row)
			index := miss
			if path == citedPath {
				index = hit
			}
			candidate := anchorCandidate{name: row.Name, tier: bucketTiers[index]}
			if exactRef && (!shared || strings.EqualFold(row.Name, declName)) {
				candidate.path = path
			}
			buckets[index] = append(buckets[index], candidate)
		}
	}
	collect(t.byGoRef[goRef], 0, 1, true)
	if declName != "" {
		collect(t.byLastSegment[declName], 2, 3, false)
	}

	seen := map[string]bool{}
	var ordered []anchorCandidate
	for _, bucket := range buckets {
		sort.Slice(bucket, func(i, j int) bool { return bucket[i].name < bucket[j].name })
		for _, candidate := range bucket {
			if !seen[candidate.name] {
				seen[candidate.name] = true
				ordered = append(ordered, candidate)
			}
		}
	}
	// 兜底那一级永远补在最后：裁决表并没有覆盖全部声明。它的命中率不低，
	// 但命中≠证据——Go 侧的构造器、哨兵错误和测试函数名上游根本不存在，
	// 它们要么找不到（记成 NOT_FOUND），要么凑巧撞上一个无关的同名符号。
	// 所以留着它出报告，但标成 [tierDeclName]，不许 -fix 碰。
	if declName != "" && !seen[declName] {
		ordered = append(ordered, anchorCandidate{name: declName, tier: tierDeclName})
	}
	return ordered
}

// ---- 遍历 Go 代码 ----

// goDeclAnchor 是一个 Go 声明的两个身份：简单名，和裁决表 go_ref 那种带包名的全名。
type goDeclAnchor struct {
	name  string // 声明名，如 Append
	goRef string // 如 session.Store.Append
}

// collectReanchorFindings 走遍 Go 代码，把每条溯源注释和上游对质。
//
// 目录跳过规则和 [checkProvenance] 一致；解析失败同样不静默跳过——一个解析不了的
// 文件里的溯源注释一条都没对质过，而报告如果照样把它算进 OK 里那份数字就是假的。
func collectReanchorFindings(goRoot, dshRoot string, table *anchorTable) ([]reanchorFinding, error) {
	upstream := newUpstreamCache(dshRoot)
	var findings []reanchorFinding

	err := filepath.WalkDir(goRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "tmp", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, source, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s 解析失败，无法重锚其中的溯源注释：%w", path, err)
		}

		relative, err := filepath.Rel(goRoot, path)
		if err != nil {
			return err
		}
		owners := collectCommentOwners(parsed)

		for _, group := range parsed.Comments {
			owner := owners[group]
			for _, comment := range group.List {
				match := reSourceAnchored.FindStringSubmatchIndex(comment.Text)
				if match == nil {
					continue
				}
				finding := buildFinding(comment, match, fileSet, path,
					filepath.ToSlash(relative), owner, table, upstream)
				findings = append(findings, finding)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].goRel != findings[j].goRel {
			return findings[i].goRel < findings[j].goRel
		}
		return findings[i].goLine < findings[j].goLine
	})
	return findings, nil
}

// buildFinding 把一条溯源注释对质完，包含定位和判状态。
func buildFinding(comment *ast.Comment, match []int, fileSet *token.FileSet,
	goPath, goRel string, owner goDeclAnchor, table *anchorTable, upstream *upstreamCache,
) reanchorFinding {
	text := comment.Text
	citedPath := text[match[2]:match[3]]
	start, _ := strconv.Atoi(text[match[4]:match[5]])
	end := start
	if match[6] >= 0 {
		end, _ = strconv.Atoi(text[match[6]:match[7]])
	}

	// `start[-end]` 那一段的字节坐标：从行号第一位到（有的话）尾号最后一位。
	rangeFrom, rangeTo := match[4], match[5]
	if match[6] >= 0 {
		rangeTo = match[7]
	}
	base := fileSet.Position(comment.Slash).Offset

	finding := reanchorFinding{
		goPath:      goPath,
		goRel:       goRel,
		goLine:      fileSet.Position(comment.Slash).Line,
		upstream:    citedPath,
		citedStart:  start,
		citedEnd:    end,
		rangeOffset: base + rangeFrom,
		rangeLength: rangeTo - rangeFrom,
		pathOffset:  base + match[2],
	}

	// 一、注释自带的括号锚点最可信，但只认长得像标识符的那些。
	var explicit string
	if match[8] >= 0 {
		finding.hasAnchor = true
		explicit = normalizeAnchorText(text[match[8]+len("（") : match[9]-len("）")])
	}

	lines, ok := upstream.lines(citedPath)
	if !ok {
		finding.status = statusNotFound
		finding.anchor = explicit
		finding.detail = "上游文件不存在"
		return finding
	}

	var candidates []anchorCandidate
	if explicit != "" {
		candidates = append(candidates, anchorCandidate{name: explicit, tier: tierExplicit})
	}
	// 二、退回去按这条注释所文档化的 Go 声明反查裁决表。
	if owner.name != "" {
		candidates = append(candidates, table.candidates(owner.goRef, owner.name, citedPath)...)
	}
	if len(candidates) == 0 {
		finding.status = statusNoAnchor
		return finding
	}

	var ambiguous anchorCandidate
	for _, candidate := range candidates {
		span, hits, loose := locateTSSymbol(lines, candidate.name)
		switch {
		case hits == 1:
			finding.anchor = candidate.name
			finding.tier = candidate.tier
			finding.loose = loose
			finding.foundStart, finding.foundEnd = span.start, span.end
			finding.status = classifySpan(start, end, span)
			if finding.status == statusDrift && start >= span.start && end <= span.end {
				// 引的范围**落在**锚点跨度里面，是一种和「行号漂了」完全不同的形态：
				// 多半这条注释引的是某个大函数内部的一小段（DSH 的 `apply` 有三百多行，
				// Go 侧拆成了十几个方法，每个只引其中几行），而锚点是从外层声明名
				// 反查来的，于是必然对不上。
				//
				// 仍然记成 DRIFT 而不新开一个状态：状态取值是外部约定的六个。
				// 但把这句话写进备注，人过这份清单时才能先把这一类整批放掉，
				// 而不是逐条读源码才发现它不是漂移。
				finding.detail = "引的范围落在算出的范围之内，多半引的是该符号内部的一小段"
			}
			return finding
		case hits > 1 && ambiguous.name == "":
			ambiguous = candidate
		}
	}
	if ambiguous.name != "" {
		finding.anchor = ambiguous.name
		finding.tier = ambiguous.tier
		finding.status = statusAmbiguous
		finding.detail = "同名声明在该文件里出现多次"
		return finding
	}
	// 引的那个文件里没有，去裁决表记的那个文件里再找一次。上游把一个大文件拆开
	// （alpha.3 把 acp 的 index.ts 拆成了 session.ts / updates.ts / mcp.ts……）时，
	// 注释引的文件本身就过期了，只改行号是改不对的。
	if moved, ok := relocate(candidates, citedPath, upstream); ok {
		finding.anchor = moved.name
		finding.tier = moved.tier
		finding.movedPath = moved.path
		finding.foundStart, finding.foundEnd = moved.start, moved.end
		finding.status = statusMoved
		finding.detail = "锚点在裁决表记的 " + moved.path + " 里找着了，上游搬了文件"
		return finding
	}
	finding.anchor = candidates[0].name
	finding.tier = candidates[0].tier
	finding.status = statusNotFound
	if finding.tier == tierDeclName {
		// 这一条极常见且**不说明任何问题**：锚点是拿 Go 声明名硬凑的，而 Go 侧的
		// 构造器、哨兵错误、测试函数名上游本来就没有对应物。找不到只是没找对东西，
		// 不是「上游删了它」。写清楚，免得这一千多条把真的删除淹掉。
		finding.detail = "锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物"
		return finding
	}
	finding.detail = "锚点符号在该上游文件里找不到"
	return finding
}

// relocatedAnchor 是一次搬家定位的结果：在哪个文件、哪个名字、哪一段。
type relocatedAnchor struct {
	name  string
	tier  anchorTier
	path  string
	start int
	end   int
}

// relocate 在候选自带的那个上游文件里再找一次。
//
// 只认唯一命中、且不是靠放宽大小写凑上的：这两条一放松，就有可能把注释改到一个
// 同名的无关符号上，而那种错误从 diff 上看是「行号和文件名都变了」，比不改更难发现。
func relocate(candidates []anchorCandidate, citedPath string, upstream *upstreamCache) (relocatedAnchor, bool) {
	for _, candidate := range candidates {
		if candidate.path == "" || candidate.path == citedPath {
			continue
		}
		lines, ok := upstream.lines(candidate.path)
		if !ok {
			continue
		}
		span, hits, loose := locateTSSymbol(lines, candidate.name)
		if hits != 1 || loose {
			continue
		}
		return relocatedAnchor{
			name:  candidate.name,
			tier:  candidate.tier,
			path:  candidate.path,
			start: span.start,
			end:   span.end,
		}, true
	}
	return relocatedAnchor{}, false
}

// normalizeAnchorText 从括号锚点里剥出一个可用的符号名，剥不出来就交回空串。
//
// 剥的是两层包装：反引号（本仓库常写成 `（`AcpConfig`）`），以及斜杠分隔的
// 「上游名 / Go 名」双写（`（AcpConfig / Config）`）——取前一个，因为要拿去上游找。
func normalizeAnchorText(raw string) string {
	text := strings.TrimSpace(raw)
	if index := strings.IndexByte(text, '/'); index >= 0 {
		text = strings.TrimSpace(text[:index])
	}
	text = strings.Trim(text, "`")
	text = strings.TrimSpace(text)
	if !reTSIdent.MatchString(text) {
		return ""
	}
	// 只取末段：`（Session.append）` 这种写法要找的是 append。
	if index := strings.LastIndexByte(text, '.'); index >= 0 {
		text = text[index+1:]
	}
	return text
}

// classifySpan 把「引的范围」和「算出的范围」比出一个状态。
func classifySpan(citedStart, citedEnd int, span tsSpan) reanchorStatus {
	switch {
	case citedStart == span.start && citedEnd == span.end:
		return statusOK
	case citedStart <= span.start && citedEnd >= span.end:
		return statusContains
	default:
		return statusDrift
	}
}

// collectCommentOwners 把「注释组 → 它文档化的那个 Go 声明」这层关系收出来。
//
// 只认顶层 [ast.FuncDecl] / [ast.GenDecl]（以及括号 GenDecl 里各个 spec 自己的 Doc）。
// 结构体字段上的溯源注释因此判不出锚点——那是有意的取舍：字段名和上游属性名的对应
// 关系比声明名弱得多，硬猜出来的锚点会把报告的信噪比拖垮。
func collectCommentOwners(parsed *ast.File) map[*ast.CommentGroup]goDeclAnchor {
	owners := map[*ast.CommentGroup]goDeclAnchor{}
	pkg := parsed.Name.Name

	put := func(doc *ast.CommentGroup, name, goRef string) {
		if doc == nil || name == "" {
			return
		}
		owners[doc] = goDeclAnchor{name: name, goRef: goRef}
	}

	for _, declaration := range parsed.Decls {
		switch node := declaration.(type) {
		case *ast.FuncDecl:
			name := node.Name.Name
			goRef := pkg + "." + name
			if node.Recv != nil && len(node.Recv.List) > 0 {
				if receiver := receiverTypeName(node.Recv.List[0].Type); receiver != "" {
					goRef = pkg + "." + receiver + "." + name
				}
			}
			put(node.Doc, name, goRef)
		case *ast.GenDecl:
			// 只有一个 spec 时，GenDecl 的 Doc 说的就是那个 spec；括号里挤着好几个
			// 声明时它说的是整组，那种情况下按声明名反查会挑错一个，所以不认。
			if len(node.Specs) == 1 {
				if name := specName(node.Specs[0]); name != "" {
					put(node.Doc, name, pkg+"."+name)
				}
			}
			for _, spec := range node.Specs {
				name := specName(spec)
				if name == "" {
					continue
				}
				switch item := spec.(type) {
				case *ast.TypeSpec:
					put(item.Doc, name, pkg+"."+name)
				case *ast.ValueSpec:
					put(item.Doc, name, pkg+"."+name)
				}
			}
		}
	}
	return owners
}

func specName(spec ast.Spec) string {
	switch item := spec.(type) {
	case *ast.TypeSpec:
		return item.Name.Name
	case *ast.ValueSpec:
		if len(item.Names) == 1 {
			return item.Names[0].Name
		}
	}
	return ""
}

// ---- 上游文件缓存 ----

// upstreamCache 按路径缓存上游文件的行，读不到的也缓存下来（免得同一个缺失路径被反复打开）。
type upstreamCache struct {
	root  string
	files map[string][]string
	miss  map[string]bool
}

func newUpstreamCache(root string) *upstreamCache {
	return &upstreamCache{root: root, files: map[string][]string{}, miss: map[string]bool{}}
}

func (c *upstreamCache) lines(relative string) ([]string, bool) {
	if lines, ok := c.files[relative]; ok {
		return lines, true
	}
	if c.miss[relative] {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(relative)))
	if err != nil {
		c.miss[relative] = true
		return nil, false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	// 末尾换行会切出一个空串，它不是一行——留着会让「文件多少行」这个数多一。
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	c.files[relative] = lines
	return lines, true
}

// ---- 上游 TS 定位 ----

// tsSpan 是上游一个声明的行跨度，1 起、闭区间。
type tsSpan struct {
	start int
	end   int
}

// locateTSSymbol 在上游文件里按名字找一个声明，交回它的跨度和命中数。
//
// 命中数是报告能区分 NOT_FOUND 和 AMBIGUOUS 的全部依据，所以匹配分两档：
// 关键字引导的声明（class/interface/type/function/const…）算强档，
// 类成员和属性算弱档。有强档命中就只看强档——一个既被声明又被同文件调用的函数
// 如果两档混在一起数，会被判成 AMBIGUOUS，而它其实一点都不含糊。
//
// 名字先按原样找；一条都找不到才退化成大小写不敏感（Go 的 PascalCase 对上游的 camelCase）。
// 顺序不能反：`Foo` 和 `foo` 在 TS 里是两个东西，宽松匹配优先会把它们搅在一起。
//
// 第三个返回值说的是「这一次是靠放宽大小写才命中的」。调用方要拿它挡住 -fix：
// 放宽之后命中的那个符号未必是同一个东西，而据此改行号就是在编出处。
func locateTSSymbol(lines []string, name string) (tsSpan, int, bool) {
	if name == "" {
		return tsSpan{}, 0, false
	}
	for _, insensitive := range []bool{false, true} {
		var strong, weak []int
		for index, line := range lines {
			switch matchTSDecl(line, name, insensitive) {
			case declStrong:
				strong = append(strong, index+1)
			case declWeak:
				weak = append(weak, index+1)
			}
		}
		hits := strong
		if len(hits) == 0 {
			hits = weak
		}
		if len(hits) == 0 {
			continue
		}
		if len(hits) > 1 {
			return tsSpan{}, len(hits), insensitive
		}
		declLine := hits[0]
		return tsSpan{
			start: tsSpanStart(lines, declLine),
			end:   tsSpanEnd(lines, declLine),
		}, 1, insensitive
	}
	return tsSpan{}, 0, false
}

// 声明匹配的三档。
const (
	declNone = iota
	declWeak
	declStrong
)

// tsModifiers 是可以出现在声明前面的修饰词，`export default` 必须排在 `export` 之前。
var tsModifiers = []string{
	"export default", "export", "declare", "abstract", "async",
	"static", "public", "private", "protected", "readonly", "override",
}

// tsKeywords 是引导一个强档声明的关键字。
var tsKeywords = []string{
	"class", "interface", "type", "function", "const", "let", "var", "enum", "namespace",
}

// matchTSDecl 判断一行是不是 name 的声明，交回档次。
func matchTSDecl(line, name string, insensitive bool) int {
	trimmed := strings.TrimLeft(line, " \t")
	indented := len(trimmed) < len(line)
	rest, stripped := stripTSModifiers(trimmed)

	for _, keyword := range tsKeywords {
		tail, ok := consumeWord(rest, keyword)
		if !ok {
			continue
		}
		// `function* gen(` 的星号在关键字和名字之间。
		tail = strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(tail, " \t"), "*"), " \t")
		if after, ok := consumeName(tail, name, insensitive); ok && isTSDeclTail(after, keyword) {
			return declStrong
		}
	}

	// 弱档：类成员和对象属性。裸名字开头的行在顶层不带修饰词时是函数调用而不是声明，
	// 所以要么缩进过（在某个类/对象体里），要么前面有 private/readonly 之类的修饰词。
	if !stripped && !indented {
		return declNone
	}
	if after, ok := consumeName(rest, name, insensitive); ok && isTSMemberTail(after) {
		return declWeak
	}
	return declNone
}

// stripTSModifiers 把行首的修饰词一层层剥掉，交回剩下的部分和「剥掉过没有」。
func stripTSModifiers(trimmed string) (string, bool) {
	stripped := false
	for {
		advanced := false
		for _, modifier := range tsModifiers {
			tail, ok := consumeWord(trimmed, modifier)
			if !ok {
				continue
			}
			trimmed = strings.TrimLeft(tail, " \t")
			stripped, advanced = true, true
			break
		}
		if !advanced {
			return trimmed, stripped
		}
	}
}

// consumeWord 吃掉行首的一个词，要求它后面跟的是空白（否则 `constant` 会被当成 `const`）。
func consumeWord(text, word string) (string, bool) {
	if !strings.HasPrefix(text, word) {
		return text, false
	}
	rest := text[len(word):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return text, false
	}
	return rest, true
}

// consumeName 吃掉行首的标识符，要求它整体等于 name（后面不能再接标识符字符）。
func consumeName(text, name string, insensitive bool) (string, bool) {
	if len(text) < len(name) {
		return text, false
	}
	head := text[:len(name)]
	if insensitive {
		if !strings.EqualFold(head, name) {
			return text, false
		}
	} else if head != name {
		return text, false
	}
	rest := text[len(name):]
	if rest != "" && isTSIdentByte(rest[0]) {
		return text, false
	}
	return rest, true
}

func isTSIdentByte(c byte) bool {
	return c == '_' || c == '$' || (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isTSDeclTail 判断关键字声明的名字后面跟的东西合不合理。
//
// `type X` 后面必须是 `=` 或泛型参数，否则 `type` 这个词在别处（比如
// `const type = ...` 之外的类型注解里）会误命中。
func isTSDeclTail(after, keyword string) bool {
	tail := strings.TrimLeft(after, " \t")
	if tail == "" {
		return true
	}
	switch keyword {
	case "type":
		return tail[0] == '=' || tail[0] == '<'
	case "function":
		return tail[0] == '(' || tail[0] == '<'
	}
	switch tail[0] {
	case '=', '<', '{', '(', ':', ',', ';', '?', '!', '[', 'e', 'i':
		return true // e/i 留给 extends / implements
	}
	return false
}

// isTSMemberTail 判断弱档的名字后面跟的东西像不像一个成员声明。
func isTSMemberTail(after string) bool {
	tail := strings.TrimLeft(after, " \t")
	if tail == "" {
		return false
	}
	switch tail[0] {
	case '(', '<', ':', '?', '!':
		return true
	case '=':
		// `x == y` 是比较，`x => y` 是箭头参数，两个都不是声明。
		return len(tail) == 1 || (tail[1] != '=' && tail[1] != '>')
	}
	return false
}

// tsSpanStart 算跨度的起点：紧邻上方的 JSDoc 块算在里面。
//
// 这是本仓库既有的惯例，已核对过多例（`SessionEvent` 记 378-423，其中 378-390 是
// doc；`isTokenDelta` 记 153-168，其中 153-157 是 doc）。「紧邻」要求中间不隔空行——
// 隔了空行的注释块讲的是上一段代码，把它算进来会让起点无端往上跑。
func tsSpanStart(lines []string, declLine int) int {
	if declLine < 2 {
		return declLine
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[declLine-2]), "*/") {
		return declLine
	}
	for index := declLine - 1; index >= 1; index-- {
		trimmed := strings.TrimSpace(lines[index-1])
		if strings.HasPrefix(trimmed, "/*") {
			return index
		}
		if trimmed == "" {
			return declLine
		}
		// 注释块的中间行都以 `*` 开头；碰到别的说明上面那个 `*/` 不属于一个独立的块。
		if index != declLine-1 && !strings.HasPrefix(trimmed, "*") {
			return declLine
		}
	}
	return declLine
}

// tsSpanEnd 算跨度的终点：从声明行开始跟 `{}`、`()`、`[]` 的深度，回到 0 且这句话说完了。
//
// 「说完了」单独判一次，是因为 `type X = A | B` 这一族根本不开括号：只看深度的话
// 终点会停在声明行，而它的真实跨度可能有十几行。
func tsSpanEnd(lines []string, declLine int) int {
	scanner := &tsScanner{}
	for index := declLine; index <= len(lines); index++ {
		scanner.scanLine(lines[index-1])
		if scanner.depth > 0 || scanner.inString != 0 || scanner.inBlockComment {
			continue
		}
		if continuesTS(scanner.tail) {
			continue
		}
		if index < len(lines) && startsContinuationTS(lines[index]) {
			continue
		}
		return index
	}
	return len(lines)
}

// continuesTS 判断一行代码的尾巴是不是「话还没说完」。
func continuesTS(tail string) bool {
	if tail == "" {
		return true // 空行或纯注释行，等下一行
	}
	if strings.HasSuffix(tail, "=>") {
		return true
	}
	for _, keyword := range []string{"extends", "implements", "as", "keyof", "typeof", "return", "new", "in"} {
		if strings.HasSuffix(tail, keyword) {
			return true
		}
	}
	switch tail[len(tail)-1] {
	case '=', '|', '&', ',', '+', '-', '?', ':', '<', '(', '{', '[', '.':
		return true
	}
	return false
}

// startsContinuationTS 判断下一行是不是这句话的续行。
//
// 只用来接住把 `{` 或 `| Variant` 换行另起的写法；`)` `]` `}` 这类闭合符不算，
// 深度回到 0 之后它们不可能属于这句话。
func startsContinuationTS(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	for _, prefix := range []string{"{", "|", "&", ".", "?", ":", "=", "extends", "implements"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// tsScanner 是逐行的括号配平器，状态跨行保留。
//
// 字符串、模板串和注释里的括号必须不算数，否则一句 `log('}')` 就能把深度带到负数，
// 而深度一错，后面所有声明的终点都跟着错——这种错不会报出来，只会安静地给出假答案。
type tsScanner struct {
	depth          int
	inBlockComment bool
	inString       byte  // 0 / '\'' / '"' / '`'
	interpolation  []int // 模板串里 `${` 之前的深度，用来认出该回到模板串的那个 `}`
	tail           string
}

func (s *tsScanner) scanLine(line string) {
	var code []byte
	for index := 0; index < len(line); {
		char := line[index]

		if s.inBlockComment {
			if char == '*' && index+1 < len(line) && line[index+1] == '/' {
				s.inBlockComment = false
				index += 2
				continue
			}
			index++
			continue
		}

		if s.inString != 0 {
			switch {
			case char == '\\':
				index += 2
			case s.inString == '`' && char == '$' && index+1 < len(line) && line[index+1] == '{':
				s.interpolation = append(s.interpolation, s.depth)
				s.depth++
				s.inString = 0
				index += 2
			case char == s.inString:
				s.inString = 0
				code = append(code, 'x') // 字符串整体压成一个占位符，只为让尾巴判断看得出这里有东西
				index++
			default:
				index++
			}
			continue
		}

		switch {
		case char == '/' && index+1 < len(line) && line[index+1] == '/':
			index = len(line) // 行注释，本行剩下的都不算代码
		case char == '/' && index+1 < len(line) && line[index+1] == '*':
			s.inBlockComment = true
			index += 2
		case char == '\'' || char == '"' || char == '`':
			s.inString = char
			index++
		case char == '{' || char == '(' || char == '[':
			s.depth++
			code = append(code, char)
			index++
		case char == '}' || char == ')' || char == ']':
			if char == '}' && len(s.interpolation) > 0 && s.depth-1 == s.interpolation[len(s.interpolation)-1] {
				s.depth--
				s.interpolation = s.interpolation[:len(s.interpolation)-1]
				s.inString = '`'
				index++
				continue
			}
			if s.depth > 0 {
				s.depth--
			}
			code = append(code, char)
			index++
		default:
			code = append(code, char)
			index++
		}
	}
	s.tail = strings.TrimRight(string(code), " \t")
}

// ---- 报告 ----

func writeReanchorReport(outPath string, findings []reanchorFinding) error {
	counts := map[reanchorStatus]int{}
	driftByDir := map[string]int{}
	for _, finding := range findings {
		counts[finding.status]++
		if finding.status == statusDrift {
			driftByDir[finding.topDir()]++
		}
	}

	var out strings.Builder
	out.WriteString("# 溯源重锚发现\n\n")
	out.WriteString("由 `go run ./internal/devtools/portcheck -mode reanchor` 生成，**不要手工编辑**——它每次会被整份覆盖。\n\n")
	out.WriteString("做法：给每条 `// 源:` 注释找一个锚点符号（注释自带的 `（名字）`、这条注释所文档化的 ")
	out.WriteString("Go 声明经裁决表反查出的上游名、或退化的大小写不敏感匹配），去上游文件里按名字重新定位，")
	out.WriteString("算出真实跨度再和注释里写的比。跨度含紧邻上方的 JSDoc 块，终点靠括号配平。\n\n")
	fmt.Fprintf(&out, "溯源注释共 **%d** 条。\n\n", len(findings))

	out.WriteString("## 汇总\n\n")
	out.WriteString("| 状态 | 条数 | 含义 |\n|---|---:|---|\n")
	for _, status := range reanchorStatusOrder {
		fmt.Fprintf(&out, "| `%s` | %d | %s |\n", status, counts[status], statusMeaning(status))
	}
	out.WriteString("\n")

	out.WriteString("### DRIFT 按 Go 顶层目录\n\n")
	if len(driftByDir) == 0 {
		out.WriteString("没有 DRIFT。\n\n")
	} else {
		dirs := make([]string, 0, len(driftByDir))
		for dir := range driftByDir {
			dirs = append(dirs, dir)
		}
		sort.Slice(dirs, func(i, j int) bool {
			if driftByDir[dirs[i]] != driftByDir[dirs[j]] {
				return driftByDir[dirs[i]] > driftByDir[dirs[j]]
			}
			return dirs[i] < dirs[j]
		})
		out.WriteString("| 顶层目录 | DRIFT |\n|---|---:|\n")
		for _, dir := range dirs {
			fmt.Fprintf(&out, "| %s | %d |\n", dir, driftByDir[dir])
		}
		out.WriteString("\n")
	}

	out.WriteString("### DRIFT 按锚点可信度\n\n")
	out.WriteString("**这张表比上面那张重要。** 一条漂移结论只和它的锚点一样可靠：锚点要是拿 Go 声明名硬凑的，" +
		"「算出的范围」很可能算的是另一个符号，照着它改行号就是在编出处。`-fix` 只动「可自动改」那一档。\n\n")
	tierCount := map[anchorTier]int{}
	fixable := 0
	for _, finding := range findings {
		if finding.status != statusDrift {
			continue
		}
		tierCount[finding.tier]++
		if finding.fixable() {
			fixable++
		}
	}
	out.WriteString("| 锚点来路 | DRIFT | 可自动改 |\n|---|---:|---|\n")
	for _, tier := range []anchorTier{tierExplicit, tierRulingPath, tierRuling, tierDeclName} {
		mark := "否——只出报告，等人看"
		if tier.trustworthy() {
			mark = "是"
		}
		fmt.Fprintf(&out, "| %s | %d | %s |\n", tier, tierCount[tier], mark)
	}
	out.WriteString("\n")

	inner, boundary := 0, 0
	for _, finding := range findings {
		if finding.status != statusDrift || finding.detail == "" {
			continue
		}
		inner++
		if finding.jsdocBoundary() {
			boundary++
		}
	}
	fmt.Fprintf(&out, "DRIFT 里有 **%d** 条的备注是「引的范围落在算出的范围之内」——那一类多半不是行号漂了，"+
		"而是这条注释引的是某个大函数内部的一小段，而锚点只能从外层声明名反查出来，"+
		"改成整个函数的跨度反而把它引丢了，所以 `-fix` 也不碰。\n\n", inner)
	fmt.Fprintf(&out, "但其中 **%d** 条是终点一模一样、起点只早了不超过 %d 行——那是漏掉紧邻上方 JSDoc 抬头，"+
		"不是引了内部片段，锚点够硬的话 `-fix` 会改。\n\n", boundary, jsdocBoundarySlack)
	fmt.Fprintf(&out, "把两道闸都过掉之后，`-fix` 实际会改 **%d** 条。\n\n", fixable)

	writeReanchorSection(&out, findings, statusDrift,
		"## DRIFT（逐条）\n\n引的范围既不等于也不包含算出来的范围。「可改」那一列为空的要人工逐条过。\n\n")
	writeReanchorSection(&out, findings, statusMoved,
		"## MOVED（逐条）\n\n锚点在注释引的那个文件里没有，但在裁决表记的那个文件里唯一命中——上游搬了文件。"+
			"这一档 `-fix` 会把路径和行号一起改；「上游文件」那一列写的是**改成**哪个。\n\n")
	writeReanchorSection(&out, findings, statusNotFound,
		"## NOT_FOUND（逐条）\n\n锚点符号在引的那个上游文件里找不到。锚点来路是「Go 声明名」的那些"+
			"**不说明任何问题**——Go 侧的构造器、哨兵错误、测试函数名上游本来就没有对应物。\n\n")

	out.WriteString("## 其余状态\n\n")
	out.WriteString("按要求只给数量，不逐条列。\n\n")
	for _, status := range []reanchorStatus{statusAmbiguous, statusNoAnchor, statusContains, statusOK} {
		fmt.Fprintf(&out, "- `%s`：%d 条——%s\n", status, counts[status], statusMeaning(status))
	}
	out.WriteString("\n")

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(out.String()), 0o644)
}

func writeReanchorSection(out *strings.Builder, findings []reanchorFinding, want reanchorStatus, header string) {
	out.WriteString(header)
	rows := 0
	var body strings.Builder
	for _, finding := range findings {
		if finding.status != want {
			continue
		}
		rows++
		note := finding.detail
		if note == "" {
			note = "-"
		}
		anchorTag := finding.tier.String()
		if finding.loose {
			anchorTag += "・放宽大小写"
		}
		mark := ""
		if finding.fixable() {
			mark = "✓"
		}
		shownPath := finding.upstream
		if finding.movedPath != "" {
			shownPath = finding.movedPath
		}
		fmt.Fprintf(&body, "| `%s:%d` | %s | %s | %s | `%s` | %s | %s | %s |\n",
			finding.goRel, finding.goLine, shownPath,
			finding.citedRange(), finding.foundRange(), finding.anchor, anchorTag, mark, note)
	}
	if rows == 0 {
		out.WriteString("没有发现。\n\n")
		return
	}
	fmt.Fprintf(out, "共 %d 条。\n\n", rows)
	out.WriteString("| Go 位置 | 上游文件 | 引的范围 | 算出的范围 | 锚点符号 | 锚点来路 | 可改 | 备注 |\n" +
		"|---|---|---:|---:|---|---|:-:|---|\n")
	out.WriteString(body.String())
	out.WriteString("\n")
}

func statusMeaning(status reanchorStatus) string {
	switch status {
	case statusOK:
		return "引的范围和算出来的一致"
	case statusContains:
		return "引的范围完全包含算出来的（文件头部那种整体溯源，不算错）"
	case statusDrift:
		return "既不相等也不包含，**真漂移**"
	case statusNotFound:
		return "锚点符号在该上游文件里找不到"
	case statusMoved:
		return "锚点搬到了裁决表记的另一个文件里"
	case statusAmbiguous:
		return "同名声明在该文件里出现多次，定不了"
	case statusNoAnchor:
		return "判不出锚点（多为整段溯源、结构体字段上的注释）"
	}
	return ""
}

// ---- -fix ----

// applyReanchorFixes 只改 [reanchorFinding.fixable] 那些，按字节替换注释里的行号子串。
//
// **不重写整份文件、不跑 gofmt、不动注释的其余文字。** 理由是这份改动的价值全在
// 「diff 里只有行号变了」——只要顺手格式化一次，真正的改动就会被淹在噪声里，
// 而人工复核这批漂移的唯一办法就是读那份 diff。
//
// 幂等：改完之后引的范围等于算出来的，下一轮它是 OK，不再进这条路径。
func applyReanchorFixes(findings []reanchorFinding) (int, int, error) {
	byFile := map[string][]reanchorFinding{}
	for _, finding := range findings {
		if !finding.fixable() {
			continue
		}
		byFile[finding.goPath] = append(byFile[finding.goPath], finding)
	}

	paths := make([]string, 0, len(byFile))
	for path := range byFile {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	changed := 0
	for _, path := range paths {
		edits := byFile[path]
		// 从后往前改，前面那些的字节偏移才不会被前一次替换挪动。
		sort.Slice(edits, func(i, j int) bool { return edits[i].editOffset() > edits[j].editOffset() })

		source, err := os.ReadFile(path)
		if err != nil {
			return changed, 0, err
		}
		for _, edit := range edits {
			offset, length := edit.editOffset(), edit.editLength()
			if offset < 0 || offset+length > len(source) {
				return changed, 0, fmt.Errorf("%s:%d 的字节坐标越界，拒绝改写", edit.goRel, edit.goLine)
			}
			replacement := formatRange(edit.foundStart, edit.foundEnd)
			if edit.movedPath != "" {
				replacement = edit.movedPath + ":" + replacement
			}
			if !edit.hasAnchor && edit.anchor != "" {
				// 补上锚点，下一轮就不必再靠裁决表反查——那条路径依赖一张会变的表。
				replacement += "（" + edit.anchor + "）"
			}
			source = append(source[:offset],
				append([]byte(replacement), source[offset+length:]...)...)
			changed++
		}
		if err := os.WriteFile(path, source, 0o644); err != nil {
			return changed, 0, err
		}
	}
	return changed, len(paths), nil
}
