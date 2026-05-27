import { useState, useCallback, useMemo } from "react";
import { type Theme, themes } from "../themes/definitions.ts";

export interface UseThemeResult {
  theme: Theme;
  themeName: string;
  setTheme: (name: string) => void;
  availableThemes: Array<{ name: string; label: string }>;
}

export function useTheme(): UseThemeResult {
  const [themeName, setThemeName] = useState<string>("dark");

  const setTheme = useCallback((name: string) => {
    if (themes[name]) {
      setThemeName(name);
    }
  }, []);

  const theme = useMemo(
    () => themes[themeName] ?? themes["dark"]!,
    [themeName],
  );

  const availableThemes = useMemo(
    () => Object.values(themes).map((t) => ({ name: t.name, label: t.label })),
    [],
  );

  return { theme, themeName, setTheme, availableThemes };
}
