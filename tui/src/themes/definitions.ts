export interface Theme {
  name: string;
  label: string;
  background: string;
  text: string;
  accent: string;
  muted: string;
  error: string;
  success: string;
  warning: string;
  border: string;
  inputBg: string;
  highlight: string;
}

export const dark: Theme = {
  name: "dark",
  label: "Dark",
  background: "#1e1e2e",
  text: "#cdd6f4",
  accent: "#89b4fa",
  muted: "#6c7086",
  error: "#f38ba8",
  success: "#a6e3a1",
  warning: "#f9e2af",
  border: "#45475a",
  inputBg: "#313244",
  highlight: "#89b4fa",
};

export const light: Theme = {
  name: "light",
  label: "Light",
  background: "#eff1f5",
  text: "#4c4f69",
  accent: "#1e66f5",
  muted: "#9ca0b0",
  error: "#d20f39",
  success: "#40a02b",
  warning: "#df8e1d",
  border: "#ccd0da",
  inputBg: "#e6e9ef",
  highlight: "#1e66f5",
};

export const catppuccinMocha: Theme = {
  name: "catppuccin-mocha",
  label: "Catppuccin Mocha",
  background: "#1e1e2e",
  text: "#cdd6f4",
  accent: "#89b4fa",
  muted: "#6c7086",
  error: "#f38ba8",
  success: "#a6e3a1",
  warning: "#f9e2af",
  border: "#45475a",
  inputBg: "#313244",
  highlight: "#cba6f7",
};

export const themes: Record<string, Theme> = {
  dark,
  light,
  "catppuccin-mocha": catppuccinMocha,
};
