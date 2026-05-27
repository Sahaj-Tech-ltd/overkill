import React, { useState, useCallback } from "react";
import { render } from "ink";
import { App } from "./app.tsx";
import { BootAnimation } from "./components/boot-animation.tsx";
import { ErrorBoundary } from "./components/error-boundary.tsx";

function Root(): React.JSX.Element {
  const [booted, setBooted] = useState(false);

  const handleBootComplete = useCallback(() => {
    setBooted(true);
  }, []);

  return (
    <ErrorBoundary>
      {!booted ? <BootAnimation onComplete={handleBootComplete} /> : <App />}
    </ErrorBoundary>
  );
}

const { unmount } = render(React.createElement(Root));

process.on("SIGINT", () => {
  unmount();
  process.exit(0);
});

process.on("SIGTERM", () => {
  unmount();
  process.exit(0);
});
