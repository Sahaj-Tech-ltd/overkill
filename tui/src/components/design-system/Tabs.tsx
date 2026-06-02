import React, {
  useState,
  useCallback,
  createContext,
  useContext,
  useMemo,
} from "react";
import { Box, Text, useInput, useStdout } from "ink";

// ─── TabHeaderFocus Context ────────────────────────────────────────────────

interface TabHeaderFocusContextValue {
  headerFocused: boolean;
  registerOptIn: (id: string) => void;
  deregisterOptIn: (id: string) => void;
  focusHeader: () => void;
  tabsWidth: number;
}

const TabHeaderFocusContext = createContext<TabHeaderFocusContextValue>({
  headerFocused: true,
  registerOptIn: () => {},
  deregisterOptIn: () => {},
  focusHeader: () => {},
  tabsWidth: 0,
});

/**
 * Hook: content components call this to opt into ↓-to-content behavior.
 * When header is focused and ↓ is pressed, focus moves into content.
 */
export function useTabHeaderFocus(): {
  headerFocused: boolean;
  focusHeader: () => void;
} {
  const ctx = useContext(TabHeaderFocusContext);
  return { headerFocused: ctx.headerFocused, focusHeader: ctx.focusHeader };
}

/**
 * Hook: get the tab content width.
 */
export function useTabsWidth(): number {
  const ctx = useContext(TabHeaderFocusContext);
  return ctx.tabsWidth;
}

// ─── Tab Component ─────────────────────────────────────────────────────────

export interface TabProps {
  title: string;
  id: string;
  children: React.ReactNode;
}

export function Tab(_props: TabProps): React.JSX.Element {
  // Tab is just a data container; Tabs renders it.
  return <></>;
}

// ─── Tabs Container ────────────────────────────────────────────────────────

interface TabsProps {
  children: React.ReactNode;
  color?: string;
  defaultTab?: string;
  selectedTab?: string;
  onTabChange?: (tabId: string) => void;
  initialHeaderFocused?: boolean;
  contentHeight?: number;
  disableNavigation?: boolean;
}

export function Tabs({
  children,
  color = "cyan",
  defaultTab,
  selectedTab: controlledTab,
  onTabChange,
  initialHeaderFocused = true,
  contentHeight,
  disableNavigation = false,
}: TabsProps): React.JSX.Element {
  const { stdout } = useStdout();
  const tabsWidth = (stdout.columns ?? 80) - 4; // room for borders

  // Parse children to extract Tab elements
  const tabChildren = React.Children.toArray(children).filter(
    (child): child is React.ReactElement<TabProps> =>
      React.isValidElement(child) && (child.type as unknown) === Tab,
  );

  const tabIds = tabChildren.map((t) => t.props.id);
  const [internalTab, setInternalTab] = useState<string>(
    defaultTab ?? tabIds[0] ?? "",
  );
  const selectedTab = controlledTab ?? internalTab;
  const [headerFocused, setHeaderFocused] = useState(initialHeaderFocused);
  const [optInIds] = useState(new Set<string>());

  const registerOptIn = useCallback(
    (id: string) => {
      optInIds.add(id);
    },
    [optInIds],
  );
  const deregisterOptIn = useCallback(
    (id: string) => {
      optInIds.delete(id);
    },
    [optInIds],
  );
  const focusHeader = useCallback(() => {
    setHeaderFocused(true);
  }, []);

  const contextValue = useMemo<TabHeaderFocusContextValue>(
    () => ({
      headerFocused,
      registerOptIn,
      deregisterOptIn,
      focusHeader,
      tabsWidth,
    }),
    [headerFocused, registerOptIn, deregisterOptIn, focusHeader, tabsWidth],
  );

  const handleTabChange = useCallback(
    (tabId: string) => {
      if (controlledTab !== undefined) {
        onTabChange?.(tabId);
      } else {
        setInternalTab(tabId);
        onTabChange?.(tabId);
      }
    },
    [controlledTab, onTabChange],
  );

  useInput((_input, key) => {
    if (disableNavigation) return;

    if (headerFocused) {
      if (key.leftArrow) {
        const idx = tabIds.indexOf(selectedTab);
        const prevIdx = idx <= 0 ? tabIds.length - 1 : idx - 1;
        handleTabChange(tabIds[prevIdx]!);
      } else if (key.rightArrow || key.tab) {
        const idx = tabIds.indexOf(selectedTab);
        const nextIdx = idx >= tabIds.length - 1 ? 0 : idx + 1;
        handleTabChange(tabIds[nextIdx]!);
      } else if (key.downArrow && optInIds.size > 0) {
        setHeaderFocused(false);
      }
    } else {
      // In content mode
      if (key.upArrow) {
        setHeaderFocused(true);
      } else if (key.escape) {
        setHeaderFocused(true);
      }
    }
  });

  // Find selected tab's children
  const selectedChild = tabChildren.find((t) => t.props.id === selectedTab);

  // Tab header rendering
  const headerRow = (
    <Box flexDirection="row" width="100%">
      {tabChildren.map((tab, i) => {
        const isSelected = tab.props.id === selectedTab;
        const isHeaderFocused = isSelected && headerFocused;
        const label = ` ${tab.props.title} `;
        return (
          <React.Fragment key={tab.props.id}>
            {i > 0 && <Text dimColor> │ </Text>}
            <Text
              bold={isSelected}
              color={isSelected ? color : undefined}
              dimColor={!isSelected}
              inverse={isHeaderFocused}
            >
              {label}
            </Text>
          </React.Fragment>
        );
      })}
    </Box>
  );

  const contentBox = contentHeight ? (
    <Box flexDirection="column" height={contentHeight} width="100%">
      <TabHeaderFocusContext.Provider value={contextValue}>
        {selectedChild?.props.children}
      </TabHeaderFocusContext.Provider>
    </Box>
  ) : (
    <Box flexDirection="column" width="100%">
      <TabHeaderFocusContext.Provider value={contextValue}>
        {selectedChild?.props.children}
      </TabHeaderFocusContext.Provider>
    </Box>
  );

  return (
    <Box flexDirection="column" width="100%">
      {headerRow}
      <Box marginTop={1}>{contentBox}</Box>
    </Box>
  );
}
