export interface SyntaxColors {
  keyword: string;
  string: string;
  comment: string;
  number: string;
  type: string;
  function: string;
  operator: string;
  text: string;
}

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
  syntax: SyntaxColors;
}

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
  syntax: {
    keyword: "#cba6f7",
    string: "#a6e3a1",
    comment: "#6c7086",
    number: "#fab387",
    type: "#89b4fa",
    function: "#89dceb",
    operator: "#f38ba8",
    text: "#cdd6f4",
  },
};

export const tokyoNight: Theme = {
  name: "tokyo-night",
  label: "Tokyo Night",
  background: "#1a1b26",
  text: "#c0caf5",
  accent: "#7aa2f7",
  muted: "#565f89",
  error: "#f7768e",
  success: "#9ece6a",
  warning: "#e0af68",
  border: "#3b4261",
  inputBg: "#24283b",
  highlight: "#bb9af7",
  syntax: {
    keyword: "#bb9af7",
    string: "#9ece6a",
    comment: "#565f89",
    number: "#ff9e64",
    type: "#7dcfff",
    function: "#7aa2f7",
    operator: "#f7768e",
    text: "#c0caf5",
  },
};

export const cyberpunk: Theme = {
  name: "cyberpunk",
  label: "Cyberpunk",
  background: "#0d0221",
  text: "#00ff41",
  accent: "#ff00ff",
  muted: "#4a0e4e",
  error: "#ff0040",
  success: "#00ff41",
  warning: "#ffd700",
  border: "#2e004f",
  inputBg: "#1a0033",
  highlight: "#fe00fe",
  syntax: {
    keyword: "#ff00ff",
    string: "#00ff41",
    comment: "#4a0e4e",
    number: "#ffd700",
    type: "#00ffff",
    function: "#ff00ff",
    operator: "#ff0040",
    text: "#00ff41",
  },
};

export const ocean: Theme = {
  name: "ocean",
  label: "Ocean",
  background: "#0a192f",
  text: "#ccd6f6",
  accent: "#64ffda",
  muted: "#2d3a52",
  error: "#ff6b6b",
  success: "#64ffda",
  warning: "#ffd93d",
  border: "#173a5e",
  inputBg: "#112240",
  highlight: "#48cae4",
  syntax: {
    keyword: "#64ffda",
    string: "#a3be8c",
    comment: "#2d3a52",
    number: "#ffd93d",
    type: "#48cae4",
    function: "#64ffda",
    operator: "#ff6b6b",
    text: "#ccd6f6",
  },
};

export const nord: Theme = {
  name: "nord",
  label: "Nord",
  background: "#2e3440",
  text: "#e5e9f0",
  accent: "#88c0d0",
  muted: "#616e88",
  error: "#bf616a",
  success: "#a3be8c",
  warning: "#d08770",
  border: "#3b4252",
  inputBg: "#434c5e",
  highlight: "#b48ead",
  syntax: {
    keyword: "#81a1c1",
    string: "#a3be8c",
    comment: "#616e88",
    number: "#b48ead",
    type: "#88c0d0",
    function: "#88c0d0",
    operator: "#bf616a",
    text: "#e5e9f0",
  },
};

export const dracula: Theme = {
  name: "dracula",
  label: "Dracula",
  background: "#1d1e28",
  text: "#f8f8f2",
  accent: "#bd93f9",
  muted: "#6272a4",
  error: "#ff5555",
  success: "#50fa7b",
  warning: "#ffb86c",
  border: "#44475a",
  inputBg: "#282a36",
  highlight: "#ff79c6",
  syntax: {
    keyword: "#ff79c6",
    string: "#f1fa8c",
    comment: "#6272a4",
    number: "#50fa7b",
    type: "#8be9fd",
    function: "#bd93f9",
    operator: "#ff79c6",
    text: "#f8f8f2",
  },
};

export const solarized: Theme = {
  name: "solarized",
  label: "Solarized Dark",
  background: "#002b36",
  text: "#839496",
  accent: "#268bd2",
  muted: "#586e75",
  error: "#dc322f",
  success: "#859900",
  warning: "#b58900",
  border: "#073642",
  inputBg: "#073642",
  highlight: "#6c71c4",
  syntax: {
    keyword: "#6c71c4",
    string: "#2aa198",
    comment: "#586e75",
    number: "#d33682",
    type: "#b58900",
    function: "#268bd2",
    operator: "#dc322f",
    text: "#839496",
  },
};

export const gruvbox: Theme = {
  name: "gruvbox",
  label: "Gruvbox Dark",
  background: "#282828",
  text: "#ebdbb2",
  accent: "#458588",
  muted: "#7c6f64",
  error: "#cc241d",
  success: "#98971a",
  warning: "#d79921",
  border: "#3c3836",
  inputBg: "#3c3836",
  highlight: "#b16286",
  syntax: {
    keyword: "#b16286",
    string: "#98971a",
    comment: "#7c6f64",
    number: "#d3869b",
    type: "#fabd2f",
    function: "#83a598",
    operator: "#cc241d",
    text: "#ebdbb2",
  },
};

export const monokai: Theme = {
  name: "monokai",
  label: "Monokai",
  background: "#272822",
  text: "#f8f8f2",
  accent: "#a6e22e",
  muted: "#75715e",
  error: "#f92672",
  success: "#a6e22e",
  warning: "#e6db74",
  border: "#49483e",
  inputBg: "#3e3d32",
  highlight: "#fd971f",
  syntax: {
    keyword: "#f92672",
    string: "#e6db74",
    comment: "#75715e",
    number: "#ae81ff",
    type: "#66d9ef",
    function: "#a6e22e",
    operator: "#f92672",
    text: "#f8f8f2",
  },
};

export const oneDark: Theme = {
  name: "one-dark",
  label: "One Dark",
  background: "#282c34",
  text: "#abb2bf",
  accent: "#61afef",
  muted: "#5c6370",
  error: "#e06c75",
  success: "#98c379",
  warning: "#d19a66",
  border: "#3e4452",
  inputBg: "#21252b",
  highlight: "#c678dd",
  syntax: {
    keyword: "#c678dd",
    string: "#98c379",
    comment: "#5c6370",
    number: "#d19a66",
    type: "#e5c07b",
    function: "#61afef",
    operator: "#e06c75",
    text: "#abb2bf",
  },
};

export const rosePine: Theme = {
  name: "rose-pine",
  label: "Rosé Pine",
  background: "#191724",
  text: "#e0def4",
  accent: "#ebbcba",
  muted: "#6e6a86",
  error: "#eb6f92",
  success: "#31748f",
  warning: "#f6c177",
  border: "#26233a",
  inputBg: "#1f1d2e",
  highlight: "#c4a7e7",
  syntax: {
    keyword: "#c4a7e7",
    string: "#f6c177",
    comment: "#6e6a86",
    number: "#ebbcba",
    type: "#9ccfd8",
    function: "#ebbcba",
    operator: "#eb6f92",
    text: "#e0def4",
  },
};
