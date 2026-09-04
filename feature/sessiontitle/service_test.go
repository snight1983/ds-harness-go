// 本文件的作用：服务那套并发账的测试——兜底什么时候落地、一次用户改名怎么钉住
// 标题、一个跑得慢的生成器被取代之后为什么不许落地。
//
// 这里的写法有一条贯穿全篇的约定：所有会阻塞的生成器都靠 channel 交接，绝不
// sleep。一个靠 sleep 通过的并发测试只是在赌调度，慢一点的机器上它就是一个
// 随机失败的测试。

package sessiontitle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestNewRejectsABadConfig(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("空配置该被拒，得到 %v", err)
	}
}

func TestGetFoldsFromTheLogWithoutAnyState(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession(titleEvent(t, EventData{
		Title: "日志上的名字", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback},
	}))

	snapshot, ok, err := service.Get(sess)
	if err != nil || !ok {
		t.Fatalf("读出来是 ok=%v err=%v", ok, err)
	}
	if snapshot.Title != "日志上的名字" {
		t.Fatalf("读出来的是 %+v", snapshot)
	}
}

// [Service.Get] 不需要服务是活的：它纯粹是一次折叠，一份回放出来的日志照样答得上话。
func TestGetStillWorksAfterClose(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession(titleEvent(t, EventData{
		Title: "名字", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback},
	}))
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}

	if _, ok, err := service.Get(sess); err != nil || !ok {
		t.Fatalf("关掉之后读不出来：ok=%v err=%v", ok, err)
	}
}

func TestOnEventLandsTheFallbackTitle(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	sess.append(userEvent(t, "帮我把这段代码改成并发的"))
	service.OnEvent(sess, sess.Events()[0])

	snapshot, ok, err := service.Get(sess)
	if err != nil || !ok {
		t.Fatalf("兜底没落地：ok=%v err=%v", ok, err)
	}
	if snapshot.Source.Kind != SourceFallback {
		t.Fatalf("来路是 %q", snapshot.Source.Kind)
	}
	if len(snapshot.MessageSeqs) != 1 || snapshot.MessageSeqs[0] != 0 {
		t.Fatalf("引的 seq 是 %v", snapshot.MessageSeqs)
	}
}

func TestOnEventDoesNotOverwriteAnExistingTitle(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession(titleEvent(t, EventData{
		Title: "已经有的", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback},
	}))
	sess.append(userEvent(t, "后来又说了一句"))
	service.OnEvent(sess, sess.Events()[1])

	if titles := sess.titles(t); len(titles) != 1 {
		t.Fatalf("多写了标题：%v", titles)
	}
}

func TestOnEventIgnoresNonHumanMessages(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	sess.append(messageEvent(t, llm.PluginSource{Plugin: "p"}, llm.Content{llm.TextBlock{Text: "插件注入的一段话"}}))
	service.OnEvent(sess, sess.Events()[0])

	if titles := sess.titles(t); len(titles) != 0 {
		t.Fatalf("插件消息也起了名：%v", titles)
	}
}

func TestOnEventIsSilentAfterClose(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "一句话"))
	// 一个正在卸载的装配不该因为还有事件在路上而报错，也不该再往日志里写东西。
	service.OnEvent(sess, sess.Events()[0])
	if titles := sess.titles(t); len(titles) != 0 {
		t.Fatalf("关掉之后还在写：%v", titles)
	}
}

func TestRenamePinsTheTitle(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])

	snapshot, err := service.Rename(sess, "  我自己起的名字  ")
	if err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	if snapshot.Title != "我自己起的名字" {
		t.Fatalf("改完是 %q", snapshot.Title)
	}
	if snapshot.Source.Kind != SourceUser || len(snapshot.MessageSeqs) != 0 {
		t.Fatalf("来路不对：%+v", snapshot)
	}

	// 钉住了：后面再来的用户消息不再盖它。
	sess.append(userEvent(t, "又说了一句"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	after, _, err := service.Get(sess)
	if err != nil {
		t.Fatalf("读不出来：%v", err)
	}
	if after.Title != "我自己起的名字" {
		t.Fatalf("被盖掉了：%q", after.Title)
	}
}

func TestRenameRejectsATitleThatNormalizesToNothing(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()

	if _, err := service.Rename(sess, "\x1b[31m\x1b[0m   "); !errors.Is(err, ErrInvalidTitle) {
		t.Fatalf("该报 ErrInvalidTitle，得到 %v", err)
	}
	if titles := sess.titles(t); len(titles) != 0 {
		t.Fatalf("被拒的改名还是写进去了：%v", titles)
	}
}

func TestRenameTruncatesToTheByteBudget(t *testing.T) {
	t.Parallel()

	config := testConfig()
	config.MaxTitleBytes = 9
	config.FallbackMaxBytes = 9 // 兜底预算不许超过总上限，[Config.Validate] 会拦。
	service := newTestService(t, config)
	sess := newSession()

	snapshot, err := service.Rename(sess, "一二三四五六")
	if err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	if snapshot.Title != "一二三" {
		t.Fatalf("压进预算之后是 %q", snapshot.Title)
	}
}

func TestRenameRefusesWhenTheSessionIsNotLive(t *testing.T) {
	t.Parallel()

	config := testConfig()
	config.IsLive = func(Session) bool { return false }
	service := newTestService(t, config)

	if _, err := service.Rename(newSession(), "名字"); !errors.Is(err, ErrNotLive) {
		t.Fatalf("该报 ErrNotLive，得到 %v", err)
	}
}

func TestRenameRefusesAfterClose(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}

	if _, err := service.Rename(newSession(), "名字"); !errors.Is(err, ErrDisposed) {
		t.Fatalf("该报 ErrDisposed，得到 %v", err)
	}
}

func TestRenameSurfacesAnAppendFailure(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	sess.appendErr = errors.New("日志写不进去")

	if _, err := service.Rename(sess, "名字"); err == nil {
		t.Fatal("追加失败却报成功了")
	}
}

// [Service.Refresh] 是那把解钉的钥匙：一个被用户钉住的标题只有走这里才会被盖掉，
// 所以没有生成器的时候它也不是空操作。
func TestRefreshUnpinsAUserTitleWithTheFallback(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	sess.append(userEvent(t, "原始的那句话"))
	service.OnEvent(sess, sess.Events()[0])
	if _, err := service.Rename(sess, "钉住的名字"); err != nil {
		t.Fatalf("改名失败：%v", err)
	}

	snapshot, ok, err := service.Refresh(context.Background(), sess)
	if err != nil || !ok {
		t.Fatalf("刷新失败：ok=%v err=%v", ok, err)
	}
	if snapshot.Source.Kind != SourceFallback {
		t.Fatalf("刷完的来路是 %q", snapshot.Source.Kind)
	}
	if snapshot.Title != "原始的那句话" {
		t.Fatalf("刷完是 %q", snapshot.Title)
	}
}

func TestRefreshReportsNoMaterial(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())

	_, ok, err := service.Refresh(context.Background(), newSession())
	if err != nil {
		t.Fatalf("刷新报错：%v", err)
	}
	if ok {
		t.Fatal("一句素材都没有却说刷出来了")
	}
}

func TestRefreshRunsTheProviderSynchronously(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeFirstPrompt)
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])

	snapshot, ok, err := service.Refresh(context.Background(), sess)
	if err != nil || !ok {
		t.Fatalf("刷新失败：ok=%v err=%v", ok, err)
	}
	if snapshot.Title != "生成的标题" || snapshot.Source.Kind != SourceProvider {
		t.Fatalf("刷完是 %+v", snapshot)
	}
	if snapshot.Source.Provider != "p1" {
		t.Fatalf("生成器没记上：%+v", snapshot.Source)
	}
	if provider.callCount() != 1 {
		t.Fatalf("生成器被调了 %d 次", provider.callCount())
	}
}

func TestRefreshHonorsAnAlreadyCancelledContext(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := service.Refresh(ctx, newSession()); !errors.Is(err, context.Canceled) {
		t.Fatalf("该报取消，得到 %v", err)
	}
}

// 调用方取消自己那条 context 时，在途的生成必须跟着被取消——这正是
// [context.AfterFunc] 那个转发器要做的事。
func TestRefreshCancelsTheProviderWhenTheCallerCancels(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	entered := make(chan struct{})
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(ctx context.Context, _ ProviderRequest) (ProviderResult, error) {
		close(entered)
		<-ctx.Done()
		return ProviderResult{}, ctx.Err()
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := service.Refresh(ctx, sess)
		done <- err
	}()

	<-entered
	cancel()
	if err := <-done; err == nil {
		t.Fatal("取消之后该报错")
	}
}

func TestRegisterRefusesASecondProvider(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	if _, err := service.Register(newProvider("p1", ModeFirstPrompt)); err != nil {
		t.Fatalf("第一个登记失败：%v", err)
	}
	if _, err := service.Register(newProvider("p2", ModeFirstPrompt)); !errors.Is(err, ErrBadProvider) {
		t.Fatalf("第二个该被拒，得到 %v", err)
	}
}

func TestRegisterValidatesTheProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider Provider
	}{
		{"空 id", newProvider("", ModeFirstPrompt)},
		{"认不得的节奏", newProvider("p1", AutomaticMode("每小时一次"))},
		{"nil", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := newTestService(t, testConfig())
			if _, err := service.Register(test.provider); !errors.Is(err, ErrBadProvider) {
				t.Fatalf("该被拒，得到 %v", err)
			}
		})
	}
}

func TestUnregisterFreesTheSlot(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	dispose, err := service.Register(newProvider("p1", ModeFirstPrompt))
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	dispose()
	dispose() // 幂等。

	if _, err := service.Register(newProvider("p2", ModeFirstPrompt)); err != nil {
		t.Fatalf("注销之后该登记得上：%v", err)
	}
}

// 自动生成的完整一趟：一条用户消息排上活，一条请求头把它发出去。
func TestAutomaticGenerationRunsAfterTheRequestHeaderLands(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(_ context.Context, request ProviderRequest) (ProviderResult, error) {
		if request.Route == nil || request.Route.Model != "mod" {
			t.Errorf("生成器读到的路由是 %+v", request.Route)
		}
		if len(request.Messages) != 1 || request.Messages[0].Text != "第一句" {
			t.Errorf("生成器读到的消息是 %+v", request.Messages)
		}
		return ProviderResult{Title: "模型起的名字", MessageSeqs: []int{request.Messages[0].Seq}}, nil
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	appended := sess.watchAppends()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	// 排上了，但还没发出去：这时候立着的只该是兜底。
	if snapshot, _, _ := service.Get(sess); snapshot.Source.Kind != SourceFallback {
		t.Fatalf("请求头之前立着的是 %+v", snapshot)
	}

	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	if data := waitForTitle(t, appended, SourceProvider); data.Title != "模型起的名字" {
		t.Fatalf("落下来的是 %+v", data)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}

	snapshot, ok, err := service.Get(sess)
	if err != nil || !ok {
		t.Fatalf("读不出来：ok=%v err=%v", ok, err)
	}
	if snapshot.Title != "模型起的名字" || snapshot.Source.Provider != "p1" {
		t.Fatalf("最后立着的是 %+v", snapshot)
	}
}

// 分叉出来的会话不走「第一条消息就起名」那一档：它的第一条消息前面还压着一整段
// 继承来的历史，拿它起名会得到一个和上下文对不上的名字。
func TestFirstPromptModeSkipsAForkedSession(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeFirstPrompt)
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.header.ParentSession = "父会话"
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("分叉会话也排了期，生成器被调了 %d 次", provider.callCount())
	}
}

func TestAllPromptsModeReschedulesOnEveryMessage(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeAllPrompts)
	provider.generate = func(_ context.Context, request ProviderRequest) (ProviderResult, error) {
		last := request.Messages[len(request.Messages)-1]
		return ProviderResult{Title: "第 " + last.Text, MessageSeqs: []int{last.Seq}}, nil
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	appended := sess.watchAppends()
	for _, text := range []string{"一", "二"} {
		sess.append(userEvent(t, text))
		service.OnEvent(sess, sess.Events()[len(sess.Events())-1])
		sess.append(headerEvent(t, "prov", "mod"))
		service.OnEvent(sess, sess.Events()[len(sess.Events())-1])
		// 每一轮都等这一轮的产出落盘再进下一轮：这一档就是要看后一次盖掉前一次。
		if data := waitForTitle(t, appended, SourceProvider); data.Title != "第 "+text {
			t.Fatalf("这一轮落下来的是 %+v", data)
		}
	}

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if snapshot, _, _ := service.Get(sess); snapshot.Title != "第 二" {
		t.Fatalf("最后立着的是 %+v", snapshot)
	}
}

// 一个跑了很久的旧生成器不许把结果盖到一个更新的标题上面。这是整套版本号机制
// 存在的唯一理由。
func TestASupersededProviderResultIsRejected(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	entered := make(chan struct{})
	release := make(chan struct{})
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(_ context.Context, request ProviderRequest) (ProviderResult, error) {
		close(entered)
		<-release
		return ProviderResult{Title: "迟到的名字", MessageSeqs: []int{request.Messages[0].Seq}}, nil
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	<-entered
	// 生成器还卡在里面的时候，用户自己改了名——这一下把它取代掉。
	if _, err := service.Rename(sess, "用户定的名字"); err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	close(release)

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	snapshot, _, err := service.Get(sess)
	if err != nil {
		t.Fatalf("读不出来：%v", err)
	}
	if snapshot.Title != "用户定的名字" {
		t.Fatalf("迟到的结果盖上去了：%+v", snapshot)
	}
}

// 一次注销必须**等**在途的活退干净：不等的话，注销返回之后仍然可能有一个旧
// 生成器的结果落进日志。
func TestUnregisterWaitsForTheInFlightGeneration(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	entered := make(chan struct{})
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(ctx context.Context, _ ProviderRequest) (ProviderResult, error) {
		close(entered)
		<-ctx.Done()
		return ProviderResult{}, ctx.Err()
	}
	dispose, err := service.Register(provider)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	<-entered
	// 注销会取消它那条 context，生成器于是退出；dispose 等到那一刻才返回。
	dispose()

	// 注销返回之后，那一格必须已经空了。
	if _, err := service.Register(newProvider("p2", ModeFirstPrompt)); err != nil {
		t.Fatalf("注销返回之后该登记得上：%v", err)
	}
}

func TestCloseCancelsAndWaitsForInFlightGenerations(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	entered := make(chan struct{})
	returned := make(chan struct{})
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(ctx context.Context, _ ProviderRequest) (ProviderResult, error) {
		close(entered)
		<-ctx.Done()
		close(returned)
		return ProviderResult{}, ctx.Err()
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	<-entered
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	select {
	case <-returned:
	default:
		t.Fatal("Close 返回时那次生成还没退干净")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("再关一次该是空操作：%v", err)
	}
}

func TestOnSessionDisposedDropsTheWork(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeFirstPrompt)
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])
	service.OnSessionDisposed(sess)

	// 排着的活跟着那一格并发账一起没了，后来的请求头发不出任何东西。
	sess.append(headerEvent(t, "prov", "mod"))
	service.OnEvent(sess, sess.Events()[len(sess.Events())-1])

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("会话销毁之后生成器还是被调了 %d 次", provider.callCount())
	}
}

// 一次路由没变的请求不会往日志里写新的 request/header，[Service.OnEvent] 那条路
// 于是等不到自己要的东西。这条钩子补上那个缺口。
func TestOnMainRequestStartsPendingWorkWhenNoNewHeaderLands(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeAllPrompts)
	provider.generate = func(_ context.Context, request ProviderRequest) (ProviderResult, error) {
		return ProviderResult{Title: "钩子发出来的", MessageSeqs: []int{request.Messages[len(request.Messages)-1].Seq}}, nil
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	// 第一轮：路由已经落进日志了。
	sess := newSession()
	appended := sess.watchAppends()
	sess.append(headerEvent(t, "prov", "mod"))
	sess.append(userEvent(t, "第二句"))
	service.OnEvent(sess, sess.Events()[1])
	// 新的一步开始了，但路由没变，所以没有新的 request/header。
	sess.append(stepStartEvent(t, 1, 0))

	service.OnMainRequest(sess, llm.GenerateOptions{
		SessionID: llm.SessionID(sess.ID()), Provider: "prov", Model: "mod", AgentLoop: true,
	})

	waitForTitle(t, appended, SourceProvider)
	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if snapshot, _, _ := service.Get(sess); snapshot.Title != "钩子发出来的" {
		t.Fatalf("最后立着的是 %+v", snapshot)
	}
}

func TestOnMainRequestIgnoresNonAgentLoopRequests(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeAllPrompts)
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(headerEvent(t, "prov", "mod"))
	sess.append(userEvent(t, "第二句"))
	service.OnEvent(sess, sess.Events()[1])
	sess.append(stepStartEvent(t, 1, 0))

	// 一次辅助调用（不是主循环）不该把标题生成发出去。
	service.OnMainRequest(sess, llm.GenerateOptions{
		SessionID: llm.SessionID(sess.ID()), Provider: "prov", Model: "mod", AgentLoop: false,
	})

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("辅助调用也发出去了，生成器被调了 %d 次", provider.callCount())
	}
}

func TestOnMainRequestWaitsForAStepStartBoundary(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeAllPrompts)
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(headerEvent(t, "prov", "mod"))
	sess.append(userEvent(t, "第二句"))
	service.OnEvent(sess, sess.Events()[1])
	// 日志上最后一个步骤边界是 step/end：上一步已经收了，新的一步还没开。
	sess.append(stepEndEvent(t, 0, 0))

	service.OnMainRequest(sess, llm.GenerateOptions{
		SessionID: llm.SessionID(sess.ID()), Provider: "prov", Model: "mod", AgentLoop: true,
	})

	if err := service.Close(); err != nil {
		t.Fatalf("关不掉：%v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("边界不对却发出去了，生成器被调了 %d 次", provider.callCount())
	}
}

func TestValidateResultRejectsBadOutput(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	messages := []UserMessage{{Seq: 1, Text: "一"}, {Seq: 4, Text: "二"}}

	tests := []struct {
		name   string
		result ProviderResult
	}{
		{"标题洗完是空的", ProviderResult{Title: "\x1b[0m", MessageSeqs: []int{1}}},
		{"一条 seq 都没引", ProviderResult{Title: "名字"}},
		{"引了快照外的 seq", ProviderResult{Title: "名字", MessageSeqs: []int{2}}},
		{"引重了", ProviderResult{Title: "名字", MessageSeqs: []int{1, 1}}},
		{"顺序反了", ProviderResult{Title: "名字", MessageSeqs: []int{4, 1}}},
		{"负数 seq", ProviderResult{Title: "名字", MessageSeqs: []int{-1}}},
		{"model 缺一半", ProviderResult{
			Title: "名字", MessageSeqs: []int{1}, Model: &ModelProvenance{Provider: "p"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.validateResult(test.result, messages); !errors.Is(err, ErrBadProvider) {
				t.Fatalf("该被拒，得到 %v", err)
			}
		})
	}
}

func TestValidateResultAcceptsAndNormalizes(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	messages := []UserMessage{{Seq: 1, Text: "一"}, {Seq: 4, Text: "二"}}

	accepted, err := service.validateResult(ProviderResult{
		Title:       "  \x1b[31m名字\x1b[0m  ",
		MessageSeqs: []int{1, 4},
		Model:       &ModelProvenance{Provider: "prov", Model: "mod"},
	}, messages)
	if err != nil {
		t.Fatalf("该被接受：%v", err)
	}
	if accepted.Title != "名字" {
		t.Fatalf("归一化之后是 %q", accepted.Title)
	}
	if len(accepted.MessageSeqs) != 2 || accepted.MessageSeqs[1] != 4 {
		t.Fatalf("引的 seq 是 %v", accepted.MessageSeqs)
	}
	if accepted.Model == nil || accepted.Model.Model != "mod" {
		t.Fatalf("模型出处是 %+v", accepted.Model)
	}
}

// 生成器产出的模型出处必须原样记进标题，那是事后回答「这个名字是谁起的」的凭据。
func TestProviderModelProvenanceIsRecorded(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	provider := newProvider("p1", ModeFirstPrompt)
	provider.generate = func(_ context.Context, request ProviderRequest) (ProviderResult, error) {
		return ProviderResult{
			Title:       "名字",
			MessageSeqs: []int{request.Messages[0].Seq},
			Model:       &ModelProvenance{Provider: "辅助路由", Model: "小模型"},
		}, nil
	}
	if _, err := service.Register(provider); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession()
	sess.append(userEvent(t, "第一句"))
	service.OnEvent(sess, sess.Events()[0])

	snapshot, _, err := service.Refresh(context.Background(), sess)
	if err != nil {
		t.Fatalf("刷新失败：%v", err)
	}
	if snapshot.Source.Model == nil || snapshot.Source.Model.Model != "小模型" {
		t.Fatalf("模型出处是 %+v", snapshot.Source.Model)
	}
}

func TestRouteOfReadsTheCurrentHeader(t *testing.T) {
	t.Parallel()

	route, err := routeOf(nil)
	if err != nil {
		t.Fatalf("空日志报错：%v", err)
	}
	if route != nil {
		t.Fatalf("空日志折出了路由：%+v", route)
	}

	sess := newSession(headerEvent(t, "prov", "mod"))
	route, err = routeOf(sess.Events())
	if err != nil {
		t.Fatalf("折路由报错：%v", err)
	}
	if route == nil || route.Provider != "prov" || route.Model != "mod" {
		t.Fatalf("折出来的是 %+v", route)
	}
}

func TestLastStepBoundary(t *testing.T) {
	t.Parallel()

	if _, ok := lastStepBoundary(nil); ok {
		t.Fatal("空日志找出了边界")
	}

	sess := newSession(stepStartEvent(t, 0, 0), stepEndEvent(t, 0, 0), userEvent(t, "一句"))
	boundary, ok := lastStepBoundary(sess.Events())
	if !ok || boundary.Type != sessionlog.EventStepEnd {
		t.Fatalf("找出来的是 %+v ok=%v", boundary, ok)
	}
}

// 并发地往同一个会话上推事件时，兜底标题只许落一次。这条盯的是
// ensureFallbackLocked 那次「读当前标题」和「追加」之间没有让出的机会。
func TestConcurrentUserMessagesLandExactlyOneFallback(t *testing.T) {
	t.Parallel()

	service := newTestService(t, testConfig())
	sess := newSession()
	for index := 0; index < 8; index++ {
		sess.append(userEvent(t, "第"+strings.Repeat("一", index+1)+"句"))
	}
	events := sess.Events()

	var wait sync.WaitGroup
	for _, event := range events {
		wait.Add(1)
		go func() {
			defer wait.Done()
			service.OnEvent(sess, event)
		}()
	}
	wait.Wait()

	if titles := sess.titles(t); len(titles) != 1 {
		t.Fatalf("兜底落了 %d 次：%v", len(titles), titles)
	}
}
