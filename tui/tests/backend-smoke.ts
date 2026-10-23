import assert from "node:assert/strict";
import { BackendClient } from "../src/backend/client.ts";

// Launched by the Go API integration test against its real authenticated server.
const client = new BackendClient();
assert.equal(await client.health(), true);
await client.call("config.exists");
const events = [];
for await (const event of client.streamCall("agent.send", {
  session_id: "client-test",
  message: "hello",
})) events.push(event);
assert.ok(events.some((event) => JSON.stringify(event).includes("hello")));
assert.ok(events.some((event) => event.type === "done"));
const unauthorized = new BackendClient(undefined, "incorrect-credential");
assert.equal(await unauthorized.health(), false);
await assert.rejects(unauthorized.call("config.exists"), /401/);
console.log("Frontend authenticated RPC, health and streaming passed");
