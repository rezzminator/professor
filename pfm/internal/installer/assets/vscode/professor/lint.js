// `Professor: Analyze` — prompt lint. The "Analyze" link above line 1 of a prompt file (a CodeLens
// that reads "Analyzing… Ns · Cancel" while its run goes, and the palette command) sends the editor's
// current text, saved or not and numbered line by line, to Claude through `pfm headless exec`, so
// the run uses the user's own Claude Code subscription. Claude applies
// Professor's prompt law (~/.claude/commands/quality/prompt.md, read at run time, never copied here)
// under prompt-lint.md, answers in findings.schema.json's shape, and each finding becomes a
// diagnostic in the Problems panel; one that carries a rewrite gets the quick fix "Apply Professor
// rewrite". The run is click-only, sealed (no tools, settings or session persistence for the model)
// and cancellable from the lens or the progress notification; every way it can fail is shown with
// its cause, never as an empty result.
const vscode = require('vscode');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { runPfm } = require('./choose');

const SEVERITY = { error: 'Error', warning: 'Warning', info: 'Information' };
const RULE_ID = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const STDERR_TAIL = 1500;
// pfm stops the engine at --timeout itself; this margin only catches a pfm that outlives its own.
const KILL_MARGIN_S = 30;

function settings() {
  const cfg = vscode.workspace.getConfiguration('professor.promptLint');
  return { globs: cfg.get('globs') ?? [], model: cfg.get('model'), effort: cfg.get('effort'), timeout: cfg.get('timeout') };
}

function isPromptFile(document) {
  return settings().globs.some((pattern) => vscode.languages.match({ pattern }, document) > 0);
}

function lawFile() {
  return path.join(os.homedir(), '.claude', 'commands', 'quality', 'prompt.md');
}

// pfmArgs is the one argv the extension runs: Claude, the law as a framed --files block, the task on stdin.
function pfmArgs({ model, effort, timeout }, law) {
  const args = ['headless', 'exec', '--engine', 'claude'];
  if (model) args.push('--model', model);
  if (effort) args.push('--effort', effort);
  args.push(
    '--sealed',
    '--system-file', path.join(__dirname, 'prompt-lint.md'),
    '--schema', path.join(__dirname, 'findings.schema.json'),
    '--output-format', 'json',
    '--timeout', String(timeout),
    `--files=${law}`,
  );
  return args;
}

function taskText(label, lines) {
  const numbered = lines.map((text, index) => `${index + 1}\t${text}`).join('\n');
  return `Lint the target prompt file ${label} against the law.\n\n===== TARGET: ${label} =====\n${numbered}\n===== END TARGET =====\n`;
}

function withTail(cause, stderr) {
  const text = (stderr ?? '').trim();
  if (!text) return cause;
  return `${cause}: ${text.length > STDERR_TAIL ? `…${text.slice(-STDERR_TAIL)}` : text}`;
}

// readFindings validates a run at entry: the findings array, or an Error saying why there is none.
function readFindings(result, timeoutS) {
  if (!result.ok) {
    let cause;
    if (result.cause?.killed || (result.exited && result.code === 3)) {
      cause = `pfm headless exec did not finish within ${timeoutS} s (professor.promptLint.timeout)`;
    } else if (result.exited && result.code === 5) {
      cause = "Claude's answer did not match findings.schema.json";
    } else if (result.exited) {
      cause = `pfm headless exec exited ${result.code}`;
    } else {
      cause = `pfm could not run: ${result.cause?.message ?? result.cause}`;
    }
    throw new Error(withTail(cause, result.stderr));
  }
  let envelope;
  try {
    envelope = JSON.parse(result.stdout);
  } catch (err) {
    throw new Error(withTail(`pfm headless exec printed unparsable JSON (${err.message})`, result.stderr));
  }
  const findings = envelope?.structured_output?.findings;
  if (!Array.isArray(findings)) {
    throw new Error(withTail('pfm headless exec printed JSON without a structured_output.findings list', result.stderr));
  }
  return findings;
}

function invalidReason(finding) {
  if (!finding || typeof finding !== 'object') return 'not an object';
  if (!Number.isInteger(finding.line) || !Number.isInteger(finding.endLine)) return 'line and endLine must be integers';
  if (!Object.hasOwn(SEVERITY, finding.severity)) return `severity ${JSON.stringify(finding.severity)} is not error, warning or info`;
  if (typeof finding.rule !== 'string' || !RULE_ID.test(finding.rule)) return `rule ${JSON.stringify(finding.rule)} is not a kebab-case id`;
  if (typeof finding.message !== 'string' || !finding.message.trim()) return 'message is empty';
  if (finding.rewrite !== undefined && typeof finding.rewrite !== 'string') return 'rewrite is not a string';
  return '';
}

function wholeLines(document, start, end) {
  return new vscode.Range(start - 1, 0, end - 1, document.lineAt(end - 1).text.length);
}

// toDiagnostics maps findings onto the document whose text was sent (lines): a line out of range is
// clamped and says so; an invalid finding becomes an error diagnostic on line 1, never dropped.
// fixes are the rewrites a quick fix may apply, each with the text it replaces.
function toDiagnostics(document, findings, lines) {
  const last = Math.max(1, Math.min(lines.length, document.lineCount));
  const diagnostics = [];
  const fixes = [];
  let invalid = 0;
  for (const finding of findings) {
    const reason = invalidReason(finding);
    if (reason) {
      invalid++;
      const diagnostic = new vscode.Diagnostic(
        wholeLines(document, 1, 1),
        `Professor returned an invalid finding (${reason}): ${JSON.stringify(finding).slice(0, 400)}`,
        vscode.DiagnosticSeverity.Error,
      );
      diagnostic.source = 'Professor';
      diagnostic.code = 'invalid-finding';
      diagnostics.push(diagnostic);
      continue;
    }
    const start = Math.min(Math.max(finding.line, 1), last);
    const end = Math.min(Math.max(finding.endLine, start), last);
    const clamped = start !== finding.line || end !== finding.endLine;
    const message = clamped
      ? `${finding.message} (Claude cited lines ${finding.line}–${finding.endLine}; shown at ${start}–${end})`
      : finding.message;
    const diagnostic = new vscode.Diagnostic(wholeLines(document, start, end), message, vscode.DiagnosticSeverity[SEVERITY[finding.severity]]);
    diagnostic.source = 'Professor';
    diagnostic.code = finding.rule;
    diagnostics.push(diagnostic);
    // A clamped span is not the span the rewrite was written for.
    if (finding.rewrite !== undefined && !clamped) {
      fixes.push({ diagnostic, rewrite: finding.rewrite.replace(/\r?\n$/, ''), original: lines.slice(start - 1, end).join('\n') });
    }
  }
  return { diagnostics, fixes, invalid };
}

function summary(label, findings, invalid) {
  if (!findings.length) return `Professor: no findings in ${label}.`;
  // An invalid finding counts once, as invalid, whatever severity it claims.
  const count = (severity) => findings.filter((finding) => !invalidReason(finding) && finding.severity === severity).length;
  const parts = [`${count('error')} errors`, `${count('warning')} warnings`, `${count('info')} info`];
  if (invalid) parts.push(`${invalid} invalid`);
  return `Professor: ${findings.length} findings in ${label} (${parts.join(', ')}).`;
}

// startPromptLint wires the commands, the Problems-panel collection, the quick fix and the
// CodeLens that carries the Analyze link; here and whyNot are choose.js's pfmHere and
// notHere bound to this extension host.
function startPromptLint({ here, whyNot }) {
  const collection = vscode.languages.createDiagnosticCollection('professor-prompt');
  const fixes = new Map();
  // One AbortController per running file, keyed by uri: the lens, the progress
  // notification and professor.cancelAnalyze all stop the same run through it.
  const running = new Map();
  const lensChanged = new vscode.EventEmitter();
  let tick;
  // Refreshes the lens now, and once a second while a run is going so its elapsed time counts.
  const refreshLens = () => {
    lensChanged.fire();
    if (running.size && !tick) tick = setInterval(() => lensChanged.fire(), 1000);
    else if (!running.size && tick) {
      clearInterval(tick);
      tick = undefined;
    }
  };

  const clear = (document) => {
    const had = (collection.get(document.uri) ?? []).length > 0;
    collection.delete(document.uri);
    fixes.delete(document.uri.toString());
    return had ? ' Its earlier Professor findings were cleared.' : '';
  };

  // The lens passes its own document's uri, which need not be the active editor's; the palette
  // passes none and means the active editor.
  async function analyzePrompt(uri) {
    if (!here()) {
      vscode.window.showWarningMessage(`Professor: Analyze runs no model here — ${whyNot()}.`);
      return;
    }
    const document = uri?.scheme
      ? vscode.workspace.textDocuments.find((open) => open.uri.toString() === uri.toString())
      : vscode.window.activeTextEditor?.document;
    if (!document) {
      vscode.window.showWarningMessage('Professor: Analyze needs a prompt file open in the editor.');
      return;
    }
    const label = vscode.workspace.asRelativePath(document.uri);
    if (!isPromptFile(document)) {
      vscode.window.showWarningMessage(`Professor: ${label} matches no professor.promptLint.globs pattern, so it is not analyzed.`);
      return;
    }
    const key = document.uri.toString();
    if (running.has(key)) {
      vscode.window.showWarningMessage(`Professor: ${label} is already being analyzed; wait for that run or cancel it.`);
      return;
    }
    // The text and its version are taken before any await: findings map onto exactly this text.
    const lines = Array.from({ length: document.lineCount }, (_, index) => document.lineAt(index).text);
    const version = document.version;
    const config = settings();
    if (!(Number.isFinite(config.timeout) && config.timeout > 0)) {
      vscode.window.showErrorMessage(`Professor: professor.promptLint.timeout must be a positive number of seconds, not ${JSON.stringify(config.timeout)}.`);
      return;
    }
    const law = lawFile();
    try {
      fs.accessSync(law, fs.constants.R_OK);
    } catch (err) {
      vscode.window.showErrorMessage(`Professor: cannot analyze ${label} — the prompt law ${law} is unreadable (${err.code ?? err.message}); run pfm install.${clear(document)}`);
      return;
    }
    const abort = new AbortController();
    running.set(key, { abort, started: Date.now() });
    refreshLens();
    try {
      await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Notification, title: `Professor: analyzing ${label}`, cancellable: true },
        async (progress, token) => {
          const cancel = token.onCancellationRequested(() => abort.abort());
          const result = await runPfm(pfmArgs(config, law), (config.timeout + KILL_MARGIN_S) * 1000, {
            signal: abort.signal,
            input: taskText(label, lines),
          });
          cancel.dispose();
          // The signal, not the token: a lens cancel aborts it too, and is never a timeout.
          if (abort.signal.aborted) {
            vscode.window.showInformationMessage(`Professor: analysis of ${label} cancelled.${clear(document)}`);
            return;
          }
          let findings;
          try {
            findings = readFindings(result, config.timeout);
          } catch (err) {
            vscode.window.showErrorMessage(`Professor: could not analyze ${label} — ${err.message}${clear(document)}`);
            return;
          }
          const mapped = toDiagnostics(document, findings, lines);
          collection.set(document.uri, mapped.diagnostics);
          fixes.set(key, mapped.fixes);
          const done = summary(label, findings, mapped.invalid);
          if (document.version !== version) {
            vscode.window.showWarningMessage(`${done} ${label} changed during the analysis: rerun Professor: Analyze to check the current text.`);
          } else {
            vscode.window.showInformationMessage(done);
          }
        },
      );
    } finally {
      running.delete(key);
      refreshLens();
    }
  }

  const lenses = {
    onDidChangeCodeLenses: lensChanged.event,
    provideCodeLenses(document) {
      if (!here() || !isPromptFile(document)) return [];
      const run = running.get(document.uri.toString());
      const command = run
        ? {
          title: `$(sync~spin) Analyzing… ${Math.floor((Date.now() - run.started) / 1000)}s · Cancel`,
          command: 'professor.cancelAnalyze',
          arguments: [document.uri],
        }
        : { title: '$(checklist) Analyze', command: 'professor.analyzePrompt', arguments: [document.uri] };
      return [new vscode.CodeLens(new vscode.Range(0, 0, 0, 0), command)];
    },
  };

  // The fix is offered only while the span's whole lines still hold the text the rewrite was written for.
  const provider = {
    provideCodeActions(document, range) {
      const actions = [];
      for (const fix of fixes.get(document.uri.toString()) ?? []) {
        const { start, end } = fix.diagnostic.range;
        if (start.line > range.end.line || end.line < range.start.line || end.line >= document.lineCount) continue;
        const span = wholeLines(document, start.line + 1, end.line + 1);
        if (document.getText(span).replace(/\r\n/g, '\n') !== fix.original) continue;
        const action = new vscode.CodeAction('Apply Professor rewrite', vscode.CodeActionKind.QuickFix);
        action.diagnostics = [fix.diagnostic];
        action.edit = new vscode.WorkspaceEdit();
        action.edit.replace(document.uri, span, fix.rewrite);
        actions.push(action);
      }
      return actions;
    },
  };

  const disposable = vscode.Disposable.from(
    collection,
    lensChanged,
    { dispose: () => clearInterval(tick) },
    vscode.commands.registerCommand('professor.analyzePrompt', analyzePrompt),
    // Not contributed in package.json, so the palette never lists it: the running lens is its only caller.
    vscode.commands.registerCommand('professor.cancelAnalyze', (uri) => running.get(uri?.toString())?.abort.abort()),
    vscode.languages.registerCodeLensProvider({ scheme: 'file' }, lenses),
    vscode.languages.registerCodeActionsProvider({ scheme: 'file' }, provider, { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] }),
    // A globs change adds or drops the lens on files already open.
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration('professor.promptLint.globs')) lensChanged.fire();
    }),
  );
  return { disposable };
}

module.exports = { startPromptLint };
