import type { BackendClient } from "../../backend/client.ts";

type RPC = Pick<BackendClient, "call">;
export type TodoStatus = "open" | "in_progress" | "shipped" | "abandoned";
export interface Todo { id: string; intent: string; status: TodoStatus; }
export interface SkillInfo {
  name: string; description: string; category: string; tags: string[];
  enabled: boolean; bundled: boolean;
}

class PanelState<T> {
  private listeners = new Set<() => void>();
  constructor(protected state: T) {}
  getSnapshot = (): T => this.state;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  protected publish(state: T): void {
    this.state = state;
    this.listeners.forEach((listener) => listener());
  }
}

interface TodoState { sessionId: string; todos: Todo[]; busy: boolean; error: string; }
export class TodoPanelModel extends PanelState<TodoState> {
  private generation = 0;
  constructor(private backend: RPC) {
    super({ sessionId: "", todos: [], busy: false, error: "" });
  }
  dispose(): void { this.generation++; this.publish({ ...this.state, busy: false }); }
  async setSession(sessionId: string): Promise<void> {
    this.generation++;
    this.publish({ sessionId, todos: [], busy: false, error: "" });
    await this.reload();
  }
  async reload(): Promise<void> {
    if (!this.state.sessionId || this.state.busy) return;
    await this.perform(async () => {
      const todos = await this.backend.call<Todo[]>("todo.list", { session_id: this.state.sessionId });
      return () => this.publish({ ...this.state, todos });
    });
  }
  private async perform(operation: () => Promise<() => void>): Promise<boolean> {
    if (!this.state.sessionId || this.state.busy) return false;
    const generation = this.generation;
    this.publish({ ...this.state, busy: true, error: "" });
    try {
      const commit = await operation();
      if (generation !== this.generation) return false;
      commit();
      return true;
    } catch (error) {
      if (generation === this.generation) this.publish({ ...this.state, error: String((error as Error).message ?? error) });
      return false;
    } finally {
      if (generation === this.generation) this.publish({ ...this.state, busy: false });
    }
  }
  async save(description: string, id?: string): Promise<boolean> {
    description = description.trim();
    if (!description) {
      this.publish({ ...this.state, error: "Enter a task description." });
      return false;
    }
    return this.perform(async () => {
      const task = await this.backend.call<Todo>(id ? "todo.update" : "todo.add", {
        ...(id ? { id } : {}), session_id: this.state.sessionId, description,
      });
      if (!task) throw new Error("Backend did not return the saved task.");
      return () => this.publish({ ...this.state, todos: id ? this.state.todos.map((item) => item.id === id ? task : item) : [...this.state.todos, task] });
    });
  }
  async toggle(id: string): Promise<void> {
    await this.perform(async () => {
      const task = await this.backend.call<Todo>("todo.toggle", { id, session_id: this.state.sessionId });
      if (!task) throw new Error("Task no longer exists. Press r to reload.");
      return () => this.publish({ ...this.state, todos: this.state.todos.map((item) => item.id === id ? task : item) });
    });
  }
  async remove(id: string): Promise<void> {
    await this.perform(async () => {
      await this.backend.call("todo.delete", { id, session_id: this.state.sessionId });
      return () => this.publish({ ...this.state, todos: this.state.todos.filter((task) => task.id !== id) });
    });
  }
  async clear(): Promise<void> {
    // Apply each confirmed deletion immediately. On partial failure the failed
    // task and all unattempted tasks stay visible and can be retried.
    const generation = this.generation;
    const tasks = [...this.state.todos];
    await this.perform(async () => {
      for (const task of tasks) {
        await this.backend.call("todo.delete", { id: task.id, session_id: this.state.sessionId });
        if (generation !== this.generation) return () => {};
        this.publish({ ...this.state, todos: this.state.todos.filter((item) => item.id !== task.id) });
      }
      return () => {};
    });
  }
}

interface SkillState { skills: SkillInfo[]; busy: boolean; error: string; }
export class SkillsPanelModel extends PanelState<SkillState> {
  private generation = 0;
  constructor(private backend: RPC) { super({ skills: [], busy: false, error: "" }); }
  dispose(): void { this.generation++; this.publish({ ...this.state, busy: false }); }
  async reload(): Promise<void> {
    if (this.state.busy) return;
    await this.perform(async () => {
      const skills = await this.backend.call<SkillInfo[]>("skills.list");
      return () => this.publish({ ...this.state, skills: skills ?? [] });
    });
  }
  async setEnabled(name: string, enabled: boolean): Promise<void> {
    await this.perform(async () => {
      const result = await this.backend.call<{ name: string; enabled: boolean }>("skills.toggle", { name, enabled });
      return () => this.publish({ ...this.state, skills: this.state.skills.map((skill) => skill.name === name ? { ...skill, enabled: result.enabled } : skill) });
    });
  }
  private async perform(operation: () => Promise<() => void>): Promise<void> {
    if (this.state.busy) return;
    const generation = this.generation;
    this.publish({ ...this.state, busy: true, error: "" });
    try {
      const commit = await operation();
      if (generation === this.generation) commit();
    } catch (error) {
      if (generation === this.generation) this.publish({ ...this.state, error: String((error as Error).message ?? error) });
    } finally {
      if (generation === this.generation) this.publish({ ...this.state, busy: false });
    }
  }
}
