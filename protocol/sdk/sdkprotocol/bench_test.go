// 本文件的作用：SDK 行协议在洪泛下的压力基线。
//
// 这条通道的对面是**不受本进程控制**的：一个 SDK 客户端想发多快就发多快，想发
// 多大就发多大。所以这里量的三件事都是防守面的数字：
//
//   - **名额上限收多少税。** MaxConcurrentFrames 是拿读循环里同步取名额换来的
//     背压，代价是每一帧多一次通道收发。Flood 那一组把不设限和几档名额并排跑，
//     差值就是这份保护的价钱。
//
//   - **大帧的解码代价随长度怎么长。** 上限之内的大帧是要真解的，一个 16MB 的
//     合法帧照样要走完 JSON 解析。
//
//   - **一次请求往返多少钱。** 这是 SDK 侧每一次同步调用的下限延迟。
//
// 这几条都跑在 [io.Pipe] 上，没有真的系统调用开销，所以量出来的是**本包自己**
// 的成本，不是端到端延迟。

package sdkprotocol

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// benchNotification 是一帧最小的合法通知。
const benchNotification = `{"jsonrpc":"2.0","method":"session.event","params":{}}` + "\n"

// BenchmarkTransportNotificationFlood 量的是对面拿通知洪泛时本端每帧的成本。
//
// 各档名额的意义：1 是全串行（最坏的背压），8 和 64 是真实配置（64 是
// [DefaultMaxConcurrentFrames]），-1 是不设限——也就是取名额那两次通道操作的
// 对照组。不设限那一档快出来的部分，就是背压这份保护收的税。
func BenchmarkTransportNotificationFlood(b *testing.B) {
	for _, slots := range []int{1, 8, DefaultMaxConcurrentFrames, -1} {
		b.Run(strconv.Itoa(slots), func(b *testing.B) {
			frame := []byte(benchNotification)
			reader, writer := io.Pipe()
			defer func() { _ = writer.Close() }()

			var seen atomic.Int64
			total := int64(b.N)
			done := make(chan struct{})
			transport := NewLineTransportWith(b.Context(), reader, io.Discard, Handlers{
				Notification: func(context.Context, string, json.RawMessage) {
					if seen.Add(1) == total {
						close(done)
					}
				},
			}, TransportOptions{MaxConcurrentFrames: slots})
			defer transport.Close()

			b.ReportAllocs()
			b.ResetTimer()
			go func() {
				for range total {
					if _, err := writer.Write(frame); err != nil {
						return
					}
				}
			}()
			<-done
		})
	}
}

// BenchmarkTransportLargeFrame 量的是一帧合法但很大的负载要多少钱收下来。
//
// 上限只挡住**超过**上限的帧；上限之内的大帧要真读真解。这一组给的是「把
// MaxFrameBytes 调大」这个决定的代价表。
func BenchmarkTransportLargeFrame(b *testing.B) {
	for _, size := range []int{1 << 10, 1 << 16, 1 << 20} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			payload, err := json.Marshal(strings.Repeat("x", size))
			if err != nil {
				b.Fatalf("排负载失败：%v", err)
			}
			frame := []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"pad":` +
				string(payload) + "}}\n")

			reader, writer := io.Pipe()
			defer func() { _ = writer.Close() }()

			var seen atomic.Int64
			total := int64(b.N)
			done := make(chan struct{})
			transport := NewLineTransportWith(b.Context(), reader, io.Discard, Handlers{
				Notification: func(context.Context, string, json.RawMessage) {
					if seen.Add(1) == total {
						close(done)
					}
				},
			}, TransportOptions{MaxFrameBytes: -1})
			defer transport.Close()

			b.SetBytes(int64(len(frame)))
			b.ReportAllocs()
			b.ResetTimer()
			go func() {
				for range total {
					if _, err := writer.Write(frame); err != nil {
						return
					}
				}
			}()
			<-done
		})
	}
}

// BenchmarkTransportRequestRoundTrip 量的是一次请求打过去、回话拿回来的整个来回。
//
// 这是 SDK 每一次同步调用要付的固定成本：两次分帧、两次 JSON 编解码、一次
// 处理器分流，加上等待那一侧的唤醒。
func BenchmarkTransportRequestRoundTrip(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()

	clientReader, clientWriter := io.Pipe()
	serverReader, serverWriter := io.Pipe()
	client := NewLineTransport(ctx, serverReader, clientWriter, Handlers{})
	server := NewLineTransport(ctx, clientReader, serverWriter, Handlers{
		Request: func(_ context.Context, _ string, params json.RawMessage) (any, error) {
			return json.RawMessage(params), nil
		},
	})
	defer func() {
		client.Close()
		server.Close()
		_ = clientWriter.Close()
		_ = serverWriter.Close()
	}()

	params := map[string]any{"ping": true}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var result json.RawMessage
		if err := client.Request(ctx, "echo", params, &result); err != nil {
			b.Fatalf("请求失败：%v", err)
		}
	}
}
