// 本文件的作用：三张表各自的落盘校验，以及整图校验那三种违规的错误面。

package agentteam

import (
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
)

// goodRoster 是一份挑不出毛病的花名册，用例各改坏一处。
func goodRoster() Roster {
	return Roster{
		Team: "team-1",
		Lead: "session-lead",
		Members: []Member{{
			ID:       "session-reviewer",
			Name:     "reviewer",
			Phase:    PhaseActive,
			Context:  ContextFresh,
			Provider: "in-process",
		}},
	}
}

func TestRosterValidate坏在哪就说哪(t *testing.T) {
	cases := map[string]func(*Roster){
		"没有团队身份":   func(r *Roster) { r.Team = "" },
		"没有队长":     func(r *Roster) { r.Lead = "" },
		"有一行没有身份":  func(r *Roster) { r.Members[0].ID = "" },
		"名字不合式":    func(r *Roster) { r.Members[0].Name = "Code Reviewer" },
		"状态不认得":    func(r *Roster) { r.Members[0].Phase = "gone" },
		"上文来源不认得":  func(r *Roster) { r.Members[0].Context = "borrowed" },
		"没说用哪个提供方": func(r *Roster) { r.Members[0].Provider = "" },
		"两个队友重名": func(r *Roster) {
			second := r.Members[0]
			second.ID = "session-other"
			r.Members = append(r.Members, second)
		},
		"两行指向同一个会话": func(r *Roster) {
			second := r.Members[0]
			second.Name = "writer"
			r.Members = append(r.Members, second)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			roster := goodRoster()
			breakIt(&roster)
			if err := roster.Validate(); err == nil {
				t.Fatal("这一份该被拒")
			}
		})
	}

	if err := goodRoster().Validate(); err != nil {
		t.Fatalf("好的那一份不该被拒：%v", err)
	}
}

// goodBoard 是一块挑不出毛病的板。
func goodBoard() Board {
	return Board{
		Team:           "team-1",
		NextTaskNumber: 2,
		Tasks:          []Task{{ID: "task-1", Rev: 1, Subject: "一条任务", Status: TaskPending}},
	}
}

func TestBoardValidate坏在哪就说哪(t *testing.T) {
	cases := map[string]func(*Board){
		"没有团队身份":   func(b *Board) { b.Team = "" },
		"下一个序号不合法": func(b *Board) { b.NextTaskNumber = 0 },
		"有一条没有身份":  func(b *Board) { b.Tasks[0].ID = "" },
		"版本号不合法":   func(b *Board) { b.Tasks[0].Rev = 0 },
		"状态不认得":    func(b *Board) { b.Tasks[0].Status = "archived" },
		"没有标题":     func(b *Board) { b.Tasks[0].Subject = "" },
		"两条任务同名":   func(b *Board) { b.Tasks = append(b.Tasks, b.Tasks[0]) },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			board := goodBoard()
			breakIt(&board)
			if err := board.Validate(); err == nil {
				t.Fatal("这一份该被拒")
			}
		})
	}

	if err := goodBoard().Validate(); err != nil {
		t.Fatalf("好的那一份不该被拒：%v", err)
	}
}

// goodMessage 是一条挑不出毛病的消息。
func goodMessage() Message {
	return Message{
		ID:         "team-message-1",
		Team:       "team-1",
		SenderID:   "session-lead",
		SenderName: LeadName,
		TargetID:   "session-reviewer",
		Delivery:   DeliveryQuiet,
		Phase:      MessageQueued,
		Content:    llm.Content{llm.TextBlock{Text: "话"}},
		QueuedAt:   1700000000000,
	}
}

func TestMessageValidate坏在哪就说哪(t *testing.T) {
	cases := map[string]func(*Message){
		"没有身份":      func(m *Message) { m.ID = "" },
		"没有团队身份":    func(m *Message) { m.Team = "" },
		"没说谁发的":     func(m *Message) { m.SenderID = "" },
		"没说发给谁":     func(m *Message) { m.TargetID = "" },
		"发给了自己":     func(m *Message) { m.TargetID = m.SenderID },
		"没有发信人名字":   func(m *Message) { m.SenderName = "" },
		"送法不认得":     func(m *Message) { m.Delivery = "shout" },
		"状态不认得":     func(m *Message) { m.Phase = "lost" },
		"没有正文":      func(m *Message) { m.Content = nil },
		"没有排队时刻":    func(m *Message) { m.QueuedAt = 0 },
		"排着队却记着认领人": func(m *Message) { m.Claimant = "replica-a" },
		"认领了却没说是谁": func(m *Message) {
			m.Phase = MessageClaimed
			m.ClaimedAt = 1700000000000
		},
		"认领了却没说什么时候": func(m *Message) {
			m.Phase = MessageClaimed
			m.Claimant = "replica-a"
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			message := goodMessage()
			breakIt(&message)
			if err := message.Validate(); err == nil {
				t.Fatal("这一条该被拒")
			}
		})
	}

	if err := goodMessage().Validate(); err != nil {
		t.Fatalf("好的那一条不该被拒：%v", err)
	}
}

func TestMessageExpired只有认领着的才谈得上过期(t *testing.T) {
	const claimedAt = int64(1700000000000)
	const ttl = int64(60000)

	claimed := goodMessage()
	claimed.Phase = MessageClaimed
	claimed.Claimant = "replica-a"
	claimed.ClaimedAt = claimedAt

	if claimed.Expired(claimedAt+ttl-1, ttl) {
		t.Fatal("还没到点不该算过期")
	}
	if !claimed.Expired(claimedAt+ttl, ttl) {
		t.Fatal("到点了就该算过期")
	}

	queued := goodMessage()
	if queued.Expired(claimedAt+ttl*10, ttl) {
		t.Fatal("排着队的谈不上过期")
	}
}

func TestGraphError三种违规各对一个码(t *testing.T) {
	cases := map[GraphViolation]Code{
		ViolationMissing:   CodeTaskNotFound,
		ViolationDuplicate: CodeInvalidArgument,
		ViolationCycle:     CodeTaskDependencyCycle,
	}
	for violation, want := range cases {
		err := error(&GraphError{Violation: violation, Message: "坏了"})
		if !errors.Is(err, want) {
			t.Fatalf("%q 该对上 %q，得到 %v", violation, want, err)
		}
		if got := err.Error(); got == "" {
			t.Fatalf("%q 该有一句给人读的话", violation)
		}
	}
}

func TestValidateGraph三种违规都拦得住(t *testing.T) {
	cases := map[string]struct {
		tasks []Task
		code  Code
	}{
		"前置不在板上": {
			[]Task{{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-9"}}},
			CodeTaskNotFound,
		},
		"前置写了两遍": {
			[]Task{
				{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-2", "task-2"}},
				{ID: "task-2", Status: TaskPending},
			},
			CodeInvalidArgument,
		},
		"自己挡自己": {
			[]Task{{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-1"}}},
			CodeTaskDependencyCycle,
		},
		"两条互相等": {
			[]Task{
				{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-2"}},
				{ID: "task-2", Status: TaskPending, BlockedBy: []TaskID{"task-1"}},
			},
			CodeTaskDependencyCycle,
		},
		"前置已经删了": {
			[]Task{
				{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-2"}},
				{ID: "task-2", Status: TaskDeleted},
			},
			CodeTaskNotFound,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateGraph(testCase.tasks, Task{}); !errors.Is(err, testCase.code) {
				t.Fatalf("该报 %q，得到 %v", testCase.code, err)
			}
		})
	}
}

func TestValidateGraph候选那一条也算进图里(t *testing.T) {
	current := []Task{{ID: "task-1", Status: TaskPending, BlockedBy: []TaskID{"task-2"}}}
	candidate := Task{ID: "task-2", Status: TaskPending, BlockedBy: []TaskID{"task-1"}}

	if err := ValidateGraph(current, candidate); !errors.Is(err, CodeTaskDependencyCycle) {
		t.Fatalf("候选那一条把图绕成了圈，该报 %q，得到 %v", CodeTaskDependencyCycle, err)
	}
}

func TestSpec三张表都带校验(t *testing.T) {
	spec := Spec()
	if spec.Name != DomainName || spec.Version != DomainVersion {
		t.Fatalf("域的身份该是 %q/%d，得到 %q/%d", DomainName, DomainVersion, spec.Name, spec.Version)
	}
	if len(spec.Tables) != 3 {
		t.Fatalf("该有三张表，得到 %d 张", len(spec.Tables))
	}
	// 每次叫都该是一份新的：里面带着切片，共用一个底层数组会让调用方互相改到。
	if &spec.Tables[0] == &Spec().Tables[0] {
		t.Fatal("两次叫该给出两份")
	}
}

func TestNormalizeWriteScope归一化和拒掉的各是哪些(t *testing.T) {
	good := map[string]string{
		`./src/api/`:  "src/api",
		`src\api`:     "src/api",
		`docs//`:      "docs",
		`feature/a/b`: "feature/a/b",
	}
	for input, want := range good {
		if got, err := NormalizeWriteScope(input); err != nil || got != want {
			t.Fatalf("%q 该归一化成 %q，得到 %q（%v）", input, want, got, err)
		}
	}

	bad := []string{"", "/etc", `C:\code`, "src//api", "src/./api", "../别处", "."}
	for _, input := range bad {
		if _, err := NormalizeWriteScope(input); !errors.Is(err, CodeInvalidWriteScope) {
			t.Fatalf("%q 该被拒，得到 %v", input, err)
		}
	}
}

func TestScopesOverlap比的是路径段不是字符串前缀(t *testing.T) {
	overlapping := [][2]string{
		{"src", "src"},
		{"src", "src/api"},
		{"src/api", "src"},
	}
	for _, pair := range overlapping {
		if !ScopesOverlap(pair[0], pair[1]) {
			t.Fatalf("%q 和 %q 该算压在一起", pair[0], pair[1])
		}
	}

	apart := [][2]string{
		{"src/api", "src/app"},
		{"src", "srcx"},
		{"docs", "src"},
	}
	for _, pair := range apart {
		if ScopesOverlap(pair[0], pair[1]) {
			t.Fatalf("%q 和 %q 不该算压在一起", pair[0], pair[1])
		}
	}
}

// 这个包占的是一个空位置：三张表各自的校验体在域的编解码两头都会走，
// 用例只钉那个位置真的占得下来，以及缺注册表时不假装占到了。
func TestRegisterInvariants占住包名但一条检查都不装(t *testing.T) {
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表不该失败：%v", err)
	}
	t.Cleanup(registry.Close)

	release, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记不变量不该失败：%v", err)
	}
	release()

	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatal("没有注册表该装不上")
	}
}
