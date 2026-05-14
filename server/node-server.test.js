import assert from "node:assert/strict";
import { once } from "node:events";
import { afterEach, describe, test } from "node:test";

import { createNodeServer } from "./node-server.js";

const openServers = [];

afterEach(async () => {
  await Promise.all(openServers.splice(0).map((server) => closeServer(server)));
});

describe("node server adapter", () => {
  test("serves API responses through the proxy core", async () => {
    const server = createNodeServer();
    openServers.push(server);
    const baseUrl = await listen(server);

    const response = await fetch(`${baseUrl}/api/status`);

    assert.equal(response.status, 200);
    const payload = await response.json();
    assert.equal(payload.mode, "stateless");
    assert.equal(payload.storage, "browser");
  });

  test("serves static files from the app directory", async () => {
    const server = createNodeServer();
    openServers.push(server);
    const baseUrl = await listen(server);

    const response = await fetch(`${baseUrl}/`);
    const html = await response.text();

    assert.equal(response.status, 200);
    assert.match(response.headers.get("content-type"), /text\/html/);
    assert.match(html, /<title>Say Lab<\/title>/);
  });
});

async function listen(server) {
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const { port } = server.address();
  return `http://127.0.0.1:${port}`;
}

function closeServer(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}
