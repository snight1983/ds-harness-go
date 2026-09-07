// 本文件验带码失败那一头：码原样传给 llm.Error，以及「这条错误该不该把脚本打断」
// 那条判据。

package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

// 这条接缝不新造错误类型，所以上游那句 errors.As(err, &target)（target 是
// *llm.Error）在包了一层之后照样认得出来。
func TestNewErrorStaysAnLLMError(t *testing.T) {
	cause := errors.New("底下那件事")
	err := NewError("脚本解析不了", CodeScriptParse, cause)

	wrapped := fmt.Errorf("跑工作流：%w", err)
	var carrier *llm.Error
	if !errors.As(wrapped, &carrier) {
		t.Fatal("包了一层之后该还认得出这是一条 llm.Error")
	}
	if carrier.Failure.Code != CodeScriptParse {
		t.Fatalf("码该原样带着，实际 %q", carrier.Failure.Code)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("底下那条原因该还连着")
	}
}

// 每一个声明出来的码都是致命的：撞上它们的组合子必须大声把脚本打死，
// 而不是把那一项解成 null。
func TestEveryDeclaredCodeIsFatal(t *testing.T) {
	for _, code := range []string{
		CodeScriptParse, CodeMetaInvalid, CodeInvalidArgument, CodeUnsupportedOption,
		CodeUnsupportedSchema, CodeAgentCap, CodeItemCap, CodeAgentStart,
		CodeAgentResult, CodeResultUnserializable, CodeCancelled,
	} {
		t.Run(code, func(t *testing.T) {
			if !IsFatal(NewError("出事了", code, nil)) {
				t.Fatalf("%s 该是致命的", code)
			}
		})
	}
}

// 要表达「这条错误不该打断脚本」，用一个不在那张表里的码——一个普通的孩子失败
// 走的就是这条路。
func TestAnUnlistedCodeIsNotFatal(t *testing.T) {
	if IsFatal(NewError("这一轮没成", "CHILD_FAILED", nil)) {
		t.Fatal("表外的码不该被判成致命")
	}
}

// 一条压根不是 llm.Error 的错误不是致命的：这条判据只对本接缝自己发出的失败说话。
func TestAPlainErrorIsNotFatal(t *testing.T) {
	if IsFatal(errors.New("随便一条")) {
		t.Fatal("普通错误不该被判成致命")
	}
	if IsFatal(nil) {
		t.Fatal("nil 不该被判成致命")
	}
}

// 判据认的是被包住的那一条，不是最外面那层。
func TestIsFatalSeesThroughWrapping(t *testing.T) {
	err := fmt.Errorf("第一轮：%w", NewError("撞上限了", CodeAgentCap, nil))
	if !IsFatal(err) {
		t.Fatal("包了一层之后该还判得出致命")
	}
}

// ---- 那几个值类型排进排出 ----

// Result 的 Value 是一段生的 JSON：接缝只保证它排得出去，不解释它是什么。
func TestResultCarriesItsValueAsRawJSON(t *testing.T) {
	result := Result{
		Value:         json.RawMessage(`{"改了":12}`),
		StopReason:    StopCompleted,
		AgentsStarted: 3,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("排结果失败：%v", err)
	}

	var read Result
	if err := json.Unmarshal(encoded, &read); err != nil {
		t.Fatalf("读结果失败：%v", err)
	}
	if read.StopReason != StopCompleted || read.AgentsStarted != 3 {
		t.Fatalf("结局该原样转一圈回来，实际 %#v", read)
	}
	var value map[string]int
	if err := json.Unmarshal(read.Value, &value); err != nil {
		t.Fatalf("读那段生 JSON 失败：%v", err)
	}
	if value["改了"] != 12 {
		t.Fatalf("那段生 JSON 该一个字节不动，实际 %#v", value)
	}
}

// meta 那三段可选的说明不给就不出现在字节里，好让身份比对不受「没填」影响。
func TestMetaOmitsEmptyOptionalFields(t *testing.T) {
	encoded, err := json.Marshal(Meta{Name: "整理", Description: "说明"})
	if err != nil {
		t.Fatalf("排 meta 失败：%v", err)
	}
	if got := string(encoded); got != `{"name":"整理","description":"说明"}` {
		t.Fatalf("没填的字段不该出现在字节里，实际 %s", got)
	}
}
