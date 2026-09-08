// 本文件的作用：等待那一问的用例——指纹变了就放行、没动静就到点、撤了就报错，
// 外加数在跑的同侪。

package agentteam

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
)

// brisk 把轮询间隔压到用例等得起的长度。
func brisk(config *Config) { config.PollInterval = 2 * time.Millisecond }

func TestWait拒非正时限(t *testing.T) {
	box := boot(t, brisk)
	who := box.leader(t)

	if _, err := box.service.Wait(t.Context(), who, 0); !errors.Is(err, CodeInvalidTimeout) {
		t.Fatalf("零时限该报 %s，拿到 %v", CodeInvalidTimeout, err)
	}
}

func TestWait没动静就到点(t *testing.T) {
	box := boot(t, brisk)
	who := box.leader(t)

	result, err := box.service.Wait(t.Context(), who, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("等待不该失败：%v", err)
	}
	if !result.TimedOut {
		t.Fatal("没人动过这支团队，该报到点")
	}
}

func TestWait任务板一动就放行(t *testing.T) {
	box := boot(t, brisk)
	who := box.leader(t)

	done := make(chan WaitResult, 1)
	go func() {
		result, err := box.service.Wait(context.Background(), who, 5*time.Second)
		if err != nil {
			t.Errorf("等待不该失败：%v", err)
		}
		done <- result
	}()

	// 让那一头先取到基线，再往板上写一条。
	time.Sleep(10 * time.Millisecond)
	box.createTask(t, who, CreateTaskRequest{})

	select {
	case result := <-done:
		if result.TimedOut {
			t.Fatal("板上多了一条任务，不该报到点")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("板动了，等待该醒过来")
	}
}

func TestWait任务改一个字也放行(t *testing.T) {
	box := boot(t, brisk)
	who := box.leader(t)
	task := box.createTask(t, who, CreateTaskRequest{Subject: "原来的标题"})

	done := make(chan WaitResult, 1)
	go func() {
		result, err := box.service.Wait(context.Background(), who, 5*time.Second)
		if err != nil {
			t.Errorf("等待不该失败：%v", err)
		}
		done <- result
	}()

	time.Sleep(10 * time.Millisecond)
	subject := "改过的标题"
	box.update(t, who, UpdateTaskRequest{
		TaskID:      task.ID,
		ExpectedRev: task.Rev,
		Action:      ActionEdit,
		Subject:     &subject,
	})

	select {
	case result := <-done:
		if result.TimedOut {
			// 标题不进指纹，靠的是版本号跟着动，见 [Service.fingerprint]。
			t.Fatal("改标题也是动静，不该报到点")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("任务改了，等待该醒过来")
	}
}

func TestWait撤了上下文就报错(t *testing.T) {
	box := boot(t, brisk)
	who := box.leader(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := box.service.Wait(ctx, who, 5*time.Second)
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, CodeWaitAborted) {
			t.Fatalf("撤了该报 %s，拿到 %v", CodeWaitAborted, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("上下文撤了，等待该当场回来")
	}
}

func TestActivePeers不数发起人自己(t *testing.T) {
	box := boot(t)
	worker := box.spawn(t, "worker")
	who := box.caller(t, leadID)

	// 队长自己活着，队友歇着：一个都不该数进来。
	count, err := box.service.ActivePeers(t.Context(), who)
	if err != nil {
		t.Fatalf("数同侪不该失败：%v", err)
	}
	if count != 0 {
		t.Fatalf("没人在跑，该数出 0，拿到 %d", count)
	}

	box.agents.add(&stubAgent{id: worker.ID, status: agent.StatusRunning})
	count, err = box.service.ActivePeers(t.Context(), who)
	if err != nil {
		t.Fatalf("数同侪不该失败：%v", err)
	}
	if count != 1 {
		t.Fatalf("队友跑起来了，该数出 1，拿到 %d", count)
	}
}

func TestTasks不列删掉的(t *testing.T) {
	box := boot(t)
	who := box.leader(t)
	kept := box.createTask(t, who, CreateTaskRequest{Subject: "留着的"})
	dropped := box.createTask(t, who, CreateTaskRequest{Subject: "删掉的"})
	box.update(t, who, UpdateTaskRequest{
		TaskID:      dropped.ID,
		ExpectedRev: dropped.Rev,
		Action:      ActionDelete,
	})

	views, err := box.service.Tasks(t.Context(), box.team())
	if err != nil {
		t.Fatalf("列任务不该失败：%v", err)
	}
	if len(views) != 1 || views[0].ID != kept.ID {
		t.Fatalf("该只剩 %q 一条，拿到 %+v", kept.ID, views)
	}
}

func TestTask读得出删掉的那条(t *testing.T) {
	box := boot(t)
	who := box.leader(t)
	task := box.createTask(t, who, CreateTaskRequest{})
	box.update(t, who, UpdateTaskRequest{
		TaskID:      task.ID,
		ExpectedRev: task.Rev,
		Action:      ActionDelete,
	})

	view, err := box.service.Task(t.Context(), box.team(), task.ID)
	if err != nil {
		t.Fatalf("读删掉的任务不该失败：%v", err)
	}
	if view.Status != TaskDeleted {
		t.Fatalf("该如实报 %s，拿到 %s", TaskDeleted, view.Status)
	}

	if _, err := box.service.Task(t.Context(), box.team(), "task-404"); !errors.Is(err, CodeTaskNotFound) {
		t.Fatalf("没有的任务该报 %s，拿到 %v", CodeTaskNotFound, err)
	}
}
