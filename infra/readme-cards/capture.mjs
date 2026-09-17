// capture.mjs — record.sh's motion driver: opens the still page in headless
// Chrome over the DevTools protocol (no library: Node's own WebSocket), waits
// for the fonts, measures the section, then screenshots that clip on a fixed
// cadence while the deck's scene plays. Frames land as NNNN.png in the given
// directory; ffmpeg (record.sh) turns them into the animated card.
//
//   node capture.mjs <chrome> <page.html> <section-id> <frames-dir> <seconds> <fps> <scale>
//
// BROKEN STATE: Chrome not exposing a DevTools port within 10 s, a section id
// the page does not have, or a frame Chrome refuses to capture each end the
// run with a named error and a non-zero exit; a frame count is printed at the
// end so a short recording is visible as such.
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [chrome, page, section, framesDir, secondsArg, fpsArg, scaleArg] = process.argv.slice(2);
if (!chrome || !page || !section || !framesDir) {
  console.error("usage: capture.mjs <chrome> <page.html> <section-id> <frames-dir> <seconds> <fps> <scale>");
  process.exit(2);
}
const seconds = Number(secondsArg || 24), fps = Number(fpsArg || 12), scale = Number(scaleArg || 1);
const profile = mkdtempSync(join(tmpdir(), "readme-cards-"));
const proc = spawn(chrome, [
  "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
  "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--window-size=1400,4000", "about:blank",
], { stdio: "ignore" });
// Cleanup waits for Chrome to be gone: rmSync while Chrome still writes its
// profile is ENOTEMPTY. A profile that will not go is named, never fatal —
// the frames are the product.
let cleaned = false;
const done = async () => {
  if (cleaned) return; cleaned = true;
  if (proc.exitCode === null) { proc.kill(); await new Promise((r) => proc.once("exit", r)); }
  try { rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 }); }
  catch (err) { console.error(`capture: profile ${profile} left behind (${err.code})`); }
};
process.on("exit", () => { if (!cleaned && proc.exitCode === null) proc.kill(); });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function devtoolsPort() {
  const file = join(profile, "DevToolsActivePort");
  for (let i = 0; i < 100; i++) {
    if (existsSync(file)) return Number(readFileSync(file, "utf8").split("\n")[0]);
    await sleep(100);
  }
  throw new Error("Chrome exposed no DevTools port within 10 s (DevToolsActivePort never appeared)");
}
const port = await devtoolsPort();
const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const target = targets.find((t) => t.type === "page");
if (!target) throw new Error("Chrome has no page target to drive");

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error("DevTools websocket refused")); });
let seq = 0;
const pending = new Map();
const events = new Map();
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) {
    const { res, rej } = pending.get(msg.id); pending.delete(msg.id);
    msg.error ? rej(new Error(`${msg.error.message}`)) : res(msg.result);
  } else if (msg.method && events.has(msg.method)) {
    events.get(msg.method)(); events.delete(msg.method);
  }
};
const send = (method, params = {}) => new Promise((res, rej) => { const id = ++seq; pending.set(id, { res, rej }); ws.send(JSON.stringify({ id, method, params })); });
const once = (method) => new Promise((res) => events.set(method, res));
const evaluate = async (expression) => {
  const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(`page threw: ${r.exceptionDetails.text}`);
  return r.result.value;
};

await send("Page.enable");
await send("Runtime.enable");
await send("Emulation.setDeviceMetricsOverride", { width: 1400, height: 4000, deviceScaleFactor: scale, mobile: false });
const loaded = once("Page.loadEventFired");
await send("Page.navigate", { url: `file://${page}` });
await loaded;
await evaluate("document.fonts.ready.then(() => 'fonts')");
// The section is measured before EVERY frame: a scene that swaps a short view
// for a tall one grows the section, and a clip fixed at the start would cut
// the bottom off. Frames therefore differ in height; record.sh pads them to
// the tallest when it assembles the card.
const measure = () => evaluate(`(() => { const el = document.getElementById(${JSON.stringify(section)}); if (!el) return null; const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; })()`);
const pad = 24;
const clipOf = (box) => ({ x: Math.max(0, box.x - pad), y: Math.max(0, box.y - pad), width: box.width + 2 * pad, height: box.height + 2 * pad, scale: 1 });
const first = await measure();
if (!first) throw new Error(`no element with id "${section}" in ${page}`);
let clip = clipOf(first);

const total = Math.round(seconds * fps);
const start = Date.now();
let frames = 0;
for (let i = 0; i < total; i++) {
  const due = start + (i * 1000) / fps;
  const wait = due - Date.now();
  if (wait > 0) await sleep(wait);
  const box = await measure();
  if (box) clip = clipOf(box);
  const shot = await send("Page.captureScreenshot", { format: "png", clip, captureBeyondViewport: true });
  writeFileSync(join(framesDir, String(i).padStart(4, "0") + ".png"), Buffer.from(shot.data, "base64"));
  frames++;
}
ws.close();
await done();
console.log(`capture: ${frames} frames · ${Math.round(clip.width)}x${Math.round(clip.height)} @${scale}x · ${fps} fps · ${seconds} s`);
