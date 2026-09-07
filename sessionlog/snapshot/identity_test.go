// 本文件的作用：标识换成记号之后，那些「谁指向谁」的关系还在不在——尤其是跨着
// 父会话和它的孩子。

package snapshot_test

import (
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog/snapshot"
)

func TestRedactIDsKeepsTheParentChildLink(t *testing.T) {
	t.Parallel()

	parent := []byte(`{"version":1,"id":"11111111-1111-4111-8111-111111111111","createdAt":1}` + "\n" +
		`{"type":"subagent/spawn","seq":0,"time":2,` +
		`"data":{"runId":"33333333-3333-4333-8333-333333333333",` +
		`"childSessionId":"22222222-2222-4222-8222-222222222222"}}` + "\n")
	child := []byte(`{"version":1,"id":"22222222-2222-4222-8222-222222222222","createdAt":3,` +
		`"parentSession":"11111111-1111-4111-8111-111111111111"}` + "\n")

	out, err := snapshot.RedactIDs([][]byte{parent, child})
	if err != nil {
		t.Fatalf("换不出来：%v", err)
	}

	gotParent, gotChild := string(out[0]), string(out[1])
	for _, raw := range []string{"1111-4111", "2222-4222", "3333-4333"} {
		if strings.Contains(gotParent+gotChild, raw) {
			t.Fatalf("还留着原值 %s：\n%s%s", raw, gotParent, gotChild)
		}
	}
	// 父会话是第一个被认领的，所以是 {{session:1}}；孩子是第二个。
	if !strings.Contains(gotParent, `"id":"{{session:1}}"`) {
		t.Errorf("父会话身份没换对：%s", gotParent)
	}
	if !strings.Contains(gotChild, `"id":"{{session:2}}"`) {
		t.Errorf("孩子身份没换对：%s", gotChild)
	}
	// 这是本函数存在的全部理由：孩子指回父会话的那条线要还认得出来。
	if !strings.Contains(gotChild, `"parentSession":"{{session:1}}"`) {
		t.Errorf("父子线断了：%s", gotChild)
	}
	// 父会话里指向孩子的那条同理。
	if !strings.Contains(gotParent, `"childSessionId":"{{session:2}}"`) {
		t.Errorf("父指子那条线断了：%s", gotParent)
	}
}

func TestRedactIDsSeparatesKindsByKeyName(t *testing.T) {
	t.Parallel()

	log := []byte(`{"version":1,"id":"s-1","createdAt":1}` + "\n" +
		`{"type":"workflow/start","seq":0,"time":2,` +
		`"data":{"runId":"aaaaaaaa-1111-4111-8111-111111111111",` +
		`"retryId":"bbbbbbbb-2222-4222-8222-222222222222",` +
		`"commandId":"给个名字就行"}}` + "\n")

	out := redact(t, log)

	// 三个值长得一样（都是不透明标识），分类靠的是键名而不是值的形状。
	for want, key := range map[string]string{
		"{{workflow:1}}": "runId",
		"{{retry:1}}":    "retryId",
		"{{command:1}}":  "commandId",
	} {
		if !strings.Contains(out, `"`+key+`":"`+want+`"`) {
			t.Errorf("%s 没换成 %s：%s", key, want, out)
		}
	}
}

func TestRedactIDsLeavesOrdinaryTextAlone(t *testing.T) {
	t.Parallel()

	log := []byte(`{"version":1,"id":"s-1","createdAt":1}` + "\n" +
		`{"type":"user/message","seq":0,"time":2,` +
		`"data":{"text":"帮我看看 config-2024 这个版本号"}}` + "\n")

	out := redact(t, log)

	// 只有看着像随机铸出来的（UUID 形状）才换；别的字符串一律原样。
	if !strings.Contains(out, "config-2024 这个版本号") {
		t.Errorf("普通文本被动了：%s", out)
	}
}

func TestRedactIDsFindsAnIDEmbeddedInProse(t *testing.T) {
	t.Parallel()

	// 子 agent 汇报工具回的是一句人话，消息身份嵌在里面。
	log := []byte(`{"version":1,"id":"s-1","createdAt":1}` + "\n" +
		`{"type":"tool/result","seq":0,"time":2,"data":{"text":` +
		`"report accepted by the agent that started you as message ` +
		`44444444-4444-4444-8444-444444444444"}}` + "\n")

	out := redact(t, log)

	if strings.Contains(out, "4444-4444-8444") {
		t.Fatalf("嵌在人话里的身份没换：%s", out)
	}
	if !strings.Contains(out, "as message {{message:1}}") {
		t.Errorf("换出来的句子不对：%s", out)
	}
}

func TestRedactIDsKeepsAlreadyTokenizedOrdinals(t *testing.T) {
	t.Parallel()

	// 一份压过的日志再压一遍：已有的记号原样留着，而且要把序号占住，
	// 否则后来的新值会撞上它。
	log := []byte(`{"version":1,"id":"{{session:3}}","createdAt":1}` + "\n" +
		`{"type":"user/message","seq":0,"time":2,` +
		`"data":{"agentId":"55555555-5555-4555-8555-555555555555"}}` + "\n")

	out := redact(t, log)

	if !strings.Contains(out, `"id":"{{session:3}}"`) {
		t.Errorf("已有的记号被重编号了：%s", out)
	}
	if !strings.Contains(out, `"agentId":"{{id:1}}"`) {
		t.Errorf("新值没换对：%s", out)
	}
}

func TestRedactIDsIsDeterministic(t *testing.T) {
	t.Parallel()

	// Go 的 map 迭代顺序是随机的，认领次序要是跟着它走，同一份日志压两次就会
	// 得到两套编号——那正好毁掉这个包存在的理由。
	log := []byte(`{"version":1,"id":"s-1","createdAt":1}` + "\n" +
		`{"type":"user/message","seq":0,"time":2,"data":{` +
		`"zetaId":"aaaaaaaa-0000-4000-8000-000000000001",` +
		`"alphaId":"aaaaaaaa-0000-4000-8000-000000000002",` +
		`"midId":"aaaaaaaa-0000-4000-8000-000000000003"}}` + "\n")

	first := redact(t, log)
	for range 20 {
		if again := redact(t, log); again != first {
			t.Fatalf("两次压出了两套编号：\n%s%s", first, again)
		}
	}
}

func TestRedactIDsRefusesABrokenLine(t *testing.T) {
	t.Parallel()

	if _, err := snapshot.RedactIDs([][]byte{[]byte("{不是 JSON}\n")}); err == nil {
		t.Fatal("坏行该被拒")
	}
}

func redact(t *testing.T, log []byte) string {
	t.Helper()

	out, err := snapshot.RedactIDs([][]byte{log})
	if err != nil {
		t.Fatalf("换不出来：%v", err)
	}
	return string(out[0])
}
