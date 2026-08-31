// 本文件的作用：缓存本身——建得出来建不出来、两级读各自看到什么、
// 那三个写触发点真的写没写，以及每一条 fail-soft 路径咽下去的时候留没留下痕迹。
//
// 源: packages/session/session-projection-cache/src/index.ts:36-297

package projectioncache

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"ds-harness-go/session"
	"ds-harness-go/session/persistence"
	"ds-harness-go/session/projection"
	"ds-harness-go/storage/domain"
	"ds-harness-go/storage/storagetest"
)

func TestNewRefusesEveryMissingPiece(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		spoil func(*Options)
		want  string
	}{
		"没有投影单元表": {
			spoil: func(o *Options) { o.Registry = nil },
			want:  "投影单元表",
		},
		"没有会话存储": {
			spoil: func(o *Options) { o.Store = nil },
			want:  "会话存储",
		},
		"没有落盘屏障": {
			// 见 [Options.Flush]：没有它，缓存可能跑到日志前面去。
			spoil: func(o *Options) { o.Flush = nil },
			want:  "落盘屏障",
		},
		"写间隔事件数是零": {
			spoil: func(o *Options) { o.WriteEveryEvents = 0 },
			want:  "WriteEveryEvents",
		},
		"写间隔事件数是负数": {
			spoil: func(o *Options) { o.WriteEveryEvents = -1 },
			want:  "WriteEveryEvents",
		},
		"写间隔是零": {
			spoil: func(o *Options) { o.WriteInterval = 0 },
			want:  "WriteInterval",
		},
		"写间隔是负数": {
			spoil: func(o *Options) { o.WriteInterval = -time.Second },
			want:  "WriteInterval",
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			options := validOptions(f)
			item.spoil(&options)

			_, err := New(openDomain(t, Spec()), options)
			if err == nil {
				t.Fatalf("该拒掉")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("错误里该说清缺的是什么，实际是 %q", err.Error())
			}
		})
	}
}

func TestNewRefusesWithoutAnOpenedDomain(t *testing.T) {
	t.Parallel()

	// 域由装配方打开、也由装配方关闭（见包文档第 1 条），所以「没给域」
	// 是一个必须当场说清的装配错误，而不是一个之后才空指针的字段。
	f := newFixture(t, 1, time.Second)
	_, err := New(nil, validOptions(f))
	if err == nil || !strings.Contains(err.Error(), "已经打开的域") {
		t.Fatalf("该拒掉并说清：%v", err)
	}
}

func TestNewRefusesADomainThatWasNotOpenedFromThisSpec(t *testing.T) {
	t.Parallel()

	// 这一条正是 [New] 收「已经打开的域」而不是自己打开的理由：域对不对
	// 由取表那一步当场核对出来，而不是留下一道谁也走不到的 requireTable 分支。
	cases := map[string]domain.Spec{
		"根本没有这张表":   otherSpec(),
		"表名对得上类型不对": wrongTypeSpec(),
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			if _, err := New(openDomain(t, spec), validOptions(f)); err == nil {
				t.Fatalf("该拒掉")
			}
		})
	}
}

func TestNewFillsInTheTwoOptionalPieces(t *testing.T) {
	t.Parallel()

	// 后台 context 和 logger 都可以留空：前者退到 context.Background()，
	// 后者退到 slog.Default()。留空不是丢弃，见 [Options.Logger]。
	f := newFixture(t, 1, time.Second)
	options := validOptions(f)
	options.Background, options.Logger = nil, nil

	cache, err := New(openDomain(t, Spec()), options)
	if err != nil {
		t.Fatalf("两个可选项留空该照样建得出来：%v", err)
	}
	defer cache.Close()

	if cache.background == nil || cache.logger == nil {
		t.Fatalf("留空该被填上，不是留一个 nil：%v %v", cache.background, cache.logger)
	}
}

func TestCachedSnapshotServesNothingWhenThereIsNoUsableRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		// arrange 摆好介质，并给出这次要读的那份头。
		arrange func(t *testing.T, f *fixture) session.SessionHeader
	}{
		"介质上压根没有这条记录": {
			arrange: func(_ *testing.T, _ *fixture) session.SessionHeader {
				return session.SessionHeader{ID: "s1", CreatedAt: 7}
			},
		},
		"记录绑的是另一段日志": {
			arrange: func(t *testing.T, f *fixture) session.SessionHeader {
				mustRegister(t, f.registry, countUnit("count", 0))
				live := newLive("s1", 7, userEvent(0))
				if err := f.cache.Write(context.Background(), live); err != nil {
					t.Fatalf("写不该失败：%v", err)
				}
				// 同一个 id 被删掉重建：建会话时刻变了，旧记录整条作废。
				return session.SessionHeader{ID: "s1", CreatedAt: 8}
			},
		},
		"有记录但一行都端不出来": {
			arrange: func(t *testing.T, f *fixture) session.SessionHeader {
				// 只给宿主看的单元进检查点但不进读切，所以记录在、值是空的。
				mustRegister(t, f.registry, hostOnlyUnit("hidden", 0))
				live := newLive("s1", 7, userEvent(0))
				if err := f.cache.Write(context.Background(), live); err != nil {
					t.Fatalf("写不该失败：%v", err)
				}
				return live.Header()
			},
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			meta := item.arrange(t, f)

			snapshot, err := f.cache.CachedSnapshot(meta)
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if snapshot != nil {
				t.Fatalf("该是「没有可用的记录」：%#v", snapshot)
			}
		})
	}
}

func TestCachedSnapshotTakesTheLowestWatermarkAmongTheRowsItServes(t *testing.T) {
	t.Parallel()

	// 整块只带**一个**水位：所有被端出来的行里最低的那个。少报是安全的
	//（客户端按高 seq 覆盖低 seq），多报会让一个旧值压住一次推送。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("early", 0))
	mustRegister(t, f.registry, countUnit("late", 0))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 7}
	if err := f.table.Put(context.Background(), "s1", Record{
		Identity: IdentityOf(meta),
		Rows: projection.Checkpoint{
			"early": countRow(t, 0, 3, 4),
			"late":  countRow(t, 0, 9, 10),
		},
	}); err != nil {
		t.Fatalf("摆记录不该失败：%v", err)
	}

	snapshot, err := f.cache.CachedSnapshot(meta)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if snapshot == nil {
		t.Fatalf("该端出一份读切")
	}
	if snapshot.AsOfSeq != 3 {
		t.Fatalf("水位该取最低的那个：%d", snapshot.AsOfSeq)
	}
	if snapshot.Values["early"] != 4 || snapshot.Values["late"] != 10 {
		t.Fatalf("两个键都该端出来：%#v", snapshot.Values)
	}
}

func TestCachedSnapshotSkipsRowsWhoseStateVersionMovedOn(t *testing.T) {
	t.Parallel()

	// 版本对不上就丢掉那一行，不迁移——这一层一个单元的状态语义都不认识。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 1))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 7}
	if err := f.table.Put(context.Background(), "s1", Record{
		Identity: IdentityOf(meta),
		Rows:     projection.Checkpoint{"count": countRow(t, 0, 3, 4)},
	}); err != nil {
		t.Fatalf("摆记录不该失败：%v", err)
	}

	snapshot, err := f.cache.CachedSnapshot(meta)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if snapshot != nil {
		t.Fatalf("旧版本的行不该被端出来：%#v", snapshot)
	}
}

func TestCachedSnapshotSurfacesAStorageFailure(t *testing.T) {
	t.Parallel()

	// 零 I/O 那一级也读介质（读的是已经在内存里的那份），域关了就得说出来
	// ——而不是安静地返回「没有记录」，让调用方以为这只是还没热起来。
	f := newFixture(t, 1, time.Second)
	closeFixtureDomain(t, f)

	if _, err := f.cache.CachedSnapshot(session.SessionHeader{ID: "s1", CreatedAt: 7}); err == nil {
		t.Fatalf("该把读失败报上来")
	}
}

func TestColdSnapshotSurfacesAFailureOnTheRecordRead(t *testing.T) {
	t.Parallel()

	// 冷读的第一步是读那条缓存行。它砸了不能当成「没有行」——那样会让一次
	// 介质故障静悄悄地退化成一次整读，而整读的结果看上去完全正常。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))
	f.store.put(session.SessionHeader{ID: "s1", CreatedAt: 7}, userEvent(0))
	closeFixtureDomain(t, f)

	if _, err := f.cache.ColdSnapshot(context.Background(), "s1"); err == nil {
		t.Fatalf("该把读失败报上来")
	}
	if reads := f.store.reads(); len(reads) != 0 {
		t.Fatalf("读行就砸了，日志一条都不该读：%v", reads)
	}
}

func TestColdSnapshotProbesTheLogEvenWithNoUnitRegistered(t *testing.T) {
	t.Parallel()

	// 一个单元都没登记就没有可折的东西，但「没有这个会话」这条契约在这条路上
	// 也得成立，所以照样探一次。
	cases := map[string]struct {
		events []session.Event
		want   int
	}{
		"空日志":   {want: -1},
		"日志有三条": {events: []session.Event{userEvent(0), otherEvent(1), userEvent(2)}, want: 2},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			f.store.put(session.SessionHeader{ID: "s1", CreatedAt: 7}, item.events...)

			snapshot, err := f.cache.ColdSnapshot(context.Background(), "s1")
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if snapshot.AsOfSeq != item.want {
				t.Fatalf("水位该切在日志末尾：%d", snapshot.AsOfSeq)
			}
			if len(snapshot.Values) != 0 {
				t.Fatalf("没有单元就没有值：%#v", snapshot.Values)
			}
			if reads := f.store.reads(); len(reads) != 1 || reads[0] != 0 {
				t.Fatalf("该正好探一次、从 0 起：%v", reads)
			}
		})
	}
}

func TestColdSnapshotPassesStorageFailuresStraightThrough(t *testing.T) {
	t.Parallel()

	// 存储那一侧的失败原样穿过去：缓存对「这个会话在不在」没有自己的说法。
	cases := map[string]struct {
		register func(t *testing.T, f *fixture)
	}{
		"一个单元都没登记（探测那条路）": {
			register: func(*testing.T, *fixture) {},
		},
		"登记了单元（读尾巴那条路）": {
			register: func(t *testing.T, f *fixture) { mustRegister(t, f.registry, countUnit("count", 0)) },
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			item.register(t, f)

			// 存储里根本没有这个会话。
			_, err := f.cache.ColdSnapshot(context.Background(), "s1")
			if !errors.Is(err, persistence.ErrSessionNotFound) {
				t.Fatalf("该原样穿过来：%v", err)
			}
		})
	}
}

func TestColdSnapshotFoldsOnlyTheTailAboveAUsableRow(t *testing.T) {
	t.Parallel()

	// 这是这一层存在的全部理由：不读整份日志，从上一次检查点的水位往下读一截。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 7}
	f.store.put(meta, userEvent(0), userEvent(1), userEvent(2), userEvent(3))
	if err := f.table.Put(context.Background(), "s1", Record{
		Identity: IdentityOf(meta),
		Rows:     projection.Checkpoint{"count": countRow(t, 0, 1, 2)},
	}); err != nil {
		t.Fatalf("摆记录不该失败：%v", err)
	}

	snapshot, err := f.cache.ColdSnapshot(context.Background(), "s1")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if snapshot.AsOfSeq != 3 || snapshot.Values["count"] != 4 {
		t.Fatalf("该从行上接着折那一截尾巴：%d %#v", snapshot.AsOfSeq, snapshot.Values)
	}
	// 地板比行的水位低一条，见 [projection.Registry.RestoreFloor]。
	if reads := f.store.reads(); len(reads) != 1 || reads[0] != 1 {
		t.Fatalf("该只读一次、从地板 1 起：%v", reads)
	}

	// 折完写回去，于是下一次冷读起点更近。
	row := f.record(t, "s1").Rows["count"]
	if row.Seq != 3 || string(row.Val) != `{"count":4}` {
		t.Fatalf("刷新出来的行该落回介质：%#v %s", row, row.Val)
	}
}

func TestColdSnapshotWithoutARecordReadsTheWholeLogAndSeedsOne(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 7}
	f.store.put(meta, userEvent(0), otherEvent(1), userEvent(2))

	snapshot, err := f.cache.ColdSnapshot(context.Background(), "s1")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if snapshot.AsOfSeq != 2 || snapshot.Values["count"] != 2 {
		t.Fatalf("没有行就整读一遍：%d %#v", snapshot.AsOfSeq, snapshot.Values)
	}
	if got := f.record(t, "s1").Identity; got != IdentityOf(meta) {
		t.Fatalf("写回去的记录该绑在这段日志的身份上：%#v", got)
	}
}

func TestColdSnapshotDiscardsARecordBoundToAnotherLifetime(t *testing.T) {
	t.Parallel()

	// 会话 id 是一个槽位不是一段生命。这条记录能通过所有版本和水位检查，
	// 只有身份见证拦得住它——拦不住就是把一段毫无关系的日志折出来的状态
	// 当成这个会话的当前值端出去。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 8}
	f.store.put(meta, userEvent(0), userEvent(1))
	if err := f.table.Put(context.Background(), "s1", Record{
		// 上一段生命留下的记录：建会话时刻对不上，计数还是个大数。
		Identity: Identity{CreatedAt: 7},
		Rows:     projection.Checkpoint{"count": countRow(t, 0, 1, 999)},
	}); err != nil {
		t.Fatalf("摆记录不该失败：%v", err)
	}

	snapshot, err := f.cache.ColdSnapshot(context.Background(), "s1")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if snapshot.Values["count"] != 2 {
		t.Fatalf("该从头重折，而不是拿那段无关的状态：%#v", snapshot.Values)
	}
	// 一次整读（第二次读），加上之前那次按地板读的尾巴。
	if reads := f.store.reads(); len(reads) != 2 || reads[1] != 0 {
		t.Fatalf("回退那一读该从 0 起：%v", reads)
	}
	if got := f.record(t, "s1").Identity; got != IdentityOf(meta) {
		t.Fatalf("写回去的记录该改绑到当前这段日志上：%#v", got)
	}
	if messages := f.sink.messages(); len(messages) != 1 ||
		!strings.Contains(messages[0], "退回整读") {
		t.Fatalf("回退必须留下痕迹（见包文档第 4 条）：%v", messages)
	}
	if reason := f.sink.attr(0, "error"); !strings.Contains(reason, "日志身份") {
		t.Fatalf("痕迹里该写清回退的理由：%q", reason)
	}
}

func TestColdSnapshotFallsBackWhenTheRowItselfIsBroken(t *testing.T) {
	t.Parallel()

	// 一行版本对得上却折不出来，说明**这个构建自己**写坏了它。
	// [projection.Registry.Restore] 忠实地把这次失败报上来，缓存这一层
	// 照旧回退（一个缓存不该因为自己坏了就让读失败），但必须留下痕迹。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, unmarshalableUnit("bad"))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 7}
	f.store.put(meta, userEvent(0))

	_, err := f.cache.ColdSnapshot(context.Background(), "s1")
	if err == nil {
		t.Fatalf("回退之后还是折不出来，就该报上去")
	}
	if messages := f.sink.messages(); len(messages) != 1 ||
		!strings.Contains(messages[0], "退回整读") {
		t.Fatalf("回退必须留下痕迹：%v", messages)
	}
	if reads := f.store.reads(); len(reads) != 2 {
		t.Fatalf("该读两次：按地板一次、回退整读一次：%v", reads)
	}
}

func TestColdSnapshotSurfacesAFailureOnTheFallbackRead(t *testing.T) {
	t.Parallel()

	// 回退是最后一级阶梯：它再砸就没有更慢但更对的路了，只能报上去。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	meta := session.SessionHeader{ID: "s1", CreatedAt: 8}
	f.store.put(meta, userEvent(0))
	if err := f.table.Put(context.Background(), "s1", Record{
		Identity: Identity{CreatedAt: 7},
		Rows:     projection.Checkpoint{"count": countRow(t, 0, 0, 1)},
	}); err != nil {
		t.Fatalf("摆记录不该失败：%v", err)
	}

	boom := errors.New("介质塌了")
	f.store.failWith(1, boom)

	if _, err := f.cache.ColdSnapshot(context.Background(), "s1"); !errors.Is(err, boom) {
		t.Fatalf("该报上来：%v", err)
	}
}

func TestColdSnapshotStillServesWhenTheWriteBackFails(t *testing.T) {
	t.Parallel()

	// 写回去是一条优化，不是这次读的一部分：写丢了只意味着下一次冷读要多折
	// 一段尾巴，所以它绝不该让这次读失败。但它得留下痕迹。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	// 建会话时刻是负数，写回去时过不了 [ValidateRecord]。
	f.store.put(session.SessionHeader{ID: "s1", CreatedAt: -1}, userEvent(0), userEvent(1))

	snapshot, err := f.cache.ColdSnapshot(context.Background(), "s1")
	if err != nil {
		t.Fatalf("写回失败不该让这次读失败：%v", err)
	}
	if snapshot.Values["count"] != 2 {
		t.Fatalf("值该照样端出来：%#v", snapshot.Values)
	}
	if messages := f.sink.messages(); len(messages) != 1 ||
		!strings.Contains(messages[0], "写检查点失败") {
		t.Fatalf("咽下去也要留痕迹：%v", messages)
	}
	if trigger := f.sink.attr(0, "trigger"); trigger != "冷读之后写回" {
		t.Fatalf("痕迹里该写清是哪个触发点：%q", trigger)
	}
}

func TestWriteLandsARecordBoundToThisLog(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))
	mustRegister(t, f.registry, hostOnlyUnit("hidden", 0))

	live := newLive("s1", 7, userEvent(0), otherEvent(1), userEvent(2))
	if err := f.cache.Write(context.Background(), live); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	stored := f.record(t, "s1")
	if stored.Identity != IdentityOf(live.Header()) {
		t.Fatalf("记录该绑在这段日志的身份上：%#v", stored.Identity)
	}
	// 只给宿主看的单元不进读切，但照样进检查点。
	if len(stored.Rows) != 2 {
		t.Fatalf("每个已登记的单元都该有一行：%#v", stored.Rows)
	}
	if row := stored.Rows["count"]; row.Seq != 2 || string(row.Val) != `{"count":2}` {
		t.Fatalf("行不对：%#v %s", row, row.Val)
	}
}

func TestWriteFlushesTheLogBeforeItWritesTheRecord(t *testing.T) {
	t.Parallel()

	// 这是一道**落盘屏障**，不是可选优化：切面里的每一条事件都必须先于这条
	// 缓存行落到耐久上，否则介质上会出现一段「任何已存日志都不包含的事件」
	// 折出来的幽灵状态，而它会带着一个完全正常的水位堂堂正正地被端出去。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))

	sawRecord := true
	f.onFlush(func(LiveSession) error {
		_, ok, err := f.table.Get("s1")
		if err != nil {
			t.Errorf("探针读不该失败：%v", err)
		}
		sawRecord = ok
		return nil
	})

	live := newLive("s1", 7, userEvent(0))
	if err := f.cache.Write(context.Background(), live); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if sawRecord {
		t.Fatalf("屏障跑到的时候记录还不该在介质上")
	}
	if flushes := f.flushes(); len(flushes) != 1 || flushes[0] != "s1" {
		t.Fatalf("屏障该正好被调一次：%v", flushes)
	}
}

func TestWriteStopsAtTheFirstFailureWithoutTouchingTheMedium(t *testing.T) {
	t.Parallel()

	boom := errors.New("刷不动")

	cases := map[string]struct {
		arrange   func(t *testing.T, f *fixture)
		wantFlush int
	}{
		"取切面就砸了（屏障都轮不到）": {
			arrange: func(t *testing.T, f *fixture) {
				mustRegister(t, f.registry, unmarshalableUnit("bad"))
			},
			wantFlush: 0,
		},
		"屏障砸了（记录不许写下去）": {
			arrange: func(t *testing.T, f *fixture) {
				mustRegister(t, f.registry, countUnit("count", 0))
				f.failFlush(boom)
			},
			wantFlush: 0,
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Second)
			item.arrange(t, f)

			if err := f.cache.Write(context.Background(), newLive("s1", 7, userEvent(0))); err == nil {
				t.Fatalf("该报上来")
			}
			if _, ok, err := f.table.Get("s1"); err != nil || ok {
				t.Fatalf("介质上不该留下记录：%v %v", ok, err)
			}
			if got := len(f.flushes()); got != item.wantFlush {
				t.Fatalf("屏障成功次数该是 %d，实际 %d", item.wantFlush, got)
			}
		})
	}
}

func TestWriteSurfacesAMediumFailure(t *testing.T) {
	t.Parallel()

	// [Cache.Write] **不是** fail-soft 的：走 fail-soft 那几条路的调用方
	// 自己把失败咽掉，它本身把失败交回去。
	f := newFixture(t, 1, time.Second)
	mustRegister(t, f.registry, countUnit("count", 0))
	closeFixtureDomain(t, f)

	if err := f.cache.Write(context.Background(), newLive("s1", 7, userEvent(0))); err == nil {
		t.Fatalf("该报上来")
	}
}

func TestObserveWritesAtTheEndOfEveryTurn(t *testing.T) {
	t.Parallel()

	// 回合结束是必写点：多数读要的就是回合结束时那份值。
	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0), turnEndEvent(1))
	f.cache.Observe(live, live.events[1])

	waitFor(t, "回合结束触发的那次写", func() bool { return f.stored("s1") })
	if flushes := f.flushes(); len(flushes) != 1 || flushes[0] != "s1" {
		t.Fatalf("屏障该正好被调一次：%v", flushes)
	}
	if row := f.record(t, "s1").Rows["count"]; row.Seq != 1 {
		t.Fatalf("写下去的该是这一刻的切面：%#v", row)
	}
}

func TestObserveThrottlesMidTurnEventsByCount(t *testing.T) {
	t.Parallel()

	// 计数阈值管的是回合中间那一串。攒够之前一次都不写。
	f := newFixture(t, 3, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0), userEvent(1), userEvent(2))
	f.cache.Observe(live, live.events[0])
	f.cache.Observe(live, live.events[1])

	if _, ok, err := f.table.Get("s1"); err != nil || ok {
		t.Fatalf("攒够之前不该写：%v %v", ok, err)
	}

	f.cache.Observe(live, live.events[2])
	waitFor(t, "攒够事件触发的那次写", func() bool { return f.stored("s1") })

	// 写完记账清零，所以下一条又从头攒。
	f.cache.Observe(live, live.events[0])
	if got := f.cache.pending("s1"); got != 1 {
		t.Fatalf("写完该从零重新攒，实际攒到 %d", got)
	}
}

func TestObserveThrottlesMidTurnEventsByInterval(t *testing.T) {
	t.Parallel()

	// 间隔管的是另一种脏法：事件不多但间隔很长（比如一次跑了十分钟的工具调用），
	// 光靠计数永远不触发。
	f := newFixture(t, 1000, time.Millisecond)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0))
	f.cache.Observe(live, live.events[0])

	waitFor(t, "间隔到点触发的那次写", func() bool { return f.stored("s1") })
	if row := f.record(t, "s1").Rows["count"]; row.Seq != 0 {
		t.Fatalf("写下去的该是这一刻的切面：%#v", row)
	}
}

func TestObserveArmsTheIntervalTimerOnlyOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0), userEvent(1))
	f.cache.Observe(live, live.events[0])
	first := f.cache.generation("s1")
	f.cache.Observe(live, live.events[1])

	if second := f.cache.generation("s1"); second != first {
		t.Fatalf("触发器已经等着了就不该再上一枪：%d → %d", first, second)
	}
	if got := f.cache.pending("s1"); got != 2 {
		t.Fatalf("两条都该记上账，实际 %d", got)
	}
}

func TestObserveIsANoOpOnceTheCacheIsClosed(t *testing.T) {
	t.Parallel()

	// 会话比缓存活得长，而一个已经卸载的缓存不该再往介质上写。
	// 回合结束那个必写点也不例外。
	cases := map[string]session.Event{
		"回合中间的事件": userEvent(0),
		"回合结束":    turnEndEvent(0),
	}

	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, 1, time.Millisecond)
			mustRegister(t, f.registry, countUnit("count", 0))
			f.cache.Close()

			f.cache.Observe(newLive("s1", 7, event), event)

			time.Sleep(20 * time.Millisecond)
			if got := len(f.flushes()); got != 0 {
				t.Fatalf("关掉之后一条都不该写，实际写了 %d 次", got)
			}
		})
	}
}

func TestObserveSwallowsAFailedWriteButLeavesATrace(t *testing.T) {
	t.Parallel()

	// 一次缓存写失败不该让提交事件的那条路跟着失败——缓存留在旧位置就是了。
	boom := errors.New("刷不动")
	f := newFixture(t, 1, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))
	f.failFlush(boom)

	live := newLive("s1", 7, turnEndEvent(0))
	f.cache.Observe(live, live.events[0])

	waitFor(t, "那条咽下去之后留的痕迹", func() bool { return len(f.sink.messages()) == 1 })
	if trigger := f.sink.attr(0, "trigger"); trigger != "回合结束" {
		t.Fatalf("痕迹里该写清是哪个触发点：%q", trigger)
	}
	if _, ok, err := f.table.Get("s1"); err != nil || ok {
		t.Fatalf("介质上该留在旧位置：%v %v", ok, err)
	}
}

func TestDetachWritesSynchronouslyAndDropsTheBookkeeping(t *testing.T) {
	t.Parallel()

	// 脱离是活会话变冷的**最后一次机会**，这之后所有读都走 [Cache.ColdSnapshot]。
	// 做成即发即忘等于让最重要的那次检查点去和进程退出赛跑。
	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0), userEvent(1))
	f.cache.Observe(live, live.events[0])

	if err := f.cache.Detach(context.Background(), live); err != nil {
		t.Fatalf("脱离不该失败：%v", err)
	}
	// 同步：Detach 返回时记录已经在介质上，不必等。
	if row := f.record(t, "s1").Rows["count"]; row.Seq != 1 {
		t.Fatalf("最后一份检查点该已经落下去：%#v", row)
	}
	if f.cache.tracked("s1") {
		t.Fatalf("会话已经走了，记账留着只是泄漏")
	}
}

func TestDetachHandsTheFailureBackAndStillDropsTheBookkeeping(t *testing.T) {
	t.Parallel()

	// 把失败报给调用方，由调用方决定要不要忍——这才是 fail-soft 的正确落点。
	boom := errors.New("刷不动")
	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0))
	f.cache.Observe(live, live.events[0])
	f.failFlush(boom)

	if err := f.cache.Detach(context.Background(), live); !errors.Is(err, boom) {
		t.Fatalf("该把失败交回来：%v", err)
	}
	if f.cache.tracked("s1") {
		t.Fatalf("写成没写成都该把记账丢掉")
	}
}

func TestDetachStopsATimerThatGotReArmedDuringItsOwnWrite(t *testing.T) {
	t.Parallel()

	// [Cache.Write] 里的 markClean 会把触发器停掉并置空，所以脱离自己那一段
	// 里的「timer 还在就停掉」看上去永远走不到。它走得到：写和一条正在提交的
	// 事件之间没有互斥，落在 markClean 之后的那条事件会重新上一枪。
	//
	// 这里用落盘屏障当**顺序探针**：它跑到的那一刻 markClean 已经过了，
	// 在那里 Observe 一条，就把那个窗口原样摆了出来。
	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0))
	f.cache.Observe(live, live.events[0])

	f.onFlush(func(observed LiveSession) error {
		f.onFlush(nil)
		f.cache.Observe(observed, live.events[0])
		return nil
	})

	if err := f.cache.Detach(context.Background(), live); err != nil {
		t.Fatalf("脱离不该失败：%v", err)
	}
	if f.cache.tracked("s1") {
		t.Fatalf("会话已经走了，记账留着只是泄漏")
	}
	// 那一枪被停掉了：等过一个远比间隔短的时间也不该再写。
	time.Sleep(20 * time.Millisecond)
	if got := len(f.flushes()); got != 1 {
		t.Fatalf("重新上的那一枪该被停掉，实际写了 %d 次", got)
	}
}

func TestDetachOnANeverObservedSessionIsFine(t *testing.T) {
	t.Parallel()

	// 一个从没提交过事件的会话在这张表里没有条目，脱离它照样得走通。
	f := newFixture(t, 1, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	if err := f.cache.Detach(context.Background(), newLive("s1", 7)); err != nil {
		t.Fatalf("脱离不该失败：%v", err)
	}
	if row := f.record(t, "s1").Rows["count"]; row.Seq != -1 {
		t.Fatalf("空日志的行该停在 -1：%#v", row)
	}
}

func TestCloseStopsThePendingTimersAndCanBeCalledAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1000, 30*time.Millisecond)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0))
	f.cache.Observe(live, live.events[0])
	if !f.cache.tracked("s1") {
		t.Fatalf("这条事件该被记上账")
	}

	f.cache.Close()
	f.cache.Close()

	if f.cache.tracked("s1") {
		t.Fatalf("关掉之后记账该全丢掉")
	}
	time.Sleep(60 * time.Millisecond)
	if got := len(f.flushes()); got != 0 {
		t.Fatalf("等着的触发器该被停掉，实际写了 %d 次", got)
	}
}

func TestCloseDoesNotCloseTheDomain(t *testing.T) {
	t.Parallel()

	// 域是装配方打开的，也归装配方关，见包文档第 1 条。
	f := newFixture(t, 1, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))
	if err := f.cache.Write(context.Background(), newLive("s1", 7, userEvent(0))); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	f.cache.Close()

	if _, _, err := f.table.Get("s1"); err != nil {
		t.Fatalf("域该还开着：%v", err)
	}
}

func TestAStaleIntervalTimerDoesNotWriteAfterTheSessionWentAway(t *testing.T) {
	t.Parallel()

	// [time.Timer.Stop] 返回假时回调可能已经在跑、正卡在锁上，所以回调进来
	// 先看自己的代数还是不是当前那一代。这一条钉住的就是那道闸。
	f := newFixture(t, 1000, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	live := newLive("s1", 7, userEvent(0))
	f.cache.Observe(live, live.events[0])
	stale := f.cache.generation("s1")

	// 脱离把记账整条丢掉，那一枪就此过气。
	if err := f.cache.Detach(context.Background(), live); err != nil {
		t.Fatalf("脱离不该失败：%v", err)
	}
	before := len(f.flushes())

	f.cache.onInterval(live, stale)
	if got := len(f.flushes()); got != before {
		t.Fatalf("过气那一枪不该写，实际多写了 %d 次", got-before)
	}
}

func TestABackgroundWriteStopsWhenTheAssemblerCancelsTheCacheLifetime(t *testing.T) {
	t.Parallel()

	// 后台写的 context 是**这个缓存自己的寿命**：装配方在构造时给一个，
	// 卸载时取消它，就等于停掉所有还没跑完的后台写。
	f := newFixture(t, 1, time.Hour)
	mustRegister(t, f.registry, countUnit("count", 0))

	// 介质那一层要认 context 取消，否则这条约定观察不到，见 [ctxAwareBackend]。
	opened := openDomainOn(t, Spec(), ctxAwareBackend{
		KVProvider: storagetest.NewMemoryBackend(storagetest.NewMemoryMedium()),
	})
	background, cancel := context.WithCancel(context.Background())
	cancel()

	cache, err := New(opened, Options{
		Registry:         f.registry,
		Store:            f.store,
		Flush:            func(LiveSession) error { return nil },
		WriteEveryEvents: 1,
		WriteInterval:    time.Hour,
		Background:       background,
		Logger:           slog.New(f.sink),
	})
	if err != nil {
		t.Fatalf("建缓存不该失败：%v", err)
	}
	defer cache.Close()

	live := newLive("s1", 7, turnEndEvent(0))
	cache.Observe(live, live.events[0])

	waitFor(t, "那条被取消之后留的痕迹", func() bool { return len(f.sink.messages()) == 1 })
	if !strings.Contains(f.sink.messages()[0], "写检查点失败") {
		t.Fatalf("痕迹不对：%v", f.sink.messages())
	}
}

// closeFixtureDomain 把装配里那个域关掉，用来驱动介质读写失败那几条路。
//
// [domain.Domain.Close] 是幂等的，所以 [openDomain] 挂在测试结束时的那次
// 关闭照样安全。
func closeFixtureDomain(t *testing.T, f *fixture) {
	t.Helper()

	if err := f.opened.Close(context.Background()); err != nil {
		t.Fatalf("关域不该失败：%v", err)
	}
}

// 下面这几个是本包自己的白盒探针：节流记账是私有状态，而它的三条规则
//（攒够就写、触发器只上一枪、脱离就丢掉）从外面只看得到「写没写」这一个位。

// pending 是一个会话当前攒了多少条。
func (c *Cache) pending(id session.SessionID) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.dirty[id]
	if !ok {
		return -1
	}
	return state.pending
}

// generation 是一个会话当前的触发器代数。
func (c *Cache) generation(id session.SessionID) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.dirty[id]
	if !ok {
		return 0
	}
	return state.generation
}

// tracked 说明一个会话在不在节流记账里。
func (c *Cache) tracked(id session.SessionID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.dirty[id]
	return ok
}
