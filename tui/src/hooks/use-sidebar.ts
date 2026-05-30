import { useState, useCallback } from "react";

export type SidebarTab = "sessions" | "tools" | "files" | "agents" | "self-eval" | "tests" | "wizard" | "queue" | "todos" | "skills";

export interface UseSidebarResult {
  visible: boolean;
  activeTab: SidebarTab;
  toggle: () => void;
  setTab: (tab: SidebarTab) => void;
}

export function useSidebar(): UseSidebarResult {
  const [visible, setVisible] = useState(false);
  const [activeTab, setActiveTab] = useState<SidebarTab>("sessions");

  const toggle = useCallback(() => {
    setVisible((prev) => !prev);
  }, []);

  const setTab = useCallback((tab: SidebarTab) => {
    setActiveTab(tab);
  }, []);

  return { visible, activeTab, toggle, setTab };
}
