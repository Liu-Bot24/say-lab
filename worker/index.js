import { createProxyHandler } from "../server/proxy-core.js";

const proxyHandler = createProxyHandler();

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/api/")) {
      return proxyHandler(request);
    }
    if (env?.ASSETS?.fetch) {
      return env.ASSETS.fetch(request);
    }
    return new Response("ASSETS binding is not configured", {
      status: 500,
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  },
};
