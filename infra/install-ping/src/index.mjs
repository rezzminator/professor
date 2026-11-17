// The install-ping Worker's fetch handler: routes, reads the body within its
// limit, and upserts the row ping.mjs builds through the D1 binding.
import { HOME_TEXT, MAX_BODY_BYTES, UPSERT_SQL, buildRow, rowValues, validatePing } from "./ping.mjs";

const TEXT = { "content-type": "text/plain; charset=utf-8" };

function text(status, body, headers = {}) {
  return new Response(body.endsWith("\n") ? body : `${body}\n`, { status, headers: { ...TEXT, ...headers } });
}

// readCapped is the request body as text, read only up to MAX_BODY_BYTES: a
// body with no or a false content-length (chunked) is cut off there, never
// buffered whole; null when it runs past the limit.
async function readCapped(request) {
  if (!request.body) return "";
  const reader = request.body.getReader();
  const chunks = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_BODY_BYTES) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

// handle answers one request; db is the D1 binding, now the receipt time.
export async function handle(request, db, now) {
  const { pathname } = new URL(request.url);
  if (pathname === "/") {
    if (request.method !== "GET" && request.method !== "HEAD") return text(405, "method not allowed", { allow: "GET" });
    return text(200, HOME_TEXT);
  }
  if (pathname !== "/v1/ping") return text(404, "not found");
  if (request.method !== "POST") return text(405, "method not allowed", { allow: "POST" });
  const type = (request.headers.get("content-type") || "").split(";")[0].trim().toLowerCase();
  if (type !== "application/json") return text(400, "content-type must be application/json");
  const declared = Number(request.headers.get("content-length") || "0");
  if (declared > MAX_BODY_BYTES) return text(400, `body is over ${MAX_BODY_BYTES} bytes`);
  const body = await readCapped(request);
  if (body === null) return text(400, `body is over ${MAX_BODY_BYTES} bytes`);
  const checked = validatePing(body);
  if (checked.error) return text(400, checked.error);
  const values = rowValues(buildRow(checked.ping, now));
  try {
    await db.prepare(UPSERT_SQL).bind(...values).run();
  } catch (error) {
    console.error("install-ping: upsert failed", { day: values[0], product: values[2], error: String(error) });
    return text(500, "storage failed");
  }
  return new Response(null, { status: 204 });
}

export default {
  async fetch(request, env) {
    return handle(request, env.install_ping, new Date());
  },
};
