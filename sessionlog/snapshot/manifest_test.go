// 本文件的作用：一份 `snapshot.yml` 什么样算合法——归属声明读得出来，写错的字段
// 名当场就红，以及那六个只在 DSH 有意义的字段在这里一律不认。

package snapshot_test

import (
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog/snapshot"
)

func TestParseManifestReadsAFullOwnershipDeclaration(t *testing.T) {
	t.Parallel()

	got := parseManifest(t, `
version: 1
scenario: tool-approval-denied
recording: authored
header:
  class: default-tools
  pin: true
  systemPromptSource: baseline/plain-chat
  childSystemPrompts: [1, 3]
  changes: 0
replay:
  override: true
input:
  task: 帮我看一眼这个文件
  attachments:
    - id: "sha256:aaaa"
      mediaType: image/png
      data: iVBORw0KGgo=
session:
  source: ../plain-chat/session.jsonl
`)

	if got.Scenario != "tool-approval-denied" || got.Recording != snapshot.RecordingAuthored {
		t.Fatalf("头两个字段读错了：%+v", got)
	}
	if !got.Header.Pin || got.Header.Class != "default-tools" {
		t.Errorf("请求头归属读错了：%+v", got.Header)
	}
	// changes 是零和不填是两件事：前者说「这个场景一次都不该换头」，后者没表态。
	if got.Header.Changes == nil || *got.Header.Changes != 0 {
		t.Errorf("changes 读错了：%v", got.Header.Changes)
	}
	if len(got.Header.ChildSystemPrompts) != 2 || got.Header.ChildSystemPrompts[1] != 3 {
		t.Errorf("子会话序号读错了：%v", got.Header.ChildSystemPrompts)
	}
	if got.Replay == nil || !got.Replay.Override {
		t.Errorf("脚本覆盖读错了：%v", got.Replay)
	}
	if got.Input.Task == "" || len(got.Input.Attachments) != 1 {
		t.Fatalf("输入读错了：%+v", got.Input)
	}
	if got.Input.Attachments[0].MediaType != "image/png" {
		t.Errorf("附件读错了：%+v", got.Input.Attachments[0])
	}
	if got.Session == nil || got.Session.Source != "../plain-chat/session.jsonl" {
		t.Errorf("借用声明读错了：%v", got.Session)
	}
}

func TestParseManifestAcceptsTheSmallestManifest(t *testing.T) {
	t.Parallel()

	got := parseManifest(t, "version: 1\n")

	// 一份自己拥有 session.jsonl、没有任何例外的场景就该只写这一行。
	if got.Version != 1 || got.Header != nil || got.Replay != nil ||
		got.Input != nil || got.Session != nil {
		t.Errorf("最小清单不该带出别的东西：%+v", got)
	}
}

func TestParseManifestRefusesTheFieldsThisRepoHasNoMeaningFor(t *testing.T) {
	t.Parallel()

	// DSH 那六个字段说的都是「起一个什么样的进程」，本仓库不起进程。沉默地忽略它们
	// 等于让一份从 DSH 照录过来的清单看着像是生效了。
	for _, field := range []string{
		"profile: headless", "composition: default", "platform: posix",
		"permission: read-only", "environment: {DSH_X: '1'}", "workspace: {final: true}",
	} {
		name, _, _ := strings.Cut(field, ":")
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := parseManifestError(t, "version: 1\n"+field+"\n")
			if !strings.Contains(err, name) {
				t.Errorf("报错里没点名 %s：%s", name, err)
			}
		})
	}
}

func TestParseManifestNamesEveryUnknownFieldInOrder(t *testing.T) {
	t.Parallel()

	// 一次报全、而且次序固定：写错三个字段名的人不该被迫改一个跑一次。
	err := parseManifestError(t, "version: 1\nzeta: 1\nalpha: 2\nmid: 3\n")
	if !strings.Contains(err, "alpha、mid、zeta") {
		t.Errorf("未知字段没按字典序一次报全：%s", err)
	}
}

func TestParseManifestRefusesMalformedValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"不是 YAML":     "version: 1\n\tbad",
		"根不是映射":       "- version\n",
		"版本号不对":       "version: 2\n",
		"版本号溢出当前平台":   "version: 4294967297\n",
		"版本号缺席":       "scenario: plain-chat\n",
		"场景名不是连字符写法":  "version: 1\nscenario: Plain_Chat\n",
		"录制方式不认识":     "version: 1\nrecording: sometimes\n",
		"请求头没有类名":     "version: 1\nheader: {pin: true}\n",
		"pin 写成了假":    "version: 1\nheader: {class: a, pin: false}\n",
		"换头次数是负的":     "version: 1\nheader: {class: a, changes: -1}\n",
		"子会话序号从零起":    "version: 1\nheader: {class: a, childToolSchemas: [0]}\n",
		"子会话序号重了":     "version: 1\nheader: {class: a, childSystemPrompts: [2, 2]}\n",
		"覆盖写成了假":      "version: 1\nreplay: {override: false}\n",
		"输入两样都没写":     "version: 1\ninput: {}\n",
		"任务是空串":       "version: 1\ninput: {task: '   '}\n",
		"附件列表是空的":     "version: 1\ninput: {attachments: []}\n",
		"附件标识不是内容寻址的": "version: 1\ninput: {attachments: [{id: a, mediaType: image/png, data: x}]}\n",
		"附件重了":        "version: 1\ninput: {attachments: [{id: 'sha256:a', mediaType: image/png, data: x}, {id: 'sha256:a', mediaType: image/png, data: y}]}\n",
		"借用的是绝对路径":    "version: 1\nsession: {source: /tmp/session.jsonl}\n",
		"借用的路径带盘符":    "version: 1\nsession: {source: 'C:/tmp/session.jsonl'}\n",
		"借用的路径是反斜杠写的": "version: 1\nsession: {source: '..\\plain\\session.jsonl'}\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := parseManifestError(t, source); !strings.HasPrefix(err, "snapshot: snapshot.yml：") {
				t.Errorf("报错没带上出处：%s", err)
			}
		})
	}
}

func parseManifest(t *testing.T, source string) snapshot.Manifest {
	t.Helper()

	got, err := snapshot.ParseManifest([]byte(source), "snapshot.yml")
	if err != nil {
		t.Fatalf("读不出来：%v", err)
	}
	return got
}

func parseManifestError(t *testing.T, source string) string {
	t.Helper()

	got, err := snapshot.ParseManifest([]byte(source), "snapshot.yml")
	if err == nil {
		t.Fatalf("这份清单该被拒：%+v", got)
	}
	return err.Error()
}
