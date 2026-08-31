// 本文件的作用：把那份轮次报告的收与验、以及交回父手上那段文字的排版，钉在它们
// 各自真会出错的边上。
//
// # 这些测试防的是什么错
//
//   - **一份形状不对的东西被当成报告收下**。轮与轮之间只有这一份载荷，它收错了，
//     下一轮就是在一份编出来的交接上接着干。
//   - **多一个键被悄悄忽略**。那份 schema 是封闭的，多出来的键说明这个值压根没经过
//     它——那它是谁给的就说不清了。
//   - **那几条跨字段的规矩被放过**。「说完成了却拿不出证据」是这类循环最常见的
//     假阳性，`complete` 那条就是专门堵它的。
//   - **字数上限按字节算**。同一份配置会在中英文两种交接上宽严差三倍，中文那边
//     早早就被拒掉。
//   - **截断把一个多字节的字劈成两半**。交出去的就是一段带替换字符的坏文本。
//   - **孩子的自述被说成认证**。措辞必须是「worker reported …」，本包一个字的
//     证据都没验过。
//   - **一次轮次失败把上一份交接丢了**。那是这次调用留下来的唯一成果，丢了父就
//     只知道「Ralph 挂了」，接不下去。

package toolralph

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ---- 造报告的几个手脚架 ----

// goodReport 是一份过得了每一道闸的 continue 报告。
func goodReport() RoundReport {
	return RoundReport{
		Status:    RoundContinue,
		Summary:   "读了那三个文件",
		Evidence:  []string{"go build 过了"},
		NextSteps: []string{"把测试补上"},
	}
}

// reportValue 把一份报告排成提供方交回来的那种松散值（一个 map），
// 好让用例往里塞形状不对的东西。
func reportValue(report RoundReport) map[string]any {
	return map[string]any{
		"status":    string(report.Status),
		"summary":   report.Summary,
		"evidence":  report.Evidence,
		"nextSteps": report.NextSteps,
		"blocker":   report.Blocker,
	}
}

// ---- 收报告 ----

// TestReadReportAcceptsAWellFormedRound 钉住一份规规矩矩的报告收得进来，
// 而且收进来的每一个字段都和交出来的那份一样。
func TestReadReportAcceptsAWellFormedRound(t *testing.T) {
	t.Parallel()
	want := goodReport()
	got, err := readReport(reportValue(want), "", DefaultMaxHandoffChars)
	if err != nil {
		t.Fatalf("这份报告该收得进来：%v", err)
	}
	if got.Status != want.Status || got.Summary != want.Summary ||
		len(got.Evidence) != 1 || got.Evidence[0] != want.Evidence[0] ||
		len(got.NextSteps) != 1 || got.NextSteps[0] != want.NextSteps[0] ||
		got.Blocker != "" {
		t.Fatalf("收回来的报告是 %#v", got)
	}
}

// TestReadReportRejectsWhatIsNotAFiveKeyObject 钉住「不是那个恰好五个键的对象」
// 这一整类东西全被挡在外面。
//
// 多一个键必须拒而不是忽略：那份 schema 是封闭的，多出来的键说明这个值没经过它。
func TestReadReportRejectsWhatIsNotAFiveKeyObject(t *testing.T) {
	t.Parallel()
	extra := reportValue(goodReport())
	extra["smuggled"] = "夹带的私货"
	missing := reportValue(goodReport())
	delete(missing, "blocker")

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"什么都没有", nil, "no structured round report"},
		{"是个数组", []string{"nope"}, "no structured round report"},
		{"是个标量", 42, "no structured round report"},
		{"排不动的东西", make(chan int), "no structured round report"},
		{"多一个键", extra, "must carry exactly"},
		{"少一个键", missing, "must carry exactly"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			_, err := readReport(each.value, "", DefaultMaxHandoffChars)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
		})
	}
}

// TestReadReportRejectsFieldsOfTheWrongType 钉住键对了但类型不对的那一份走
// malformed 那条路。
//
// 这一条盯的是 [readReport] 里那次解进结构体的失败：键表是对的，所以前一道闸放它
// 过去了，形状不对要到这一步才现原形。
func TestReadReportRejectsFieldsOfTheWrongType(t *testing.T) {
	t.Parallel()
	value := reportValue(goodReport())
	value["evidence"] = "本该是一串"
	_, err := readReport(value, "", DefaultMaxHandoffChars)
	if err == nil || !strings.Contains(err.Error(), "is malformed") {
		t.Fatalf("类型不对该报 malformed，实际 %v", err)
	}
}

// TestReadReportEnforcesTheExpectedStatus 钉住调用方点了名的那个定性说了算。
//
// 空串表示三种都收——轮次循环里那一道就是这么调的。
func TestReadReportEnforcesTheExpectedStatus(t *testing.T) {
	t.Parallel()
	value := reportValue(goodReport())
	if _, err := readReport(value, RoundContinue, DefaultMaxHandoffChars); err != nil {
		t.Fatalf("点名 continue 该收得进来：%v", err)
	}
	if _, err := readReport(value, "", DefaultMaxHandoffChars); err != nil {
		t.Fatalf("空串该三种都收：%v", err)
	}
	_, err := readReport(value, RoundComplete, DefaultMaxHandoffChars)
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("定性对不上该报错，实际 %v", err)
	}
}

// TestReadReportRunsTheCrossFieldRules 钉住那几条跨字段的规矩确实挂在收报告这条路上。
//
// 形状对、类型对、定性也对的一份仍然可能是退化的（这里是一份说完成却拿不出证据的），
// 它必须在 [readReport] 里就被拦下——那道校验单独测过不算，得确认它真的被调到了。
func TestReadReportRunsTheCrossFieldRules(t *testing.T) {
	t.Parallel()
	value := reportValue(RoundReport{Status: RoundComplete, Summary: "做完了"})
	_, err := readReport(value, "", DefaultMaxHandoffChars)
	if err == nil || !strings.Contains(err.Error(), "needs evidence") {
		t.Fatalf("该在收的时候就被那几条规矩拦下，实际 %v", err)
	}
}

// TestValidateReportGuardsEachDegeneration 逐条钉住那几条跨字段的规矩。
//
// 它们不是形式主义，每一条各自堵着一种很具体的退化，见 [validateReport] 上那段话。
func TestValidateReportGuardsEachDegeneration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		report RoundReport
		want   string
	}{
		{"summary 是空的", RoundReport{Status: RoundContinue, NextSteps: []string{"下一步"}},
			"summary must be non-empty"},
		{"summary 前后带空白",
			RoundReport{Status: RoundContinue, Summary: " 干了活 ", NextSteps: []string{"下一步"}},
			"summary must be non-empty"},
		{"evidence 里有空条目",
			RoundReport{Status: RoundContinue, Summary: "干了活", Evidence: []string{""},
				NextSteps: []string{"下一步"}},
			"only non-empty normalized strings"},
		{"nextSteps 里有带空白的条目",
			RoundReport{Status: RoundContinue, Summary: "干了活", NextSteps: []string{" 下一步"}},
			"only non-empty normalized strings"},
		{"blocker 前后带空白",
			RoundReport{Status: RoundBlocked, Summary: "卡住了", Blocker: " 缺权限 "},
			"blocker must be a normalized string"},
		{"continue 却没有下一步",
			RoundReport{Status: RoundContinue, Summary: "干了活"},
			"needs nextSteps and an empty blocker"},
		{"continue 却写了 blocker",
			RoundReport{Status: RoundContinue, Summary: "干了活", NextSteps: []string{"下一步"},
				Blocker: "缺权限"},
			"needs nextSteps and an empty blocker"},
		{"complete 却拿不出证据",
			RoundReport{Status: RoundComplete, Summary: "做完了"},
			"needs evidence, no nextSteps"},
		{"complete 却还留着下一步",
			RoundReport{Status: RoundComplete, Summary: "做完了", Evidence: []string{"测试全绿"},
				NextSteps: []string{"还有活"}},
			"needs evidence, no nextSteps"},
		{"complete 却写了 blocker",
			RoundReport{Status: RoundComplete, Summary: "做完了", Evidence: []string{"测试全绿"},
				Blocker: "缺权限"},
			"needs evidence, no nextSteps"},
		{"blocked 却说不出被什么挡住",
			RoundReport{Status: RoundBlocked, Summary: "卡住了"},
			"needs a concrete blocker"},
		{"定性根本不在那三个里",
			RoundReport{Status: "done", Summary: "做完了"},
			"status is invalid"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			err := validateReport(each.report)
			if err == nil || !strings.Contains(err.Error(), each.want) {
				t.Fatalf("该报 %q，实际 %v", each.want, err)
			}
		})
	}
}

// TestValidateReportAcceptsEachGoodShape 钉住三种定性各自那份规矩的报告都放得过去。
//
// 光验拒收不验放行的话，一条把什么都拒掉的规则也能让上面那张表全绿。
func TestValidateReportAcceptsEachGoodShape(t *testing.T) {
	t.Parallel()
	good := []RoundReport{
		{Status: RoundContinue, Summary: "干了活", NextSteps: []string{"下一步"}},
		{Status: RoundComplete, Summary: "做完了", Evidence: []string{"测试全绿"}},
		{Status: RoundBlocked, Summary: "卡住了", Blocker: "缺一把仓库的写权限"},
	}
	for _, report := range good {
		if err := validateReport(report); err != nil {
			t.Fatalf("%s 那份该放行：%v", report.Status, err)
		}
	}
}

// TestHandoffLimitCountsRunesNotBytes 钉住字数上限数的是**字**不是字节。
//
// 数字节的话，同一份配置在中英文两种交接上宽严差三倍——一份中文报告会在英文报告
// 还宽宽松松的时候就被拒掉，而部署方对着同一个数字完全看不出这件事。
func TestHandoffLimitCountsRunesNotBytes(t *testing.T) {
	t.Parallel()
	report := RoundReport{
		Status:    RoundContinue,
		Summary:   strings.Repeat("字", 20),
		Evidence:  []string{},
		NextSteps: []string{"下一步"},
	}
	encoded := encodeReport(report)
	if jsonChars(encoded) >= len(encoded) {
		t.Fatalf("这份报告得让码点数比字节数少，才验得出这件事")
	}
	if _, err := readReport(reportValue(report), "", jsonChars(encoded)); err != nil {
		t.Fatalf("正好卡在上限上的一份该收得进来：%v", err)
	}
	_, err := readReport(reportValue(report), "", jsonChars(encoded)-1)
	if err == nil || !strings.Contains(err.Error(), "exceeds maxHandoffChars") {
		t.Fatalf("超一个字该被拒，实际 %v", err)
	}
}

// TestReportSchemaIsClosed 钉住那份发给孩子的 schema 是封闭的、五个键全必填。
//
// 封闭这件事是「孩子往交接里夹私货」那条路在提供方那一层的断口。它松了，
// [reportFields] 那道键表检查就得独自扛住一切，而那时候那次调用已经花完钱了。
func TestReportSchemaIsClosed(t *testing.T) {
	t.Parallel()
	schema := reportSchema()
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("那份 schema 必须显式关掉 additionalProperties")
	}
	if len(schema.Required) != len(reportKeys) {
		t.Fatalf("五个键都得是必填的，实际必填 %v", schema.Required)
	}
	names := make([]string, 0, len(schema.Properties))
	for _, property := range schema.Properties {
		names = append(names, property.Name)
	}
	for _, key := range reportKeys {
		if !contains(names, key) {
			t.Fatalf("schema 里少了 %q，实际有 %v", key, names)
		}
	}
	status := schema.Properties[0].Schema
	if len(status.Enum) != 3 {
		t.Fatalf("status 该是那三个的枚举，实际 %v", status.Enum)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// ---- 排版 ----

// TestBoundResultKeepsWholeRunes 钉住截断落在字的边界上。
//
// 直接对 string 切片切的是字节，会把一个多字节的字劈成两半，交出去就是一段带替换
// 字符的坏文本。
func TestBoundResultKeepsWholeRunes(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("字", 40)
	got := boundResult(text, 20)
	if !strings.HasSuffix(got, truncationNotice) {
		t.Fatalf("截过的文字该带上那句标记，实际 %q", got)
	}
	if count := len([]rune(got)); count != 20 {
		t.Fatalf("截出来该正好 20 个字，实际 %d", count)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("截出来的文字里有替换字符，说明有个字被劈开了：%q", got)
	}
}

// TestBoundResultLeavesShortTextAlone 钉住够短的原样交回，一个字都不动。
func TestBoundResultLeavesShortTextAlone(t *testing.T) {
	t.Parallel()
	if got := boundResult("短", 10); got != "短" {
		t.Fatalf("够短的该原样交回，实际 %q", got)
	}
	exact := strings.Repeat("a", 10)
	if got := boundResult(exact, 10); got != exact {
		t.Fatalf("正好卡在上限上的该原样交回，实际 %q", got)
	}
}

// TestBoundResultWithNoRoomForTheNotice 钉住上限比那句标记还短时的落点。
//
// 这时候已经装不下任何正文了。交回标记的前几个字而不是一段空的：让人看见这里
// 被截过，比交回一段看不出问题的空文本强。
func TestBoundResultWithNoRoomForTheNotice(t *testing.T) {
	t.Parallel()
	notice := []rune(truncationNotice)
	got := boundResult(strings.Repeat("a", 100), 3)
	if got != string(notice[:3]) {
		t.Fatalf("该交回那句标记的前 3 个字，实际 %q", got)
	}
	if got := boundResult(strings.Repeat("a", 100), len(notice)); got != truncationNotice {
		t.Fatalf("上限正好等于标记长度时也走这一支，实际 %q", got)
	}
}

// TestRenderResultNeverCertifiesTheWorker 钉住三种收场的措辞都是转述，不是认证。
//
// 「worker reported completion」而不是「done」：本包一个字的证据都没验过，
// 说成后者就是替孩子作证。
func TestRenderResultNeverCertifiesTheWorker(t *testing.T) {
	t.Parallel()
	report := RoundReport{Status: RoundComplete, Summary: "做完了", Evidence: []string{"测试全绿"}}
	cases := []struct {
		status RunStatus
		rounds int
		want   string
	}{
		{RunComplete, 1, "Ralph worker reported completion after 1 round."},
		{RunBlocked, 2, "Ralph worker reported a blocker after 2 rounds."},
		{RunBudgetLimited, 3, "Ralph reached its 3 rounds limit; the worker reported work remaining."},
		{"weird", 1, "Ralph ended with an unknown status (weird)."},
	}
	for _, each := range cases {
		t.Run(string(each.status), func(t *testing.T) {
			t.Parallel()
			text := renderResult(
				runResult{Status: each.status, RoundsStarted: each.rounds, Report: report},
				DefaultMaxResultChars)
			if !strings.HasPrefix(text, each.want) {
				t.Fatalf("话头该是 %q，实际 %q", each.want, text)
			}
			if !strings.Contains(text, "\"summary\": \"做完了\"") {
				t.Fatalf("那份报告该跟着一起呈上去，实际 %q", text)
			}
		})
	}
}

// TestRenderResultObeysTheCharLimit 钉住那段终局文字也过那道字数闸。
func TestRenderResultObeysTheCharLimit(t *testing.T) {
	t.Parallel()
	report := RoundReport{
		Status:   RoundComplete,
		Summary:  strings.Repeat("长", 500),
		Evidence: []string{"测试全绿"},
	}
	text := renderResult(runResult{Status: RunComplete, RoundsStarted: 1, Report: report}, 64)
	if count := len([]rune(text)); count != 64 {
		t.Fatalf("该截到 64 个字，实际 %d", count)
	}
}

// TestRenderRoundFailureCarriesTheLastHandoff 钉住一次轮次失败把上一份交接带上。
//
// 那份交接是这次调用留下来的唯一成果：孩子在工作区上真干过的活儿还在那儿，
// 而这段 JSON 是父唯一能看见的、关于「干到哪儿了」的说明。
func TestRenderRoundFailureCarriesTheLastHandoff(t *testing.T) {
	t.Parallel()
	last := goodReport()
	text := renderRoundFailure(&roundFailure{
		round:      3,
		lastReport: &last,
		cause:      errors.New("孩子挂了"),
		maxChars:   DefaultMaxResultChars,
	})
	for _, want := range []string{
		"Ralph round 3 child failed before producing a structured report.",
		"Cause: 孩子挂了",
		"Last successful handoff:",
		"读了那三个文件",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("那段话里该有 %q，实际 %q", want, text)
		}
	}
}

// TestRenderRoundFailureSaysSoWhenThereIsNoHandoff 钉住第一轮就砸了的那句话。
//
// 那时候确实什么都没留下。明说「没有」比交回一段看起来像是丢了东西的空白强。
func TestRenderRoundFailureSaysSoWhenThereIsNoHandoff(t *testing.T) {
	t.Parallel()
	text := renderRoundFailure(&roundFailure{round: 1, maxChars: DefaultMaxResultChars})
	if !strings.Contains(text, "No previous handoff was available.") {
		t.Fatalf("该明说没有交接，实际 %q", text)
	}
	if strings.Contains(text, "Cause:") {
		t.Fatalf("没有原因时不该凭空写一行 Cause，实际 %q", text)
	}
}

// TestRoundFailureIsAnErrorCarryingItsCause 钉住它就是一个普通的 error，
// 而且拆得开。
//
// 新增: DSH 那边孩子失败的原因留在了 worker 线程里，父只知道「孩子挂了」。
// 这边它在手上，所以既进那段给模型的话，也从 [errors.Is] 那条路查得到。
func TestRoundFailureIsAnErrorCarryingItsCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("提供方开不出孩子")
	var err error = &roundFailure{round: 2, cause: cause, maxChars: DefaultMaxResultChars}
	if !errors.Is(err, cause) {
		t.Fatalf("该拆得出那个原因")
	}
	if !strings.Contains(err.Error(), "Ralph round 2 child failed") {
		t.Fatalf("Error() 该是那段排好的话，实际 %q", err.Error())
	}
}

// TestIndentReportIsReadable 钉住呈给模型的那份报告是缩进过的。
//
// 它是拿给人和模型直接读的，挤成一行就等于没呈。
func TestIndentReportIsReadable(t *testing.T) {
	t.Parallel()
	text := indentReport(goodReport())
	if !strings.Contains(text, "\n  \"status\": \"continue\"") {
		t.Fatalf("该是缩进两格的 JSON，实际 %q", text)
	}
	var back RoundReport
	if err := json.Unmarshal([]byte(text), &back); err != nil {
		t.Fatalf("排出来的东西该还解得回去：%v", err)
	}
}
