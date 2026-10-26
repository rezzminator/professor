# context-audit.py — reconstructs one loop's initial context from disk, reconciles it with the latest real first request, ranks what never earned its place.
# Output: one tab-separated line per finding, opened by its kind; a probe that could not look prints ERROR, never an empty row.
import argparse, glob, html, json, os, re, sqlite3, subprocess, sys, tempfile, time, tomllib
ap = argparse.ArgumentParser()
ap.add_argument('project', nargs='?', default='.')
ap.add_argument('--engine', choices=['claude', 'codex', 'opencode'], default='claude')
ap.add_argument('--loop', default='main')
ap.add_argument('--days', type=int, default=30)
A = ap.parse_args()
P, ENG, LOOP, DAYS = os.path.realpath(A.project), A.engine, A.loop, A.days
H = os.path.expanduser('~')
L, W, T0 = [], [], [0]  # layers [kind, path, text, matcher, use_key]; wire blocks [name, text]
def out(*a): print('\t'.join(str(x) for x in a))
def rd(p):
    try: return open(p, encoding='utf-8', errors='replace').read()
    except FileNotFoundError: return None
    except OSError as e: out('ERROR', f'read {p}: {e}')
def fm(t):  # frontmatter dict (flat keys; block scalars joined), body
    m = re.match(r'---\n(.*?)\n---\n?(.*)', t or '', re.S)
    if not m: return {}, t or ''
    d, k = {}, None
    for ln in m.group(1).splitlines():
        kv = re.match(r'([A-Za-z_-]+):\s*(.*)', ln)
        if kv:
            k, v = kv.groups(); v = v.strip()
            if v[:1] == "'" and v[-1:] == "'": v = v[1:-1].replace("''", "'")
            elif v[:1] == '"' and v[-1:] == '"': v = json.loads(v)
            d[k] = '' if v in ('>', '|', '>-', '|-') else v
        elif k and ln.startswith((' ', '\t')): d[k] = (d[k] + ' ' + ln.strip()).strip()
    return d, m.group(2)
def add(kind, path, text=None, matcher=None, key=None):
    L.append([kind, path, text, matcher, key])
def probe(path, kind, **kw):  # every probe prints found/absent, so absence never reads as not-looked
    t = rd(path)
    out('PROBE', kind, 'found' if t is not None else 'absent', path)
    if t is not None: add(kind, path, t, **kw)
    return t
def listing(kind, files, namer, key):  # one layer per model-visible description
    for f in sorted(files):
        d, _ = fm(rd(f))
        if d.get('disable-model-invocation') == 'true': continue
        desc = (d.get('description', '') + ' ' + d.get('when_to_use', '')).strip()
        if desc: add(kind, f, desc, key=(key, namer(f, d))); L[-1].append(d)
        else: out('WARN', 'no description', f)
def ancestors(root):
    ds, d = [], P
    while True:
        ds.append(d)
        if d == root or d == '/': break
        d = os.path.dirname(d)
    return ds[::-1]

def claude():
    CFG = os.environ.get('CLAUDE_CONFIG_DIR') or H + '/.claude'
    S = json.loads(rd(CFG + '/settings.json') or '{}')
    FENCE, SPAN = re.compile(r'```.*?```', re.S), re.compile(r'`[^`\n]*`')
    HTML, IMP = re.compile(r'(?ms)^[ \t]*<!--.*?-->[ \t]*\n?'), re.compile(r'(?<![\w`])@((?:\\ |[^\s`])+)')
    def chain(kind, p, hop=0, seen=()):
        t = rd(p) if hop else probe(p, kind)
        if t is None: return False
        if os.path.getsize(p) > 4 << 20: out('LIMIT', p, 'over 4 MiB: Claude Code skips the file')
        t = HTML.sub('', t)
        if hop: add(f'{kind} import hop {hop}', p, t)
        else: L[-1][2] = t
        if hop < 4:  # four hops
            for m in IMP.finditer(SPAN.sub('', FENCE.sub('', t))):
                q = os.path.join(os.path.dirname(p), os.path.expanduser(m.group(1).replace('\\ ', ' ')))
                if os.path.isfile(q) and q not in seen: chain(kind, q, hop + 1, seen + (p,))
        return True
    agent, af = {}, None
    if LOOP != 'main':
        af = next((f for f in [P + f'/.claude/agents/{LOOP}.md', CFG + f'/agents/{LOOP}.md'] if os.path.isfile(f)), None)
        agent, body = fm(rd(af)) if af else ({}, '')
        out('PROBE', 'agent file', 'found' if af else 'absent (built-in or plugin agent: body read from the wire only)', af or LOOP)
    nochain = LOOP in ('Explore', 'Plan') or agent.get('omitClaudeMd') == 'true'
    for p in ['/etc/claude-code/CLAUDE.md', '/Library/Application Support/ClaudeCode/CLAUDE.md']: chain('managed', p)
    if not nochain:
        chain('user', CFG + '/CLAUDE.md')
        for r in glob.glob(CFG + '/rules/**/*.md', recursive=True):
            if 'paths' not in fm(rd(r))[0]: chain('user rule', r)
        mode = S.get('pluginConfigs', {}).get('cc-plugin-agents-md@builtin', {}).get('options', {}).get('instructionFiles', 'claude-md-or-agents-md')
        dirs = ancestors('/')
        hasclaude = any(os.path.isfile(os.path.join(d, n)) for d in dirs for n in ('CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md'))
        agents_md = mode == 'claude-md-and-agents-md' or (mode == 'claude-md-or-agents-md' and not hasclaude)
        out('PROBE', 'instructionFiles', mode, 'AGENTS.md ' + ('read' if agents_md else 'not read'))
        for d in dirs:
            for n in ['CLAUDE.md', '.claude/CLAUDE.md', 'CLAUDE.local.md'] + (['AGENTS.md', '.claude/AGENTS.md'] if agents_md else []):
                if os.path.isfile(os.path.join(d, n)): chain('project', os.path.join(d, n))
        for r in sorted(glob.glob(P + '/.claude/rules/**/*.md', recursive=True)):
            if 'paths' not in fm(rd(r))[0]: chain('project rule', r)
        out('PROBE', 'rules', f'{len(glob.glob(CFG + "/rules/**/*.md", recursive=True))} user, {len(glob.glob(P + "/.claude/rules/**/*.md", recursive=True))} project')
        slug = os.environ.get('CLAUDE_CODE_PROJECT_DIR_NAME') or re.sub(r'[^A-Za-z0-9]', '-', P)
        mem = CFG + f'/projects/{slug}/memory/MEMORY.md'
        t = probe(mem, 'auto memory')
        if t is not None:
            keep = '\n'.join(t.splitlines()[:200])[:25 * 1024]
            if len(keep) < len(t.rstrip('\n')): out('LIMIT', mem, f'cut after line {keep.count(chr(10)) + 1}: 200 lines or 25KB; lost:', *re.findall(r'(?m)^#+ .*', t[len(keep):]))
    if LOOP == 'main':
        src = (rd(H + '/.local/share/pfm/install/source-repo') or '').strip()
        if src: probe(src + '/pfm/harness-prompts/generated/claude.md', 'fleet prompt')
        else: out('PROBE', 'fleet prompt', 'absent', 'no pfm source-repo marker')
    else:
        add('agent body', af or f'(built-in {LOOP})', body if af else None, matcher=None if af else r'^system\[1\]')
        for s in [x.strip() for x in agent.get('skills', '').strip('[]').split(',') if x.strip()][:32]:
            c = s.replace(':', '/')  # a namespaced skill can be a command file
            probe(next(iter(glob.glob(f'{CFG}/skills/{s}/SKILL.md') + glob.glob(f'{P}/.claude/skills/{s}/SKILL.md') + glob.glob(f'{P}/.claude/commands/{c}.md') + glob.glob(f'{CFG}/commands/{c}.md')), f'{s} (missing)'), 'preloaded skill')
            if L[-1][0] == 'preloaded skill': L[-1][2] = fm(L[-1][2])[1]  # the body loads, the frontmatter does not
    tools = agent.get('tools', '') if LOOP != 'main' else ''
    plug, mk = [], json.loads(rd(CFG + '/plugins/known_marketplaces.json') or '{}')
    def plugdir(name, r):  # a directory marketplace's plugin loads from its folder, never its cache; any other from its cache
        n, m = name.split('@')
        src = mk.get(m, {}).get('source', {})
        if src.get('source') != 'directory': return r['installPath']
        e = next((e for e in json.loads(rd(src['path'] + '/.claude-plugin/marketplace.json') or '{"plugins":[]}')['plugins'] if e.get('name') == n), {})
        if isinstance(e.get('source'), str): return os.path.normpath(os.path.join(src['path'], e['source']))
        out('ERROR', f'directory marketplace {m}: no folder for plugin {n} in {src["path"]}/.claude-plugin/marketplace.json — its listings not read')
    for name, rows in json.loads(rd(CFG + '/plugins/installed_plugins.json') or '{"plugins":{}}')['plugins'].items():
        if S.get('enabledPlugins', {}).get(name):
            r = next((r for r in rows if r.get('scope') == 'user' or P.startswith(r.get('projectPath', '\0'))), None)
            ip = plugdir(name, r) if r else None
            if ip: plug.append((name.split('@')[0], ip))
    out('PROBE', 'plugins', f'{len(plug)} enabled and installed for this project')
    cmdname = lambda base: lambda f, d: os.path.relpath(f, base)[:-3].replace('/', ':')
    if not tools or 'Skill' in tools:
        listing('skill', glob.glob(CFG + '/skills/*/SKILL.md') + glob.glob(P + '/.claude/skills/*/SKILL.md'), lambda f, d: os.path.basename(os.path.dirname(f)), 'skill')
        listing('command', glob.glob(CFG + '/commands/**/*.md', recursive=True), cmdname(CFG + '/commands'), 'skill')
        listing('command', glob.glob(P + '/.claude/commands/**/*.md', recursive=True), cmdname(P + '/.claude/commands'), 'skill')
        for n, ip in plug:
            listing('plugin skill', glob.glob(ip + '/skills/*/SKILL.md'), lambda f, d, n=n: f'{n}:' + os.path.basename(os.path.dirname(f)), 'skill')
            listing('plugin command', glob.glob(ip + '/commands/**/*.md', recursive=True), lambda f, d, n=n, ip=ip: f'{n}:' + cmdname(ip + '/commands')(f, d), 'skill')
    if not tools or 'Agent' in tools:
        listing('agent', glob.glob(CFG + '/agents/*.md') + glob.glob(P + '/.claude/agents/*.md'), lambda f, d: d.get('name') or os.path.basename(f)[:-3], 'agent')
        for n, ip in plug: listing('plugin agent', glob.glob(ip + '/agents/*.md'), lambda f, d, n=n: f'{n}:' + (d.get('name') or os.path.basename(f)[:-3]), 'agent')
    for kind, m in [('identity (harness)', r'^system\[0\]'), ('system prompt (harness)', r'^system\['), ('tool schemas (harness)', r'^tool:'),
                    ('environment, git status, date (harness)', r'Environment|As you answer|gitStatus|<env>'), ('SessionStart hook output (settings and plugin hooks)', r'^SessionStart'),
                    ('deferred tool names (MCP and harness)', r'deferred tools'), ('MCP server instructions (MCP config)', r'MCP Server'),
                    ('skill and agent listing framing (harness)', r'skills|agent types'), ('instruction framing (harness)', r'system-reminder|Contents of'), ('task message', r'^prompt')]:
        add(kind, '(wire)', None, matcher=m)
    # measured: latest transcript of this loop
    proj = CFG + '/projects/' + re.sub(r'[^A-Za-z0-9]', '-', P)
    if LOOP == 'main': cands = glob.glob(proj + '/*.jsonl')
    else: cands = [m[:-10] + '.jsonl' for m in glob.glob(proj + '/*/subagents/*.meta.json') if json.loads(rd(m) or '{}').get('agentType') == LOOP]
    for t in sorted(cands, key=os.path.getmtime, reverse=True)[:20]:
        shape, blobs, use = None, [], None
        for ln in open(t, errors='replace'):
            try: r = json.loads(ln)
            except ValueError: continue
            if r.get('type') == 'api-request-shape' and not shape: shape = r['shape']; T0[0] = time.mktime(time.strptime(r['timestamp'][:19], '%Y-%m-%dT%H:%M:%S')) - time.timezone
            elif r.get('type') == 'api-request-blob' and not use: blobs.append(r['message'])
            elif r.get('type') == 'assistant' and r.get('message', {}).get('usage'):
                u = r['message']['usage']; use = u.get('input_tokens', 0) + u.get('cache_creation_input_tokens', 0) + u.get('cache_read_input_tokens', 0); break
        if not use: continue
        if shape:
            W.extend((f'system[{i}]', b.get('text', '')) for i, b in enumerate(shape.get('system', [])))
            W.extend((f'tool:{x.get("name")}', json.dumps(x)) for x in shape.get('tools', []))
            parts = [(b['role'], p.get('text', '')) for b in blobs for p in (b['content'] if isinstance(b['content'], list) else [{'text': b['content']}]) if isinstance(p, dict)]
            task = next((i for i, (role, x) in enumerate(parts) if role == 'user' and not x.lstrip().startswith(('<', 'SessionStart'))), None)  # the first plain user text
            for i, (_, txt) in enumerate(parts):
                segs = re.split(r'(?m)^(?=<system-reminder>|SessionStart:|# Environment|The following deferred tools|Available agent types|# MCP Server Instructions|The following skills|Contents of )', txt)
                W.extend(('prompt' if i == task else (s.strip().splitlines() or [''])[0][:60], s) for s in segs if s.strip())
        disk = {x[4] for x in L if x[4]}
        for n, seg in list(W):  # listing entries with no file on disk: built-in or bundled, named from the wire itself
            k = 'agent' if n.startswith('Available agent types') else 'skill' if n.startswith('The following skills') else None
            for m in re.finditer(r'(?ms)^- ([\w:.-]+): (.*?)(?=^- |\Z)', seg if k else ''):
                if (k, m.group(1)) not in disk: add(f'{k} on the wire only (built-in, bundled, or deleted since)', '(no file)', m.group(0).strip(), key=(k, m.group(1)))
        return t, use, bool(shape)
    out('ERROR', f'no transcript with a first assistant usage for loop {LOOP} under {proj}')
    return None, None, False

def codex():
    CH = os.environ.get('CODEX_HOME') or H + '/.codex'
    C = tomllib.loads(rd(CH + '/config.toml') or '')
    for n in ['AGENTS.override.md', 'AGENTS.md']:
        t = probe(CH + '/' + n, 'global')
        if t and t.strip(): break
    root = next((d for d in ancestors('/')[::-1] if os.path.exists(d + '/.git')), None)
    trust = C.get('projects', {}).get(root or P, {}).get('trust_level', 'unset')
    out('PROBE', 'project root', root or 'none (cwd only)', f'trust_level={trust}')
    budget = C.get('project_doc_max_bytes', 32768)
    names = ['AGENTS.override.md', 'AGENTS.md'] + C.get('project_doc_fallback_filenames', [])
    for d in ancestors(root or P) if trust != 'untrusted' else []:
        f = next((os.path.join(d, n) for n in names if os.path.isfile(os.path.join(d, n))), None)
        if not f: continue
        raw = open(f, 'rb').read()
        if budget <= 0: out('LIMIT', f, f'dropped: project_doc_max_bytes={C.get("project_doc_max_bytes", 32768)} spent'); continue
        if len(raw) > budget:
            kept = raw[:budget].decode('utf-8', 'replace')
            out('LIMIT', f, f'cut at byte {budget}, line {kept.count(chr(10)) + 1}; last text read: "{(kept.rstrip().splitlines() or [""])[-1][:80]}"; lost:', *re.findall(r'(?m)^#+ .*', raw[budget:].decode('utf-8', 'replace')))
        probe(f, 'project'); L[-1][2] = raw[:budget].decode('utf-8', 'replace'); budget -= min(len(raw), budget)
    if C.get('developer_instructions'): add('fleet prompt (config.toml developer_instructions)', CH + '/config.toml', C['developer_instructions'])
    if LOOP != 'main':
        rf = next((f for f in [P + f'/.codex/agents/{LOOP}.toml', CH + f'/agents/{LOOP}.toml'] if os.path.isfile(f)), None)
        out('PROBE', 'role file', 'found' if rf else 'absent', rf or LOOP)
        if rf: add('role developer_instructions', rf, tomllib.loads(rd(rf)).get('developer_instructions'))
    pl = [g for k, v in C.get('plugins', {}).items() if v.get('enabled') for g in glob.glob(f"{CH}/plugins/cache/{k.split('@')[1]}/{k.split('@')[0]}/*/skills/*/SKILL.md")]
    out('PROBE', 'plugins', f"{sum(1 for v in C.get('plugins', {}).values() if v.get('enabled'))} enabled, {len(pl)} plugin skills (every cached version listed: the loaded one shows LOADED)")
    listing('skill', glob.glob(CH + '/skills/*/SKILL.md') + glob.glob(CH + '/skills/.system/*/SKILL.md') + glob.glob(P + '/.codex/skills/*/SKILL.md') + pl, lambda f, d: d.get('name') or os.path.basename(os.path.dirname(f)), 'skill')
    for kind, m in [('base instructions (harness)', r'^base'), ('permissions, collaboration, multi-agent (harness)', r'<permissions|<collaboration|<multi_agent|<model_switch|<recommended|<apps_instructions|<plugins_instructions|You are `/root`'),
                    ('skills framing (harness)', r'<skills_instructions'), ('environment context (harness)', r'<environment_context'), ('instruction framing (harness)', r'AGENTS.md instructions'), ('task message', r'^prompt')]:
        add(kind, '(rollout)', None, matcher=m)
    for t in sorted(glob.glob(CH + '/sessions/**/rollout-*.jsonl', recursive=True), key=os.path.getmtime, reverse=True)[:3000]:
        with open(t, errors='replace') as fh:
            try: first = json.loads(fh.readline()); meta = first.get('payload', {})
            except ValueError: continue
            if os.path.realpath(str(meta.get('cwd', ''))) != P: continue  # a worktree cwd loads a different chain
            if (LOOP == 'main') == (meta.get('thread_source') == 'subagent') or (LOOP != 'main' and meta.get('agent_role') != LOOP): continue
            blocks = [('base', (meta.get('base_instructions') or {}).get('text', ''))]
            for ln in fh:
                r = json.loads(ln); p = r.get('payload') or {}
                if p.get('type') == 'message' and p.get('role') in ('developer', 'user'):
                    for c in p.get('content', []):
                        x = c.get('text', ''); blocks.append(((x.strip().splitlines() or [''])[0][:60] if x.lstrip().startswith(('<', '#', 'You are')) else 'prompt', x))
                if p.get('type') == 'token_count' and p.get('info'):
                    T0[0] = time.mktime(time.strptime(first['timestamp'][:19], '%Y-%m-%dT%H:%M:%S')) - time.timezone
                    W.extend(blocks); return t, p['info']['last_token_usage']['input_tokens'], False
    out('ERROR', f'no rollout with a token_count for loop {LOOP} under cwd {P}')
    return None, None, False

def opencode():
    G = H + '/.config/opencode'
    off = os.environ.get('OPENCODE_DISABLE_CLAUDE_CODE') or os.environ.get('OPENCODE_DISABLE_CLAUDE_CODE_PROMPT')
    for f in [G + '/AGENTS.md'] + ([] if off else [H + '/.claude/CLAUDE.md']):
        if probe(f, 'global') is not None: break
    root = subprocess.run(['git', '-C', P, 'rev-parse', '--show-toplevel'], capture_output=True, text=True).stdout.strip() or P
    for n in ['AGENTS.md'] + ([] if off else ['CLAUDE.md']) + ['CONTEXT.md']:
        hits = [os.path.join(d, n) for d in ancestors(root)[::-1] if os.path.isfile(os.path.join(d, n))]
        for h in hits: probe(h, 'project')
        if hits: break
    with tempfile.TemporaryFile('w+') as fh:  # a pipe truncates opencode's stdout at 64 KiB; a file does not
        r = subprocess.run(['opencode', 'debug', 'config'], cwd=P, stdout=fh, stderr=subprocess.PIPE, text=True, timeout=60); fh.seek(0)
        try: cfg = json.loads(fh.read()) if not r.returncode else {}
        except ValueError as e: cfg = {}; out('ERROR', f'opencode debug config: unparseable JSON ({e}) — instructions and skills.paths not assembled')
    if r.returncode: out('ERROR', f'opencode debug config exited {r.returncode}: {r.stderr.strip()[:200]} — instructions and skills.paths not assembled')
    for i in cfg.get('instructions', []):
        if i.startswith(('http://', 'https://')): add('remote instruction (not fetched)', i); continue
        i = os.path.expanduser(i)
        hits = glob.glob(i) if os.path.isabs(i) else [g for d in ancestors(root) for g in glob.glob(os.path.join(d, i))]
        out('PROBE', 'instructions entry', f'{len(hits)} files', i)
        for h in sorted(set(hits)): probe(h, 'instructions')
    if LOOP not in ('main', 'build', 'plan'):
        af = next((f for f in [P + f'/.opencode/agent/{LOOP}.md', G + f'/agent/{LOOP}.md', G + f'/agents/{LOOP}.md'] if os.path.isfile(f)), None)
        out('PROBE', 'agent file', 'found' if af else 'absent', af or LOOP)
        if af: add('agent body', af, fm(rd(af))[1])
    sk = [g for d in [P + '/.opencode/skills', G + '/skills'] + cfg.get('skills', {}).get('paths', []) + ([] if off else [P + '/.claude/skills', H + '/.claude/skills']) for g in glob.glob(d + '/*/SKILL.md')]
    listing('skill', sk, lambda f, d: d.get('name') or os.path.basename(os.path.dirname(f)), 'skill')
    add('base prompt and tool schemas (harness, unrecorded)', '(binary)', None, matcher=r'^$never')
    db = H + '/.local/share/opencode/opencode.db'
    try:
        q = sqlite3.connect(f'file:{db}?mode=ro', uri=True, timeout=15)
        row = q.execute("""SELECT s.id, json_extract(m.data,'$.tokens.input')+json_extract(m.data,'$.tokens.cache.read')+json_extract(m.data,'$.tokens.cache.write')
            FROM message m JOIN session s ON s.id=m.session_id WHERE (s.directory=? OR s.directory LIKE ?) AND json_extract(m.data,'$.role')='assistant'
            AND json_extract(m.data,'$.agent')=? AND json_extract(m.data,'$.tokens.input')+json_extract(m.data,'$.tokens.cache.read')>0
            ORDER BY s.time_created DESC, m.time_created ASC LIMIT 1""", (P, P + '/%', 'build' if LOOP == 'main' else LOOP)).fetchone()
    except sqlite3.Error as e: out('ERROR', f'{db}: {e}'); return None, None, False
    if not row: out('ERROR', f'no OpenCode assistant message with tokens for agent {LOOP} under {P}'); return None, None, False
    return f'{db} session {row[0]}', row[1], False

def used():  # real usage over the window, per listing key
    n = {}
    since = int((time.time() - DAYS * 86400) * 1000)
    try:
        if ENG == 'claude':
            s = os.environ.get('XDG_STATE_HOME', ''); s = s if s.startswith('/') else H + '/.local/state'
            q = sqlite3.connect(f'file:{os.environ.get("CALLMETER_HOME") or s + "/callmeter"}/callmeter.db?mode=ro', uri=True, timeout=15)
            for k, sql in [('skill', "SELECT json_extract(c.input,'$.skill'), count(*) FROM calls c JOIN sessions s USING(session_id) WHERE c.tool='Skill' AND c.ts>=? AND (s.cwd=? OR s.cwd LIKE ?) GROUP BY 1"),
                           ('skill', "SELECT e.command_name, count(*) FROM events e JOIN sessions s USING(session_id) WHERE e.event='UserPromptExpansion' AND e.ts>=? AND (s.cwd=? OR s.cwd LIKE ?) GROUP BY 1"),
                           ('agent', "SELECT json_extract(c.input,'$.subagent_type'), count(*) FROM calls c JOIN sessions s USING(session_id) WHERE c.tool IN ('Agent','Task') AND c.ts>=? AND (s.cwd=? OR s.cwd LIKE ?) GROUP BY 1")]:
                for name, c in q.execute(sql, (since, P, P + '/%')): n[(k, name)] = n.get((k, name), 0) + c
            sess = q.execute("SELECT count(*) FROM sessions WHERE last_ts>=? AND (cwd=? OR cwd LIKE ?)", (since, P, P + '/%')).fetchone()[0]
        elif ENG == 'codex':
            sess = 0
            for t in glob.glob((os.environ.get('CODEX_HOME') or H + '/.codex') + '/sessions/**/rollout-*.jsonl', recursive=True):
                if os.path.getmtime(t) * 1000 < since: continue
                txt = open(t, errors='replace').read()
                if f'"cwd":"{P}' not in txt[:4000].replace(' ', ''): continue
                sess += 1
                for a in re.findall(r'agent_type\\?":\\?"([\w-]+)', txt): n[('agent', a)] = n.get(('agent', a), 0) + 1
                for a in re.findall(r'skills/([\w:-]+)/SKILL\.md', txt): n[('skill', a)] = n.get(('skill', a), 0) + 1
        else:
            q = sqlite3.connect(f'file:{H}/.local/share/opencode/opencode.db?mode=ro', uri=True, timeout=15)
            for tool, k, fld in [('skill', 'skill', 'name'), ('task', 'agent', 'subagent_type')]:
                for name, c in q.execute(f"""SELECT json_extract(p.data,'$.state.input.{fld}'), count(*) FROM part p JOIN session s ON s.id=p.session_id
                    WHERE json_extract(p.data,'$.tool')=? AND p.time_created>=? AND (s.directory=? OR s.directory LIKE ?) GROUP BY 1""", (tool, since, P, P + '/%')):
                    n[(k, name)] = c
            sess = q.execute("SELECT count(*) FROM session WHERE time_updated>=? AND (directory=? OR directory LIKE ?)", (since, P, P + '/%')).fetchone()[0]
    except (sqlite3.Error, OSError) as e:
        out('ERROR', f'usage store unreadable: {e} — earned-its-place not checked'); return None
    out('USAGE', f'{sess} sessions under {P} in the last {DAYS} days, {sum(n.values())} recorded uses')
    if not sess: out('ERROR', 'no session in the window — earned-its-place not checked')
    return n if sess else None

src, measured, wire = {'claude': claude, 'codex': codex, 'opencode': opencode}[ENG]()
U = used()
WB = sum(len(t.encode()) for _, t in W)
R = WB / measured if wire and measured else 4.0
norm = lambda s: re.sub(r'\s+', ' ', s).strip()
WN = [(n, norm(html.unescape(t))) for n, t in W]
claimed = [0] * len(W)
rows = []
inblock = [0] * len(W)
loaded = {x[4] for x in L if x[4] and x[2] and W and any(norm(x[2]) in w for _, w in WN)}
for kind, path, text, matcher, key, *meta in L:
    b = len(text.encode()) if text else 0
    st = 'UNVERIFIED' if not W else ''
    if key and not W and any(r[6] == key[1] and r[0] == kind for r in rows): st = 'DUPLICATE NAME (one copy loads; which is unverified)'
    if text and W:
        t = norm(text)
        i = next((j for j, (_, w) in enumerate(WN) if t in w), None)
        if i is not None: st = 'LOADED'
        elif os.path.exists(path) and os.path.getmtime(path) > T0[0] > 0: st = 'CHANGED-SINCE (file newer than the measured session)'
        elif any(t[:120] in w for _, w in WN): st = 'DIFFERS (loaded text differs from disk: cut, or a copy cached before an edit)'
        elif meta and 'visibility' in meta[0]: st = 'HIDDEN (visibility:)'
        elif key and key in loaded: st = 'SHADOWED (same name loaded from another file)'
        else: st = 'NOT-LOADED'
        i = i if i is not None else next((j for j, (_, w) in enumerate(WN) if t[:120] in w), None)  # an edited or cut layer still claims its block
        if i is not None: claimed[i] = min(claimed[i] + b, len(W[i][1].encode())); inblock[i] += 1
    elif matcher and W:  # harness and framing claim the residual; framing bounded so a missing layer stays UNLISTED
        hit = [j for j, (n, _) in enumerate(W) if re.search(matcher, n) and claimed[j] < len(W[j][1].encode())]
        for j in hit:
            rest = len(W[j][1].encode()) - claimed[j]
            take = min(rest, 200 * inblock[j] + 500) if inblock[j] else rest
            b += take; claimed[j] += take
        st = 'LOADED' if hit else 'NOT-ON-WIRE'
    elif matcher: st = 'UNMEASURED'
    u = '' if not key else ('n/a' if U is None else U.get(key, 0) + U.get((key[0], key[1].split(':')[-1]), 0) * (':' in key[1]))
    rows.append((kind, b, round(b / R), st, u, path, key[1] if key else ''))
out('#', 'kind', 'bytes', '~tokens', 'status', f'uses/{DAYS}d', 'source', 'name')
for r in rows: out('LAYER', *r)
listed = sum(r[1] for r in rows if r[3] in ('LOADED', 'UNVERIFIED') or r[3].startswith('DIFFERS'))
if measured:
    out('MEASURED', f'{measured} tokens', f'{WB} recorded bytes', f'{R:.2f} bytes/token ' + ('calibrated on this request' if wire else 'assumed'), src)
    for j, (n, t) in enumerate(W):
        rest = len(t.encode()) - claimed[j]
        if rest > 500: out('UNLISTED', n, f'{rest} B', f'~{round(rest / R)} tokens')
    gap = sum(max(0, len(t.encode()) - claimed[j]) for j, (_, t) in enumerate(W)) / R if W else measured - listed / R
    if W and not wire: out('UNRECORDED', f'~{measured - round(WB / R)} tokens of the measured count are not in the log (tool schemas): unaudited, not part of GAP')
    why = ' — FINDING: a layer is missing from the list' if W else ' — UNVERIFIED: the store records no request text, so the unrecorded harness prompt and tool schemas sit inside this gap'
    out('GAP', f'{100 * gap / measured:.1f}% of measured first-turn tokens not traced to a listed layer' + (why if gap > 0.1 * measured else ''))
# across layers: one rule in two layers
seen, dup = {}, 0
for kind, path, text, *_ in L:
    for ln in set(norm(x).strip('-*# ').lower() for x in (text or '').splitlines()):
        if len(ln) >= 40:
            if ln in seen and seen[ln] != path: dup += 1; out('DUP', seen[ln], path, ln[:110]) if dup <= 40 else None
            seen.setdefault(ln, path)
out('DUP', f'{dup} lines stated in two layers' + (' (first 40 shown)' if dup > 40 else ''))
# mirror byte overhead over source
for kind, mirror, source in [('AGENTS.md', [P + '/AGENTS.md'], lambda m: P + '/CLAUDE.md'),
                             ('codex roles', glob.glob(P + '/.codex/agents/*.toml'), lambda m: P + '/.claude/agents/' + os.path.basename(m)[:-5] + '.md'),
                             ('opencode agents', glob.glob(P + '/.opencode/agent/*.md'), lambda m: P + '/.claude/agents/' + os.path.basename(m))]:
    pairs = [(m, source(m)) for m in mirror if os.path.isfile(m)]
    sb = sum(os.path.getsize(s) for _, s in pairs if s and os.path.isfile(s)); mb = sum(os.path.getsize(m) for m, s in pairs if s and os.path.isfile(s))
    out('MIRROR', kind, f'{len(pairs)} files', f'{len([1 for _, s in pairs if s and os.path.isfile(s)])} with a source', f'source {sb} B → mirror {mb} B', f'overhead {mb - sb:+} B')
# earned its place: loaded items real usage never touched, ranked by tokens reclaimed
idle = sorted((r for r in rows if r[4] == 0 and (r[3] in ('LOADED', 'UNVERIFIED') or r[3].startswith('DIFFERS'))), key=lambda r: -r[2])
for r in idle: out('UNUSED', r[2], r[0], r[6], r[5])
out('UNUSED', f'{len(idle)} loaded items with 0 uses in {DAYS} days, ~{sum(r[2] for r in idle)} tokens' if U is not None else 'ERROR: usage unknown — earned-its-place not checked')
