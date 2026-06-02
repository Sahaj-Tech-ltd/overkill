import { useState, useCallback, useMemo } from "react";
import type { Theme } from "../themes/definitions.ts";
import { themes } from "../themes/all-themes.ts";

export interface UseThemeResult {
  theme: Theme;
  themeName: string;
  setTheme: (name: string) => void;
  availableThemes: Array<{ name: string; label: string }>;
}

const DEFAULT_THEME = "catppuccin";

function getInitialTheme(): string {
  const envTheme = process.env["OVERKILL_THEME"];
  if (envTheme && themes[envTheme]) {
    return envTheme;
  }
  return DEFAULT_THEME;
}

export function useTheme(): UseThemeResult {
  const [themeName, setThemeName] = useState<string>(getInitialTheme);

  const setTheme = useCallback((name: string) => {
    if (themes[name]) {
      setThemeName(name);
    }
  }, []);

  const theme = useMemo(
    () => themes[themeName] ?? themes[DEFAULT_THEME]!,
    [themeName],
  );

  const availableThemes = useMemo(
    () =>
      Object.values(themes as Record<string, Theme>).map((t) => ({
        name: t.name,
        label: t.label,
      })),
    [],
  );

  return { theme, themeName, setTheme, availableThemes };
}
