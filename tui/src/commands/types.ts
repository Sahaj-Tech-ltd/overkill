/** Command types for the lazy-loaded command registry. */

export interface Command {
  id: string;
  title: string;
  description: string;
  /** Human-readable keybinding for display (e.g. "Ctrl+K"). */
  keybind?: string;
  /** The action to execute. Receives no arguments — closures capture context. */
  action: () => void;
  /** Optional enablement gate. Defaults to true. */
  isEnabled?: () => boolean;
  /** Hide from typeahead/help. Defaults to false. */
  isHidden?: boolean;
  /** Grayed-out hint text shown after command in typeahead. */
  argumentHint?: string;
}

/** Factory function — returns a Command, typically closing over app state. */
export type CommandFactory = () => Command;

/** A command module can export a single factory or an array. */
export type CommandModule =
  | { default: CommandFactory }
  | { default: CommandFactory[] }
  | { commands: CommandFactory[] };
