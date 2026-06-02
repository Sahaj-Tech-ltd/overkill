// Structured logger that prepends timestamps and formats errors consistently.
// Replace direct console.error calls with log.error() for structured output.

const LOG_PREFIX = "[overkill]";

export const log = {
  error(msg: string, err?: unknown): void {
    const ts = new Date().toISOString();
    const errStr =
      err instanceof Error ? (err.stack ?? err.message) : String(err ?? "");
    console.error(`${ts} ${LOG_PREFIX} ERROR ${msg} ${errStr}`.trim());
  },
  warn(msg: string): void {
    const ts = new Date().toISOString();
    console.warn(`${ts} ${LOG_PREFIX} WARN ${msg}`);
  },
  info(msg: string): void {
    const ts = new Date().toISOString();
    console.log(`${ts} ${LOG_PREFIX} INFO ${msg}`);
  },
};
