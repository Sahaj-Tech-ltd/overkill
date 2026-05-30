import React, { useState, useCallback } from "react";
import { render } from "ink";
import { App } from "./app.tsx";
import { BootAnimation } from "./components/boot-animation.tsx";
import { ErrorBoundary } from "./components/error-boundary.tsx";
import { MouseProvider, ScrollHandlerProvider } from "./hooks/use-mouse.tsx";

function Root(): React.JSX.Element {
  const [booted, setBooted] = useState(false);

  const handleBootComplete = useCallback(() => {
    setBooted(true);
  }, []);

  return (
    <ErrorBoundary>
      <MouseProvider enabled>
        <ScrollHandlerProvider>
          {!booted ? <BootAnimation onComplete={handleBootComplete} /> : <App />}
        </ScrollHandlerProvider>
      </MouseProvider>
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
