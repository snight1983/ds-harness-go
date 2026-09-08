// 本文件的作用：「等这支团队下一次有动静」这一问，以及它为什么在这里是一次
// 介质上的条件轮询而不是一张进程内的等待者名单。

package agentteam

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"
)

// DefaultPollInterval 是两次探团队状态之间隔多久。
//
// 新增: DSH 没有这个概念——它的等待者挂在一张进程内的表上，团队一有边就当场放行，
// 不需要去问介质。本包的动静可能发生在别的副本上，那台机器不会来敲本副本的门，
// 所以只能自己回头看。
//
// 一秒是照着「一次唤醒晚一秒没人察觉，而每秒一次读换不来可观的介质开销」定的：
// 一次探要读一份花名册加一块板，都是按键直取。
const DefaultPollInterval = time.Second

// WaitResult 是一次 [Service.Wait] 的结果。
//
// 源: packages/experimental/agent-team/src/types.ts:216（TeamWaitResult）
type WaitResult struct {
	// TimedOut 说等到时限也没等着动静。等着了就是 false。
	TimedOut bool `json:"timedOut"`
}

// Wait 等这支团队下一次有动静，或者等到时限。
//
// 源: packages/experimental/agent-team/src/activity.ts:11-60（TeamActivity）
//
// 新增: 形状照录，做法整个换掉。DSH 是一张 `TeamId → 等待者集合` 的进程内表，
// 团队一有边就把集合里的人全放行。那张表在多副本下等不到东西：改状态的那个副本
// 不在这张表所在的进程里，它写完介质就走了，没有谁会来敲这扇门。
//
// 所以这里是一次**介质上的条件轮询**：进来先取一次指纹，隔 [Config.PollInterval]
// 再取一次，不一样就放行。代价和收益都要说清楚：
//
//	换来  多副本下成立。别的机器上那次改动，下一次探就看得见。
//	付出  唤醒最晚迟到一个轮询周期；而且两次探之间那些**又变回去**的动静看不见
//	      （比如一条任务被认领又被放回），因为比的是两头的状态而不是中间的事件流。
//
// 第二条是刻意收下的：这一问要回答的是「我还该不该接着等」，一次抵消掉的改动对
// 那个判断没有影响。真要逐条看见每一次改动，得让团队状态也走事件流，那是另一件东西。
//
// **它不叫醒任何人。**探的是状态，不是投递——一个歇着的队友不会因为有人在等它而
// 开始跑，那得靠 [DeliveryWakeup]。
//
// 时限由调用方给，本层只拒非正数。给模型看的那副上下界在工具那一层，
// 见 [github.com/snight1983/ds-harness-go/feature/agentteam/agentteamtool] 的
// wait_agent：那副界是说给模型听的话，不是这一问本身的约束。
func (s *Service) Wait(ctx context.Context, who Caller, timeout time.Duration) (WaitResult, error) {
	if timeout <= 0 {
		return WaitResult{}, newError(CodeInvalidTimeout, "等待时长要是正的，收到 %s", timeout)
	}
	baseline, err := s.fingerprint(ctx, who)
	if err != nil {
		return WaitResult{}, err
	}

	// 等的是别的副本什么时候写进来，那是墙上时间，所以这里不走 [Config.Now]——
	// 一个停住的假时钟会让这次等待永远醒不来。
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return WaitResult{}, wrapError(CodeWaitAborted, ctx.Err(), "等团队 %q 的动静被撤了", who.Team)
		case <-deadline.C:
			return WaitResult{TimedOut: true}, nil
		case <-ticker.C:
		}
		current, err := s.fingerprint(ctx, who)
		if err != nil {
			return WaitResult{}, err
		}
		if current != baseline {
			return WaitResult{}, nil
		}
	}
}

// fingerprint 把「这支团队此刻在发起人眼里长什么样」压成一个数。
//
// 新增: 这一问在 DSH 那边不存在——它有事件流，一条边就是一次通知。这里没有事件流，
// 只有介质上的整值，所以要一个能两头比的摘要。
//
// 进摘要的只有**能说明动静**的那几列，不是整份状态：
//
//	花名册   每一行的名字和此刻状态（含本副本的活/闲，那正是 DSH 也在看的那道边）
//	任务板   每一条的身份、版本号、状态、认领人——[Task.Rev] 每改一次加一，
//	         所以改标题、改正文、改前置都逃不掉，不必把正文本身塞进来
//	收件箱   发起人手里还压着几条没送到的
//
// 不进摘要的是正文那些大字段：一块满板可以有几兆字节，每秒哈希一遍它换不来任何
// 新信息——正文一改，[Task.Rev] 就跟着动了。
func (s *Service) fingerprint(ctx context.Context, who Caller) (uint64, error) {
	members, err := s.Members(ctx, who.Team)
	if err != nil {
		return 0, err
	}
	board, err := s.board(ctx, who.Team)
	if err != nil {
		return 0, err
	}
	pending, err := s.pendingMessages(ctx, who.Team, who.ID)
	if err != nil {
		return 0, err
	}

	digest := fnv.New64a()
	for _, member := range members {
		fmt.Fprintf(digest, "m\x00%s\x00%s\x00", member.Name, member.Status)
	}
	for _, task := range board.Tasks {
		fmt.Fprintf(digest, "t\x00%s\x00%d\x00%s\x00%s\x00", task.ID, task.Rev, task.Status, task.Owner)
	}
	fmt.Fprintf(digest, "i\x00%d", len(pending))
	return digest.Sum64(), nil
}

// ActivePeers 数一数除了发起人之外，还有几个成员正在跑或者还在开工。
//
// 源: packages/experimental/tool-agent-team/src/index.ts:39,255-256（ACTIVE_WAIT_STATUSES）
//
// 一个人的团队里等下去没有意义：能改这支团队状态的只剩发起人自己，而它正堵在这次
// 等待上。工具那一层拿这个数当短路条件，见 wait_agent。
//
// 这个数只对**本副本**成立，理由同 [StatusInactive]：别的机器上那个队友跑得再欢，
// 这里也看不见。所以它只用来做「明显等不到」的短路，不用来判定「一定等得到」。
func (s *Service) ActivePeers(ctx context.Context, who Caller) (int, error) {
	members, err := s.Members(ctx, who.Team)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, member := range members {
		if member.ID == who.ID {
			continue
		}
		if member.Status == StatusRunning || member.Status == StatusProvisioning {
			count++
		}
	}
	return count, nil
}
