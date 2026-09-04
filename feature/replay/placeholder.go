// 本文件的作用：`{{fromRequest:<正则>}}` 这个占位符——它拿活的请求解算一个静态
// 剧本不可能知道的值。
//
// 源: packages/test-support/llm-replay/src/index.ts:320-418

package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
)

// ErrScriptedPlaceholder 是「剧本里那个占位符解算不了」。
var ErrScriptedPlaceholder = errors.New("llm-replay: 占位符解算不了")

const (
	// fromRequestOpen 是占位符的起始记号。
	fromRequestOpen = "{{fromRequest:"
	// fromRequestClose 是占位符的结束记号。
	fromRequestClose = "}}"
)

// collectStrings 收一份 JSON 值里每一个字符串**叶子**，按遍历顺序。
//
// 源: packages/test-support/llm-replay/src/index.ts:328-340
//
// 对象的**键**不算叶子——DSH 走的是 Object.values，键从来没进过语料。
//
// 新增: 这里走的是 [encoding/json.Decoder] 的记号流，而不是先解成
// `map[string]any` 再遍历。理由是 Go 的 map 遍历顺序是随机的，那样一来同一份请求
// 每一跑拼出来的语料顺序都不一样，而「**最后**一次匹配赢」这条规则正好依赖顺序。
// 记号流按字节顺序走，于是语料的顺序就是这份 JSON 的书写顺序。
func collectStrings(data []byte, out []string) ([]string, error) {
	// frame 记着当前这一层是不是对象、以及下一个字符串记号是不是键。
	type frame struct {
		object    bool
		expectKey bool
	}
	var stack []frame
	// consumedValue 在一个值读完之后把所在对象层切回「下一个是键」。
	consumedValue := func() {
		if depth := len(stack); depth > 0 && stack[depth-1].object {
			stack[depth-1].expectKey = true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w：请求语料读不回来：%w", ErrScriptedPlaceholder, err)
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				stack = append(stack, frame{object: true, expectKey: true})
			case '[':
				stack = append(stack, frame{})
			default:
				stack = stack[:len(stack)-1]
				consumedValue()
			}
		case string:
			if depth := len(stack); depth > 0 && stack[depth-1].object && stack[depth-1].expectKey {
				stack[depth-1].expectKey = false
				continue
			}
			out = append(out, value)
			consumedValue()
		default:
			consumedValue()
		}
	}
}

// resolveFromRequest 拿一条模式在请求语料里解算一次，**最后**一次匹配赢。
//
// 源: packages/test-support/llm-replay/src/index.ts:343-357
//
// 有捕获组就交它的第一个捕获组，没有就交整段匹配。用带下标的那一族匹配函数，是为了
// 分得开「这一组匹配到了一个空串」和「这一组根本没参与」——前者该交出空串，后者该
// 回落到整段匹配，而只看结果字符串的那一族把两者都给成 ""。
func resolveFromRequest(pattern, corpus string) (string, error) {
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("%w：fromRequest 的模式 %q 编译不了：%w", ErrScriptedPlaceholder, pattern, err)
	}
	matches := expression.FindAllStringSubmatchIndex(corpus, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("%w：fromRequest 的模式 %q 在请求里一个都没匹配上", ErrScriptedPlaceholder, pattern)
	}
	last := matches[len(matches)-1]
	if len(last) >= 4 && last[2] >= 0 {
		return corpus[last[2]:last[3]], nil
	}
	return corpus[last[0]:last[1]], nil
}

// substituteString 把一个剧本字符串里每一处 `{{fromRequest:<正则>}}` 换掉。
//
// 源: packages/test-support/llm-replay/src/index.ts:360-379
func substituteString(text, corpus string) (string, error) {
	var builder strings.Builder
	cursor := 0
	for {
		offset := strings.Index(text[cursor:], fromRequestOpen)
		if offset < 0 {
			builder.WriteString(text[cursor:])
			return builder.String(), nil
		}
		open := cursor + offset
		body := open + len(fromRequestOpen)
		tail := strings.Index(text[body:], fromRequestClose)
		if tail < 0 {
			return "", fmt.Errorf("%w：fromRequest 占位符没闭合：%q", ErrScriptedPlaceholder, text)
		}
		end := body + tail
		// 一串连续 `}` 的**最后两个**才是结束符，所以模式可以拿 `[0-9a-f]{4}` 这样的
		// 花括号量词收尾。
		for end+len(fromRequestClose) < len(text) && text[end+len(fromRequestClose)] == '}' {
			end++
		}
		resolved, err := resolveFromRequest(text[body:end], corpus)
		if err != nil {
			return "", err
		}
		builder.WriteString(text[cursor:open])
		builder.WriteString(resolved)
		cursor = end + len(fromRequestClose)
	}
}

// substituteValue 深复制一份 JSON 值，顺路把里面的占位符解算掉。
//
// 源: packages/test-support/llm-replay/src/index.ts:382-390
//
// 新增: 对象这一支走 `map[string]json.RawMessage` 再排回去，键的顺序因此被重排成
// 字典序。这不要紧——交出去的这份字节马上又被 [readEntry] 读回结构体，键序对结果
// 没有影响；真正依赖顺序的是**语料**那一侧，那边走的是记号流。
func substituteValue(data json.RawMessage, corpus string) (json.RawMessage, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 {
		return data, nil
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, fmt.Errorf("%w：剧本里的字符串读不回来：%w", ErrScriptedPlaceholder, err)
		}
		if !strings.Contains(text, fromRequestOpen) {
			return data, nil
		}
		resolved, err := substituteString(text, corpus)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resolved)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, fmt.Errorf("%w：剧本里的数组读不回来：%w", ErrScriptedPlaceholder, err)
		}
		for index, item := range items {
			replaced, err := substituteValue(item, corpus)
			if err != nil {
				return nil, err
			}
			items[index] = replaced
		}
		return json.Marshal(items)
	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, fmt.Errorf("%w：剧本里的对象读不回来：%w", ErrScriptedPlaceholder, err)
		}
		for key, value := range fields {
			replaced, err := substituteValue(value, corpus)
			if err != nil {
				return nil, err
			}
			fields[key] = replaced
		}
		return json.Marshal(fields)
	default:
		return data, nil
	}
}

// ResolveScriptedEntry 拿活的请求把一条剧本条目里每一处 `{{fromRequest:<正则>}}`
// 解算掉。
//
// 源: packages/test-support/llm-replay/src/index.ts:412-418
//
// 语料是请求消息里每一个字符串叶子拿换行接起来；模式的**最后**一次匹配赢，它的第一个
// 捕获组（没有捕获组就是整段匹配）替回原处。场景的旁挂文件拿它来写一个静态文件不可能
// 知道的参数，比如一个随机铸出来的 goal id，模型必须把它原样回填。匹配不上、模式编译
// 不了、占位符没闭合，三件事全都当场失败。推导出来的条目和旁挂文件里的条目走同一条
// 解算路。
//
// 一条不带占位符的条目原样交回，不复制。
func ResolveScriptedEntry(entry Entry, messages []llm.Message) (Entry, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("%w：剧本条目排不出去：%w", ErrScriptedPlaceholder, err)
	}
	if !bytes.Contains(encoded, []byte(fromRequestOpen)) {
		return entry, nil
	}
	request, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("%w：请求消息排不出去：%w", ErrScriptedPlaceholder, err)
	}
	leaves, err := collectStrings(request, nil)
	if err != nil {
		return nil, err
	}
	resolved, err := substituteValue(encoded, strings.Join(leaves, "\n"))
	if err != nil {
		return nil, err
	}
	// 解算完再走一遍读条目那道校验：一次替换可能把一个分块的字段换成解释不了的东西，
	// 那种错误应该在派流之前当场报，而不是变成一条读者看不懂的分块。
	return readEntry(resolved, "剧本条目", "resolved entry")
}
