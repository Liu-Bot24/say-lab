import assert from "node:assert/strict";
import { describe, test } from "node:test";

import worker from "./index.js";

describe("cloudflare worker adapter", () => {
  test("routes API requests through the proxy core", async () => {
    const response = await worker.fetch(new Request("https://say-lab.test/api/status"), {
      ASSETS: { fetch: async () => new Response("asset") },
    });

    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.equal(payload.mode, "stateless");
  });

  test("serves non-api requests from the assets binding", async () => {
    let sawUrl = "";
    const response = await worker.fetch(new Request("https://say-lab.test/"), {
      ASSETS: {
        fetch: async (request) => {
          sawUrl = request.url;
          return new Response("<!doctype html>", {
            headers: { "content-type": "text/html; charset=utf-8" },
          });
        },
      },
    });

    assert.equal(response.status, 200);
    assert.equal(sawUrl, "https://say-lab.test/");
    assert.match(response.headers.get("content-type"), /text\/html/);
  });
});
