import React, { createContext, useContext } from "react";
import type { Theme } from "../themes/definitions.ts";
import { catppuccin as catppuccinMocha } from "../themes/all-themes.ts";

// ─── Theme Context ─────────────────────────────────────────────────────────

const ThemeContext = createContext<Theme>(catppuccinMocha);

export interface ThemeProviderProps {
  readonly theme: Theme;
  readonly children: React.ReactNode;
}

/**
 * Provide a theme to all design-system components below it in the tree.
 * Wrap your app root or subtree with this to set the active theme.
 */
export function ThemeProvider({
  theme,
  children,
}: ThemeProviderProps): React.JSX.Element {
  return React.createElement(
    ThemeContext.Provider,
    { value: theme },
    children,
  );
}

/**
 * Consume the current theme. Falls back to the built-in dark theme
 * when used outside a ThemeProvider.
 */
export function useTheme(): Theme {
  return useContext(ThemeContext);
}
