// 本文件的作用：验「这条消息是不是一个压缩检查点」这个判定认得准。

package compaction

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestIsCheckpointSource只认那一个产出方名字(t *testing.T) {
	for name, item := range map[string]struct {
		source llm.MessageSource
		want   bool
	}{
		"就是检查点":     {llm.PluginSource{Plugin: CheckpointPlugin}, true},
		"别的插件注入的":   {llm.PluginSource{Plugin: "workspace-instructions"}, false},
		"用户自己说的话":   {llm.UserSource{}, false},
		"模型产出的":     {llm.ModelSource{}, false},
		"工具结果":      {llm.ToolSource{CallID: "call-1"}, false},
		"本构建不认识的来源": {llm.UnknownSource{Kind: "别的层"}, false},
		"插件名空着":     {llm.PluginSource{}, false},
	} {
		if got := IsCheckpointSource(item.source); got != item.want {
			t.Fatalf("%s：判成 %v，要的是 %v", name, got, item.want)
		}
	}
}

func TestCheckpointPlugin是常数不随后端变(t *testing.T) {
	// 一个后端换个名字，别的层就再也认不出它写下的检查点了。
	if CheckpointPlugin != "compact" {
		t.Fatalf("产出方名字是 %q", CheckpointPlugin)
	}
}

func TestNewCheckpointSource盖出来的章认得回来(t *testing.T) {
	t.Parallel()

	want := CheckpointSource{CompactionID: "c-1", SourceCommandID: "cmd-9"}
	source, err := NewCheckpointSource(want)
	if err != nil {
		t.Fatalf("造不出来：%v", err)
	}
	if source.Plugin != CheckpointPlugin {
		t.Fatalf("产出方名字是 %q", source.Plugin)
	}

	got, isCheckpoint, err := CheckpointSourceOf(source)
	if err != nil || !isCheckpoint {
		t.Fatalf("取不回来：isCheckpoint=%v err=%v", isCheckpoint, err)
	}
	if got != want {
		t.Fatalf("取回来的是 %+v，盖进去的是 %+v", got, want)
	}
}

func TestNewCheckpointSource排出去的字节和DSH一致(t *testing.T) {
	t.Parallel()

	source, err := NewCheckpointSource(CheckpointSource{CompactionID: "c-1"})
	if err != nil {
		t.Fatalf("造不出来：%v", err)
	}
	// 不是人工发起的时候 sourceCommandId 整个不出现，和 DSH 的可选字段一致。
	if string(source.Extra) != `{"compactionId":"c-1"}` {
		t.Fatalf("排出来的是 %s", source.Extra)
	}
}

func TestNewCheckpointSource拒掉空身份(t *testing.T) {
	t.Parallel()

	// 一条身份为空的检查点落进持久日志之后，它属于哪次压缩就再也查不出来了，
	// 而那条日志本身读得回来，不会有别的地方报警。
	_, err := NewCheckpointSource(CheckpointSource{SourceCommandID: "cmd-9"})
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCheckpointSourceOf分得清三种情况(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		source         llm.MessageSource
		wantCheckpoint bool
		wantErr        error
		want           CheckpointSource
	}{
		"根本不是插件来源": {
			source: llm.UserSource{},
		},
		"是别的插件": {
			source: llm.PluginSource{Plugin: "workspace-instructions", Extra: json.RawMessage(`{}`)},
		},
		"是检查点但没带身份": {
			// 认出来是检查点，身份缺了留给不变量去报——两件事分开。
			source:         llm.PluginSource{Plugin: CheckpointPlugin},
			wantCheckpoint: true,
		},
		"是检查点但那点出处读不回来": {
			source:         llm.PluginSource{Plugin: CheckpointPlugin, Extra: json.RawMessage(`{`)},
			wantCheckpoint: true,
			wantErr:        ErrMalformedEvent,
		},
		"是检查点而且完整": {
			source: llm.PluginSource{
				Plugin: CheckpointPlugin,
				Extra:  json.RawMessage(`{"compactionId":"c-2","sourceCommandId":"cmd-1"}`),
			},
			wantCheckpoint: true,
			want:           CheckpointSource{CompactionID: "c-2", SourceCommandID: "cmd-1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, isCheckpoint, err := CheckpointSourceOf(item.source)
			if isCheckpoint != item.wantCheckpoint {
				t.Fatalf("isCheckpoint=%v，要的是 %v", isCheckpoint, item.wantCheckpoint)
			}
			if item.wantErr != nil {
				if !errors.Is(err, item.wantErr) {
					t.Fatalf("报的是 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if got != item.want {
				t.Fatalf("取回来的是 %+v，要的是 %+v", got, item.want)
			}
		})
	}
}
