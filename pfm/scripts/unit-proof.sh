#!/usr/bin/env bash
# unit-proof.sh — runs pfm's untagged unit tier with every test binary inside a planted hostile host
# and, on Linux, under strace, then judges each exec, file and socket reach outside the zones a unit
# test may touch: the unit net's work list while the tier is red, its standing proof once green (the
# gate step pfm.unit-proof, infra/fence/checks.sh).
#
#   scripts/unit-proof.sh [--out DIR] [package…]   packages default ./...; DIR defaults to
#                                                    /tmp/{project}/unit-proof/{UTC}-{pid}, refused when it holds files
#   scripts/unit-proof.sh --exec OUT BINARY ARG…    go test -exec's wrapper: one test binary in the ambient
#   scripts/unit-proof.sh --filter OUT PKG TRACEDIR BINARY [PART]   one process's traces → OUT/reach/{slug}.tsv,
#                                                    {slug}#{PART}.tsv for one part of a split binary
#   scripts/unit-proof.sh --verdict OUT              OUT/tests.json and OUT/reach/ → reach.tsv, causes.tsv, verdict
#
# The run: `go test -count=1 -json [-p N] -exec '{this} --exec {out}' {packages}` from the pfm module,
# no build tag, its event stream in {out}/tests.json; N is PFM_FENCE_TEST_P where the fence sets it,
# the unit tier's package concurrency. The go tool itself is never traced or planted.
#
# The hostile ambient (--exec), what a lived-in machine would hand the binary:
#   environment  emptied but PFM_TEST_ARTIFACT_DIR, PFM_DEV_FENCE, GITHUB_ACTIONS, RUNNER_ENVIRONMENT;
#                HOME={out}/ambient/home (empty), PATH={out}/ambient/bin, HERDR_BIN_PATH={out}/ambient/bin/herdr,
#                CLAUDECODE=1, CLAUDE_CONFIG_DIR, CODEX_HOME, TMUX, TMUX_PANE and SHELL under {out}/ambient
#   umask        002
#   tripwires    {out}/ambient/bin holds a link to one tripwire under every executable name of the
#                caller's PATH; it appends `{name}⇥{argv}` to {out}/tripwire.log, prints
#                `UNIT-PROOF TRIPWIRE {name} {argv}` on stderr and exits 97, so a red run never
#                reaches the real tool
#   trace        Linux: strace -ff -y --seccomp-bpf -e trace=%file,connect,bind plus the clone family
#                (to drop what a started program does after its exec, which its exec row already names)
#   split        Linux: a package PROOF_SHARDS names runs its binary as that many traced processes at
#                once, the tests a traced `-test.list` run names dealt over them by `-test.run`, each
#                process filtered on its own; go test reads their output in part order, and the verdict
#                adds the parts' reach rows up as the package's
#
# Judged (the filter, one record at a time, by its path, never its result):
#   allowed      under a directory testjail makes directly under the system temp directory (a mkdir of
#                j{N}, u{N}, pfm-jail-home-{N} or pfm-jail-engine-sync-{N} that returned 0 in the same
#                binary's traces), read and write; {out}/profile (the test
#                profiler's artifacts), read and write; /dev/null, read and write; read only: a checkout
#                file git does not ignore and each directory holding one, GOROOT, /proc, /sys, /dev,
#                /etc/ld.so.*, /etc/localtime, /etc/nsswitch.conf, /etc/passwd, the go tool's work
#                directory holding the binary, each ancestor directory of an allowed root (the path itself
#                only), and each row of pfm/.arch/unit-proof-allow.txt (`{glob}⇥{r|rw|absent}⇥{reason}`, an absent row only while the path does not exist, `*`
#                crossing `/`, a leading `{checkout}` or `{out}` standing for that root, a leading `{above}` for each directory above the checkout); a socket on a loopback address or a unix path under a run-made directory
#   residue      every other path (kind read or write); every exec after the binary's own (kind exec);
#                every other socket address (kind socket); each tripwire.log line (kind tripwire)
#   a relative path without a directory descriptor resolves against the package directory, where go
#   test starts the binary, but a relative chdir is no path (every later AT_FDCWD<…> names the cwd it made); an open of
#   an empty path looks nothing up; a bare dirfd is named by the open that returned it in any thread
#   of the binary; one against a bare AT_FDCWD (strace could not name a cwd past PATH_MAX)
#   is allowed only when every absolute chdir of the binary went under a run-made directory or back
#   to the package directory; /proc/self/fd/N (a file the process opened, judged at that open),
#   the Go CPU profiler's open of a kernel mapping name ([vdso], [vsyscall], [vvar]) read from /proc/self/maps, relative
#   in the package directory, is no path; /dev/ptmx and, once the binary opened it, /dev/pts/N (its own pseudo-terminal pair), read and
#   write; a clone the binary exited inside, unfinished at the end of its trace, names no path
#
# Output, in {out}: tests.json; reach.tsv `package⇥kind⇥target⇥count⇥sample`; causes.tsv, one
# `cause⇥package⇥test⇥evidence` row per failing or skipping top-level test (subtests rolled up) then
# `cause⇥count` totals — cause is the first `UNIT-NET REFUSED {door}: {name}` as `{door}:{name}`, else
# `tripwire:{name}`, else `net-dir:{NAME}` for a path under `/u{digits}/{NAME}/`, else `skip:{message}`,
# else `unclassified`, its evidence the output line it came from (else the test's first failure line).
# stdout: on FAIL the first 40 reach rows, then the first 60 failing tests, each `--- FAIL {package}
# {test}` (a package that failed with no failing test: `(package)`) over up to 3 of its output lines;
# then the verdict, always the last line:
#   UNIT-PROOF {PASS|FAIL|ERROR} · tests {p} pass {f} fail {s} skip · reach {n} rows ({e} exec · {r} read ·
#   {w} write · {k} socket · {t} tripwire) · syscalls {traced|not traced ({os})} · {out}
# Exit 0 PASS: no failing test, no reach row, no skip naming a missing tool. 1 FAIL: any of those.
# BROKEN STATE, exit 2 ERROR {why}: strace absent on Linux, no trace file, a trace record the filter
# cannot parse, the checkout's file list unreadable, an allow row without a reason, no test event, a
# reused out directory, a split binary whose test list cannot be read or names no test, a listed test
# no part started, a UNIT_PROOF_SHARDS word that is no `{package}={parts}`, a PFM_FENCE_TEST_P that is
# no positive integer — "could not look" never reads as a pass.
# UNIT_PROOF_KEEP_TRACES=1 keeps the raw traces and each part's output in {out}/trace/ (removed once
# filtered otherwise). UNIT_PROOF_SHARDS replaces the split table, `{package}={parts}` words; empty
# splits nothing.
set -uo pipefail
export LC_ALL=C

SELF="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/$(basename -- "${BASH_SOURCE[0]}")"
PFM="$(cd -- "$(dirname -- "$SELF")/.." && pwd -P)"
REPO="$(cd -- "$PFM/.." && pwd -P)"
PROJECT="$(basename -- "$REPO")"; PROJECT="${PROJECT#.}"
TRACE_SET='%file,connect,bind,clone,clone3,fork,vfork'
KEEP_NAMES=(PFM_TEST_ARTIFACT_DIR PFM_DEV_FENCE GITHUB_ACTIONS RUNNER_ENVIRONMENT)

read -r -d '' FILTER_PY <<'PY'
import fnmatch, json, os, re, sys

def load_zones(out):
    zones = {}
    with open(os.path.join(out, 'zones.tsv'), encoding='utf-8') as fh:
        for line in fh:
            key, _, value = line.rstrip('\n').partition('\t')
            zones[key] = value
    return zones

def ancestors(path):
    found = []
    while True:
        parent = os.path.dirname(path)
        if parent == path:
            return found
        found.append(parent)
        path = parent

def load_allow(path, checkout, out):
    rows = []
    with open(path, encoding='utf-8') as fh:
        for number, line in enumerate(fh, 1):
            text = line.rstrip('\n')
            if not text.strip() or text.startswith('#'):
                continue
            parts = text.split('\t')
            if len(parts) != 3 or parts[1] not in ('r', 'rw', 'absent') or not parts[2].strip():
                raise ValueError('allow row without a reason or mode, %s:%d: %s' % (path, number, text))
            glob = parts[0].replace('{checkout}', checkout).replace('{out}', out)
            if glob.startswith('{above}'):
                # one row per directory above the checkout, the filesystem root last: a walk up from it
                # passes them all, however deep the checkout sits (/worktree in the fence, a runner's work tree)
                rows.extend((a.rstrip('/') + glob[len('{above}'):], parts[1]) for a in ancestors(checkout))
            else:
                rows.append((glob, parts[1]))
    return rows

ESCAPE = re.compile(r'\\(x[0-9a-fA-F]{2}|[0-7]{1,3}|.)')
def unescape(text):
    def one(m):
        code = m.group(1)
        simple = {'n': '\n', 't': '\t', 'r': '\r', 'v': '\v', 'f': '\f', '\\': '\\', '"': '"'}
        if code[0] == 'x':
            return chr(int(code[1:], 16))
        if code[0] in '01234567':
            return chr(int(code, 8))
        return simple.get(code, code)
    return ESCAPE.sub(one, text)

def split_args(text):
    """Top-level comma split of a strace argument list: quotes, (), [], {} and fd <...> decorations nest."""
    args, depth, cur, i, n = [], 0, [], 0, len(text)
    while i < n:
        c = text[i]
        if c == '"':
            j = i + 1
            while j < n and text[j] != '"':
                j += 2 if text[j] == '\\' else 1
            cur.append(text[i:j + 1]); i = j + 1; continue
        if c == '<' and i > 0 and (text[i - 1].isalnum() or text[i - 1] == '_'):
            j = text.find('>', i)
            if j < 0:
                raise ValueError('open fd decoration')
            cur.append(text[i:j + 1]); i = j + 1; continue
        if c in '([{':
            depth += 1
        elif c in ')]}':
            depth -= 1
        if c == ',' and depth == 0:
            args.append(''.join(cur).strip()); cur = []
        else:
            cur.append(c)
        i += 1
    if depth != 0:
        raise ValueError('unbalanced arguments')
    tail = ''.join(cur).strip()
    if tail or args:
        args.append(tail)
    return args

STRING = re.compile(r'^@?"((?:[^"\\]|\\.)*)"(\.\.\.)?$')
FD = re.compile(r'^(?:AT_FDCWD|-?\d+)<(.*)>$')
def as_string(arg):
    m = STRING.match(arg)
    return unescape(m.group(1)) if m else None
def as_fd(arg):
    m = FD.match(arg)
    return unescape(m.group(1)) if m else None

# name -> [(dirfd index or None, path index, mode)]; mode 'o' reads open flags, 'r' read, 'w' write, 'x' exec
PATHS = {}
for names, spec in [
    ('open', [(None, 0, 'o')]), ('openat openat2', [(0, 1, 'o')]), ('creat', [(None, 0, 'w')]),
    ('stat lstat stat64 lstat64 access chdir chroot readlink statfs statfs64 getxattr lgetxattr listxattr '
     'llistxattr uselib swapon swapoff umount umount2 acct', [(None, 0, 'r')]),
    ('newfstatat fstatat64 statx faccessat faccessat2 readlinkat name_to_handle_at', [(0, 1, 'r')]),
    ('inotify_add_watch', [(None, 1, 'r')]), ('fanotify_mark', [(3, 4, 'r')]),
    ('mkdir rmdir unlink truncate truncate64 chmod chown chown32 lchown lchown32 utime utimes mknod setxattr '
     'lsetxattr removexattr lremovexattr', [(None, 0, 'w')]),
    ('mkdirat unlinkat fchmodat fchmodat2 fchownat futimesat mknodat', [(0, 1, 'w')]),
    ('utimensat', [(0, 1, 'w')]),
    ('rename link', [(None, 0, 'w'), (None, 1, 'w')]),
    ('renameat renameat2 linkat', [(0, 1, 'w'), (2, 3, 'w')]),
    ('symlink', [(None, 1, 'w')]), ('symlinkat', [(1, 2, 'w')]),
    ('execve', [(None, 0, 'x')]), ('execveat', [(0, 1, 'x')]),
    ('mount', [(None, 1, 'w')]), ('pivot_root', [(None, 0, 'w'), (None, 1, 'w')]),
]:
    for name in names.split():
        PATHS[name] = spec
CLONES = {'clone', 'clone3', 'fork', 'vfork'}
CLONE_PREFIX = re.compile(r'^(clone|clone3|fork|vfork)\(')
OUTPUTS = {'getcwd'}  # its string is the answer, never a path it reached
SOCKETS = {'connect', 'bind'}
WRITE_FLAGS = re.compile(r'O_WRONLY|O_RDWR|O_CREAT|O_TRUNC|O_APPEND')
RECORD = re.compile(r'^([a-z_0-9]+)\((.*)\)\s+=\s+(.*)$')
LOOPBACK4 = re.compile(r'inet_addr\("127\.')
LOOPBACK6 = re.compile(r'inet_pton\(AF_INET6, "(::1|::ffff:127\.[0-9.]+)"')

def records(path):
    """(line number, name, args text, result) per syscall record; ('?', line) for one that cannot be read."""
    pending, pending_no = None, 0
    with open(path, encoding='utf-8', errors='surrogateescape') as fh:
        for number, raw in enumerate(fh, 1):
            line = raw.rstrip('\n')
            if not line or line.startswith('--- ') or line.startswith('+++ '):
                continue
            # strace's `???(`: a syscall it never decoded, the task already inside it when strace
            # attached (`<unfinished ...>`) or gone under it (`<detached ...>`): no record, no path,
            # nothing to judge.
            if line.startswith('???( <'):
                continue
            if line.endswith(' <unfinished ...>'):
                pending, pending_no = line[:-len(' <unfinished ...>')], number
                continue
            m = re.match(r'^<\.\.\. [a-z_0-9]+ resumed>(.*)$', line)
            if m:
                if pending is None:
                    yield (number, '?', line, None); continue
                line, number, pending = pending + m.group(1), pending_no, None
            m = RECORD.match(line)
            if not m:
                # a clone-family record names no path; one strace could not finish (the task killed
                # inside it) only loses its child's link, and an unlinked child is judged as a root
                if CLONE_PREFIX.match(line) is None:
                    yield (number, '?', line, None)
                continue
            yield (number, m.group(1), m.group(2), m.group(3))
    if pending is not None:
        # a task that died inside clone/fork (the binary exiting under it) names no path: nothing to
        # judge; any other unfinished record still is one
        if CLONE_PREFIX.match(pending) is None:
            yield (pending_no, '?', pending, None)

def ok(result):
    return result is not None and re.match(r'^(0|[1-9]\d*)\b', result) is not None

def resolve(base, path):
    if path is None:
        return None
    if not os.path.isabs(path):
        path = os.path.join(base, path)
    return os.path.normpath(path)

class Judge:
    def __init__(self, out, pkgdir, binary):
        zones = load_zones(out)
        self.out, self.pkgdir, self.binary = out, pkgdir, binary
        self.checkout = zones['checkout']
        self.systmp = zones['systmp']
        files = set()
        with open(zones['files'], 'rb') as fh:
            for rel in fh.read().split(b'\0'):
                if rel:
                    files.add(os.path.join(self.checkout, os.fsdecode(rel)))
        if not files:
            raise ValueError('the checkout file list %s is empty' % zones['files'])
        self.files, self.dirs = files, set()
        for f in files:
            for parent in ancestors(f):
                if parent in self.dirs:
                    break
                self.dirs.add(parent)
        self.rw_roots = [os.path.join(out, 'profile')]
        self.r_roots = [zones['goroot'], '/proc', '/sys', '/dev', os.path.dirname(os.path.dirname(binary))]
        self.r_exact = {'/etc/localtime', '/etc/nsswitch.conf', '/etc/passwd', self.systmp}
        self.rw_exact = {'/dev/null', '/dev/ptmx'}
        self.own_pty = False  # set when the binary opened /dev/ptmx: its /dev/pts/N are its own pair
        self.allow = load_allow(os.path.join(self.checkout, 'pfm', '.arch', 'unit-proof-allow.txt'), self.checkout, out)
        for root in self.rw_roots + self.r_roots + [self.checkout]:
            self.r_exact.update(ancestors(root))
        self.owned = set()

    def owned_root(self, path):
        for root in self.owned:
            if path == root or path.startswith(root + '/'):
                return True
        return False

    def allowed(self, path, mode):
        if self.owned_root(path) or path in self.rw_exact:
            return True
        # /proc/self/fd/N names a file this process already opened, judged at that open
        if OWN_FD.match(path) or (self.own_pty and OWN_PTS.match(path)):
            return True
        if any(path == r or path.startswith(r + '/') for r in self.rw_roots):
            return True
        if mode == 'r' and os.path.dirname(path) == self.pkgdir and os.path.basename(path) in KERNEL_MAPPINGS:
            return True
        for glob, gmode in self.allow:
            if not fnmatch.fnmatchcase(path, glob):
                continue
            # an `absent` row allows only a lookup that found nothing: the path must not exist here,
            # so the same row reads as a reach on a machine where it does
            if gmode == 'absent' and mode == 'r' and not os.path.lexists(path):
                return True
            if gmode != 'absent' and (mode == 'r' or gmode == 'rw'):
                return True
        if mode != 'r':
            return False
        if path in self.files or path in self.dirs or path in self.r_exact:
            return True
        if fnmatch.fnmatchcase(path, '/etc/ld.so.*'):
            return True
        return any(path == r or path.startswith(r + '/') for r in self.r_roots)

    def show(self, path):
        for root, token in ((self.out, '{out}'), (self.checkout, '{checkout}')):
            if path == root or path.startswith(root + '/'):
                return token + path[len(root):]
        return path

def paths_of(name, args, base):
    found = []
    for dirfd, index, mode in PATHS[name]:
        if index >= len(args):
            continue
        value = as_string(args[index])
        dirpath = as_fd(args[dirfd]) if dirfd is not None and dirfd < len(args) else None
        if mode == 'o':
            flags = args[index + 1] if index + 1 < len(args) else ''
            mode = 'w' if WRITE_FLAGS.search(flags) else 'r'
        if value is None:
            if args[index] == 'NULL' and dirpath:
                found.append((dirpath, mode)); continue
            continue
        if value == '' and name in ('open', 'openat', 'openat2'):
            continue  # an empty path is ENOENT for every open: nothing is looked up
        if value == '' and dirpath:
            found.append((dirpath, mode)); continue
        found.append((resolve(dirpath or base, value), mode))
    return found

FD_DECORATED = re.compile(r'^(\d+)<.*>$')
def undecorated_dirfd(name, argl, fds):
    """Decorates a bare numeric dirfd with a relative path from fds in place; False when fds lacks it."""
    for dirfd, index, _ in PATHS.get(name, ()):
        if dirfd is None or dirfd >= len(argl) or index >= len(argl) or not re.match(r'^\d+$', argl[dirfd]):
            continue
        value = as_string(argl[index])
        if value is None or value.startswith('/'):
            continue
        if argl[dirfd] not in fds:
            return False
        argl[dirfd] = fds[argl[dirfd]]
    return True

BARE_FD = re.compile(r'^\d+$')
def bare_fd_paths(parsed):
    """fd -> every path an open the trace shows returned it bare: strace -y cannot name an fd whose
    path passes PATH_MAX, so a tree built by relative steps (testjail.ChdirUnnamable) and its
    removal open and unlink through bare fds. Each such open names its child from its own dirfd —
    decorated, or bare and named here — so the chain is followed to a fixed point. Threads of a
    binary share one fd table, so the map spans every trace file of the binary."""
    opens = []
    for rows in parsed.values():
        for _, name, argl, result, _ in rows:
            if name in ('openat', 'openat2') and len(argl) > 1 and BARE_FD.match(result or ''):
                value = as_string(argl[1])
                if value is not None:
                    opens.append((argl[0], value, result))
    derived, changed, rounds = {}, True, 0
    while changed and rounds < 4096:
        changed, rounds = False, rounds + 1
        for dirfd, value, result in opens:
            if value.startswith('/'):
                parents = {'/'}
            elif as_fd(dirfd) is not None:
                parents = {as_fd(dirfd)}
            else:
                parents = derived.get(dirfd, set())
            new = {os.path.normpath(os.path.join(p, value)) for p in parents} - derived.get(result, set())
            if new:
                derived.setdefault(result, set()).update(new)
                changed = True
    return derived

def decorate_bare_dirfd(judge, name, argl, derived):
    """Names each bare dirfd of a record from derived in place, preferring a path the run does not own
    so a reach is never hidden; False when an fd has no path."""
    for dirfd, index, _ in PATHS.get(name, ()):
        if dirfd is None or dirfd >= len(argl) or not BARE_FD.match(argl[dirfd]):
            continue
        paths = sorted(derived.get(argl[dirfd], ()))
        if not paths:
            return False
        foreign = [p for p in paths if not judge.owned_root(p)]
        argl[dirfd] = '%s<%s>' % (argl[dirfd], (foreign or paths)[0])
    return True

def unnamed_cwd(name, argl):
    """A relative path against a bare AT_FDCWD: strace could not name the cwd (past PATH_MAX)."""
    for dirfd, index, _ in PATHS[name]:
        if dirfd is not None and dirfd < len(argl) and argl[dirfd] == 'AT_FDCWD' and index < len(argl):
            value = as_string(argl[index])
            if value is not None and not value.startswith('/'):
                return True
    return False

def inside_own_cwds(judge, chdirs):
    """Every absolute chdir of the binary went under a run-made directory or back to the package
    directory, so a cwd too long to name was built by relative steps inside its own tree."""
    return bool(chdirs) and all(judge.owned_root(d) or d == judge.pkgdir for d in chdirs) and \
        any(judge.owned_root(d) for d in chdirs)

def socket_target(judge, args, base):
    addr = args[1] if len(args) > 1 else ''
    if 'AF_UNSPEC' in addr:
        return None
    if 'AF_INET6' in addr:
        return None if LOOPBACK6.search(addr) else addr
    if 'AF_INET' in addr:
        return None if LOOPBACK4.search(addr) else addr
    m = re.search(r'sun_path=(@?"(?:[^"\\]|\\.)*")', addr)
    if m:
        if m.group(1).startswith('@'):
            return '@' + as_string(m.group(1)[1:])
        path = resolve(base, as_string(m.group(1)))
        return None if judge.owned_root(path) else judge.show(path)
    return addr

def exec_target(args):
    argv = args[1] if len(args) > 1 else '[]'
    words = [as_string(a) if as_string(a) is not None else a for a in split_args(argv[1:-1])] if argv.startswith('[') else []
    if not words:
        return as_string(args[0]) or args[0]
    return ' '.join([os.path.basename(words[0])] + words[1:])[:200]

def filter_mode(out, pkg, tracedir, binary, part=''):
    slug = pkg.replace('/', '%') + ('#' + part if part else '')
    checkout = load_zones(out)['checkout']
    judge = Judge(out, os.path.normpath(os.path.join(checkout, 'pfm', pkg)), binary)
    base = judge.pkgdir
    names = sorted(f for f in os.listdir(tracedir) if re.match(r'^t\.\d+$', f))
    if not names:
        raise ValueError('no trace file in %s' % tracedir)
    parsed, children, unparsed, chdirs, bare = {}, {}, [], [], []
    for f in names:
        pid = int(f[2:])
        rows, fds = [], {}
        for number, name, args, result in records(os.path.join(tracedir, f)):
            if name == '?':
                unparsed.append('%s:%d: %s' % (os.path.join(tracedir, f), number, args[:200])); continue
            try:
                argl = split_args(args)
            except ValueError:
                unparsed.append('%s:%d: %s' % (os.path.join(tracedir, f), number, args[:200])); continue
            # strace -y leaves a dirfd bare now and then; the task's last decorated sight of that fd
            # names it; with none it is resolved after every file is read (bare_fd_paths), never
            # against the cwd
            if not undecorated_dirfd(name, argl, fds):
                bare.append(('%s:%d: %s' % (os.path.join(tracedir, f), number, args[:200]), name, argl))
            for arg in argl + [result or '']:
                m = FD_DECORATED.match(arg)
                if m:
                    fds[m.group(1)] = m.group(0)
            rows.append((number, name, argl, result, '%s(%s) = %s' % (name, args, result)))
            if name in CLONES and ok(result):
                children[int(re.match(r'^\d+', result).group(0))] = pid
            if name in ('mkdir', 'mkdirat') and ok(result):
                for path, _ in paths_of(name, argl, base):
                    if os.path.dirname(path) == judge.systmp and OWNED_NAME.match(os.path.basename(path)):
                        judge.owned.add(path)
            if name in ('open', 'openat', 'openat2') and ok(result) and '"/dev/ptmx"' in args:
                judge.own_pty = True
            if name == 'chdir' and ok(result) and argl and (as_string(argl[0]) or '').startswith('/'):
                chdirs.append(os.path.normpath(as_string(argl[0])))
        parsed[pid] = rows
    derived = bare_fd_paths(parsed)
    for where, name, argl in bare:
        if not decorate_bare_dirfd(judge, name, argl, derived):
            unparsed.append(where)
            argl[:] = []  # never judged against a guessed directory
    reach, seen_binary = {}, [False]
    def add(kind, target, sample):
        row = reach.setdefault((kind, target), [0, sample[:300]])
        row[0] += 1
    foreign_at_birth, done = {}, set()
    pending = sorted(pid for pid in parsed if pid not in children) + sorted(parsed)
    while pending:
        pid = pending.pop(0)
        if pid in done:
            continue
        done.add(pid)
        foreign = foreign_at_birth.get(pid, False)
        for number, name, argl, result, text in parsed[pid]:
            if name in CLONES:
                if ok(result):
                    child = int(re.match(r'^\d+', result).group(0))
                    foreign_at_birth[child] = foreign
                    if child in parsed:
                        pending.insert(0, child)
                continue
            if foreign:
                continue
            if name in ('execve', 'execveat'):
                path = paths_of(name, argl, base)
                target_path = path[0][0] if path else None
                if not seen_binary[0] and target_path == binary:
                    seen_binary[0] = True
                    continue
                add('exec', exec_target(argl if name == 'execve' else argl[1:]), text)
                if ok(result):
                    foreign = True
                continue
            if name in SOCKETS:
                target = socket_target(judge, argl, base)
                if target is not None:
                    add('socket', target, text)
                continue
            if name in PATHS:
                if unnamed_cwd(name, argl) and inside_own_cwds(judge, chdirs):
                    continue
                if name == 'chdir' and argl and not (as_string(argl[0]) or '/').startswith('/'):
                    continue  # its cwd is unknown here; every later AT_FDCWD<…> record names where it went
                for path, mode in paths_of(name, argl, base):
                    if not judge.allowed(path, mode):
                        add('write' if mode == 'w' else 'read', judge.show(path), text)
                continue
            if name in OUTPUTS:
                continue
            for arg in argl:  # a %file call no table row names: its paths are reads
                value = as_string(arg)
                if value and '/' in value:
                    path = resolve(base, value)
                    if not judge.allowed(path, 'r'):
                        add('read', judge.show(path), text)
    os.makedirs(os.path.join(out, 'reach'), exist_ok=True)
    with open(os.path.join(out, 'reach', slug + '.tsv'), 'w', encoding='utf-8') as fh:
        for (kind, target), (count, sample) in sorted(reach.items()):
            fh.write('%s\t%s\t%d\t%s\n' % (kind, target.replace('\t', ' ').replace('\n', ' '), count, sample.replace('\t', ' ').replace('\n', ' ')))
    if unparsed:
        raise ValueError('unparsed %d records, first %s' % (len(unparsed), unparsed[0]))

# The directories testjail makes directly under the system temp directory (os.MkdirTemp: a prefix, then
# digits): its short roots (j), the unit net (u) and the jail homes (testjail.go, unitnet.go), plus the
# empty process table the installer's TestMain makes before testjail.Run and removes after it
# (internal/installer/testmain_test.go). Any other directory a traced task makes there is not the run's:
# in a sterile fence it would be new, and owning it would hide the host reach a lived-in machine shows
# (/tmp/tmux-1000, /tmp/cc-sid).
OWNED_NAME = re.compile(r'^(j|u|pfm-jail-home-|pfm-jail-engine-sync-|installer-proc-)\d+$')

OWN_FD = re.compile(r'^/proc/(self|thread-self)/fd/\d+$')
OWN_PTS = re.compile(r'^/dev/pts/\d+$')
# the Go CPU profiler (the test profile) opens each /proc/self/maps entry as a file to read its build id;
# the kernel's own entries are no files, so the open is a name in the package directory, not a path
KERNEL_MAPPINGS = ('[vdso]', '[vsyscall]', '[vvar]', '[vvar_vclock]')

REFUSAL = re.compile(r'UNIT-NET REFUSED (start|lookup): (\S+)')
TRIPWIRE = re.compile(r'UNIT-PROOF TRIPWIRE (\S+)')
NETDIR = re.compile(r'/u\d+/([A-Z_][A-Z0-9_]*)(?:/|\b)')
FRAME = re.compile(r'^\s*(=== (RUN|PAUSE|CONT|NAME)|--- (PASS|FAIL|SKIP)|PASS$|FAIL$|ok\s|FAIL\s)')
TOOL_MISSING = re.compile(r'is not installed|not on PATH|executable file not found|not found in \$PATH', re.I)
MODULE = 'github.com/rezzminator/professor/pfm'

def short_pkg(pkg):
    if pkg == MODULE:
        return '.'
    return pkg[len(MODULE) + 1:] if pkg.startswith(MODULE + '/') else pkg

def cause_of(lines, action):
    body = [l.rstrip('\n') for l in lines if not FRAME.match(l)]
    for regex, fmt in ((REFUSAL, lambda m: '%s:%s' % (m.group(1), os.path.basename(m.group(2)))),
                       (TRIPWIRE, lambda m: 'tripwire:%s' % m.group(1)),
                       (NETDIR, lambda m: 'net-dir:%s' % m.group(1))):
        for l in body:
            m = regex.search(l)
            if m:
                return fmt(m), l.strip()
    first = next((l.strip() for l in body if l.strip()), '')
    if action == 'skip':
        message = re.sub(r'^\S+\.go:\d+: ', '', first)
        return 'skip:' + message, first
    return 'unclassified', first

def verdict_mode(out, goos, traced):
    errors = []
    errdir = os.path.join(out, 'errors')
    if os.path.isdir(errdir):
        for f in sorted(os.listdir(errdir)):
            with open(os.path.join(errdir, f), encoding='utf-8', errors='replace') as fh:
                errors.append(fh.read().strip())
    events, outputs, status, pkgfail = 0, {}, {}, {}
    try:
        fh = open(os.path.join(out, 'tests.json'), encoding='utf-8', errors='replace')
    except OSError as err:
        errors.append('no test event: %s' % err); fh = []
    for line in fh:
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        pkg, test, action = short_pkg(ev.get('Package', '')), ev.get('Test'), ev.get('Action')
        if not test:
            if action == 'output':
                outputs.setdefault((pkg, '(package)'), []).append(ev.get('Output', ''))
            elif action in ('fail', 'pass', 'skip'):
                pkgfail[pkg] = action
            continue
        events += 1
        top = test.split('/')[0]
        if action == 'output':
            outputs.setdefault((pkg, top), []).append(ev.get('Output', ''))
        elif action in ('pass', 'fail', 'skip') and '/' not in test:
            status[(pkg, top)] = action
    if events == 0:
        errors.append('no test event in %s/tests.json' % out)
    counts = {'pass': 0, 'fail': 0, 'skip': 0}
    for key, action in status.items():
        counts[action] += 1
    rows, missing_tool = [], 0
    for key, action in sorted(status.items()):
        if action == 'pass':
            continue
        cause, evidence = cause_of(outputs.get(key, []), action)
        if action == 'skip' and TOOL_MISSING.search(cause):
            missing_tool += 1
        rows.append((cause, key[0], key[1], evidence))
    for pkg, action in sorted(pkgfail.items()):
        if action == 'fail' and not any(k[0] == pkg and a == 'fail' for k, a in status.items()):
            cause, evidence = cause_of(outputs.get((pkg, '(package)'), []), 'fail')
            rows.append((cause, pkg, '(package)', evidence)); counts['fail'] += 1
    with open(os.path.join(out, 'causes.tsv'), 'w', encoding='utf-8') as fh:
        totals = {}
        for cause, pkg, test, evidence in rows:
            fh.write('%s\t%s\t%s\t%s\n' % (cause, pkg, test, evidence.replace('\t', ' ')[:300]))
            totals[cause] = totals.get(cause, 0) + 1
        for cause, count in sorted(totals.items(), key=lambda kv: (-kv[1], kv[0])):
            fh.write('%s\t%d\n' % (cause, count))
    reach, merged = [], {}
    rdir = os.path.join(out, 'reach')
    if os.path.isdir(rdir):
        for f in sorted(os.listdir(rdir)):
            # a split binary leaves one file a part ({slug}#{part}.tsv): its package's rows are one set
            pkg = f[:-4].split('#')[0].replace('%', '/')
            with open(os.path.join(rdir, f), encoding='utf-8', errors='replace') as rfh:
                for line in rfh:
                    row = [pkg] + line.rstrip('\n').split('\t', 3)
                    first = merged.setdefault(tuple(row[:3]), row)
                    if first is row:
                        reach.append(row)
                    else:
                        first[3] = str(int(first[3]) + int(row[3]))
    elif traced:
        errors.append('no trace file: %s holds no filtered binary' % rdir)
    try:
        with open(os.path.join(out, 'tripwire.log'), encoding='utf-8', errors='replace') as tfh:
            trips = {}
            for line in tfh:
                trips[line.rstrip('\n')] = trips.get(line.rstrip('\n'), 0) + 1
        for target, count in sorted(trips.items()):
            reach.append(['-', 'tripwire', target.replace('\t', ' '), str(count), target.replace('\t', ' ')])
    except FileNotFoundError:
        pass
    with open(os.path.join(out, 'reach.tsv'), 'w', encoding='utf-8') as fh:
        for row in reach:
            fh.write('\t'.join(row) + '\n')
    kinds = {k: sum(1 for r in reach if r[1] == k) for k in ('exec', 'read', 'write', 'socket', 'tripwire')}
    syscalls = 'traced' if traced else 'not traced (%s)' % goos
    if errors:
        word, code = 'ERROR', 2
    elif counts['fail'] or reach or missing_tool:
        word, code = 'FAIL', 1
    else:
        word, code = 'PASS', 0
    if word == 'FAIL':
        for row in reach[:40]:
            print('%s %s ×%s (%s)' % (row[1], row[2], row[3], row[0]))
        if len(reach) > 40:
            print('… %d more reach rows in %s/reach.tsv' % (len(reach) - 40, out))
        # Each failing test by package and name with its first output lines, so a log whose out
        # directory is gone (a CI runner's) still names them.
        failed = [key for key, action in sorted(status.items()) if action == 'fail']
        failed += [(pkg, '(package)') for pkg, action in sorted(pkgfail.items())
                   if action == 'fail' and not any(k[0] == pkg for k in failed)]
        for pkg, test in failed[:60]:
            print('--- FAIL %s %s' % (pkg, test))
            body = [l.rstrip('\n') for l in outputs.get((pkg, test), []) if not FRAME.match(l) and l.strip()]
            for line in body[:3]:
                print('    %s' % line.strip()[:300])
        if len(failed) > 60:
            print('… %d more failing tests in %s/causes.tsv' % (len(failed) - 60, out))
    if word == 'ERROR':
        print('UNIT-PROOF ERROR %s' % errors[0])
    print('UNIT-PROOF %s · tests %d pass %d fail %d skip · reach %d rows (%d exec · %d read · %d write · %d socket · %d tripwire) · syscalls %s · %s' % (
        word, counts['pass'], counts['fail'], counts['skip'], len(reach), kinds['exec'], kinds['read'],
        kinds['write'], kinds['socket'], kinds['tripwire'], syscalls, out))
    return code

if sys.argv[1] == 'filter':
    filter_mode(*sys.argv[2:7])
else:
    sys.exit(verdict_mode(sys.argv[2], sys.argv[3], sys.argv[4] == '1'))
PY

die() { echo "UNIT-PROOF ERROR $*"; exit 2; }

# The packages whose traced binary runs as several processes, `{package}={parts}` words. Each is
# hundreds of short serial tests that one process ran last and alone at the proof's end (installer
# 622 tests, hookentry 290, doctor 242, chat 190). UNIT_PROOF_SHARDS replaces the table.
PROOF_SHARDS='internal/installer=6 internal/hookentry=4 internal/doctor=4 internal/chat=4'

# proof_shards <pkg>: the parts <pkg>'s binary runs as, 1 when the table does not name it.
proof_shards() {
  local word parts=1
  # Every word is checked, not only <pkg>'s: a misspelt word would otherwise split nothing, silently.
  for word in ${UNIT_PROOF_SHARDS-$PROOF_SHARDS}; do
    [[ "$word" =~ ^[^=]+=[1-9][0-9]*$ ]] || { echo "UNIT_PROOF_SHARDS: $word is no {package}={parts}"; return 1; }
    [[ "${word%=*}" != "$1" ]] || parts="${word##*=}"
  done
  echo "$parts"
}

# proof_error <part> <message>: one error the verdict reports, in exec_binary's out directory.
proof_error() { mkdir -p "$out/errors"; printf '%s\n' "$2" > "$out/errors/$slug${1:+#$1}"; }

# traced_run <part> <binary> <arg>…: one process of a test binary under strace, then its traces
# filtered into reach/; <part> is empty for a whole binary. Called by exec_binary, whose out, pkg,
# slug, envs and tool paths it reads.
traced_run() {
  local part="$1"; shift
  local tdir="$out/trace/$slug${part:+#$part}" rc ferr
  mkdir -p "$tdir"
  (umask 002 && exec "$env_bin" -i "${envs[@]}" "$strace_bin" -qq -ff -y -s 256 --seccomp-bpf \
    -e "trace=$TRACE_SET" -o "$tdir/t" "$@")
  rc=$?
  if ! ferr="$("$python_bin" -c "$FILTER_PY" filter "$out" "$pkg" "$tdir" "$1" "$part" 2>&1)"; then
    ferr="$(tail -n 1 <<<"$ferr")"
    proof_error "$part" "$pkg${part:+ (part $part)}: ${ferr#ValueError: }"
  fi
  [[ "${UNIT_PROOF_KEEP_TRACES:-}" == 1 ]] || rm -rf -- "$tdir"
  return "$rc"
}

# shard_run <parts> <binary> <arg>…: the binary's tests dealt over <parts> traced processes that run
# at once, each on its own -test.run list; their output follows in part order. The names come from a
# traced -test.list run, and a listed test no part started is an error. Reads what traced_run reads.
shard_run() {
  local parts="$1"; shift
  local base="$out/trace/$slug" names name lost i=0 k rc=0 part_rc pids=() runs=()
  mkdir -p "$out/trace"
  if ! traced_run list "$1" -test.list=. > "$base#list.out" 2>&1; then
    # Keyed `names`, not `list`: the list run's own filter error, if any, stays beside it.
    proof_error names "$pkg: its test list could not be read: $(tail -n 1 "$base#list.out")"; return 2
  fi
  names="$(grep -E '^(Test|Fuzz|Example)[^[:space:]/]*$' "$base#list.out" | sort -u)"
  [[ -n "$names" ]] || { proof_error names "$pkg: its binary lists no test to split"; return 2; }
  while IFS= read -r name; do
    k=$((i % parts)); runs[k]="${runs[k]:+${runs[k]}|}$name"; i=$((i + 1))
  done <<<"$names"
  (( i >= parts )) || parts="$i"
  for ((k = 0; k < parts; k++)); do
    traced_run "$((k + 1))" "$@" "-test.run=^(${runs[k]})\$" > "$base#$((k + 1)).out" 2>&1 &
    pids[k]=$!
  done
  for ((k = 0; k < parts; k++)); do
    wait "${pids[k]}"; part_rc=$?
    cat "$base#$((k + 1)).out"
    (( rc != 0 || part_rc == 0 )) || rc="$part_rc"
  done
  # test2json framing puts one marker byte before a `=== RUN` line.
  lost="$(comm -23 <(printf '%s\n' "$names") \
    <(sed -n 's/^.\{0,1\}=== RUN   \([^[:space:]/]*\)$/\1/p' "$base#"[0-9]*.out | sort -u))"
  [[ "${UNIT_PROOF_KEEP_TRACES:-}" == 1 ]] || rm -f -- "$base#list.out" "$base#"[0-9]*.out
  # A part that failed already fails the package; the tests behind its failure are no second error.
  if (( rc == 0 )) && [[ -n "$lost" ]]; then
    proof_error lost "$pkg: $(grep -c . <<<"$lost") listed test(s) ran in no part, first $(head -n 1 <<<"$lost")"; return 2
  fi
  return "$rc"
}

# exec_binary <out> <binary> <arg>…: go test -exec's wrapper — one test binary in the hostile ambient.
exec_binary() {
  local out="$1"; shift
  local amb="$out/ambient" pkg slug envs=() name
  pkg="${PWD#"$PFM"}"; pkg="${pkg#/}"; pkg="${pkg:-.}"; slug="${pkg//\//%}"
  envs=("HOME=$amb/home" "PATH=$amb/bin" "HERDR_BIN_PATH=$amb/bin/herdr" "CLAUDECODE=1"
    "CLAUDE_CONFIG_DIR=$amb/home/.claude" "CODEX_HOME=$amb/home/.codex" "TMUX=$amb/tmux/default,4242,0"
    "TMUX_PANE=%9" "SHELL=$amb/bin/zsh")
  for name in "${KEEP_NAMES[@]}"; do [[ -z "${!name+x}" ]] || envs+=("$name=${!name}"); done
  local env_bin strace_bin python_bin parts
  env_bin="$(command -v env)" || { proof_error '' "no env for $pkg"; return 2; }
  if [[ "$(cat "$out/goos")" == linux ]]; then
    strace_bin="$(command -v strace)" || { proof_error '' "strace absent for $pkg"; return 2; }
    python_bin="$(command -v python3)"
    parts="$(proof_shards "$pkg")" || { proof_error '' "$parts"; return 2; }
    if (( parts > 1 )); then shard_run "$parts" "$@"; else traced_run '' "$@"; fi
    return
  fi
  (umask 002 && exec "$env_bin" -i "${envs[@]}" "$@")
}

# plant_ambient <out>: the canary home, the tripwire and its link under every executable name on PATH.
plant_ambient() {
  local out="$1" amb="$1/ambient" dir entry base
  mkdir -p "$amb/bin" "$amb/home" "$amb/tmux" || return 1
  # bash, as its PATH holds only tripwires: an argv's newlines fold to spaces, one log line a call.
  printf '#!/bin/bash\na="$*"; a="${a//$'"'"'\\n'"'"'/ }"\nprintf '"'"'%%s\\t%%s\\n'"'"' "${0##*/}" "$a" >> '"'"'%s'"'"'\necho "UNIT-PROOF TRIPWIRE ${0##*/} $a" >&2\nexit 97\n' \
    "$out/tripwire.log" > "$amb/tripwire" && chmod 0755 "$amb/tripwire" || return 1
  local IFS=:
  for dir in $PATH; do
    [[ -d "$dir" ]] || continue
    for entry in "$dir"/*; do
      base="${entry##*/}"
      [[ -x "$entry" && ! -d "$entry" && ! -e "$amb/bin/$base" && "$base" =~ ^[A-Za-z0-9._+-]+$ ]] || continue
      ln -s "$amb/tripwire" "$amb/bin/$base" || return 1
    done
  done
}

# checkout_files: every checkout file git does not ignore, NUL-separated, relative to the checkout.
checkout_files() {
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git -C "$REPO" --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" ls-files -z --cached --others --exclude-standard
  else
    git -C "$REPO" ls-files -z --cached --others --exclude-standard
  fi
}

run_proof() {
  local out="" pkgs=() jobs=() goos goroot traced=0
  while (( $# )); do
    case "$1" in
      --out) out="${2:?--out needs a directory}"; shift 2 ;;
      *) pkgs+=("$1"); shift ;;
    esac
  done
  (( ${#pkgs[@]} )) || pkgs=(./...)
  # The unit tier's package concurrency (pfm/Makefile TESTFLAGS) where the fence sets it: go's own
  # default is every CPU, and the proof runs beside the unit tier in the gate.
  if [[ -n "${PFM_FENCE_TEST_P:-}" ]]; then
    [[ "$PFM_FENCE_TEST_P" =~ ^[1-9][0-9]*$ ]] || die "PFM_FENCE_TEST_P must be a positive integer (got '$PFM_FENCE_TEST_P')"
    jobs=(-p "$PFM_FENCE_TEST_P")
  fi
  [[ -n "$out" ]] || out="/tmp/$PROJECT/unit-proof/$(date -u +%Y%m%dT%H%M%SZ)-$$"
  if [[ -d "$out" ]] && [[ -n "$(ls -A -- "$out")" ]]; then die "out directory $out already holds files"; fi
  mkdir -p "$out" && out="$(cd -- "$out" && pwd -P)" || die "cannot create out directory $out"
  command -v python3 >/dev/null 2>&1 || die "python3 not on PATH"
  goos="$(go env GOOS)" && goroot="$(go env GOROOT)" || die "go env failed"
  printf '%s\n' "$goos" > "$out/goos"
  if [[ "$goos" == linux ]]; then
    command -v strace >/dev/null 2>&1 || die "strace not on PATH (Linux traces every test binary)"
    traced=1
  fi
  checkout_files > "$out/files.z" && [[ -s "$out/files.z" ]] || die "the checkout file list of $REPO is unreadable"
  printf 'checkout\t%s\ngoroot\t%s\nsystmp\t%s\nfiles\t%s\n' "$REPO" "$goroot" \
    "$(cd /tmp && pwd -P)" "$out/files.z" > "$out/zones.tsv"
  plant_ambient "$out" || die "cannot plant the ambient under $out/ambient"
  mkdir -p "$out/profile"
  (cd "$PFM" && PFM_TEST_ARTIFACT_DIR="$out/profile" go test -count=1 -json -timeout 20m ${jobs[@]+"${jobs[@]}"} \
    -exec "bash '$SELF' --exec '$out'" "${pkgs[@]}" > "$out/tests.json" 2> "$out/go.stderr")
  python3 -c "$FILTER_PY" verdict "$out" "$goos" "$traced"
}

case "${1:-}" in
  --exec) shift; out="$1"; shift; exec_binary "$out" "$@" ;;
  --filter) shift; python3 -c "$FILTER_PY" filter "$@" ;;
  --verdict) shift; python3 -c "$FILTER_PY" verdict "$1" "$(cat "$1/goos" 2>/dev/null || echo linux)" "${2:-1}" ;;
  *) run_proof "$@" ;;
esac
