// 本文件的作用：在仓库**外面**真的建一个 Go 模块，用公开 module path 把本仓库
// 引进去，编译、vet、跑起来。这是 module path 这件事唯一测得到的地方。
//
// 新增: DSH 没有对应物——TypeScript 那边靠 `npm pack` 之后装进一个空目录来验同一
// 件事，而 Go 的对应物只能是「另起一个模块，replace 到本地」。
//
// 为什么非要在仓库外面跑：仓库**里面**的每一个包都能编译，恰恰说不出 module path
// 对不对。`go build ./...` 解析 import 时走的是「主模块的 module 行 + 相对路径」，
// 所以哪怕 go.mod 写的是 `module ds-harness-go`、代码写的是
// `github.com/snight1983/ds-harness-go/...`，仓库内部照样全绿——而一个外部调用方
// 从 GitHub 路径引进来时会撞上 `package ds-harness-go/core/scope is not in std`。
// 那正是这个门禁存在的由头。
//
// 它同时是「每一个公开包都能被外部引用」的证据：生成出来的那个程序空引全部
// 可发布包，任何一个包漏进了 internal 依赖、或者只在仓库内部才解析得开，这里
// 当场编译不过。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// consumerModule 是生成出来那个外部模块的名字。
//
// 刻意用一个**不属于本仓库**的路径：它要模仿的是一个第三方宿主，而不是本仓库
// 自己的一个子模块。
const consumerModule = "consumercheck.example/host"

// namedImports 是生成的程序里要具名引的那几个包，它们不再进空引清单。
var namedImports = map[string]string{
	"github.com/snight1983/ds-harness-go/core/agentloop":      "agentloop",
	"github.com/snight1983/ds-harness-go/core/scope":          "scope",
	"github.com/snight1983/ds-harness-go/core/session":        "coresession",
	"github.com/snight1983/ds-harness-go/llm":                 "llm",
	"github.com/snight1983/ds-harness-go/session":             "sessionlog",
	"github.com/snight1983/ds-harness-go/session/persistence": "persistence",
}

func main() {
	root, err := findRepositoryRoot()
	if err != nil {
		fail(err)
	}
	module, err := readModulePath(root)
	if err != nil {
		fail(err)
	}
	packages, err := listConsumablePackages(root, module)
	if err != nil {
		fail(err)
	}
	if err := check(root, module, packages); err != nil {
		fail(err)
	}
	fmt.Printf("仓库外消费检查通过：module path %s，%d 个公开包引得进来，最小闭环跑通\n",
		module, len(packages))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "仓库外消费检查失败：")
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func findRepositoryRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil && !info.IsDir() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("从当前目录向上找不到 go.mod")
		}
		directory = parent
	}
}

// readModulePath 读出本仓库声明的 module path。
//
// 顺带把「它得像个能被外部引用的路径」这一条钉在这里：一个不带域名的裸路径
// （`ds-harness-go`）在仓库内部完全可用，只有到了外部调用方那里才炸——所以这条
// 检查不能只依赖下面那次编译，它要在错误信息里说得出**为什么**。
func readModulePath(root string) (string, error) {
	output, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m 失败：%w", err)
	}
	module := strings.TrimSpace(string(output))
	if module == "" {
		return "", errors.New("go list -m 没给出 module path")
	}
	if !strings.Contains(strings.SplitN(module, "/", 2)[0], ".") {
		return "", fmt.Errorf(
			"module path %q 的第一段不含域名，仓库外的调用方解析不了它"+
				"（会报 package %s/... is not in std）", module, module)
	}
	return module, nil
}

// listConsumablePackages 列出所有「外部装配方引得进来」的包。
//
// 三类排除掉：命令（package main，引不了）、internal（按语言规则外部就进不来）、
// 以及被 Git 忽略的临时目录（不是可发布的东西）。口径和 tools/doccheck 一致。
func listConsumablePackages(root, module string) ([]string, error) {
	command := exec.Command("go", "list", "-f", "{{.ImportPath}}\t{{.Name}}\t{{.Dir}}", "./...")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./... 失败：%w", err)
	}

	type listed struct{ path, dir string }
	var candidates []listed
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("无法解析 go list 输出：%q", line)
		}
		path, name, dir := fields[0], fields[1], fields[2]
		if name == "main" || strings.Contains(path, "/internal/") {
			continue
		}
		candidates = append(candidates, listed{path: path, dir: dir})
	}

	relatives := make([]string, 0, len(candidates))
	for _, item := range candidates {
		rel, relErr := filepath.Rel(root, item.dir)
		if relErr != nil {
			return nil, relErr
		}
		relatives = append(relatives, filepath.ToSlash(rel))
	}
	ignored, err := gitIgnored(root, relatives)
	if err != nil {
		return nil, err
	}

	packages := make([]string, 0, len(candidates))
	for index, item := range candidates {
		if _, skip := ignored[relatives[index]]; !skip {
			packages = append(packages, item.path)
		}
	}
	if len(packages) == 0 {
		return nil, errors.New("一个可发布包都没列出来，这不对")
	}
	sort.Strings(packages)
	return packages, nil
}

// gitIgnored 问 Git 这些相对路径里哪些是被忽略的。
func gitIgnored(root string, relatives []string) (map[string]struct{}, error) {
	command := exec.Command("git", "check-ignore", "--stdin")
	command.Dir = root
	command.Stdin = strings.NewReader(strings.Join(relatives, "\n") + "\n")
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		// 退出码 1 是「一个都没忽略」，不是失败。
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("git check-ignore 失败：%w", err)
	}
	ignored := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if path := filepath.ToSlash(strings.TrimSpace(line)); path != "" {
			ignored[path] = struct{}{}
		}
	}
	return ignored, nil
}

// check 把那个外部模块摆出来，然后编译、vet、跑。
func check(root, module string, packages []string) error {
	directory, err := os.MkdirTemp("", "consumercheck-")
	if err != nil {
		return fmt.Errorf("建临时模块目录失败：%w", err)
	}
	defer os.RemoveAll(directory)

	if err := materialize(directory, root, module, packages); err != nil {
		return err
	}
	// 三步各自说明一件事：build 说「引得进来」，vet 说「引进来之后是像话的」，
	// run 说「跑得起来」。只 build 的话，一个装配顺序上的错要到宿主上线才暴露。
	for _, step := range [][]string{
		{"build", "-buildvcs=false", "./..."},
		{"vet", "./..."},
		{"run", "-buildvcs=false", "."},
	} {
		command := exec.Command("go", step...)
		command.Dir = directory
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return fmt.Errorf("在仓库外的模块里跑 go %s 失败：%w\n%s",
				strings.Join(step, " "), runErr, output)
		}
	}
	return nil
}

// materialize 把那个外部模块的 go.mod、go.sum 和源码写出来。
//
// go.mod 里的 require / go.sum 直接抄本仓库那一份：这个门禁要验的是 module path,
// 不是依赖解析，让它去联网重新解一遍只会把网络抖动算进失败里。replace 指到本地
// 仓库，因为这个 module path 此刻还没发布——一个外部调用方拿 GitHub 路径引、
// replace 到本地检出，正是本仓库还没打 tag 时唯一的消费方式。
func materialize(directory, root, module string, packages []string) error {
	source, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("读仓库 go.mod 失败：%w", err)
	}
	lines := strings.Split(string(source), "\n")
	var goDirective string
	var requires []string
	for index := 0; index < len(lines); index++ {
		line := strings.TrimRight(lines[index], "\r")
		switch {
		case strings.HasPrefix(line, "go "):
			goDirective = line
		case strings.HasPrefix(line, "require"):
			requires = append(requires, line)
			if strings.HasSuffix(strings.TrimSpace(line), "(") {
				for index++; index < len(lines); index++ {
					body := strings.TrimRight(lines[index], "\r")
					requires = append(requires, body)
					if strings.TrimSpace(body) == ")" {
						break
					}
				}
			}
		}
	}
	if goDirective == "" {
		return errors.New("仓库 go.mod 里没有 go 指令")
	}

	var manifest strings.Builder
	fmt.Fprintf(&manifest, "module %s\n\n%s\n\n", consumerModule, goDirective)
	fmt.Fprintf(&manifest, "require %s v0.0.0\n\n", module)
	fmt.Fprintf(&manifest, "replace %s => %s\n\n", module, filepath.ToSlash(root))
	manifest.WriteString(strings.Join(requires, "\n"))
	manifest.WriteString("\n")
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(manifest.String()), 0o644); err != nil {
		return fmt.Errorf("写外部模块 go.mod 失败：%w", err)
	}

	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		return fmt.Errorf("读仓库 go.sum 失败：%w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go.sum"), sums, 0o644); err != nil {
		return fmt.Errorf("写外部模块 go.sum 失败：%w", err)
	}

	program := renderProgram(packages)
	if err := os.WriteFile(filepath.Join(directory, "main.go"), []byte(program), 0o644); err != nil {
		return fmt.Errorf("写外部模块源码失败：%w", err)
	}
	return nil
}

// renderProgram 排出那个外部宿主程序。
func renderProgram(packages []string) string {
	var out strings.Builder
	out.WriteString("// 由 tools/consumercheck 生成：一个仓库外的宿主。\n\npackage main\n\nimport (\n")
	for _, standard := range []string{"context", "encoding/json", "fmt", "os"} {
		fmt.Fprintf(&out, "\t%q\n", standard)
	}
	out.WriteString("\n")

	aliases := make([]string, 0, len(namedImports))
	for path := range namedImports {
		aliases = append(aliases, path)
	}
	sort.Strings(aliases)
	for _, path := range aliases {
		fmt.Fprintf(&out, "\t%s %q\n", namedImports[path], path)
	}
	out.WriteString("\n")
	for _, path := range packages {
		if _, named := namedImports[path]; named {
			continue
		}
		fmt.Fprintf(&out, "\t_ %q\n", path)
	}
	out.WriteString(")\n")
	out.WriteString(consumerBody)
	return out.String()
}

// consumerBody 是那个外部宿主的正文。
//
// 它做两件事。一是编译期那句断言：持久化编排器**自己**就满足 agent 工厂要的会话
// 持久化。这条接缝一度装不起来（编排器交回准备期、工厂声明的是会话），而且两边
// 各自的测试都绿着——外部装配方是唯一撞得上它的人，所以这句断言摆在这里。
// 二是真的把最小闭环走一遍：建作用域、建会话存储、建会话、追加一条用户消息、
// 读回来。只编译不跑的话，一个装配顺序上的错要到宿主上线才暴露。
const consumerBody = `
var _ agentloop.SessionPersistence = (*persistence.Coordinator)(nil)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	root := scope.NewRoot()
	defer func() { _ = root.Dispose(ctx) }()

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		return fmt.Errorf("造会话存储失败：%w", err)
	}
	live, err := store.Create(ctx, root, sessionlog.SessionID("仓库外的宿主"), coresession.CreateOptions{})
	if err != nil {
		return fmt.Errorf("建会话失败：%w", err)
	}

	payload, err := json.Marshal(sessionlog.UserMessageData{Message: llm.Message{
		ID:      llm.MessageID("u1"),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: "从仓库外面说话"}},
		Source:  llm.UserSource{},
	}})
	if err != nil {
		return fmt.Errorf("用户消息排不出去：%w", err)
	}
	if _, err := live.Append(sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      payload,
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		return fmt.Errorf("追加失败：%w", err)
	}
	if got := len(live.Events()); got != 1 {
		return fmt.Errorf("该有一条事件，实际 %d 条", got)
	}
	return nil
}
`
