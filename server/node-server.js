import { createReadStream } from "node:fs";
import { stat } from "node:fs/promises";
import http from "node:http";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { createProxyHandler } from "./proxy-core.js";

const STATIC_DIR = fileURLToPath(new URL("../static/", import.meta.url));

const CONTENT_TYPES = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".svg": "image/svg+xml",
  ".webp": "image/webp",
};

export function createNodeServer(options = {}) {
  const staticDir = path.resolve(options.staticDir || STATIC_DIR);
  const proxyHandler = createProxyHandler({ fetch: options.fetch || globalThis.fetch });

  return http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url || "/", `http://${req.headers.host || "127.0.0.1"}`);
      if (url.pathname.startsWith("/api/")) {
        await serveAPI(req, res, proxyHandler);
        return;
      }
      await serveStatic(req, res, staticDir, url.pathname);
    } catch (error) {
      res.writeHead(500, { "content-type": "text/plain; charset=utf-8" });
      res.end(error.message || "server error");
    }
  });
}

async function serveAPI(req, res, proxyHandler) {
  const request = await toWebRequest(req);
  const response = await proxyHandler(request);
  const headers = Object.fromEntries(response.headers.entries());
  res.writeHead(response.status, response.statusText, headers);
  if (req.method === "HEAD") {
    res.end();
    return;
  }
  const body = await response.arrayBuffer();
  res.end(Buffer.from(body));
}

async function toWebRequest(req) {
  const headers = new Headers();
  for (const [name, value] of Object.entries(req.headers)) {
    if (Array.isArray(value)) {
      for (const item of value) headers.append(name, item);
    } else if (value !== undefined) {
      headers.set(name, value);
    }
  }
  const method = req.method || "GET";
  const body = ["GET", "HEAD"].includes(method) ? undefined : await readRequestBody(req);
  return new Request(`http://${headers.get("host") || "127.0.0.1"}${req.url || "/"}`, {
    method,
    headers,
    body,
  });
}

function readRequestBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on("data", (chunk) => chunks.push(chunk));
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });
}

async function serveStatic(req, res, staticDir, pathname) {
  if (req.method !== "GET" && req.method !== "HEAD") {
    res.writeHead(405, { "content-type": "text/plain; charset=utf-8" });
    res.end("method not allowed");
    return;
  }

  const filePath = await resolveStaticFile(staticDir, pathname);
  const contentType = CONTENT_TYPES[path.extname(filePath).toLowerCase()] || "application/octet-stream";
  res.writeHead(200, {
    "content-type": contentType,
    "cache-control": "no-store",
  });
  if (req.method === "HEAD") {
    res.end();
    return;
  }
  createReadStream(filePath).pipe(res);
}

async function resolveStaticFile(staticDir, pathname) {
  const safePath = safeJoin(staticDir, pathname === "/" ? "/index.html" : pathname);
  if (safePath) {
    try {
      const info = await stat(safePath);
      if (info.isFile()) return safePath;
    } catch {
      // Fall through to index.html for app routes.
    }
  }
  return path.join(staticDir, "index.html");
}

function safeJoin(root, pathname) {
  let decoded = "/";
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return "";
  }
  const joined = path.resolve(root, `.${decoded}`);
  return joined.startsWith(`${root}${path.sep}`) || joined === root ? joined : "";
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const port = Number(process.env.PORT || 5567);
  const host = process.env.HOST || "127.0.0.1";
  const server = createNodeServer();
  server.listen(port, host, () => {
    console.log(`Say Lab demo listening on http://${host}:${port}`);
  });
}
