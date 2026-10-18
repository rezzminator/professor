"""Chrome launcher seam test — runs with NO browser and NO patchright.

On macOS the TCC "responsible process" of a Chrome this worker launches is the
`pfm` binary, and tccd refuses Chrome's touch of its own app bundle as pfm
modifying apps. The worker therefore points patchright at a wrapper that
posix_spawns the real Chrome with responsibility disclaimed (browser.py
chrome_launcher_main). These cases pin what that wrapper must keep: Chrome's
arguments, environment and inherited descriptors, its exit status and the
signals sent to it, a normal launch when the disclaim is unavailable, and no
wrapper at all off darwin.

The platform-independent cases drive the launcher with a stand-in libSystem or
spawn function. The wrapper cases run the real wrapper script around a fake
Chrome: on darwin the real libSystem disclaims, anywhere else its absence takes
the fallback — the pass-through assertions hold on both. The last case asks the
kernel who is responsible for a disclaimed process and runs only on darwin.

Run: python3 browser_chrome_launcher_test.py
"""

import asyncio
import contextlib
import ctypes
import errno
import io
import json
import os
import shlex
import signal
import subprocess
import sys
import tempfile
import textwrap
import threading
import time

import browser

ON_DARWIN = sys.platform == "darwin"


def fake_chrome(directory, body):
    """A stand-in Chrome: a Python script at a path with a space in it."""
    folder = os.path.join(directory, "Fake Chrome.app")
    os.makedirs(folder, exist_ok=True)
    path = os.path.join(folder, "Fake Chrome")
    with open(path, "w", encoding="utf-8") as script:
        script.write(f"#!{sys.executable}\n" + textwrap.dedent(body))
    os.chmod(path, 0o755)
    return path


def read(path):
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def wait_for(path, seconds=10):
    deadline = time.monotonic() + seconds
    while not os.path.exists(path):
        assert time.monotonic() < deadline, f"{path} never appeared"
        time.sleep(0.02)


def ready_then_await_signal(ready, seconds=30):
    """Fake-Chrome body lines, after its handlers are set: publish *ready*, then
    wait up to *seconds* for a signal. Two races end a case that waits on a
    bare time.sleep with a lost signal, and a loaded machine wins both:
    `open(ready, "w").write("up")` shows the file before the statement ends,
    and a signal landing in the file's implicit close runs its handler inside
    that destructor, which discards the handler's SystemExit; and a signal
    landing just before the sleep starts waits out the whole sleep, since
    Python runs a handler only between bytecodes. So *ready* appears only
    once it is written and closed, by rename; and the wakeup fd records the
    signal whenever it lands, so select returns at once and the handler runs
    in plain bytecode, where its exit ends the process."""
    return textwrap.dedent(f"""
        import os, select, signal
        wake_read, wake_write = os.pipe()
        os.set_blocking(wake_write, False)
        signal.set_wakeup_fd(wake_write)
        with open({ready + ".tmp"!r}, "w") as up:
            up.write("up")
        os.replace({ready + ".tmp"!r}, {ready!r})
        select.select([wake_read], [], [], {seconds})
        """)


def spawn_for_real(path, argv, env):
    """A spawn that really starts the child, as spawn_disclaimed would."""
    return os.posix_spawn(path, argv, env)


# --- spawn_disclaimed against a stand-in libSystem ---------------------------


class FakeLibc:
    """Records the libSystem calls spawn_disclaimed makes, in order."""

    def __init__(self, disclaim_result=0, spawn_result=0, with_symbol=True):
        self.calls = []
        self.disclaim_result = disclaim_result
        self.spawn_result = spawn_result
        if with_symbol:
            self.responsibility_spawnattrs_setdisclaim = self._disclaim

    def posix_spawnattr_init(self, attr):
        self.calls.append("init")
        return 0

    def _disclaim(self, attr, value):
        self.calls.append(("disclaim", value))
        return self.disclaim_result

    def posix_spawn(self, pid, path, actions, attr, argv, env):
        self.calls.append(("spawn", path, actions, [a for a in argv], [e for e in env]))
        pid._obj.value = 4242
        return self.spawn_result

    def posix_spawnattr_destroy(self, attr):
        self.calls.append("destroy")
        return 0


def test_spawn_disclaimed_sets_the_disclaim_before_it_spawns_with_argv_and_env():
    libc = FakeLibc()
    pid = browser.spawn_disclaimed("/Apps/Chrome Dir/Chrome", ["Chrome", "--a=1", "b c"], {"K": "v", "L": "w x"}, libc=libc)
    assert pid == 4242
    kinds = [c if isinstance(c, str) else c[0] for c in libc.calls]
    assert kinds == ["init", "disclaim", "spawn", "destroy"], libc.calls
    assert libc.calls[1] == ("disclaim", 1), libc.calls
    _, path, actions, argv, env = libc.calls[2]
    assert path == b"/Apps/Chrome Dir/Chrome" and actions is None, libc.calls[2]
    assert argv == [b"Chrome", b"--a=1", b"b c", None], argv
    assert env == [b"K=v", b"L=w x", None], env


def test_spawn_disclaimed_refuses_a_libsystem_without_the_symbol():
    libc = FakeLibc(with_symbol=False)
    try:
        browser.spawn_disclaimed("/c", ["c"], {}, libc=libc)
    except AttributeError as e:
        assert browser.DISCLAIM_SYMBOL in str(e), e
    else:
        raise AssertionError("a libSystem without the disclaim symbol spawned anyway")
    assert not any(isinstance(c, tuple) and c[0] == "spawn" for c in libc.calls), libc.calls


def test_spawn_disclaimed_does_not_spawn_when_the_disclaim_is_refused():
    libc = FakeLibc(disclaim_result=errno.EINVAL)
    try:
        browser.spawn_disclaimed("/c", ["c"], {}, libc=libc)
    except OSError as e:
        assert e.errno == errno.EINVAL and browser.DISCLAIM_SYMBOL in str(e), e
    else:
        raise AssertionError("a refused disclaim spawned anyway")
    assert not any(isinstance(c, tuple) and c[0] == "spawn" for c in libc.calls), libc.calls
    assert libc.calls[-1] == "destroy", "the spawn attributes leaked"


def test_spawn_disclaimed_names_a_refused_spawn_and_still_destroys_the_attributes():
    libc = FakeLibc(spawn_result=errno.ENOENT)
    try:
        browser.spawn_disclaimed("/missing/Chrome", ["c"], {}, libc=libc)
    except OSError as e:
        assert e.errno == errno.ENOENT and "/missing/Chrome" in str(e), e
    else:
        raise AssertionError("a refused posix_spawn returned a pid")
    assert libc.calls[-1] == "destroy", libc.calls


# --- chrome_launcher_main: what Chrome keeps ---------------------------------


def launch(tmp, body, chrome_arguments=(), env=None, **hooks):
    """Run chrome_launcher_main around a fake Chrome in this process. Returns
    (how it ended, the status line)."""
    chrome = fake_chrome(tmp, body)
    status = os.path.join(tmp, "status")
    saved = dict(os.environ)
    os.environ.update(env or {})
    try:
        how = browser.chrome_launcher_main([status, chrome, *chrome_arguments],
                                           spawn=hooks.pop("spawn", spawn_for_real), **hooks)
    finally:
        os.environ.clear()
        os.environ.update(saved)
    return how, read(status).strip() if os.path.exists(status) else None


def test_launcher_passes_argv_and_env_to_chrome_unchanged():
    with tempfile.TemporaryDirectory() as tmp:
        out = os.path.join(tmp, "seen.json")
        how, status = launch(tmp, f"""
            import json, os, sys
            json.dump({{"argv": sys.argv[1:], "env": os.environ.get("PFM_LAUNCHER_PROBE")}}, open({out!r}, "w"))
            """, ["--remote-debugging-pipe", "--user-data-dir=/profiles/a b", "--headless"],
                            env={"PFM_LAUNCHER_PROBE": "kept"})
        seen = json.load(open(out))
    assert how == ("exit", 0), how
    assert seen == {"argv": ["--remote-debugging-pipe", "--user-data-dir=/profiles/a b", "--headless"],
                    "env": "kept"}, seen
    assert status.startswith(browser.STATUS_DISCLAIMED), status


def test_launcher_passes_inherited_descriptors_through_to_chrome():
    """Playwright drives Chrome over --remote-debugging-pipe on descriptors 3 and
    4: a descriptor open in the launcher must be open, at the same number, in Chrome."""
    read_end, write_end = os.pipe()
    os.set_inheritable(write_end, True)
    try:
        with tempfile.TemporaryDirectory() as tmp:
            how, _ = launch(tmp, f"""
                import os
                os.write({write_end}, b"through descriptor {write_end}")
                """)
        assert how == ("exit", 0), how
        os.close(write_end)
        expected = f"through descriptor {write_end}".encode()
        write_end = None
        assert os.read(read_end, 100) == expected
    finally:
        os.close(read_end)
        if write_end is not None:
            os.close(write_end)


def test_launcher_returns_chromes_exit_code():
    for code in (0, 1, 7, 255):
        with tempfile.TemporaryDirectory() as tmp:
            how, _ = launch(tmp, f"import sys\nsys.exit({code})\n")
        assert how == ("exit", code), (code, how)


def test_launcher_reports_the_signal_chrome_died_of():
    # Never a crash-class signal in a real process: on macOS the crash reporter
    # answers SIGABRT, SIGSEGV and the like with a dialog.
    with tempfile.TemporaryDirectory() as tmp:
        how, _ = launch(tmp, "import os, signal\nos.kill(os.getpid(), signal.SIGUSR1)\n")
    assert how == ("signal", signal.SIGUSR1), how


def test_launcher_reports_a_crash_signal_from_the_wait_status_without_a_real_crash():
    with tempfile.TemporaryDirectory() as tmp:
        for number in browser.CRASH_SIGNALS:
            how = browser.chrome_launcher_main(
                [os.path.join(tmp, "status"), "/chrome"], spawn=lambda path, argv, env: 4242,
                wait=lambda pid, flags, number=number: (pid, int(number)),  # the wait status of a death by that signal
            )
            assert how == ("signal", int(number)), (number, how)


def test_exit_like_chrome_exits_with_chromes_code():
    ended = []
    browser.exit_like_chrome(("exit", 7), kill=lambda *a: ended.append(("kill", a)), finish=ended.append)
    assert ended == [7], ended


def test_exit_like_chrome_never_raises_a_crash_signal_on_itself():
    """A Chrome crash must not become a crash of the launcher: macOS answers a
    core-dump signal with a "quit unexpectedly" dialog blamed on this process."""
    for number in browser.CRASH_SIGNALS:
        ended, killed = [], []
        captured = io.StringIO()
        with contextlib.redirect_stderr(captured):
            browser.exit_like_chrome(("signal", int(number)), kill=lambda *a: killed.append(a), finish=ended.append)
        assert killed == [], f"{signal.Signals(number).name} was raised on the launcher"
        assert ended == [128 + int(number)], (number, ended)
        assert signal.Signals(number).name in captured.getvalue(), "the crash is not logged"


def test_exit_like_chrome_ends_by_the_same_ordinary_signal():
    for number in (signal.SIGTERM, signal.SIGUSR1):
        killed, handler = [], signal.getsignal(number)
        try:
            browser.exit_like_chrome(("signal", int(number)), kill=lambda *a: killed.append(a),
                                     finish=lambda code: None)
            assert killed == [(os.getpid(), int(number))], (number, killed)
            assert signal.getsignal(number) == signal.SIG_DFL, "the signal would not terminate the launcher"
        finally:
            signal.signal(number, handler)


def test_a_launcher_process_ends_128_plus_the_signal_when_chrome_crashes():
    """The process-level proof of the mapping: a crash-class signal comes back as
    an exit code, not as the launcher dying of it. Off darwin only — if the
    mapping were wrong, this case would crash a Python on a Mac."""
    if ON_DARWIN:
        print("GAP the crash-signal process check runs off darwin only (the crash reporter); "
              "the in-process cases above cover the mapping", file=sys.stderr)
        return
    for number in browser.CRASH_SIGNALS:
        process = subprocess.run(
            [sys.executable, "-c", f"import browser; browser.exit_like_chrome(('signal', {int(number)}))"],
            capture_output=True, timeout=30, check=False, cwd=os.path.dirname(os.path.abspath(__file__)),
        )
        assert process.returncode == 128 + int(number), (number, process.returncode, process.stderr)


def test_launcher_forwards_sigterm_to_chrome_and_returns_its_exit():
    for sent in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        with tempfile.TemporaryDirectory() as tmp:
            ready = os.path.join(tmp, "ready")
            got = os.path.join(tmp, "got")

            def send():
                wait_for(ready)
                os.kill(os.getpid(), sent)

            sender = threading.Thread(target=send)
            sender.start()
            try:
                how, _ = launch(tmp, textwrap.dedent(f"""
                    import signal, sys
                    def stop(number, frame):
                        open({got!r}, "w").write(str(number))
                        sys.exit(3)
                    for number in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
                        signal.signal(number, stop)
                    """) + ready_then_await_signal(ready) + "sys.exit(9)\n")
            finally:
                sender.join()
            assert how == ("exit", 3), (sent, how)
            assert read(got) == str(int(sent)), f"Chrome got {read(got)!r}, sent {int(sent)}"


def test_launcher_restores_the_signal_handlers_it_took():
    before = {n: signal.getsignal(n) for n in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP, signal.SIGPIPE,
                                              signal.SIGXFSZ, signal.SIGCHLD)}
    with tempfile.TemporaryDirectory() as tmp:
        launch(tmp, "pass\n")
    after = {n: signal.getsignal(n) for n in before}
    assert after == before, (before, after)
    assert signal.set_wakeup_fd(-1) == -1, "the launcher left its wait's wakeup fd set"


def test_launcher_leaves_chromes_signals_default_not_ignored():
    """Python ignores SIGPIPE for itself, and an ignored signal survives into the
    child: Chrome must start with the default, as one spawned by node does. A
    shell stands in for Chrome here because a Python child ignores SIGPIPE again."""
    with tempfile.TemporaryDirectory() as tmp:
        chrome = os.path.join(tmp, "chrome.sh")
        with open(chrome, "w", encoding="utf-8") as script:
            script.write("#!/bin/sh\nkill -PIPE $$\nexit 0\n")
        os.chmod(chrome, 0o755)
        assert signal.getsignal(signal.SIGPIPE) is signal.SIG_IGN, "the premise: this Python ignores SIGPIPE"
        how = browser.chrome_launcher_main([os.path.join(tmp, "status"), chrome], spawn=spawn_for_real)
        assert how == ("signal", signal.SIGPIPE), how
        at_exec = []
        browser.chrome_launcher_main(
            [os.path.join(tmp, "status"), chrome], spawn=refusing(AttributeError("no symbol")),
            execute=lambda path, argv, env: at_exec.append(
                {signal.Signals(n).name: signal.getsignal(n) for n in (signal.SIGPIPE, signal.SIGXFSZ)}),
        )
    assert at_exec == [{"SIGPIPE": signal.SIG_DFL, "SIGXFSZ": signal.SIG_DFL}], \
        f"the fallback exec hands Chrome ignored signals: {at_exec}"


# --- chrome_launcher_main: when the disclaim is unavailable ------------------


def refusing(error):
    def spawn(path, argv, env):
        raise error

    return spawn


def test_launcher_launches_chrome_normally_and_says_why_whatever_the_disclaim_hit():
    for error in (AttributeError(f"function '{browser.DISCLAIM_SYMBOL}' not found"),
                  OSError("/usr/lib/libSystem.B.dylib: cannot open shared object file"),
                  ImportError("No module named '_ctypes'"), ctypes.ArgumentError("argument 2: wrong type")):
        execs = []
        with tempfile.TemporaryDirectory() as tmp:
            _, status = launch(tmp, "pass\n", ["--x"], spawn=refusing(error),
                               execute=lambda path, argv, env: execs.append(argv))
        assert len(execs) == 1 and execs[0][1:] == ["--x"], f"Chrome was not exec'd once with its arguments: {execs}"
        assert status.startswith(browser.STATUS_FAILED), status
        assert type(error).__name__ in status and str(error) in status, (error, status)


def test_launcher_starts_no_chrome_when_stopped_before_it_could():
    """A SIGTERM that reaches the launcher while the disclaim is being tried
    ends the launch as that signal would have ended Chrome; Chrome never starts."""
    execs = []

    def stopped_then_refused(path, argv, env):
        os.kill(os.getpid(), signal.SIGTERM)
        raise AttributeError("no symbol")

    with tempfile.TemporaryDirectory() as tmp:
        how, _ = launch(tmp, "pass\n", spawn=stopped_then_refused, execute=lambda *a: execs.append(a))
    assert execs == [] and how == ("signal", signal.SIGTERM), (how, execs)


def test_launcher_reports_both_failures_and_exits_127_when_chrome_cannot_start():
    def no_exec(path, argv, env):
        raise FileNotFoundError(errno.ENOENT, "No such file or directory", path)

    with tempfile.TemporaryDirectory() as tmp:
        how, status = launch(tmp, "pass\n", spawn=refusing(AttributeError("no symbol")), execute=no_exec)
    assert how == ("exit", 127), how
    assert "failed to exec" in status.lower() and "No such file" in status, status


def test_launcher_does_not_exec_a_second_chrome_after_a_successful_spawn():
    execs = []
    with tempfile.TemporaryDirectory() as tmp:
        how, _ = launch(tmp, "pass\n", execute=lambda *a: execs.append(a))
    assert how == ("exit", 0) and execs == [], (how, execs)


# --- the wrapper patchright runs ----------------------------------------------


def make_launcher(tmp, chrome):
    return browser.ChromeLauncher(tmp, chrome)


def run_wrapper(tmp, body, chrome_arguments=(), env=None, pass_fds=()):
    chrome = fake_chrome(tmp, body)
    launcher_dir = os.path.join(tmp, "launcher dir", "it's")
    os.makedirs(launcher_dir)
    launcher = make_launcher(launcher_dir, chrome)
    process = subprocess.run([launcher.executable, *chrome_arguments], capture_output=True, pass_fds=pass_fds,
                             env={**os.environ, **(env or {})}, timeout=30, check=False)
    return process, read(launcher.status_path).strip()


def test_wrapper_runs_chrome_with_its_arguments_environment_descriptors_and_exit_status():
    read_end, write_end = os.pipe()
    os.set_inheritable(write_end, True)
    try:
        with tempfile.TemporaryDirectory() as tmp:
            process, status = run_wrapper(tmp, f"""
                import os, sys
                os.write({write_end}, b"fd ok")
                print(" ".join(sys.argv[1:]), os.environ["PFM_LAUNCHER_PROBE"])
                sys.exit(5)
                """, ["--remote-debugging-pipe", "--proxy-server=http://127.0.0.1:1"],
                                          env={"PFM_LAUNCHER_PROBE": "env ok"}, pass_fds=(write_end,))
        os.close(write_end)
        write_end = None
        assert process.returncode == 5, (process.returncode, process.stderr)
        assert process.stdout.decode().strip() == "--remote-debugging-pipe --proxy-server=http://127.0.0.1:1 env ok"
        assert os.read(read_end, 100) == b"fd ok", "the inherited descriptor did not reach Chrome"
        expected = browser.STATUS_DISCLAIMED if ON_DARWIN else browser.STATUS_FAILED
        assert status.startswith(expected), f"{status!r} on {sys.platform}, want {expected}"
    finally:
        os.close(read_end)
        if write_end is not None:
            os.close(write_end)


def test_wrapper_ends_with_the_signal_chrome_died_of():
    for number in (signal.SIGUSR1, signal.SIGKILL):
        with tempfile.TemporaryDirectory() as tmp:
            process, _ = run_wrapper(tmp, f"import os\nos.kill(os.getpid(), {int(number)})\n")
        assert process.returncode == -int(number), (number, process.returncode)


def test_wrapper_delivers_sigterm_to_chrome():
    with tempfile.TemporaryDirectory() as tmp:
        chrome = fake_chrome(tmp, textwrap.dedent("""
            import signal, sys
            signal.signal(signal.SIGTERM, lambda number, frame: sys.exit(4))
            """) + ready_then_await_signal(os.path.join(tmp, "ready")))
        launcher_dir = os.path.join(tmp, "launcher")
        os.mkdir(launcher_dir)
        launcher = make_launcher(launcher_dir, chrome)
        process = subprocess.Popen([launcher.executable], start_new_session=True)
        try:
            wait_for(os.path.join(tmp, "ready"))
            process.send_signal(signal.SIGTERM)
            assert process.wait(timeout=20) == 4
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()


# --- the launcher the rung asks for -------------------------------------------


@contextlib.contextmanager
def channel_chrome(path):
    """Stand *path* in for the Chrome the darwin chrome channel launches."""
    saved = browser.CHROME_CHANNEL_DARWIN
    browser.CHROME_CHANNEL_DARWIN = path
    try:
        yield path
    finally:
        browser.CHROME_CHANNEL_DARWIN = saved


def wrapped_chrome(launcher):
    """The Chrome a wrapper script hands the launcher, or None for no wrapper."""
    if not launcher.options:
        return None
    exec_line = read(launcher.options["executable_path"]).splitlines()[1]
    words = shlex.split(exec_line)
    assert words[:1] == ["exec"] and words[3] == browser.LAUNCHER_FLAG and words[-1] == "$@", words
    return words[5]


def test_off_darwin_the_launch_is_unchanged_and_makes_nothing():
    with tempfile.TemporaryDirectory() as tmp, channel_chrome(fake_chrome(tmp, "pass\n")):
        for platform in ("linux", "win32"):
            with browser.chrome_launcher(platform=platform) as launcher:
                assert launcher.options == {}, (platform, launcher.options)


def test_darwin_wraps_only_the_chrome_the_channel_launches():
    """The wrapper runs the binary channel="chrome" launches on darwin — never
    a google-chrome in the worker's working directory (the user's project) or
    on PATH — and no wrapper is made when that binary is absent or not
    executable, so patchright reports its own missing channel."""
    with tempfile.TemporaryDirectory() as tmp:
        planted = os.path.join(tmp, "project")
        shims = os.path.join(tmp, "shims")
        for folder in (planted, shims):
            os.mkdir(folder)
            with open(os.path.join(folder, "google-chrome"), "w", encoding="utf-8") as script:
                script.write("#!/bin/sh\nexit 0\n")
            os.chmod(os.path.join(folder, "google-chrome"), 0o755)
        not_executable = os.path.join(tmp, "Google Chrome")
        with open(not_executable, "w", encoding="utf-8") as script:
            script.write("#!/bin/sh\n")
        real = fake_chrome(tmp, "pass\n")
        cwd, path = os.getcwd(), os.environ.get("PATH", "")
        os.chdir(planted)
        os.environ["PATH"] = shims + os.pathsep + path
        try:
            seen = {}
            for name, channel in (("absent", os.path.join(tmp, "no such Chrome")),
                                  ("not executable", not_executable), ("present", real)):
                with channel_chrome(channel), browser.chrome_launcher(platform="darwin") as launcher:
                    seen[name] = wrapped_chrome(launcher)
        finally:
            os.chdir(cwd)
            os.environ["PATH"] = path
    assert seen == {"absent": None, "not executable": None, "present": real}, seen


def test_darwin_points_patchright_at_a_wrapper_and_removes_it_after():
    with tempfile.TemporaryDirectory() as tmp, channel_chrome(fake_chrome(tmp, "pass\n")):
        with browser.chrome_launcher(platform="darwin") as launcher:
            wrapper = launcher.options["executable_path"]
            directory = os.path.dirname(wrapper)
            assert os.path.isfile(wrapper) and os.access(wrapper, os.X_OK), wrapper
            assert oct(os.stat(directory).st_mode & 0o777) == oct(0o700), "the launcher directory is not private"
    assert not os.path.exists(directory), "the wrapper directory outlived its launch"


def test_darwin_says_so_when_the_wrapper_directory_cannot_be_removed():
    captured = io.StringIO()
    with tempfile.TemporaryDirectory() as tmp, channel_chrome(fake_chrome(tmp, "pass\n")):
        with contextlib.redirect_stderr(captured):
            with browser.chrome_launcher(platform="darwin") as launcher:
                directory = os.path.dirname(launcher.options["executable_path"])
                os.rename(directory, os.path.join(tmp, "moved away"))
    assert directory in captured.getvalue(), f"a failed cleanup is silent: {captured.getvalue()!r}"


def test_wrapper_runs_the_worker_file_itself_not_a_link_a_reprovision_can_flip():
    """The worker runs through env-browser/…/current, a symlink re-provisioning
    flips; the wrapper must name the browser.py the running worker loaded."""
    here = os.path.dirname(os.path.abspath(browser.__file__))
    with tempfile.TemporaryDirectory() as tmp:
        current = os.path.join(tmp, "current")
        os.symlink(here, current)
        out = os.path.join(tmp, "launcher")
        os.mkdir(out)
        subprocess.run([sys.executable, "-c", f"import browser; browser.ChromeLauncher({out!r}, '/c')"],
                       env={**os.environ, "PYTHONPATH": current}, cwd=tmp, check=True, timeout=30,
                       capture_output=True)
        words = shlex.split(read(os.path.join(out, "chrome")).splitlines()[1])
    assert words[2] == os.path.join(os.path.realpath(here), "browser.py"), words


def test_report_says_nothing_for_a_clean_disclaim_and_says_everything_otherwise():
    with tempfile.TemporaryDirectory() as tmp:
        launcher = browser.ChromeLauncher(tmp, "/c")
        outputs = {}
        for name, content in (("clean", "disclaimed pid=9\n"),
                              ("failed", "FAILED to disclaim responsibility: AttributeError: x\n"),
                              ("missing", None)):
            if content is None:
                if os.path.exists(launcher.status_path):
                    os.unlink(launcher.status_path)
            else:
                with open(launcher.status_path, "w", encoding="utf-8") as handle:
                    handle.write(content)
            captured = io.StringIO()
            with contextlib.redirect_stderr(captured):
                launcher.report()
            outputs[name] = captured.getvalue()
    assert outputs["clean"] == "", outputs["clean"]
    assert "FAILED to disclaim responsibility" in outputs["failed"], outputs["failed"]
    assert "left no status" in outputs["missing"], outputs["missing"]


class RecordingChromium:
    def __init__(self, fail=None):
        self.launches = []
        self.closed = 0
        self.fail = fail

    async def launch(self, **kwargs):
        self.launches.append(kwargs)
        if self.fail:
            raise self.fail
        chromium = self

        class Launched:
            async def close(self):
                chromium.closed += 1

        return Launched()


class RecordingPlaywright:
    def __init__(self, chromium):
        self.chromium = chromium


def launch_through_chrome_browser(platform, chromium):
    saved = browser.sys.platform
    browser.sys.platform = platform

    async def go():
        async with browser.chrome_browser(RecordingPlaywright(chromium), proxy={"server": "http://p"},
                                          args=["--a"]):
            pass

    try:
        with tempfile.TemporaryDirectory() as tmp, channel_chrome(fake_chrome(tmp, "pass\n")):
            asyncio.run(go())
    finally:
        browser.sys.platform = saved


def test_chrome_browser_launches_the_same_headless_chrome_off_darwin():
    chromium = RecordingChromium()
    launch_through_chrome_browser("linux", chromium)
    assert chromium.launches == [{"channel": "chrome", "headless": True, "proxy": {"server": "http://p"},
                                  "args": ["--a"]}], chromium.launches
    assert chromium.closed == 1


def test_chrome_browser_on_darwin_keeps_the_channel_and_arguments_and_adds_only_the_wrapper():
    chromium = RecordingChromium()
    launch_through_chrome_browser("darwin", chromium)
    (launch,) = chromium.launches
    wrapper = launch.pop("executable_path")
    assert wrapper.endswith(os.sep + "chrome"), wrapper
    assert launch == {"channel": "chrome", "headless": True, "proxy": {"server": "http://p"},
                      "args": ["--a"]}, launch
    assert chromium.closed == 1


def test_chrome_browser_reports_the_launchers_diagnosis_when_the_launch_fails():
    chromium = RecordingChromium(fail=RuntimeError("Target closed"))
    captured = io.StringIO()
    try:
        with contextlib.redirect_stderr(captured):
            launch_through_chrome_browser("darwin", chromium)
    except RuntimeError as e:
        assert "Target closed" in str(e)
    else:
        raise AssertionError("a failed launch did not propagate")
    assert "left no status" in captured.getvalue(), captured.getvalue()


# --- the kernel's own answer, darwin only --------------------------------------


def test_a_disclaimed_chrome_is_its_own_responsible_process():
    if not ON_DARWIN:
        print("GAP responsibility lookup runs on darwin only (this host: %s)" % sys.platform, file=sys.stderr)
        return
    libc = ctypes.CDLL(browser.LIBSYSTEM, use_errno=True)
    libc.responsibility_get_pid_responsible_for_pid.argtypes = [ctypes.c_int]
    libc.responsibility_get_pid_responsible_for_pid.restype = ctypes.c_int
    disclaimed = browser.spawn_disclaimed("/bin/sleep", ["sleep", "20"], dict(os.environ))
    inherited = os.posix_spawn("/bin/sleep", ["sleep", "20"], dict(os.environ))
    try:
        assert libc.responsibility_get_pid_responsible_for_pid(disclaimed) == disclaimed, \
            "the disclaimed process is attributed to another process"
        assert libc.responsibility_get_pid_responsible_for_pid(inherited) != inherited, \
            "control: a normally spawned child is its own responsible process, so the check proves nothing"
    finally:
        for pid in (disclaimed, inherited):
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)


if __name__ == "__main__":
    failures = 0
    for name, case in sorted(globals().items()):
        if name.startswith("test_") and callable(case):
            try:
                case()
            except AssertionError as e:
                print(f"FAIL {name}: {e}", file=sys.stderr)
                failures += 1
            except Exception as e:  # noqa: BLE001 — an unexpected error is a failed case, never a silent pass
                print(f"FAIL {name}: {type(e).__name__}: {e}", file=sys.stderr)
                failures += 1
            else:
                print(f"ok {name}")
    sys.exit(1 if failures else 0)
