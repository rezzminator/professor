package mockengine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

type openCodePlugins struct {
	mu     sync.Mutex
	proc   *process
	child  deps.Process
	input  io.WriteCloser
	output *bufio.Reader
}

// The native plugins stay loaded so their parentage and report queues survive
// across actions, while every invocation refreshes the child environment.
const openCodePluginDriver = `import {createInterface} from "node:readline"
import {pathToFileURL} from "node:url"
let plugins = [], current = {}, logs = []
const client = {session:{get:async ({path}) => ({data:current[path.id]})}, app:{log:async ({body}) => { logs.push(body) }}}
for await (const line of createInterface({input:process.stdin})) {
  try {
    const request = JSON.parse(line)
    Object.assign(process.env, request.env)
    if (request.sessions) Object.assign(current, request.sessions)
    logs = []
    if (request.modules) {
      for (const path of request.modules) {
        const module = await import(path.startsWith("file:") ? path : pathToFileURL(path).href)
        for (const entry of new Set(Object.values(module))) {
          if (typeof entry !== "function") continue
          const hooks = await entry({client, directory:request.directory, worktree:request.directory, serverUrl:new URL("http://127.0.0.1")})
          plugins.push(hooks)
          if (hooks.config) await hooks.config(request.config)
        }
      }
    } else {
      for (const hooks of plugins) {
        if (hooks[request.hook]) await hooks[request.hook](request.input, request.output)
      }
    }
    process.stdout.write(JSON.stringify({logs}) + "\n")
  } catch (error) { process.stdout.write(JSON.stringify({error:String(error),logs}) + "\n") }
}
`

func startOpenCodePlugins(proc *process) (*openCodePlugins, error) {
	config, err := openCodeConfig(proc)
	if err != nil {
		return nil, err
	}
	var modules []string
	if entries, ok := config[openCodePluginKey].([]any); ok {
		for _, entry := range entries {
			path, ok := entry.(string)
			if !ok || (!filepath.IsAbs(path) && !strings.HasPrefix(path, "file:")) {
				return nil, fmt.Errorf("mock OpenCode plugin must name an installed local file: %v", entry)
			}
			modules = append(modules, path)
		}
	}
	root := openCodeConfigRoot(proc)
	for _, directory := range []string{filepath.Join(root, "plugins"), filepath.Join(proc.cwd, ".opencode", "plugins")} {
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read OpenCode plugins %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".js") && !strings.HasSuffix(entry.Name(), ".mjs")) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if !slices.Contains(modules, path) {
				modules = append(modules, path)
			}
		}
	}
	if len(modules) == 0 {
		return nil, nil
	}
	environment, err := proc.actionEnvironment(os.Environ())
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"HOME", "HERDR_ENV", "TMUX", "OPENCODE_CONFIG_CONTENT"} {
		environment = append(environment, name+"="+proc.env(name))
	}
	child, err := (deps.RealRunner{}).Start(
		proc.ctx,
		[]string{"node", "--experimental-default-type=module", "--input-type=module", "-e", openCodePluginDriver},
		deps.StartOptions{Env: environment, Dir: proc.cwd, StdinPipe: true, StdoutPipe: true, Stderr: proc.stderr},
	)
	if err != nil {
		return nil, fmt.Errorf("start installed OpenCode plugins: %w", err)
	}
	input, err := child.StdinPipe()
	if err != nil {
		_ = child.Kill()
		_ = child.Wait()
		return nil, err
	}
	output, err := child.StdoutPipe()
	if err != nil {
		_ = input.Close()
		_ = child.Kill()
		_ = child.Wait()
		return nil, err
	}
	plugins := &openCodePlugins{proc: proc, child: child, input: input, output: bufio.NewReader(output)}
	if err := plugins.call(
		map[string]any{"modules": modules, openCodeDirectoryKey: proc.cwd, "config": config},
	); err != nil {
		return nil, errors.Join(err, plugins.close())
	}
	return plugins, nil
}

func (plugins *openCodePlugins) call(request map[string]any) error {
	plugins.mu.Lock()
	defer plugins.mu.Unlock()
	environment, err := plugins.proc.actionEnvironment(nil)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	request["env"] = values
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode OpenCode plugin invocation: %w", err)
	}
	if _, err := plugins.input.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write OpenCode plugin invocation: %w", err)
	}
	line, err := plugins.output.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read OpenCode plugin answer: %w", err)
	}
	var answer struct {
		Error string                            `json:"error"`
		Logs  []struct{ Level, Message string } `json:"logs"`
	}
	if err := json.Unmarshal(line, &answer); err != nil {
		return fmt.Errorf("decode OpenCode plugin answer %q: %w", line, err)
	}
	for _, entry := range answer.Logs {
		warn(plugins.proc.stderr, "OpenCode plugin %s: %s", entry.Level, entry.Message)
	}
	if answer.Error != "" {
		return errors.New(answer.Error)
	}
	return nil
}

func (plugins *openCodePlugins) close() error {
	plugins.mu.Lock()
	defer plugins.mu.Unlock()
	return errors.Join(plugins.input.Close(), plugins.child.Wait())
}
