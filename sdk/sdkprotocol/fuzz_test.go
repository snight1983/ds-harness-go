// 本文件的作用：拿任意字节压按行分帧那一层。
//
// 为什么这一层要 fuzz：它是本进程和**别人写的 SDK** 之间那道缝上唯一自己写的
// 解析器。对面发来的字节不受本仓库控制，而这一层的规矩是「认不出的行跳过去，
// 接着读」——那条规矩必须在任意输入下都成立，不能变成崩、变成挂、也不能变成
// 「攒到进程被杀」。用例只能举出想得到的坏输入，想不到的那些正是要防的。

package sdkprotocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func FuzzLineStreamReadObject(f *testing.F) {
	f.Add("")
	f.Add("\n")
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	f.Add("不是 json\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	f.Add("[1,2,3]\n")
	f.Add(`{"a":`)
	f.Add(strings.Repeat("x", 9000) + "\n{}\n")
	f.Add("{}\n{}\n{}\n")
	f.Add("\x00{}\n")
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"ping"}`) // 最后一行没有换行

	f.Fuzz(func(t *testing.T, input string) {
		const maxFrame = 512
		stream := &lineStream{
			reader:   bufio.NewReaderSize(strings.NewReader(input), 64),
			writer:   io.Discard,
			maxFrame: maxFrame,
		}

		// 每读一帧至少吃掉一个字节，所以帧数不会超过输入长度。这个上限是那条
		// 「一定会停」的判据：readLine 里那个 continue 一旦漏掉推进，循环就不停了，
		// 而不停在 fuzz 下表现成超时，定位起来比一个断言难得多。
		for frames := 0; frames <= len(input)+1; frames++ {
			var frame json.RawMessage
			err := stream.ReadObject(&frame)
			if err != nil {
				// 结束只有一个理由：底下的流读完了。
				if !errors.Is(err, io.EOF) {
					t.Fatalf("按行分帧只该以 EOF 结束，实际 %v", err)
				}
				return
			}
			// 交出来的必须是一个 JSON 对象——ReadObject 的契约就是把不是对象的
			// 行跳过去。放行一个数组或者标量，上面那些按具名字段解 params 的
			// 处理器会收到一份它们没法解的东西。
			trimmed := bytes.TrimSpace(frame)
			if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
				t.Fatalf("交出来的不是一个 JSON 对象：%q", string(frame))
			}
			// 超长的行说了要丢掉。放过一帧超过上限的，那条「一条不带换行的输入
			// 不能把进程攒死」的保证就没了。
			if len(frame) > maxFrame {
				t.Fatalf("交出来的帧有 %d 字节，上限是 %d", len(frame), maxFrame)
			}
		}
		t.Fatalf("%d 字节的输入解出了超过这么多帧，分帧没在推进", len(input))
	})
}

// objectParams 的契约是「交出来的一定是一个对象」，它在每一条请求上都跑。
// 塌不成对象的时候交回 {}，让处理器走「必填字段没给」那条正常的路。
func FuzzObjectParams(f *testing.F) {
	f.Add(`{"a":1}`)
	f.Add(`[1,2,3]`)
	f.Add(``)
	f.Add(`   `)
	f.Add(`null`)
	f.Add(`"字符串"`)
	f.Add("\t\n {\"a\":1} \n")

	f.Fuzz(func(t *testing.T, input string) {
		got := objectParams(json.RawMessage(input))
		if len(got) == 0 || got[0] != '{' {
			t.Fatalf("%q 塌出来的不是一个对象：%q", input, string(got))
		}
		// 原来就是对象的，除了两头的空白之外一个字节都不该改——那是请求的负载。
		trimmed := bytes.TrimSpace([]byte(input))
		if len(trimmed) > 0 && trimmed[0] == '{' && !bytes.Equal(got, trimmed) {
			t.Fatalf("已经是对象的负载被改写了\n进去：%q\n出来：%q", string(trimmed), string(got))
		}
	})
}
